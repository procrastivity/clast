package brief

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/query"
	"github.com/procrastivity/clast/internal/surface"
)

// sessionSchema is the session.json fact set every entry/session row's
// --json shape embeds verbatim — sessions'/wake's own outputSchema
// fragment, repeated here rather than shared (no schema-fragment package
// exists to import from; every other document verb duplicates this the
// same way).
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

// sessionRequired is the required-key list every session.json fact set
// carries (sessions'/wake's own outputSchema fragment).
const sessionRequired = `"schema_version", "harness", "session_id", "machine", "worktree",
		"branch", "started_at", "last_active_at", "captured_at",
		"source_path", "counts", "substantive", "transcript"`

// outputSchema is brief's --json payload shape (V8/V35: filled — the
// brief flow's step 1 is `clast plumbing brief --json`). Each entry row
// is a session fact set plus its curated entry's title/tags/body (brief
// gathers content, unlike sessions/wake's title-only rows); each session
// row is a session fact set plus state/stale/title (sessions'/wake's own
// embedding, V8 says to follow it rather than invent a second one).
var outputSchema = fmt.Sprintf(`{
	"type": "object",
	"properties": {
		"project": {"type": "string"},
		"current_workspace": {"type": "string"},
		"groups": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"workspace": {"type": "string"},
					"branch": {"type": "string"},
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
					}
				},
				"required": ["workspace", "branch", "entries"]
			}
		},
		"breadcrumbs": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"at": {"type": "string"},
					"slug": {"type": ["string", "null"]},
					"text": {"type": "string"},
					"machine": {"type": "string"}
				},
				"required": ["at", "slug", "text", "machine"]
			}
		},
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
		"empty": {"type": "boolean"}
	},
	"required": ["project", "current_workspace", "groups", "breadcrumbs", "sessions", "empty"]
}`, sessionSchema, sessionRequired)

