package stats

import (
	"encoding/json"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
	"github.com/procrastivity/clast/internal/surface"
)

// defaultSinceArg is stats' own --since default: "all" (V21), deliberately
// not the `since` config key `sessions` defaults to (V17/V31) — a health
// count should cover the whole journal unless narrowed.
const defaultSinceArg = "all"

// Command constructs the `clast plumbing stats` verb (V21).
func Command(streams *iostreams.Streams) *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "count sessions by state, harness, project, and day",
		Long: "stats walks the journal once (M4: no side index, no cache) and counts sessions on four " +
			"fixed axes: curation state, harness, project, and day. --since <duration|all> restricts the " +
			"walk to sessions on or after that recent window (default: all — deliberately not the " +
			"since config key `sessions` defaults to, V21).",
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
			sinceBound, err := query.ResolveSince(since, defaultSinceArg, cutoff, time.Now())
			if err != nil {
				return err
			}

			st, err := Run(root, sinceBound, cutoff)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "stats: %d session(s) counted\n", st.Total); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, st)
			}
			return writeHuman(streams, st)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "restrict the walk to sessions on or after this recent window (-Nd, -Nw, or all; default: all)")

	// OutputSchema stays unfilled: V35/C3.7 — no consumer yet.
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// statsJSON is stats' stable-shaped --json payload (schema deliberately
// unfilled, V35/C3.7).
type statsJSON struct {
	Total     int            `json:"total"`
	ByState   map[string]int `json:"by_state"`
	ByHarness map[string]int `json:"by_harness"`
	ByProject map[string]int `json:"by_project"`
	ByDay     map[string]int `json:"by_day"`
}

func writeJSON(streams *iostreams.Streams, st Stats) error {
	payload := statsJSON(st)
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits one section per axis, each row sorted by key for
// deterministic output. No byte promise; layout is this build's own call
// (HANDOFF §8 remainder).
func writeHuman(streams *iostreams.Streams, st Stats) error {
	if _, err := fmt.Fprintf(streams.Out, "sessions: %d\n", st.Total); err != nil {
		return err
	}
	sections := []struct {
		title  string
		counts map[string]int
	}{
		{"by state", st.ByState},
		{"by harness", st.ByHarness},
		{"by project", st.ByProject},
		{"by day", st.ByDay},
	}
	for _, s := range sections {
		if len(s.counts) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(streams.Out, "%s:\n", s.title); err != nil {
			return err
		}
		if err := writeCountRows(streams, s.counts); err != nil {
			return err
		}
	}
	return nil
}

func writeCountRows(streams *iostreams.Streams, counts map[string]int) error {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
	for _, k := range keys {
		if _, err := fmt.Fprintf(w, "  %s\t%d\n", k, counts[k]); err != nil {
			return err
		}
	}
	return w.Flush()
}
