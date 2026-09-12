package curate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing curate <session>` verb (V14).
func Command(streams *iostreams.Streams) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "curate <session>",
		Short: "write a complete entry.md for a session, valid from every state",
		Long: "curate reads a complete entry.md document — frontmatter included — from stdin or " +
			"--file <path>, validates that the frontmatter parses and a title is present (nothing " +
			"else), and writes it atomically alongside a fresh curation.json carrying the current " +
			"transcript fingerprint.\n\n" +
			"Valid from every curation state: from captured it curates, from curated it re-curates " +
			"(refreshing the stale fingerprint), and from dismissed it revives straight to curated, " +
			"replacing the dismissal in the same call — no undismiss step required. <session> is the " +
			"session's directory name or any unique prefix (claude-8f3a typically suffices).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())
			locator := args[0]

			data, err := readEntryInput(streams, file)
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
			}

			result, err := Run(root, locator, data, time.Now())
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "curate: %s curated (was %s)\n", result.Key.DirName(), result.PriorState); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the entry.md document from this path instead of stdin")
	// OutputSchema stays unfilled until a consumer exists (C3.7/V35: curate
	// is not among the filled-schema verbs).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// readEntryInput reads the complete entry.md document from --file when
// given, otherwise from stdin. A named file that cannot be read is
// not-found.entry-file (the matter finding's addition within V34's
// not-found. prefix) rather than a raw OS error surfacing as an internal
// failure.
func readEntryInput(streams *iostreams.Streams, file string) ([]byte, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, clasterr.New("not-found.entry-file", fmt.Sprintf("reading %s: %v", file, err))
		}
		return data, nil
	}
	data, err := io.ReadAll(streams.In)
	if err != nil {
		return nil, fmt.Errorf("curate: reading stdin: %w", err)
	}
	return data, nil
}

// resultJSON is curate's --json payload shape.
type resultJSON struct {
	Harness           string `json:"harness"`
	SessionID         string `json:"session_id"`
	Shard             string `json:"shard"`
	PriorState        string `json:"prior_state"`
	ReplacedDismissal bool   `json:"replaced_dismissal"`
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := resultJSON{
		Harness:           result.Key.Harness,
		SessionID:         result.Key.NativeID,
		Shard:             result.Shard,
		PriorState:        string(result.PriorState),
		ReplacedDismissal: result.PriorState == journal.StateDismissed,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits curate's one human-mode line, naming the dismissal
// replacement explicitly when it happened (V14's no-ceremony edge is easy
// to miss otherwise).
func writeHuman(streams *iostreams.Streams, result Result) error {
	line := fmt.Sprintf("curated %s", result.Key.DirName())
	if result.PriorState == journal.StateDismissed {
		line += " · replaced dismissal"
	}
	_, err := fmt.Fprintln(streams.Out, line)
	return err
}
