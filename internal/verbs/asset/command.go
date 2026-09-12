package asset

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// outputSchema is asset's --json payload shape (V25, schema filled per
// V35: skills consume it).
const outputSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string"},
		"link": {"type": "string", "enum": ["override", "shipped", "embedded"]},
		"resolved_from": {"type": "string"},
		"sha256": {"type": "string"},
		"content": {"type": "string"}
	},
	"required": ["path", "link", "resolved_from", "sha256", "content"]
}`

// Command constructs the `clast plumbing asset <path>` verb (V25). The Use
// line records the positional verbatim (C3.8).
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "asset <path>",
		Short: "print one asset's resolved content — override -> shipped -> embedded",
		Long: "asset <path> resolves path (an asset-relative name, e.g. prompts/wake-draft-user.md " +
			"or flows/retro.md) through the resolution chain (M18): a user override shadows a " +
			"shipped default, which shadows the fallback compiled into the binary. Human mode " +
			"prints the resolved bytes on stdout and nothing else, so flows and scripts can " +
			"consume it directly. -v adds one stderr line naming which link answered. --json " +
			"emits {path, link, resolved_from, sha256, content}; link is override, shipped, or " +
			"embedded (resolved_from is \"embedded\" for that link). An unknown path is " +
			"not-found.asset. There is no listing mode; `manifest --json` already enumerates " +
			"every shipped asset with its checksum.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			result, err := Run(args[0])
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err, "asset: resolved %s via %s\n", result.Path, result.Link); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			_, err = streams.Out.Write(result.Content)
			return err
		},
	}

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// resultJSON is asset's --json payload (V25).
type resultJSON struct {
	Path         string `json:"path"`
	Link         string `json:"link"`
	ResolvedFrom string `json:"resolved_from"`
	SHA256       string `json:"sha256"`
	Content      string `json:"content"`
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := resultJSON{
		Path:         result.Path,
		Link:         result.Link,
		ResolvedFrom: result.ResolvedFrom,
		SHA256:       result.SHA256,
		Content:      string(result.Content),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}
