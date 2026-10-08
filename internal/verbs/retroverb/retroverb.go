// Package retroverb implements the top-level `clast retro [<day>]`
// (SURFACE V11): the verb form of the retro shape. It is the second of
// the three shape verb forms to land (llm-verbs/step-05), following
// briefverb's own conventions (llm-verbs/step-04): gather through the
// shape's plumbing document verb (internal/verbs/retro's own Run — never
// re-walking the journal or re-deriving its grouping/windowing), a
// package-namespaced clasterr code for a request failure ("retro.*",
// mirroring brief.llm-request-failed), and — new here — the flow's own
// *Non-normative (verb form)* cache paragraph: a content-fingerprinted
// summary cache under $XDG_CACHE_HOME/clast/retro/ (V7's private-state
// carve-out), implemented in internal/retrocache and wired in from
// command.go.
//
// This file holds flows/retro.md §2 (summarize each entry, against the
// cache) and §3 (fold summaries into the document) — kept separate from
// command.go's Cobra wiring/flag parsing and command.go's own §4
// (render), the same split briefverb.go draws from briefverb/command.go.
// §1 (gather) lives entirely in command.go, as a direct call into
// internal/verbs/retro's own exported Run: composing directly over
// another verb's package this way is the same posture briefverb's own
// doc comment already argues for (V8's "same-named plumbing document
// verb" is exactly for this, plumbing-to-plumbing same-tier duplication
// is what the codebase's usual no-cross-import convention guards
// against, not this direction).
package retroverb

import (
	"context"
	"fmt"
	"sync"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/progress"
	"github.com/procrastivity/clast/internal/prompt"
	"github.com/procrastivity/clast/internal/retrocache"
	retroplumbing "github.com/procrastivity/clast/internal/verbs/retro"
)

// unprojectedLabel is how the no-project bucket (retro's own unexported
// "-" sentinel key) reads in prose: both the retro-summary prompt's
// {{project}} placeholder and this verb's own "(no project)" heading
// (command.go's writeHuman) use it, rather than leaking retro's internal
// sentinel or rendering a bare "-" a reader (or the model) could mistake
// for real content.
const unprojectedLabel = "(no project)"

// FoldedEntry is one summarized entry, attached to its project group
// (flows/retro.md §3): the plumbing retro EntryRow it summarizes, plus
// the summary text §2 produced for it (from cache or fresh).
type FoldedEntry struct {
	retroplumbing.EntryRow
	Summary string
}

// FoldedGroup is one project's folded section (§3): the same Sessions/
// Breadcrumbs plumbing retro already gathered, verbatim, and Entries
// carrying each summary alongside its EntryRow.
type FoldedGroup struct {
	Slug        string
	Sessions    []retroplumbing.SessionRow
	Entries     []FoldedEntry
	Breadcrumbs []journal.BreadcrumbEntry
}

// Document is the folded day→project document flows/retro.md §4 renders
// (command.go's writeHuman/writeJSON) — retroplumbing.Result's own shape,
// with each group's Entries folded per §3.
type Document struct {
	Day               journal.Day
	WindowStart       journal.Day
	Groups            []FoldedGroup
	GlobalBreadcrumbs []journal.BreadcrumbEntry
}

// Stats tallies cache hits vs. fresh LLM requests across one Summarize
// call, for --verbose reporting (command.go) — not part of Document,
// since neither flow form's own output carries this verb-form-only fact.
type Stats struct {
	CacheHits int
	Requests  int
}

// HasEntries reports whether result carries at least one session with an
// entry body, across every group — command.go's own empty-before-client
// gate (mirroring briefverb's Result.Empty check): a window with zero
// entry-bearing sessions must never require an llm.Client, let alone
// construct one, so command.go calls this before NewClient, exactly the
// way it calls result.Empty before Synthesize in briefverb.
func HasEntries(result retroplumbing.Result) bool {
	for _, g := range result.Groups {
		if len(g.Entries) > 0 {
			return true
		}
	}
	return false
}

