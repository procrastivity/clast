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
// carve-out), implemented in cache.go and wired in from command.go.
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
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/prompt"
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
// fingerprint. Returns every summary keyed by its session's directory
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
	summaries := map[string]string{}
	var stats Stats

	singleDay := result.WindowStart == result.Day
	for _, g := range result.Groups {
		for _, row := range g.Entries {
			text, hit, err := summarizeEntry(ctx, g.Slug, singleDay, result.Day, row, client, cacheDir, refresh)
			if err != nil {
				return nil, stats, err
			}
			summaries[row.Item.Key.DirName()] = text
			if hit {
				stats.CacheHits++
			} else {
				stats.Requests++
			}
		}
	}
	return summaries, stats, nil
}

// summarizeEntry is Summarize's own per-entry step: render, then either
// serve a cache hit or call the endpoint and store the fresh result.
func summarizeEntry(ctx context.Context, groupSlug string, singleDay bool, topDay journal.Day, row retroplumbing.EntryRow, client *llm.Client, cacheDir string, refresh bool) (text string, cacheHit bool, err error) {
	rendered, err := prompt.Render(prompt.RetroSummary, summaryPromptData(groupSlug, singleDay, topDay, row))
	if err != nil {
		return "", false, clasterr.New("retro.prompt-unavailable",
			fmt.Sprintf("retro: resolving the retro-summary prompt pair: %v", err))
	}

	fingerprint := Fingerprint(rendered)
	if !refresh {
		if cached, ok := cacheGet(cacheDir, fingerprint); ok {
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
	// run — never this run's own correctness (cache.go's cachePut doc).
	_ = cachePut(cacheDir, fingerprint, text)

	return text, false, nil
}

// summaryPromptData maps one EntryRow onto prompt.RetroSummary's
// user-template placeholders (assets/prompts/retro-summary-user.md):
// {{project}}, {{started_at}}, {{day}}, {{session_id}}, {{body}} —
// flows/retro.md §2's own list ("the project, the session's started_at,
// the top-level day (when the window is a single day), the session id,
// and the entry body").
//
// Two porcelain-owned findings fill gaps §2 leaves open:
//   - {{project}} renders unprojectedLabel ("(no project)") for the
//     no-project bucket rather than retro's internal "-" sentinel or a
//     bare empty string — the same readable convention plumbing retro's
//     own writeHuman heading already uses for a human reader; here it
//     also reaches the model's own input.
//   - {{day}} is the top-level window day only when the window is a
//     single day (§2's own stated case); when --since widened the window
//     across several days, §2 names no fallback, so this falls back to
//     the entry's own row.Day (the calendar day its session actually
//     falls on) — always a real, single day, never the ambiguous range.
func summaryPromptData(groupSlug string, singleDay bool, topDay journal.Day, row retroplumbing.EntryRow) map[string]string {
	project := groupSlug
	if groupSlug == unprojectedSlug {
		project = unprojectedLabel
	}

	day := row.Day
	if singleDay {
		day = topDay
	}

	return map[string]string{
		"project":    project,
		"started_at": row.Item.Session.StartedAt.UTC().Format(time.RFC3339),
		"day":        string(day),
		"session_id": row.Item.Session.SessionID,
		"body":       row.Entry.Body,
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
