// Package cli is the one registration point for every verb (C1.3): it
// builds the root Cobra command, binds the two global flags once, and maps
// whatever Execute returns to the process exit code. cmd/clast/main.go
// does nothing beyond calling into this package.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	doctorverb "github.com/procrastivity/clast/internal/verbs/doctor"
	installverb "github.com/procrastivity/clast/internal/verbs/install"
	manifestverb "github.com/procrastivity/clast/internal/verbs/manifest"
	uninstallverb "github.com/procrastivity/clast/internal/verbs/uninstall"
	versionverb "github.com/procrastivity/clast/internal/verbs/version"
)

// NewRootCommand builds the clast root command with both global flags
// bound and every verb registered. It is the only place any verb package
// gets imported — no ad hoc init() side effects live anywhere else (C1.3).
func NewRootCommand(streams *iostreams.Streams, build buildinfo.Info) *cobra.Command {
	root := &cobra.Command{
		Use:   "clast",
		Short: "clast — TODO: one line on what this tool is",
		// We render every error ourselves (see Execute) so human and
		// --json modes come from one code path; Cobra's own printing
		// would double up or bypass the --json envelope (C2.5).
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			jsonOut, err := cmd.Flags().GetBool("json")
			if err != nil {
				return err
			}
			verbose, err := cmd.Flags().GetBool("verbose")
			if err != nil {
				return err
			}
			cmd.SetContext(cliflags.WithFlags(cmd.Context(), cliflags.Flags{JSON: jsonOut, Verbose: verbose}))
			return nil
		},
	}
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	root.PersistentFlags().Bool("json", false, "emit the success payload as one JSON value")
	root.PersistentFlags().BoolP("verbose", "v", false, "extra diagnostic lines on stderr")

	root.AddCommand(versionverb.Command(streams, build))
	root.AddCommand(manifestverb.Command(streams, build, root))
	root.AddCommand(installverb.Command(streams, build, root))
	root.AddCommand(uninstallverb.Command(streams))
	root.AddCommand(doctorverb.Command(streams, build, root))

	// Register the tool's own verbs here, one package per verb under
	// internal/verbs/ (C1.4). Every Command constructor ends with
	// surface.Annotate — the manifest walk hard-errors without it (C3.2).

	return root
}
