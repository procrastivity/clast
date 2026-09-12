// Package doctor implements the `clast doctor` verb: it reports every
// registered harness target's drift state (C4.5) and every finding, flat
// and with no severity levels (C4.7). Only a code without the "advisory."
// prefix fails the run. A tool grows doctor by registering checks, never
// by a second command or a second output path.
package doctor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/checks"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast doctor` verb. root is the
// *cobra.Command NewRootCommand is assembling, captured by reference — the
// same pattern manifest/install use — so checks.HarnessTargets reads the
// manifest every verb ultimately registered on it.
func Command(streams *iostreams.Streams, build buildinfo.Info, root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "diagnose this host's clast installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())

			// HarnessTargets returns both findings and per-target state
			// from one registry walk (internal/checks), so there is
			// nothing left for the generic checks.Run to compose here;
			// a future check with no per-target state would run through
			// checks.Run and have its findings appended below.
			findings, targets, err := checks.HarnessTargets(root, build)
			if err != nil {
				return err
			}

			if flags.JSON {
				out := findings
				if out == nil {
					out = []checks.Finding{}
				}
				b, err := json.Marshal(struct {
					Findings []checks.Finding     `json:"findings"`
					Targets  []checks.TargetState `json:"targets"`
				}{Findings: out, Targets: targets})
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintln(streams.Out, string(b)); err != nil {
					return err
				}
				return findingsError(findings)
			}

			for _, t := range targets {
				if _, err := fmt.Fprintf(streams.Out, "%s %s: %s at %s\n", t.Harness, t.Target, t.State, t.Dir); err != nil {
					return err
				}
			}
			if len(findings) == 0 {
				_, err := fmt.Fprintln(streams.Out, "no issues found")
				return err
			}
			for _, f := range findings {
				if _, err := fmt.Fprintf(streams.Out, "%s: %s\n", f.Code, f.Message); err != nil {
					return err
				}
			}
			return findingsError(findings)
		},
	}
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// findingsError signals doctor's exit posture — 0 with no failing
// findings, 1 with one or more failing findings. Advisory-prefixed codes
// remain in the same flat output list but never make doctor fail (C4.7).
func findingsError(findings []checks.Finding) error {
	failing := 0
	for _, finding := range findings {
		if !strings.HasPrefix(finding.Code, "advisory.") {
			failing++
		}
	}
	if failing == 0 {
		return nil
	}
	return clasterr.New("doctor.findings-present", fmt.Sprintf("%d finding(s) reported; see above", failing))
}
