// Package uninstall implements the `clast uninstall <harness>` verb: it
// removes exactly the stamped trees a prior `clast install <harness>`
// wrote, and refuses — rather than silently proceeding — if it finds
// unstamped content at a target path (C4.7).
//
// A harness may project more than one stamped target (SURFACE V32:
// claude-code's three skills, each independently stamped) — this walks
// every one of them, stopping at the first refusal, same as install does.
//
// It does not touch a harness's splice target (C4.8: claude-code's
// settings.json SessionStart hook). Reversing that splice cleanly —
// removing exactly the entry install added, not any hook a human added
// beside it — is deliberately deferred: see this Matter's step-03
// ("uninstall claude-code: reverse the projection cleanly"). Today's
// `uninstall claude-code` removes the three skill directories and leaves
// the spliced settings.json hook in place.
package uninstall

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/harness/registry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/surface"
)

// targetResult is one target's removal outcome.
type targetResult struct {
	Target string `json:"target"`
	Dir    string `json:"dir"`
}

// Command constructs the `clast uninstall <harness>` verb.
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall <harness>",
		Short: "remove a previously installed harness self-projection",
		Long: "remove a previously installed harness self-projection.\n\n" +
			"Available harnesses: " + strings.Join(registry.Names, ", ") + ".",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				_, err := fmt.Fprintf(streams.Out, "available harnesses: %s\nusage: clast uninstall <harness>\n", strings.Join(registry.Names, ", "))
				return err
			}
			harnessName := args[0]
			if !slices.Contains(registry.Names, harnessName) {
				quoted := make([]string, len(registry.Names))
				for i, name := range registry.Names {
					quoted[i] = fmt.Sprintf("%q", name)
				}
				return clasterr.New("validation.unknown-harness",
					fmt.Sprintf("unknown harness %q — only %s is supported", harnessName, strings.Join(quoted, ", or ")))
			}

			flags := cliflags.FromContext(cmd.Context())

			// harnessName was already validated against registry.Names
			// above, so Lookup is guaranteed to find it here.
			h, _ := registry.Lookup(harnessName)

			results := make([]targetResult, 0, len(h.Targets))
			for _, target := range h.Targets {
				dir, err := target.Uninstall()
				if err != nil {
					return err
				}
				results = append(results, targetResult{Target: target.Label, Dir: dir})
			}

			if flags.JSON {
				payload := struct {
					Harness string         `json:"harness"`
					Targets []targetResult `json:"targets"`
				}{Harness: harnessName, Targets: results}
				b, err := json.Marshal(payload)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(streams.Out, string(b))
				return err
			}

			for _, r := range results {
				if _, err := fmt.Fprintf(streams.Out, "uninstalled %s %s skill from %s\n", harnessName, r.Target, r.Dir); err != nil {
					return err
				}
			}
			return nil
		},
	}
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}
