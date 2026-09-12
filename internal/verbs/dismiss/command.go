package dismiss

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing dismiss <session>` verb (V15).
func Command(streams *iostreams.Streams) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "dismiss <session>",
		Short: "mark a session deliberately excluded, with a reason",
		Long: "dismiss marks <session> excluded, recording --reason (default \"manual\"). It refuses a " +
			"curated session (validation.curated — the entry would be orphaned; delete is not a v1 " +
			"surface) and refuses \"auto:no-op\", reserved to capture's own auto-dismissal (M3). " +
			"Dismissing an already-dismissed session succeeds and replaces its reason. <session> is " +
			"the session's directory name or any unique prefix.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())
			locator := args[0]

			cfg, err := config.Load()
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
			}
			machine, err := journal.Hostname()
			if err != nil {
				return err
			}

			result, err := Run(root, locator, reason, time.Now(), machine)
			if err != nil {
				return err
			}

			if flags.Verbose {
				verb := "dismissed"
				if result.Redismiss {
					verb = "re-dismissed"
				}
				if _, err := fmt.Fprintf(streams.Err, "dismiss: %s %s (reason %q)\n", verb, result.Key.DirName(), reason); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result, reason)
			}
			return writeHuman(streams, result, reason)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", DefaultReason, "the dismissal reason")
	// OutputSchema stays unfilled until a consumer exists (C3.7/V35).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// resultJSON is dismiss's --json payload shape.
type resultJSON struct {
	Harness   string `json:"harness"`
	SessionID string `json:"session_id"`
	Shard     string `json:"shard"`
	Reason    string `json:"reason"`
	Redismiss bool   `json:"redismiss"`
}

func writeJSON(streams *iostreams.Streams, result Result, reason string) error {
	payload := resultJSON{
		Harness:   result.Key.Harness,
		SessionID: result.Key.NativeID,
		Shard:     result.Shard,
		Reason:    reason,
		Redismiss: result.Redismiss,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits dismiss's one human-mode line, naming the reason and
// whether this call replaced a prior dismissal.
func writeHuman(streams *iostreams.Streams, result Result, reason string) error {
	verb := "dismissed"
	if result.Redismiss {
		verb = "re-dismissed"
	}
	_, err := fmt.Fprintf(streams.Out, "%s %s (reason %q)\n", verb, result.Key.DirName(), reason)
	return err
}
