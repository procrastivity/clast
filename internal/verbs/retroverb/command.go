package retroverb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/surface"
	retroplumbing "github.com/procrastivity/clast/internal/verbs/retro"
)

// Command constructs the top-level `clast retro [<day>]` verb (SURFACE
// V11): the retro shape's verb form. <day> and --since mirror `plumbing
// retro`'s own surface exactly (day/--since resolution posture, task
// brief) — retroplumbing.DefaultDayArg/ResolveWindowStart are plumbing
// retro's own exported default/composition (llm-verbs seal sweep nit: no
// longer hand-copied here — the wake.ConfiguredAutoMinChars precedent).
func Command(streams *iostreams.Streams) *cobra.Command {
	var since string
	var refresh bool

	cmd := &cobra.Command{
		Use:   "retro [<day>]",
		Short: "day→project retrospective, summarized (shape, flows/retro.md)",
		Long: "retro renders a day→project retrospective: for each project active on the target day " +
			"(or window), its sessions, its curated entries condensed into short summaries, and its " +
			"breadcrumbs. <day> is the V5 day grammar (YYYY-MM-DD, today, yesterday, -Nd); default: " +
			"yesterday. --since <duration> widens the single day into a window ending at <day> (-Nd or " +
			"-Nw; never \"all\" — the window is always anchored to a fixed day, the same posture " +
			"`plumbing retro` takes). Every session with a curated entry is summarized through the " +
			"configured LLM endpoint (SURFACE V12) using the retro-summary prompt pair; a session with " +
			"no entry body still lists (state, title), with no summary. Summaries are served from a " +
			"content-fingerprinted cache under $XDG_CACHE_HOME/clast/retro/ so an unchanged session is " +
			"not re-summarized on a later run; --refresh bypasses the cache and rewrites every summary " +
			"it produces. A window with no entry-bearing session at all needs no llm configuration — " +
			"no endpoint call is made for it. This flow writes nothing — it renders a document, it does " +
			"not curate.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())
			ctx := cmd.Context()

			dayArg := retroplumbing.DefaultDayArg
			if len(args) == 1 {
				dayArg = args[0]
			}

			cfg, err := config.Load()
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
			}
			cutoff, err := journal.ConfiguredCutoff(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving day cutoff: %v", err))
			}
			day, err := journal.ParseDay(dayArg, cutoff)
			if err != nil {
				return err
			}
			windowStart, err := retroplumbing.ResolveWindowStart(day, since)
			if err != nil {
				return err
			}

			// flows/retro.md §1 — gather the window, via plumbing retro's
			// own Run: same grouping/windowing, no second implementation.
			result, err := retroplumbing.Run(root, day, windowStart, cutoff)
			if err != nil {
				return err
			}

			var summaries map[string]string
			var stats Stats
			if HasEntries(result) {
				// §2 needs the LLM endpoint — constructed only now, never
				// before this check, so a zero-entry window never
				// requires llm configuration at all (mirrors briefverb's
				// own empty-before-client ordering).
				client, err := llm.NewClient(cfg)
				if err != nil {
					return err
				}
				cacheDir, err := CacheDir()
				if err != nil {
					return clasterr.New("validation.config", fmt.Sprintf("resolving retro cache directory: %v", err))
				}

				summaries, stats, err = Summarize(ctx, result, client, cacheDir, refresh)
				if err != nil {
					return err
				}
			}

			// §3 — fold summaries into the document.
			doc := Fold(result, summaries)

			if flags.Verbose {
				total := 0
				for _, g := range doc.Groups {
					total += len(g.Sessions)
				}
				if _, err := fmt.Fprintf(streams.Err,
					"retro: %s to %s, %d project(s), %d session(s), %d summary/summaries (%d cache hit(s), %d request(s))\n",
					doc.WindowStart, doc.Day, len(doc.Groups), total, len(summaries), stats.CacheHits, stats.Requests,
				); err != nil {
					return err
				}
			}

			// §4 — render the document.
			if flags.JSON {
				return writeJSON(streams, doc)
			}
			return writeHuman(streams, doc)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "widen <day> into a window this many days back (-Nd or -Nw); default: no widening, exactly <day>")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "bypass the summary cache and re-summarize every entry-bearing session")

	surface.Annotate(cmd, surface.LLM)
	return cmd
}

// displaySlug renders a group's slug for --json/human output: "" for the
// no-project bucket — plumbing retro's own displaySlug, repeated here —
// rather than leaking the internal "-" sentinel.
func displaySlug(slug string) string {
	if slug == unprojectedSlug {
		return ""
	}
	return slug
}

// --- --json payload (schema UNFILLED — C3.7, the same "no speculative
// schema" posture briefverb's own jsonPayload takes: no consumer yet, so
// manifest.SetOutputSchema is deliberately not called here). Shape:
// plumbing retro's own document shape (day/window_start/projects/
// global_breadcrumbs, and each session/entry/breadcrumb fact set) plus
// one additive field, "summary", on each entry-bearing session — task
// brief's own suggested shape, chosen over inventing a divergent one. ---

type sessionRowJSON struct {
	journal.Session
	State string `json:"state"`
	Stale bool   `json:"stale"`
	Title string `json:"title"`
}

type entryRowJSON struct {
	journal.Session
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Body    string   `json:"body"`
	Summary string   `json:"summary"`
}

type breadcrumbJSON struct {
	At      time.Time `json:"at"`
	Slug    *string   `json:"slug"`
	Text    string    `json:"text"`
	Machine string    `json:"machine"`
}

