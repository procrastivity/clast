package wake

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
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

// outputSchema is wake's --json payload shape (V8/V35: filled — the wake
// flow's step 1 is `clast plumbing wake --json`). Each row is exactly
// `sessions --json`'s own shape (V17): the full session.json fact set
// plus state/stale/title — V8 says to follow that embedding posture
// rather than invent a second one, so the schema below is sessions' own,
// carried here verbatim.
const outputSchema = `{
	"type": "object",
	"properties": {
		"sessions": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"schema_version": {"type": "integer"},
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
					},
					"state": {"type": "string"},
					"stale": {"type": "boolean"},
					"title": {"type": "string"}
				},
				"required": [
					"schema_version", "harness", "session_id", "machine", "worktree",
					"branch", "started_at", "last_active_at", "captured_at",
					"source_path", "counts", "substantive", "transcript",
					"state", "stale", "title"
				]
			}
		}
	},
	"required": ["sessions"]
}`

// Command constructs the `clast plumbing wake` verb (SURFACE V8/V20).
func Command(streams *iostreams.Streams) *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "wake",
		Short: "the wake shape's deterministic working set (V8/V20)",
		Long: "wake emits the wake shape's working set: every session in state captured, plus every " +
			"curated session that is stale (MODEL M7 — its transcript grew since curation); a " +
			"curated-but-fresh or dismissed session never appears. --since <duration|all> restricts " +
			"to sessions on or after that recent window (default: the since config key, V31 — the " +
			"same resolution `sessions` uses, not a second path). Order is project-grouped, the " +
			"most-recently-active project first, chronological within each group (SURFACE V7/V8) — a " +
			"plumbing fact, not presentation. Human output is a readable markdown document; --json " +
			"emits the working set in the shape `sessions --json` already embeds (session facts plus " +
			"state/stale/title). Pure query: no writes, no LLM calls.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())

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
			sinceBound, err := query.ResolveSince(since, defaultSince, cutoff, time.Now())
			if err != nil {
				return err
			}

			rows, err := Run(root, sinceBound, cutoff)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "wake: %d session(s) in the working set\n", len(rows)); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, rows)
			}
			return writeHuman(streams, rows)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "restrict to sessions on or after this recent window (-Nd, -Nw, or all; default: the since config key)")

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// rowJSON is one working-set session's --json shape: sessions' own
// rowJSON (V17), reused verbatim rather than forked (unexported there, so
// duplicated here rather than imported).
type rowJSON struct {
	journal.Session
	State string `json:"state"`
	Stale bool   `json:"stale"`
	Title string `json:"title"`
}

func writeJSON(streams *iostreams.Streams, rows []Row) error {
	out := make([]rowJSON, len(rows))
	for i, r := range rows {
		out[i] = rowJSON{
			Session: r.Item.Session,
			State:   string(r.Item.State()),
			Stale:   r.Item.Stale(),
			Title:   r.Title,
		}
	}
	payload := struct {
		Sessions []rowJSON `json:"sessions"`
	}{Sessions: out}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// displayGroup is one project's contiguous run of rows, as Run's own
// ordering already produced it — writeHuman re-detects the run boundaries
// (by projectKey) rather than re-deriving the grouping itself.
type displayGroup struct {
	heading string
	rows    []Row
}

func groupRowsForDisplay(rows []Row) []displayGroup {
	var groups []displayGroup
	var currentKey string
	for _, r := range rows {
		key := projectKey(r.Item)
		if len(groups) == 0 || key != currentKey {
			groups = append(groups, displayGroup{heading: projectHeading(r.Item)})
			currentKey = key
		}
		last := &groups[len(groups)-1]
		last.rows = append(last.rows, r)
	}
	return groups
}

// projectHeading renders a group's markdown heading: "<slug>/<label>" for
// a frozen project (mirrors sessions' own human projectLabel column), or
// "(no project)" for the unprojected bucket.
func projectHeading(it journal.WalkItem) string {
	if it.Session.Project == nil {
		return "(no project)"
	}
	return it.Session.Project.Slug + "/" + it.Session.Project.Label
}

// stateColumn renders a session's state column, appending " (stale)" per
// M7 when it applies — sessions' own stateColumn (V17), duplicated here
// for the same unexported-elsewhere reason as rowJSON.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}

// writeHuman renders the working set as a readable markdown document
// (V8: this is a document verb, not a one-line-per-row listing like
// `sessions`) — a title, a total, then one `##` section per project in
// Run's own order, each a short list of its sessions in chronological
// order. No byte promise (HANDOFF §8); layout is this build's own call.
func writeHuman(streams *iostreams.Streams, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(streams.Out, "# Wake\n\nNothing in the working set.")
		return err
	}

	if _, err := fmt.Fprintf(streams.Out, "# Wake\n\n%d session(s) in the working set.\n", len(rows)); err != nil {
		return err
	}

	for _, g := range groupRowsForDisplay(rows) {
		if _, err := fmt.Fprintf(streams.Out, "\n## %s\n\n", g.heading); err != nil {
			return err
		}
		w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
		for _, r := range g.rows {
			line := fmt.Sprintf("- %s\t%s\t%s", r.Item.Key.DirName(), r.Day, stateColumn(r.Item))
			if r.Item.State() == journal.StateCurated && r.Title != "" {
				line += "\t" + r.Title
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}
