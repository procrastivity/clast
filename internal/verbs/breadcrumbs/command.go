package breadcrumbs

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
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing breadcrumbs` verb (V19, list
// side; the write verb `breadcrumb` is porcelain, V27).
func Command(streams *iostreams.Streams) *cobra.Command {
	var day, project string
	var global bool

	cmd := &cobra.Command{
		Use:   "breadcrumbs",
		Short: "list a day's breadcrumbs, cross-machine",
		Long: "list breadcrumbs for --day (default today), reading every machine's file for that day's " +
			"bucket (M5/M8). --project <slug> lists that project's crumbs; --global lists slug-null " +
			"crumbs only; neither flag lists every crumb in the bucket. The write verb, `breadcrumb`, is " +
			"porcelain (V27).",
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
			dayBucket, err := journal.ParseDay(day, cutoff)
			if err != nil {
				return err
			}

			entries, err := Run(root, dayBucket, cutoff, project, global)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "breadcrumbs: %d crumb(s) for %s\n", len(entries), dayBucket); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, entries)
			}
			return writeHuman(streams, entries)
		},
	}

	cmd.Flags().StringVar(&day, "day", "today", "the day bucket to list (YYYY-MM-DD, today, yesterday, -Nd; M8)")
	cmd.Flags().StringVar(&project, "project", "", "list only this project's crumbs")
	cmd.Flags().BoolVar(&global, "global", false, "list only slug-null (global) crumbs")
	cmd.MarkFlagsMutuallyExclusive("project", "global")

	// OutputSchema stays unfilled: V35/C3.7 — no consumer yet.
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// crumbJSON is one breadcrumb's stable-shaped --json row (schema
// deliberately unfilled, V35/C3.7).
type crumbJSON struct {
	At      time.Time `json:"at"`
	Slug    *string   `json:"slug"`
	Text    string    `json:"text"`
	Machine string    `json:"machine"`
}

func writeJSON(streams *iostreams.Streams, entries []journal.BreadcrumbEntry) error {
	rows := make([]crumbJSON, len(entries))
	for i, e := range entries {
		rows[i] = crumbJSON{At: e.At, Slug: e.Slug, Text: e.Text, Machine: e.Machine}
	}
	payload := struct {
		Breadcrumbs []crumbJSON `json:"breadcrumbs"`
	}{Breadcrumbs: rows}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits one line per crumb, chronological: time, scope
// (slug, or "(global)"), text, machine. No byte promise.
func writeHuman(streams *iostreams.Streams, entries []journal.BreadcrumbEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintln(streams.Out, "no breadcrumbs")
		return err
	}
	w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
	for _, e := range entries {
		line := fmt.Sprintf("%s\t%s\t%s\t%s", e.At.Local().Format("15:04"), scopeLabel(e), e.Text, e.Machine)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return w.Flush()
}

func scopeLabel(e journal.BreadcrumbEntry) string {
	if e.Slug == nil {
		return "(global)"
	}
	return *e.Slug
}
