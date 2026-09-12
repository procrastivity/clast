package retro

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// defaultDayArg is retro's own default when no positional is given (V8:
// "default day: yesterday") — journal.ParseDay's own "yesterday" case
// resolves it against the current instant and cutoff (MODEL M8), the
// same seam breadcrumbs' --day "today" default already leans on.
const defaultDayArg = "yesterday"

// sessionSchema/sessionRequired are the session.json fact set every row's
// --json shape embeds verbatim — brief's/wake's own schema fragment,
// repeated here since no schema-fragment package exists to import from
// (every other document verb duplicates this the same way).
const sessionSchema = `"schema_version": {"type": "integer"},
	"harness": {"type": "string"},
	"session_id": {"type": "string"},
	"machine": {"type": "string"},
	"project": {
		"type": "object",
		"properties": {
			"id": {"type": "string"},
			"slug": {"type": "string"},
			"clone": {"type": "string"},
			"label": {"type": "string"},
			"path": {"type": "string"}
		},
		"required": ["id", "slug", "clone", "label", "path"]
	},
	"worktree": {"type": "string"},
	"branch": {"type": "string"},
	"started_at": {"type": "string"},
	"last_active_at": {"type": "string"},
	"captured_at": {"type": "string"},
	"source_path": {"type": "string"},
	"counts": {
		"type": "object",
		"properties": {
			"user": {"type": "integer"},
			"assistant": {"type": "integer"}
		},
		"required": ["user", "assistant"]
	},
	"substantive": {"type": "boolean"},
	"transcript": {
		"type": "object",
		"properties": {
			"format": {"type": "string"},
			"lines": {"type": "integer"},
			"sha256": {"type": "string"}
		},
		"required": ["format", "lines", "sha256"]
	}`

const sessionRequired = `"schema_version", "harness", "session_id", "machine", "worktree",
		"branch", "started_at", "last_active_at", "captured_at",
		"source_path", "counts", "substantive", "transcript"`

// breadcrumbSchema is one breadcrumb's shape, embedded both per-project
// and at the top level (global) — breadcrumbs'/brief's own crumbJSON
// shape.
const breadcrumbSchema = `{
	"type": "object",
	"properties": {
		"at": {"type": "string"},
		"slug": {"type": ["string", "null"]},
		"text": {"type": "string"},
		"machine": {"type": "string"}
	},
	"required": ["at", "slug", "text", "machine"]
}`

// outputSchema is retro's --json payload shape (V8/V35: filled — the
// retro flow's step 1 is `clast plumbing retro --json`). Each project
// group carries the session fact set (state/stale/title) for its
// sessions, the session fact set plus title/tags/body for its entries
// (brief's own embedding — V8 says to follow that posture rather than
// invent a second one), and its own breadcrumbs; global_breadcrumbs sits
// outside every group (finding: a slug-null crumb names no project).
var outputSchema = fmt.Sprintf(`{
	"type": "object",
	"properties": {
		"day": {"type": "string"},
		"window_start": {"type": "string"},
		"projects": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"project": {"type": "string"},
					"sessions": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								%[1]s,
								"state": {"type": "string"},
								"stale": {"type": "boolean"},
								"title": {"type": "string"}
							},
							"required": [%[2]s, "state", "stale", "title"]
						}
					},
					"entries": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								%[1]s,
								"title": {"type": "string"},
								"tags": {"type": "array", "items": {"type": "string"}},
								"body": {"type": "string"}
							},
							"required": [%[2]s, "title", "tags", "body"]
						}
					},
					"breadcrumbs": {"type": "array", "items": %[3]s}
				},
				"required": ["project", "sessions", "entries", "breadcrumbs"]
			}
		},
		"global_breadcrumbs": {"type": "array", "items": %[3]s}
	},
	"required": ["day", "window_start", "projects", "global_breadcrumbs"]
}`, sessionSchema, sessionRequired, breadcrumbSchema)