// Summarize implements flows/retro.md §2 for an already-gathered plumbing
// retro Result: for every session with an entry body (every row in a
// group's Entries list), render the retro-summary prompt pair, resolve a
// cache hit (unless refresh) or call client.Complete, and store the
// result (fresh or refreshed) back into the cache under its own
// fingerprint. Entries are summarized concurrently, at most
// summarizeConcurrency at a time. Returns every summary keyed by its session's directory
// name (journal.SessionKey.DirName, EntryRow.Item.Key.DirName()) —
// Fold's own lookup key — alongside Stats for command.go's --verbose
// line.
//
// Callers must never call Summarize when HasEntries(result) is false,
// and must not construct client until after that check — the same
// empty-before-client ordering briefverb.Synthesize's own doc comment
// requires of its caller, enforced the same way: Summarize takes an
// already-built *llm.Client rather than a config.Config.
func Summarize(ctx context.Context, result retroplumbing.Result, client *llm.Client, cacheDir string, refresh bool) (map[string]string, Stats, error) {
	type job struct {
		slug string
		row  retroplumbing.EntryRow
	}
	var jobs []job
	for _, g := range result.Groups {
		for _, row := range g.Entries {
			jobs = append(jobs, job{slug: g.Slug, row: row})
		}
	}

	// Entries are independent, so a cold window (a first --since run, or
	// a template/model change) fans out over a small worker pool rather
	// than paying for every endpoint round trip in series. The first
	// failure cancels the rest and is the one reported; order of the
	// returned map is irrelevant, since Fold walks result's own ordering.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The status line shows a done/total counter only: four workers run at
	// once, so attaching the reporter's phase observer would mix their
	// phases. A nil reporter (progress off) makes every call a no-op.
	rep := progress.FromContext(ctx)
	defer rep.Clear()
	total := len(jobs)
	rep.Status(statusLabel(0, total, 0))

	var (
		mu        sync.Mutex
		summaries = map[string]string{}
		stats     Stats
		firstErr  error
		wg        sync.WaitGroup
	)
	next := make(chan job)
	workers := min(summarizeConcurrency, len(jobs))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range next {
				text, hit, err := summarizeEntry(ctx, j.slug, result.WindowStart == result.Day, result.Day, j.row, client, cacheDir, refresh)
				mu.Lock()
				switch {
				case err != nil:
					if firstErr == nil {
						firstErr = err
						cancel()
					}
				case hit:
					summaries[j.row.Item.Key.DirName()] = text
					stats.CacheHits++
				default:
					summaries[j.row.Item.Key.DirName()] = text
					stats.Requests++
				}
				if err == nil {
					rep.Status(statusLabel(stats.CacheHits+stats.Requests, total, stats.CacheHits))
				}
				mu.Unlock()
			}
		}()
	}
