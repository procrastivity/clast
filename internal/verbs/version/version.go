// Package version implements the chassis demo verb: `clast version`.
// Version metadata is a chassis concern in its own right, which makes it
// the natural vehicle for exercising --json/-v and the stdout/stderr
// discipline every other verb inherits.
package version

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast version` verb. streams is the writer
// pair threaded in at construction (C2.1); build is the
// version/commit/date triple main sets via -ldflags.
func Command(streams *iostreams.Streams, build buildinfo.Info) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "print clast's version, commit, and build date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())

			if flags.Verbose {
				if _, err := fmt.Fprintln(streams.Err, "version: reading compiled-in build metadata"); err != nil {
					return err
				}
			}

			if flags.JSON {
				payload := struct {
					Version string `json:"version"`
					Commit  string `json:"commit"`
					Date    string `json:"date"`
				}{Version: build.Version, Commit: build.Commit, Date: build.Date}
				b, err := json.Marshal(payload)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(streams.Out, string(b))
				return err
			}

			_, err := fmt.Fprintf(streams.Out, "clast version %s (commit %s, built %s)\n", build.Version, build.Commit, build.Date)
			return err
		},
	}
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}