// Command constructs the `clast plumbing retro [<day>]` verb (SURFACE
// V8/V20). The Use line records the positional verbatim, exactly as V8's
// own spec bullet writes it (C3.8) — brief's/show's own convention.
func Command(streams *iostreams.Streams) *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "retro [<day>]",
		Short: "the retro shape's day→project document (V8/V20)",
		Long: "retro emits the retro shape's day→project document: per project, the day's sessions " +
			"with state and titles, the entry body for sessions that have one, and the day's " +
			"breadcrumbs. <day> is the V5 day grammar (YYYY-MM-DD, today, yesterday, -Nd — the same " +
			"journal.ParseDay every --day flag on this surface already accepts); default: yesterday. " +
			"--since <duration> widens the single day into a window ending at <day> and starting " +
			"<duration> earlier (-Nd or -Nw; day - duration through day, inclusive) — absent, the " +
			"window is exactly the one day. Unlike sessions'/wake's/brief's own --since, \"all\" is not " +
			"accepted here: retro's window is anchored to a fixed day, never to \"now\", and " +
			"breadcrumbs have no unbounded reader to widen against (finding). A project named only by " +
			"a breadcrumb in the window (no session) still gets its own group; a slug-null breadcrumb " +
			"is reported separately, outside every project group. Groups sort alphabetically by " +
			"project slug (the no-project bucket first). Human output is a readable markdown document; " +
			"--json is the structure, schema filled. Pure query: no writes, no LLM calls — the " +
			"fingerprinted summary cache (V7/V11) is the llm retro verb's own private state, never " +
			"this verb's.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			dayArg := defaultDayArg
			if len(args) == 1 {
				dayArg = args[0]
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return err
			}
			cutoff, err := journal.ConfiguredCutoff(cfg)
			if err != nil {
				return err
			}
			day, err := journal.ParseDay(dayArg, cutoff)
			if err != nil {
				return err
			}
			windowStart, err := resolveWindowStart(day, since)
			if err != nil {
				return err
			}

			result, err := Run(root, day, windowStart, cutoff)
			if err != nil {
				return err
			}

			if flags.Verbose {
				total := 0
				for _, g := range result.Groups {
					total += len(g.Sessions)
				}
				if _, err := fmt.Fprintf(streams.Err, "retro: %s to %s, %d project(s), %d session(s)\n",
					result.WindowStart, result.Day, len(result.Groups), total); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "widen <day> into a window this many days back (-Nd or -Nw); default: no widening, exactly <day>")

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// resolveWindowStart is retro's own day+--since composition (finding,
// documented on Command's Long and on Result.WindowStart): absent
// --since, the window is exactly day; given it, journal.ParseDuration
// parses the same "-Nd"/"-Nw" grammar sessions'/wake's own --since
// accepts (but never "all" — see Command's doc for why), and the window's
// lower bound is day shifted back that many calendar days. day is always
// the window's fixed upper bound; --since only ever widens backward from
// it, never forward.
func resolveWindowStart(day journal.Day, since string) (journal.Day, error) {
	if since == "" {
		return day, nil
	}
	n, err := journal.ParseDuration(since)
	if err != nil {
		return "", err
	}
	return day.AddDays(-n)
}

// displaySlug renders a group's slug for --json/human output: "" for the
// no-project bucket (unprojectedKey) — the same "" convention brief's own
// CurrentWorkspace already uses for "no value" — rather than leaking the
// internal sentinel.
func displaySlug(slug string) string {
	if slug == unprojectedKey {
		return ""
	}
	return slug
}

// sessionRowJSON is one session's --json shape — sessions'/wake's/
// brief's own rowJSON, repeated here for the same unexported-elsewhere
// reason those three duplicate it from each other.
type sessionRowJSON struct {
	journal.Session
	State string `json:"state"`
	Stale bool   `json:"stale"`
	Title string `json:"title"`
}

