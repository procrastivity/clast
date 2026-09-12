package sessions

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

// outputSchema is sessions' --json payload shape (V35: filled — a flow's
// working set is exactly this list, filtered). One row is the full
// session.json fact set plus the three facts only a walk derives: state,
// stale, title.
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

// Command constructs the `clast plumbing sessions` verb (V17).
func Command(streams *iostreams.Streams) *cobra.Command {
	var (
		stateFlags []string
		staleFlag  bool
		project    string
		harness    string
		machine    string
		day        string
		since      string
	)

	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "list sessions, filtered and sorted newest-first",
		Long: "list sessions from the journal tree, newest-first by started_at. Filters compose: " +
			"--state (repeatable: captured, curated, dismissed), --stale (composes with --state curated " +
			"rather than replacing it, M7), --project <slug>, --harness <h>, --machine <name>, " +
			"--day <day>, --since <duration|all> (default: the since config key, V31). Human output is " +
			"one line per session: locator, day, project/label, state (+stale), title when curated — " +
			"no byte promise. --json emits the full session fact set plus state/stale/title.\n\n" +
			"Sources: claude. Planned before 1.0: pi, devin.",
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

			filter, err := buildFilter(cfg, cutoff, stateFlags, staleFlag, project, harness, machine, day, since)
			if err != nil {
				return err
			}

			rows, err := Run(root, filter, cutoff)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "sessions: %d session(s)\n", len(rows)); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, rows)
			}
			return writeHuman(streams, rows)
		},
	}

	cmd.Flags().StringArrayVar(&stateFlags, "state", nil, "restrict to this curation state (repeatable: captured, curated, dismissed)")
	cmd.Flags().BoolVar(&staleFlag, "stale", false, "restrict to stale sessions (M7); composes with --state")
	cmd.Flags().StringVar(&project, "project", "", "restrict to this project's slug")
	cmd.Flags().StringVar(&harness, "harness", "", "restrict to this harness")
	cmd.Flags().StringVar(&machine, "machine", "", "restrict to this machine")
	cmd.Flags().StringVar(&day, "day", "", "restrict to this M8 day bucket (YYYY-MM-DD, today, yesterday, -Nd)")
	cmd.Flags().StringVar(&since, "since", "", "restrict to sessions on or after this recent window (-Nd, -Nw, or all; default: the since config key)")

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// buildFilter validates and assembles this invocation's query.Filter from
// the raw flag values: --harness against the source registry (V30),
// --state against MODEL §2's three states, --day/--since through the V5
// day/duration grammar (journal.ParseDay, query.ResolveSince).
func buildFilter(cfg config.Config, cutoff journal.Cutoff, stateFlags []string, stale bool, project, harness, machine, day, since string) (query.Filter, error) {
	if err := query.ValidateHarness(harness); err != nil {
		return query.Filter{}, err
	}
	states, err := query.ParseStates(stateFlags)
	if err != nil {
		return query.Filter{}, err
	}

	var dayBucket *journal.Day
	if day != "" {
		d, err := journal.ParseDay(day, cutoff)
		if err != nil {
			return query.Filter{}, err
		}
		dayBucket = &d
	}

	defaultSince, err := query.ConfiguredSince(cfg)
	if err != nil {
		return query.Filter{}, err
	}
	sinceBound, err := query.ResolveSince(since, defaultSince, cutoff, time.Now())
	if err != nil {
		return query.Filter{}, err
	}

	return query.Filter{
		States:    states,
		StaleOnly: stale,
		Project:   project,
		Harness:   harness,
		Machine:   machine,
		Day:       dayBucket,
		Since:     sinceBound,
	}, nil
}

// rowJSON is one session's --json shape: every session.json field
// (embedded, MODEL §4) plus the three facts only a walk derives.
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

// writeHuman emits one line per session, in the order the open call
// resolved (finding 1): locator, day, project/label, state (+stale),
// title when curated. No byte promise (HANDOFF §8) — column widths align
// via tabwriter for readability, nothing more.
func writeHuman(streams *iostreams.Streams, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(streams.Out, "no sessions")
		return err
	}
	w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		line := fmt.Sprintf("%s\t%s\t%s\t%s", r.Item.Key.DirName(), r.Day, projectLabel(r.Item), stateColumn(r.Item))
		if r.Item.State() == journal.StateCurated && r.Title != "" {
			line += "\t" + r.Title
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return w.Flush()
}

// projectLabel renders a session's project/label column: "<slug>/<label>"
// when the session has a frozen project (MODEL §4), "-" for a projectless
// one (a captured session need not belong to a project).
func projectLabel(item journal.WalkItem) string {
	if item.Session.Project == nil {
		return "-"
	}
	return item.Session.Project.Slug + "/" + item.Session.Project.Label
}

// stateColumn renders a session's state column, appending " (stale)" per
// M7 when it applies.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}
