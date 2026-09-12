// Package plumbing builds the `clast plumbing` namespace command (SURFACE
// V2/V3): the audience boundary between porcelain (top-level, what bare
// `clast --help` lists) and plumbing (the deterministic substrate skills
// and scripts call, listed only by `clast plumbing --help`). It carries
// no logic of its own — every verb it groups lives in its own package,
// the same one-package-per-verb convention every porcelain verb already
// follows (C1.4) — and declares no RunE: a Cobra command with no Run/RunE
// is not Runnable, so invoking it bare (or with --help) falls into
// Cobra's own help path (flag.ErrHelp), which internal/cli.Execute's
// ExecuteC call already renders as the command's help text with a nil
// error — exit 0, without any special-casing here or in the chassis.
package plumbing

import (
	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/iostreams"
	breadcrumbsverb "github.com/procrastivity/clast/internal/verbs/breadcrumbs"
	captureverb "github.com/procrastivity/clast/internal/verbs/capture"
	clonesverb "github.com/procrastivity/clast/internal/verbs/clones"
	curateverb "github.com/procrastivity/clast/internal/verbs/curate"
	dismissverb "github.com/procrastivity/clast/internal/verbs/dismiss"
	projectsverb "github.com/procrastivity/clast/internal/verbs/projects"
	sessionsverb "github.com/procrastivity/clast/internal/verbs/sessions"
	showverb "github.com/procrastivity/clast/internal/verbs/show"
	statsverb "github.com/procrastivity/clast/internal/verbs/stats"
	undismissverb "github.com/procrastivity/clast/internal/verbs/undismiss"
	whereamiverb "github.com/procrastivity/clast/internal/verbs/whereami"
)

// Command constructs the `clast plumbing` namespace command and registers
// every plumbing verb under it (V22/V23; V20's shape-document verbs and
// V24's tier-2 names join here as they land). The group command itself is
// never surface.Annotate'd — it has no Runnable RunE of its own, so the
// manifest walk (internal/manifest/verbs.go's collect) recurses through it
// without requiring a kind, the same way it already treats any other
// command with children.
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plumbing",
		Short: "the deterministic substrate: JSON + exit codes, no LLM (V2)",
		Long: "clast plumbing is the audience boundary: everything under it is the deterministic " +
			"substrate skills and scripts call, and none of it is listed among clast's own top-level " +
			"(porcelain) verbs. A human crosses into it deliberately; nothing here needs an API key " +
			"or makes an LLM call.",
	}
	cmd.AddCommand(captureverb.Command(streams))
	cmd.AddCommand(whereamiverb.Command(streams))
	cmd.AddCommand(projectsverb.Command(streams))
	cmd.AddCommand(clonesverb.Command(streams))
	cmd.AddCommand(sessionsverb.Command(streams))
	cmd.AddCommand(showverb.Command(streams))
	cmd.AddCommand(breadcrumbsverb.Command(streams))
	cmd.AddCommand(statsverb.Command(streams))
	cmd.AddCommand(curateverb.Command(streams))
	cmd.AddCommand(dismissverb.Command(streams))
	cmd.AddCommand(undismissverb.Command(streams))
	return cmd
}