// entryRowJSON is one gathered entry's --json shape — brief's own
// entryJSON, repeated here.
type entryRowJSON struct {
	journal.Session
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
}

// breadcrumbJSON is one breadcrumb's --json shape — breadcrumbs'/brief's
// own crumbJSON, repeated here.
type breadcrumbJSON struct {
	At      time.Time `json:"at"`
	Slug    *string   `json:"slug"`
	Text    string    `json:"text"`
	Machine string    `json:"machine"`
}

// projectJSON is one project group's --json shape.
type projectJSON struct {
	Project     string           `json:"project"`
	Sessions    []sessionRowJSON `json:"sessions"`
	Entries     []entryRowJSON   `json:"entries"`
	Breadcrumbs []breadcrumbJSON `json:"breadcrumbs"`
}

func sessionRowPayload(r SessionRow) sessionRowJSON {
	return sessionRowJSON{
		Session: r.Item.Session,
		State:   string(r.Item.State()),
		Stale:   r.Item.Stale(),
		Title:   r.Title,
	}
}

func entryRowPayload(r EntryRow) entryRowJSON {
	tags := r.Entry.Tags
	if tags == nil {
		tags = []string{}
	}
	return entryRowJSON{Session: r.Item.Session, Title: r.Entry.Title, Tags: tags, Body: r.Entry.Body}
}

func breadcrumbPayload(c journal.BreadcrumbEntry) breadcrumbJSON {
	return breadcrumbJSON{At: c.At, Slug: c.Slug, Text: c.Text, Machine: c.Machine}
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	projects := make([]projectJSON, len(result.Groups))
	for i, g := range result.Groups {
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

	global := make([]breadcrumbJSON, len(result.GlobalBreadcrumbs))
	for i, c := range result.GlobalBreadcrumbs {
		global[i] = breadcrumbPayload(c)
	}

	payload := struct {
		Day               string           `json:"day"`
		WindowStart       string           `json:"window_start"`
		Projects          []projectJSON    `json:"projects"`
		GlobalBreadcrumbs []breadcrumbJSON `json:"global_breadcrumbs"`
	}{
		Day:               string(result.Day),
		WindowStart:       string(result.WindowStart),
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

// writeHuman renders the day→project document as readable markdown (V8:
// a document verb, not a listing) — no byte promise (HANDOFF §8).
func writeHuman(streams *iostreams.Streams, result Result) error {
	if result.WindowStart == result.Day {
		if _, err := fmt.Fprintf(streams.Out, "# Retro: %s\n", result.Day); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(streams.Out, "# Retro: %s to %s\n", result.WindowStart, result.Day); err != nil {
			return err
		}
	}

	if len(result.Groups) == 0 && len(result.GlobalBreadcrumbs) == 0 {
		_, err := fmt.Fprintln(streams.Out, "\nNothing happened in this window.")
		return err
	}

	for _, g := range result.Groups {
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
			if _, err := fmt.Fprintln(streams.Out, "\n### Entries"); err != nil {
				return err
			}
			for i, e := range g.Entries {
				if i > 0 {
					if _, err := fmt.Fprintln(streams.Out, "\n---"); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(streams.Out, "\n#### %s (%s, %s)\n\n%s\n",
					e.Entry.Title, e.Day, e.Item.Key.DirName(), e.Entry.Body); err != nil {
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

	if len(result.GlobalBreadcrumbs) > 0 {
		if _, err := fmt.Fprintln(streams.Out, "\n## Global breadcrumbs"); err != nil {
			return err
		}
		for _, c := range result.GlobalBreadcrumbs {
			if _, err := fmt.Fprintf(streams.Out, "- %s  %s\n", c.At.Local().Format("15:04"), c.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

// stateColumn renders a session's state column, appending " (stale)" per
// M7 when it applies — wake's/sessions'/brief's own stateColumn, repeated
// here.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}