type projectJSON struct {
	Project     string           `json:"project"`
	Sessions    []sessionRowJSON `json:"sessions"`
	Entries     []entryRowJSON   `json:"entries"`
	Breadcrumbs []breadcrumbJSON `json:"breadcrumbs"`
}

func sessionRowPayload(r retroplumbing.SessionRow) sessionRowJSON {
	return sessionRowJSON{
		Session: r.Item.Session,
		State:   string(r.Item.State()),
		Stale:   r.Item.Stale(),
		Title:   r.Title,
	}
}

func entryRowPayload(e FoldedEntry) entryRowJSON {
	tags := e.Entry.Tags
	if tags == nil {
		tags = []string{}
	}
	return entryRowJSON{
		Session: e.Item.Session,
		Title:   e.Entry.Title,
		Tags:    tags,
		Body:    e.Entry.Body,
		Summary: e.Summary,
	}
}

func breadcrumbPayload(c journal.BreadcrumbEntry) breadcrumbJSON {
	return breadcrumbJSON{At: c.At, Slug: c.Slug, Text: c.Text, Machine: c.Machine}
}

func writeJSON(streams *iostreams.Streams, doc Document) error {
	projects := make([]projectJSON, len(doc.Groups))
	for i, g := range doc.Groups {
		sessions := make([]sessionRowJSON, len(g.Sessions))
		for j, r := range g.Sessions {
			sessions[j] = sessionRowPayload(r)
		}
		entries := make([]entryRowJSON, len(g.Entries))
		for j, e := range g.Entries {
			entries[j] = entryRowPayload(e)
		}
		crumbs := make([]breadcrumbJSON, len(g.Breadcrumbs))
		for j, c := range g.Breadcrumbs {
			crumbs[j] = breadcrumbPayload(c)
		}
		projects[i] = projectJSON{Project: displaySlug(g.Slug), Sessions: sessions, Entries: entries, Breadcrumbs: crumbs}
	}

	global := make([]breadcrumbJSON, len(doc.GlobalBreadcrumbs))
	for i, c := range doc.GlobalBreadcrumbs {
		global[i] = breadcrumbPayload(c)
	}

	payload := struct {
		Day               string           `json:"day"`
		WindowStart       string           `json:"window_start"`
		Projects          []projectJSON    `json:"projects"`
		GlobalBreadcrumbs []breadcrumbJSON `json:"global_breadcrumbs"`
	}{
		Day:               string(doc.Day),
		WindowStart:       string(doc.WindowStart),
		Projects:          projects,
		GlobalBreadcrumbs: global,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman renders the folded document as readable markdown — plumbing
// retro's own writeHuman, with each entry's full body replaced by its
// condensed summary (flows/retro.md §3: "a condensed summary in place of
// the full entry body"). No byte promise (HANDOFF §8).
func writeHuman(streams *iostreams.Streams, doc Document) error {
	if doc.WindowStart == doc.Day {
		if _, err := fmt.Fprintf(streams.Out, "# Retro: %s\n", doc.Day); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(streams.Out, "# Retro: %s to %s\n", doc.WindowStart, doc.Day); err != nil {
			return err
		}
	}

	if len(doc.Groups) == 0 && len(doc.GlobalBreadcrumbs) == 0 {
		_, err := fmt.Fprintln(streams.Out, "\nNothing happened in this window.")
		return err
	}

	for _, g := range doc.Groups {
		heading := displaySlug(g.Slug)
		if heading == "" {
			heading = "(no project)"
		}
		if _, err := fmt.Fprintf(streams.Out, "\n## %s\n", heading); err != nil {
			return err
		}

		if len(g.Sessions) > 0 {
			if _, err := fmt.Fprintln(streams.Out, "\n### Sessions"); err != nil {
				return err
			}
			for _, r := range g.Sessions {
				line := fmt.Sprintf("- %s  %s  %s", r.Item.Key.DirName(), r.Day, stateColumn(r.Item))
				if r.Item.State() == journal.StateCurated && r.Title != "" {
					line += "  " + r.Title
				}
				if _, err := fmt.Fprintln(streams.Out, line); err != nil {
					return err
				}
			}
		}

		if len(g.Entries) > 0 {
			if _, err := fmt.Fprintln(streams.Out, "\n### Summaries"); err != nil {
				return err
			}
			for i, e := range g.Entries {
				if i > 0 {
					if _, err := fmt.Fprintln(streams.Out, "\n---"); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(streams.Out, "\n#### %s (%s, %s)\n\n%s\n",
					e.Entry.Title, e.Day, e.Item.Key.DirName(), e.Summary); err != nil {
					return err
				}
			}
		}

		if len(g.Breadcrumbs) > 0 {
			if _, err := fmt.Fprintln(streams.Out, "\n### Breadcrumbs"); err != nil {
				return err
			}
			for _, c := range g.Breadcrumbs {
				if _, err := fmt.Fprintf(streams.Out, "- %s  %s\n", c.At.Local().Format("15:04"), c.Text); err != nil {
					return err
				}
			}
		}
	}

	if len(doc.GlobalBreadcrumbs) > 0 {
		if _, err := fmt.Fprintln(streams.Out, "\n## Global breadcrumbs"); err != nil {
			return err
		}
		for _, c := range doc.GlobalBreadcrumbs {
			if _, err := fmt.Fprintf(streams.Out, "- %s  %s\n", c.At.Local().Format("15:04"), c.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

// stateColumn renders a session's state column, appending " (stale)" per
// M7 when it applies — plumbing retro's/wake's/sessions'/brief's own
// stateColumn, repeated here.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}
