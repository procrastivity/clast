package initverb

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast init` verb.
func Command(streams *iostreams.Streams) *cobra.Command {
	var slugFlag, labelFlag, identityRemoteFlag string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "register this clone with clast, creating its project if needed",
		Long: "register this clone with clast (SURFACE V26): known project (by identity remote) registers this clone; " +
			"unknown project is created first, named by --slug (default: the repo directory's basename). " +
			"Re-running init in an already-registered clone reports current and writes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			result, err := Run(cmd.Context(), root, dir, Options{
				Slug:           slugFlag,
				Label:          labelFlag,
				IdentityRemote: identityRemoteFlag,
			})
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "init: outcome=%s project=%s (created=%v) clone=%s worktree=%q\n",
					result.Outcome, result.Project.ID, result.ProjectCreated, result.Clone.ID, result.Worktree); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}
	cmd.Flags().StringVar(&slugFlag, "slug", "", "override a newly-created project's slug (default: the repo directory's basename)")
	cmd.Flags().StringVar(&labelFlag, "label", "", "override this clone's label (default: the M16 ladder)")
	cmd.Flags().StringVar(&identityRemoteFlag, "identity-remote", "", "which remote's URL provides a newly-resolved project's identity (default: origin)")
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// writeJSON emits init's stable-shaped --json payload: project id/slug/
// remote, clone id/label, and status — one of "created", "registered",
// "current" (V26). Schema is deliberately left unfilled (V35: init is not
// in the consumed-payload set).
func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := struct {
		Project struct {
			ID     string `json:"id"`
			Slug   string `json:"slug"`
			Remote string `json:"remote,omitempty"`
		} `json:"project"`
		Clone struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"clone"`
		Status string `json:"status"`
	}{}
	payload.Project.ID = result.Project.ID
	payload.Project.Slug = result.Project.Slug
	payload.Project.Remote = result.Project.Remote
	payload.Clone.ID = result.Clone.ID
	payload.Clone.Label = result.Clone.Label
	payload.Status = string(result.Outcome)

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits init's human-mode line. The current case "prints
// exactly that state" (V26/registry seal condition) — a bare "current",
// nothing else, distinct from created/registered's fuller lines (adapted
// from wip's "attached …"/"registered …" voice).
func writeHuman(streams *iostreams.Streams, result Result) error {
	var err error
	switch result.Outcome {
	case OutcomeCurrent:
		_, err = fmt.Fprintln(streams.Out, "current")
	case OutcomeCreated:
		_, err = fmt.Fprintf(streams.Out, "created project %q and registered clone %q (%s)\n",
			result.Project.Slug, result.Clone.Label, result.Project.ID)
	case OutcomeRegistered:
		_, err = fmt.Fprintf(streams.Out, "registered clone %q with project %q (%s)\n",
			result.Clone.Label, result.Project.Slug, result.Project.ID)
	}
	return err
}
