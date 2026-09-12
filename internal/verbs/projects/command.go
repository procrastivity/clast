package projects

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing projects` verb.
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "list every project clast knows about",
		Long: "list every registered project: slug, remote (\"(no remote)\" for a keyless project), " +
			"and its clone count across every machine that has registered one. Read-only — register a " +
			"project by running `clast init` in a clone of it.",
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

			rows, err := Run(root)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "projects: %d project(s)\n", len(rows)); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, rows)
			}
			return writeHuman(streams, rows)
		},
	}
	// OutputSchema is deliberately left unfilled: V35 leaves the registry
	// queries unfilled until a consumer exists (C3.7 — never speculatively).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// projectJSON is one row of projects' --json payload.
type projectJSON struct {
	Slug       string `json:"slug"`
	Remote     string `json:"remote"`
	CloneCount int    `json:"clone_count"`
}

// writeJSON emits projects' stable-shaped --json payload: an array of
// {slug, remote, clone_count}, empty rather than null when there are no
// projects.
func writeJSON(streams *iostreams.Streams, rows []Row) error {
	out := make([]projectJSON, len(rows))
	for i, r := range rows {
		out[i] = projectJSON(r)
	}
	payload := struct {
		Projects []projectJSON `json:"projects"`
	}{Projects: out}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits projects' human-mode listing: aligned columns (slug,
// remote, clone count), one row per project.
func writeHuman(streams *iostreams.Streams, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(streams.Out, "no projects registered")
		return err
	}
	w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		remote := r.Remote
		if remote == "" {
			remote = "(no remote)"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%d clone(s)\n", r.Slug, remote, r.CloneCount); err != nil {
			return err
		}
	}
	return w.Flush()
}