// Command constructs the `clast plumbing brief [<project>]` verb (SURFACE
// V8/V20). The Use line records the positional verbatim, exactly as V8's
// own spec bullet writes it (C3.8).
func Command(streams *iostreams.Streams) *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "brief [<project>]",
		Short: "the brief shape's gathered material (V8/V20)",
		Long: "brief emits the brief shape's gathered material for one project: recent curated entries " +
			"grouped by workspace (label, falling back to branch), the current workspace hoisted first, " +
			"capped 3 per group / 8 total; plus today's breadcrumbs and today's sessions for the " +
			"project. <project> is a registry locator (id or slug, the same shape `clones [<project>]` " +
			"accepts); with none, the project defaults from the cwd (the whereami path, V22) — an " +
			"unregistered cwd refuses (refusal.unknown-clone, naming `clast init`), a cwd outside any " +
			"git repository is validation.not-a-git-repo. --since <duration|all> restricts the recent- " +
			"entries window (default: the since config key, V31 — the same resolution `sessions`/`wake` " +
			"use); there is no --limit, the 3/8 caps are owned here (V7). `empty: true` when the " +
			"entries, breadcrumbs, and sessions are all empty — human output states that case readably. " +
			"Human output is a readable markdown document; --json is the structure, schema filled. Pure " +
			"query: no writes, no LLM calls.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			var projectLocator string
			if len(args) == 1 {
				projectLocator = args[0]
			}

			dir, err := os.Getwd()
			if err != nil {
				return err
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
			defaultSince, err := query.ConfiguredSince(cfg)
			if err != nil {
				return err
			}
			now := time.Now()
			sinceBound, err := query.ResolveSince(since, defaultSince, cutoff, now)
			if err != nil {
				return err
			}
			today := cutoff.DayOf(now)

			result, err := Run(cmd.Context(), root, dir, projectLocator, cutoff, sinceBound, today)
			if err != nil {
				return err
			}

			if flags.Verbose {
				total := 0
				for _, g := range result.Groups {
					total += len(g.Entries)
				}
				if _, err := fmt.Fprintf(streams.Err, "brief: %d entr(y/ies), %d breadcrumb(s), %d session(s) for %s\n",
					total, len(result.Breadcrumbs), len(result.Sessions), result.ProjectSlug); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "restrict recent curated entries to sessions on or after this window (-Nd, -Nw, or all; default: the since config key)")

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// entryJSON is one gathered entry's --json shape: the session fact set
// (embedded) plus the curated entry's own title/tags/body — show's own
// entry sub-shape (internal/verbs/show/command.go), repeated here since
// brief's entry rides at the row's top level rather than nested under an
// "entry" key.
type entryJSON struct {
	journal.Session
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
}

func entryPayload(r EntryRow) entryJSON {
	tags := r.Entry.Tags
	if tags == nil {
		tags = []string{}
	}
	return entryJSON{Session: r.Item.Session, Title: r.Entry.Title, Tags: tags, Body: r.Entry.Body}
}

// groupJSON is one workspace's --json shape.
type groupJSON struct {
	Workspace string      `json:"workspace"`
	Branch    string      `json:"branch"`
	Entries   []entryJSON `json:"entries"`
}

// sessionRowJSON is one of today's sessions' --json shape — sessions'/
// wake's own rowJSON, repeated here for the same unexported-elsewhere
// reason those two duplicate it from each other.
type sessionRowJSON struct {
	journal.Session
	State string `json:"state"`
	Stale bool   `json:"stale"`
	Title string `json:"title"`
}

// breadcrumbJSON is one breadcrumb's --json shape — breadcrumbs' own
// crumbJSON (internal/verbs/breadcrumbs/command.go), repeated here.
type breadcrumbJSON struct {
	At      time.Time `json:"at"`
	Slug    *string   `json:"slug"`
	Text    string    `json:"text"`
	Machine string    `json:"machine"`
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	groups := make([]groupJSON, len(result.Groups))
	for i, g := range result.Groups {
		entries := make([]entryJSON, len(g.Entries))
		for j, e := range g.Entries {
			entries[j] = entryPayload(e)
		}
		groups[i] = groupJSON{Workspace: g.Workspace, Branch: g.Branch, Entries: entries}
	}

	crumbs := make([]breadcrumbJSON, len(result.Breadcrumbs))
	for i, c := range result.Breadcrumbs {
		crumbs[i] = breadcrumbJSON{At: c.At, Slug: c.Slug, Text: c.Text, Machine: c.Machine}
	}

	sessions := make([]sessionRowJSON, len(result.Sessions))
	for i, r := range result.Sessions {
		sessions[i] = sessionRowJSON{
			Session: r.Item.Session,
			State:   string(r.Item.State()),
			Stale:   r.Item.Stale(),
			Title:   r.Title,
		}
	}

	payload := struct {
		Project          string           `json:"project"`
		CurrentWorkspace string           `json:"current_workspace"`
		Groups           []groupJSON      `json:"groups"`
		Breadcrumbs      []breadcrumbJSON `json:"breadcrumbs"`
		Sessions         []sessionRowJSON `json:"sessions"`
		Empty            bool             `json:"empty"`
	}{
		Project:          result.ProjectSlug,
		CurrentWorkspace: result.CurrentWorkspace,
		Groups:           groups,
		Breadcrumbs:      crumbs,
		Sessions:         sessions,
		Empty:            result.Empty,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman renders the brief's gathered material as a readable markdown
// document (V8: a document verb, not a listing) — no byte promise
// (HANDOFF §8). The empty case (V7's short-circuit fact) states itself
// readably rather than printing an empty document.
func writeHuman(streams *iostreams.Streams, result Result) error {
	if _, err := fmt.Fprintf(streams.Out, "# Brief: %s\n", result.ProjectSlug); err != nil {
		return err
	}

	if result.Empty {
		_, err := fmt.Fprintf(streams.Out,
			"\nNothing gathered — no curated entries, breadcrumbs, or sessions today. "+
				"Run `clast plumbing wake` to curate recent sessions, or check "+
				"`clast plumbing sessions --project %s`.\n", result.ProjectSlug)
		return err
	}

	if result.CurrentWorkspace != "" {
		if _, err := fmt.Fprintf(streams.Out, "\nCurrent workspace: %s\n", result.CurrentWorkspace); err != nil {
			return err
		}
	}

	for _, g := range result.Groups {
		if _, err := fmt.Fprintf(streams.Out, "\n## Workspace: %s (branch: %s)\n", g.Workspace, emptyAs(g.Branch, "(detached)")); err != nil {
			return err
		}
		for i, e := range g.Entries {
			if i > 0 {
				if _, err := fmt.Fprintln(streams.Out, "\n---"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(streams.Out, "\n### %s (%s, %s)\n\n%s\n",
				e.Entry.Title, e.Day, e.Item.Key.DirName(), e.Entry.Body); err != nil {
				return err
			}
		}
	}

	if len(result.Breadcrumbs) > 0 {
		if _, err := fmt.Fprintln(streams.Out, "\n## Breadcrumbs (today)"); err != nil {
			return err
		}
		for _, c := range result.Breadcrumbs {
			if _, err := fmt.Fprintf(streams.Out, "- %s  %s\n", c.At.Local().Format("15:04"), c.Text); err != nil {
				return err
			}
		}
	}

	if len(result.Sessions) > 0 {
		if _, err := fmt.Fprintln(streams.Out, "\n## Sessions (today)"); err != nil {
			return err
		}
		for _, r := range result.Sessions {
			line := fmt.Sprintf("- %s  %s", r.Item.Key.DirName(), stateColumn(r.Item))
			if r.Item.State() == journal.StateCurated && r.Title != "" {
				line += "  " + r.Title
			}
			if _, err := fmt.Fprintln(streams.Out, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// stateColumn renders a session's state column, appending " (stale)" per
// M7 when it applies — wake's/sessions' own stateColumn, repeated here.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
