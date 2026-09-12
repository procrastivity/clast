package breadcrumb

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
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the top-level `clast breadcrumb <text>` verb (V27).
// The Use line records the positional verbatim (C3.8).
func Command(streams *iostreams.Streams) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "breadcrumb <text>",
		Short: "jot a one-line breadcrumb, scoped to this project",
		Long: "breadcrumb <text> appends one line to today's breadcrumb file, scoped by cwd through the " +
			"same clone resolution `clast init` uses. An unregistered cwd refuses (refusal.unknown-clone), " +
			"naming --global and `clast init` as the two outs (refuse over guess, M16). --global writes " +
			"a global crumb (slug: null) regardless of cwd. List them with `plumbing breadcrumbs`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

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

			b, err := Run(cmd.Context(), root, dir, args[0], global, time.Now())
			if err != nil {
				return err
			}

			if flags.Verbose {
				scope := "(global)"
				if b.Slug != nil {
					scope = *b.Slug
				}
				if _, err := fmt.Fprintf(streams.Err, "breadcrumb: scope=%s at=%s\n", scope, b.At.Format(time.RFC3339)); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, b)
			}
			return writeHuman(streams, b)
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "write a global crumb (slug: null), regardless of cwd")
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// crumbJSON is breadcrumb's stable-shaped --json payload (schema
// deliberately unfilled, V35/C3.7 — no consumer yet).
type crumbJSON struct {
	At   time.Time `json:"at"`
	Slug *string   `json:"slug"`
	Text string    `json:"text"`
}

func writeJSON(streams *iostreams.Streams, b journal.Breadcrumb) error {
	payload := crumbJSON{At: b.At, Slug: b.Slug, Text: b.Text}
	out, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(out))
	return err
}

// writeHuman confirms the jot with one line — the scope it landed under,
// so a global crumb (easy to write by accident from the wrong cwd) is
// never silently ambiguous.
func writeHuman(streams *iostreams.Streams, b journal.Breadcrumb) error {
	if b.Slug == nil {
		_, err := fmt.Fprintln(streams.Out, "noted (global)")
		return err
	}
	_, err := fmt.Fprintf(streams.Out, "noted (%s)\n", *b.Slug)
	return err
}
