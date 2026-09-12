package whereami

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// outputSchema is whereami's --json payload shape (SURFACE V35: whereami
// is one of the few registry-facing verbs with a filled schema, since a
// flow's step 1 scoping — "which project am I in" — consumes it).
const outputSchema = `{
	"type": "object",
	"properties": {
		"project": {
			"type": "object",
			"properties": {
				"id": {"type": "string"},
				"slug": {"type": "string"},
				"remote": {"type": "string"}
			},
			"required": ["id", "slug", "remote"]
		},
		"clone": {
			"type": "object",
			"properties": {
				"id": {"type": "string"},
				"label": {"type": "string"},
				"git_common_dir": {"type": "string"}
			},
			"required": ["id", "label", "git_common_dir"]
		},
		"worktree": {"type": "string"},
		"branch": {"type": "string"},
		"machine": {"type": "string"}
	},
	"required": ["project", "clone", "worktree", "branch", "machine"]
}`

// Command constructs the `clast plumbing whereami` verb.
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whereami",
		Short: "report the registered project/clone/worktree/branch for the cwd",
		Long: "report, for the current directory: the project it belongs to (id, slug, remote), " +
			"the clone (id, label, git-common-dir), the worktree name (empty for the main worktree, M17), " +
			"the checked-out branch (empty for a detached HEAD), and this machine's name.\n\n" +
			"This is the debugging face of clast's own clone resolution (the same lookup `clast init` and " +
			"session capture use) — the tool it reaches for to answer \"does clast know where I am\". " +
			"An unregistered directory refuses; run `clast init` there first.",
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

			result, err := Run(cmd.Context(), root, dir)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "whereami: project=%s clone=%s worktree=%q branch=%q machine=%s\n",
					result.Project.ID, result.Clone.ID, result.Worktree, result.Branch, result.Machine); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}
	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// writeJSON emits whereami's --json payload (V22/V35): project id/slug/
// remote, clone id/label/git-common-dir, worktree, branch, machine.
func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := struct {
		Project struct {
			ID     string `json:"id"`
			Slug   string `json:"slug"`
			Remote string `json:"remote"`
		} `json:"project"`
		Clone struct {
			ID           string `json:"id"`
			Label        string `json:"label"`
			GitCommonDir string `json:"git_common_dir"`
		} `json:"clone"`
		Worktree string `json:"worktree"`
		Branch   string `json:"branch"`
		Machine  string `json:"machine"`
	}{}
	payload.Project.ID = result.Project.ID
	payload.Project.Slug = result.Project.Slug
	payload.Project.Remote = result.Project.Remote
	payload.Clone.ID = result.Clone.ID
	payload.Clone.Label = result.Clone.Label
	payload.Clone.GitCommonDir = result.Clone.GitCommonDir
	payload.Worktree = result.Worktree
	payload.Branch = result.Branch
	payload.Machine = result.Machine

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits whereami's human-mode lines: one fact per line, in the
// existing verbs' voice (initverb's "created project %q ... (%s)" style —
// slug/label as the readable name, id parenthesized).
func writeHuman(streams *iostreams.Streams, result Result) error {
	worktree := result.Worktree
	if worktree == "" {
		worktree = "(main)"
	}
	branch := result.Branch
	if branch == "" {
		branch = "(detached)"
	}

	lines := []string{
		fmt.Sprintf("project: %s (%s)", result.Project.Slug, result.Project.ID),
		fmt.Sprintf("remote: %s", emptyAs(result.Project.Remote, "(no remote)")),
		fmt.Sprintf("clone: %s (%s)", result.Clone.Label, result.Clone.ID),
		fmt.Sprintf("common-dir: %s", result.Clone.GitCommonDir),
		fmt.Sprintf("worktree: %s", worktree),
		fmt.Sprintf("branch: %s", branch),
		fmt.Sprintf("machine: %s", result.Machine),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(streams.Out, line); err != nil {
			return err
		}
	}
	return nil
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
