package undismiss

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing undismiss <session>` verb (V15).
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undismiss <session>",
		Short: "return a dismissed session to captured",
		Long: "undismiss removes <session>'s dismissal, returning it to captured. Its only " +
			"precondition is that the session is currently dismissed (validation.not-dismissed " +
			"otherwise) — it accepts a dismissal carrying any reason, including capture's own " +
			"auto:no-op. <session> is the session's directory name or any unique prefix.\n\n" +
			"Undismissing a session that is still not substantive is undone by the next capture " +
			"sweep (M3): that sweep re-applies auto-dismissal to any non-substantive session with no " +
			"curation state at all, by design.",
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

			result, err := Run(root, locator)
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "undismiss: %s returned to captured\n", result.Key.DirName()); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}
	// OutputSchema stays unfilled until a consumer exists (C3.7/V35).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// resultJSON is undismiss's --json payload shape.
type resultJSON struct {
	Harness   string `json:"harness"`
	SessionID string `json:"session_id"`
	Shard     string `json:"shard"`
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := resultJSON{
		Harness:   result.Key.Harness,
		SessionID: result.Key.NativeID,
		Shard:     result.Shard,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

func writeHuman(streams *iostreams.Streams, result Result) error {
	_, err := fmt.Fprintf(streams.Out, "undismissed %s\n", result.Key.DirName())
	return err
}
