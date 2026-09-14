package capture

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast plumbing capture` verb (V13).
func Command(streams *iostreams.Streams) *cobra.Command {
	var harness string
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "capture everything new or grown, silently",
		Long: "capture walks every implemented source, captures new sessions, re-captures grown or " +
			"rewritten ones, resolves project/clone/worktree facts, writes session.json, and " +
			"auto-dismisses no-op sessions (reason auto:no-op) when capture.auto_dismiss_noop is on. " +
			"An unchanged session captured before its project was init'ed retries resolution and, " +
			"once the clone is registered, backfills session.json in place (one `backfilled` line). " +
			"One line per session captured; nothing at all when there is nothing to do (exit 0, " +
			"empty stdout), so hook and cron paths stay quiet. Unreadable sessions are stderr " +
			"diagnostics, never a failed run.\n\n" +
			"Sources: claude. Planned before 1.0: pi, devin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())

			sources, err := resolveSources(harness)
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
			autoDismiss, err := autoDismissNoop(cfg)
			if err != nil {
				return err
			}

			captured, diags, err := Run(cmd.Context(), Deps{
				Root:            root,
				Sources:         sources,
				AutoDismissNoop: autoDismiss,
				Now:             time.Now,
			})
			for _, d := range diags {
				if _, werr := fmt.Fprintf(streams.Err, "capture: %s: %v\n", d.Path, d.Err); werr != nil {
					return werr
				}
			}
			if err != nil {
				return err
			}

			if flags.JSON {
				return writeJSON(streams, captured)
			}
			return writeHuman(streams, captured)
		},
	}
	cmd.Flags().StringVar(&harness, "harness", "", "capture only this source's sessions")
	// OutputSchema stays unfilled until a consumer exists (C3.7/V35).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// resolveSources maps the --harness flag to the walk set: the whole
// table bare, one row when named, validation.unknown-harness naming the
// implemented set otherwise (V30).
func resolveSources(harness string) ([]source.Source, error) {
	if harness == "" {
		return sourceregistry.All, nil
	}
	src, ok := sourceregistry.Lookup(harness)
	if !ok {
		return nil, clasterr.New("validation.unknown-harness",
			fmt.Sprintf("unknown harness %q; implemented: %s", harness, strings.Join(sourceregistry.Names, ", ")))
	}
	return []source.Source{src}, nil
}

// autoDismissNoop reads config capture.auto_dismiss_noop (M3), default
// true. A present-but-mistyped value is an error naming the key, never a
// silent fallback (the ConfiguredCutoff posture).
func autoDismissNoop(cfg config.Config) (bool, error) {
	raw, present := cfg["capture"]
	if !present || raw == nil {
		return true, nil
	}
	// yaml.v3 decodes a nested mapping into its parent map's own type, so
	// the section arrives as config.Config (not map[string]any) from
	// config.Load — accept both spellings of the same underlying map.
	var section map[string]any
	switch m := raw.(type) {
	case map[string]any:
		section = m
	case config.Config:
		section = m
	default:
		return false, fmt.Errorf("capture: config key \"capture\" must be a mapping, got %T", raw)
	}
	v, present := section["auto_dismiss_noop"]
	if !present || v == nil {
		return true, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("capture: config key \"capture.auto_dismiss_noop\" must be a boolean, got %T", v)
	}
	return b, nil
}

// capturedJSON is one row of capture's --json payload.
type capturedJSON struct {
	Harness           string `json:"harness"`
	SessionID         string `json:"session_id"`
	Shard             string `json:"shard"`
	Recaptured        bool   `json:"recaptured"`
	AutoDismissed     bool   `json:"auto_dismissed"`
	ProjectBackfilled bool   `json:"project_backfilled"`
}

func writeJSON(streams *iostreams.Streams, captured []Captured) error {
	rows := make([]capturedJSON, len(captured))
	for i, c := range captured {
		rows[i] = capturedJSON{
			Harness:           c.Key.Harness,
			SessionID:         c.Key.NativeID,
			Shard:             c.Shard,
			Recaptured:        c.Recaptured,
			AutoDismissed:     c.AutoDismissed,
			ProjectBackfilled: c.ProjectBackfilled,
		}
	}
	payload := struct {
		Captured []capturedJSON `json:"captured"`
	}{Captured: rows}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits one line per session captured — and, per V13, not one
// byte when there was nothing to do.
func writeHuman(streams *iostreams.Streams, captured []Captured) error {
	for _, c := range captured {
		verb := "captured"
		switch {
		case c.ProjectBackfilled:
			verb = "backfilled"
		case c.Recaptured:
			verb = "recaptured"
		}
		line := fmt.Sprintf("%s %s", verb, c.Key.DirName())
		if c.AutoDismissed {
			line += " · dismissed auto:no-op"
		}
		if _, err := fmt.Fprintln(streams.Out, line); err != nil {
			return err
		}
	}
	return nil
}
