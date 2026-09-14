package clones

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing clones [<project>]` verb.
func Command(streams *iostreams.Streams) *cobra.Command {
	return command(streams, "clones [project]", "")
}

// AliasCommand constructs the top-level `clast clones` alias.
func AliasCommand(streams *iostreams.Streams) *cobra.Command {
	return command(streams, "clones [project]", "clones")
}

func command(streams *iostreams.Streams, use, aliasOf string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: "list registered clones, scoped to one project or every project",
		Long: "list registered clones. With a project locator (id or slug), that project's clones. " +
			"With none: the current project's clones when run inside a registered clone, or every " +
			"project's clones otherwise. Each row names its machine (a clone is host-scoped, M15) and " +
			"marks the current clone's row when identifiable. Read-only.",
		Args: cobra.MaximumNArgs(1),
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

			var locator string
			if len(args) == 1 {
				locator = args[0]
			}

			rows, err := Run(cmd.Context(), root, dir, locator)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "clones: %d clone(s)\n", len(rows)); err != nil {
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
	if aliasOf != "" {
		manifest.SetAliasOf(cmd, "plumbing "+aliasOf)
	}
	return cmd
}

// cloneJSON is one row of clones' --json payload.
type cloneJSON struct {
	ProjectSlug  string `json:"project_slug"`
	ID           string `json:"id"`
	Label        string `json:"label"`
	GitCommonDir string `json:"git_common_dir"`
	Machine      string `json:"machine"`
	Current      bool   `json:"current"`
}

// writeJSON emits clones' stable-shaped --json payload: an array of
// {project_slug, id, label, git_common_dir, machine, current}, the same
// shape regardless of which of Run's three scoping cases produced it.
func writeJSON(streams *iostreams.Streams, rows []Row) error {
	out := make([]cloneJSON, len(rows))
	for i, r := range rows {
		out[i] = cloneJSON(r)
	}
	payload := struct {
		Clones []cloneJSON `json:"clones"`
	}{Clones: out}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits clones' human-mode listing: aligned columns (project,
// label, machine, common-dir), with a trailing " · current" marker on the
// current clone's row (wip's status convention: a line suffix, not a
// separate column).
func writeHuman(streams *iostreams.Streams, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(streams.Out, "no clones registered")
		return err
	}
	w := tabwriter.NewWriter(streams.Out, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		line := fmt.Sprintf("%s\t%s\t%s\t%s", r.ProjectSlug, r.Label, r.Machine, r.GitCommonDir)
		if r.Current {
			line += " · current"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return w.Flush()
}