dispatch:
	for _, j := range jobs {
		select {
		case next <- j:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(next)
	wg.Wait()

	if firstErr != nil {
		return nil, stats, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	return summaries, stats, nil
}

// statusLabel renders Summarize's status line text. The cached part is
// omitted while no job has been served from the cache.
func statusLabel(done, total, cached int) string {
	label := fmt.Sprintf("summarizing %d/%d", done, total)
	if cached > 0 {
		label += fmt.Sprintf(" · %d cached", cached)
	}
	return label
}

// summarizeConcurrency caps Summarize's in-flight endpoint requests: enough
// to cut a cold window's wall time several-fold, few enough to stay polite
// to a local or rate-limited endpoint.
const summarizeConcurrency = 4

// summarizeEntry is Summarize's own per-entry step: render, then either
// serve a cache hit or call the endpoint and store the fresh result.
func summarizeEntry(ctx context.Context, groupSlug string, singleDay bool, topDay journal.Day, row retroplumbing.EntryRow, client *llm.Client, cacheDir string, refresh bool) (text string, cacheHit bool, err error) {
	e := summaryEntry(groupSlug, singleDay, topDay, row)
	rendered, err := prompt.Render(prompt.RetroSummary, e.PromptData())
	if err != nil {
		return "", false, clasterr.New("retro.prompt-unavailable",
			fmt.Sprintf("retro: resolving the retro-summary prompt pair: %v", err))
	}

	// The key leaves the project out (retrocache.EntryKey's doc): a
	// project backfill must not re-summarize every backfilled session.
	fingerprint, err := retrocache.EntryKey(e, client.Model())
	if err != nil {
		return "", false, clasterr.New("retro.prompt-unavailable",
			fmt.Sprintf("retro: resolving the retro-summary prompt pair: %v", err))
	}
	if !refresh {
		if cached, ok := retrocache.Get(cacheDir, fingerprint); ok {
			return cached, true, nil
		}
	}

	text, err = client.Complete(ctx, rendered.System, rendered.User)
	if err != nil {
		return "", false, clasterr.New("retro.llm-request-failed",
			fmt.Sprintf("retro: summarizing session %s: %v", row.Item.Key.DirName(), err))
	}

	// Best-effort: a cache write failure (an unwritable $XDG_CACHE_HOME,
	// a permissions problem) costs nothing but a repeat LLM call next
	// run — never this run's own correctness (retrocache.Put's doc).
	_ = retrocache.Put(cacheDir, fingerprint, text)

	return text, false, nil
}

// summaryEntry maps one EntryRow onto the facts the retro-summary prompt
// is rendered from (retrocache.Entry) — flows/retro.md §2's own list
// ("the project, the session's started_at, the top-level day (when the
// window is a single day), the session id, and the entry body").
//
// Two porcelain-owned findings fill gaps §2 leaves open:
//   - the project renders unprojectedLabel ("(no project)") for the
//     no-project bucket rather than retro's internal "-" sentinel or a
//     bare empty string — the same readable convention plumbing retro's
//     own writeHuman heading already uses for a human reader; here it
//     also reaches the model's own input.
//   - the day is the top-level window day only when the window is a
//     single day (§2's own stated case); when --since widened the window
//     across several days, §2 names no fallback, so this falls back to
//     the entry's own row.Day (the calendar day its session actually
//     falls on) — always a real, single day, never the ambiguous range.
//     In a single-day window the two are the same day, so the day is
//     always row.Day in effect; analyze relies on that to key entries
//     without knowing retro's window.
func summaryEntry(groupSlug string, singleDay bool, topDay journal.Day, row retroplumbing.EntryRow) retrocache.Entry {
	project := groupSlug
	if groupSlug == unprojectedSlug {
		project = unprojectedLabel
	}

	day := row.Day
	if singleDay {
		day = topDay
	}

	return retrocache.Entry{
		Project:   project,
		Day:       day,
		StartedAt: row.Item.Session.StartedAt,
		SessionID: row.Item.Session.SessionID,
		Body:      row.Entry.Body,
	}
}

// unprojectedSlug is retro's own no-project grouping key ("-",
// internal/verbs/retro's unexported unprojectedKey), repeated here for
// the same reason retro.go's own projectKey doc gives for repeating
// wake's projectKey: no verb package imports another verb's unexported
// surface, so the same sentinel value is duplicated at its one other
// call site.
const unprojectedSlug = "-"

// Fold implements flows/retro.md §3: attach each session's summary
// (produced by Summarize, keyed by DirName) to its own EntryRow, within
// its project's group, preserving retroplumbing.Result's own ordering
// untouched (groups alphabetical by slug, no-project bucket first;
// global breadcrumbs reported separately) — Fold only ever attaches, it
// never reorders or re-groups.
func Fold(result retroplumbing.Result, summaries map[string]string) Document {
	groups := make([]FoldedGroup, len(result.Groups))
	for i, g := range result.Groups {
		entries := make([]FoldedEntry, len(g.Entries))
		for j, row := range g.Entries {
			entries[j] = FoldedEntry{EntryRow: row, Summary: summaries[row.Item.Key.DirName()]}
		}
		groups[i] = FoldedGroup{
			Slug:        g.Slug,
			Sessions:    g.Sessions,
			Entries:     entries,
			Breadcrumbs: g.Breadcrumbs,
		}
	}
	return Document{
		Day:               result.Day,
		WindowStart:       result.WindowStart,
		Groups:            groups,
		GlobalBreadcrumbs: result.GlobalBreadcrumbs,
	}
}
