package capture

import (
	"context"
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
		Long: "capture sweeps the selected sources, captures new sessions, re-captures grown or " +
			"rewritten ones, resolves project/clone/worktree facts, writes session.json, and " +
			"auto-dismisses no-op sessions (reason auto:no-op) when capture.auto_dismiss_noop is on. " +
			"An unchanged session captured before its project was init'ed retries resolution and, " +
			"once the clone is registered, backfills session.json in place (one `backfilled` line). " +
			"One line per session captured; nothing at all when there is nothing to do (exit 0, " +
			"empty stdout), so hook and cron paths stay quiet.\n\n" +
			"A bare run walks every implemented source that is not excluded (config " +
			"capture.exclude) and is either local or opted in — capture.<name>.auto: true is the " +
			"only way a network-resident source joins a sweep (off by default; inert on local " +
			"sources). --harness <name> captures just that source and is itself the authorization " +
			"for that one run: exclusion and the opt-in gate do not apply, nothing is persisted, " +
			"and an unknown name refuses (validation.unknown-harness). Availability means readable " +
			"session storage, never whether the harness's own binary or clast's projection is " +
			"installed — a source answers from its files or endpoint, not from an install. " +
			"Selection is a sweep input only: exclusion never makes a captured session unreadable, " +
			"and no path deletes local history.\n\n" +
			"A source that cannot enumerate at all is disclosed on stderr (capture: <name>: <err>, " +
			"and an \"unavailable\" list under --json) while the rest of the sweep runs and still " +
			"exits 0; a session that cannot be read is a stderr diagnostic on the same terms. The " +
			"same failure under --harness is the run's verdict instead — capture.source-unavailable, " +
			"exit 1, naming the source and the probed root — and absent storage, silent in a sweep, " +
			"answers the same way when named. Store failures and cancellation abort as " +
			"internal.capture (exit 4).\n\n" +
			"Sources: claude, amp. Planned before 1.0: pi, devin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())

			// --harness validation runs before anything config-
			// dependent: a bad name must report unknown-harness even
			// under malformed config (V30 + the selection contract's
			// ordering rule).
			var named source.Source
			if harness != "" {
				src, err := namedSource(harness)
				if err != nil {
					return err
				}
				named = src
			}

			cfg, err := config.Load()
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
			}
			autoDismiss, err := autoDismissNoop(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("reading capture config: %v", err))
			}

			var sources []source.Source
			if named != nil {
				// An explicit --harness is the authorization for this
				// invocation: exclusion and the auto opt-in do not
				// apply. Presence is probed here — the only path that
				// consults it — before Discover, so absent storage is
				// disclosed rather than silently exiting.
				if err := checkPresence(cmd.Context(), named); err != nil {
					return err
				}
				sources = []source.Source{named}
			} else {
				sel, err := sweepSources(cfg)
				if err != nil {
					return clasterr.New("validation.config", fmt.Sprintf("reading capture config: %v", err))
				}
				sources = sel
			}

			captured, diags, failed, err := Run(cmd.Context(), Deps{
				Root:            root,
				Sources:         sources,
				AutoDismissNoop: autoDismiss,
				Now:             time.Now,
			})
			if err := reportOutcome(streams, named, diags, failed, err); err != nil {
				return err
			}

			if flags.JSON {
				return writeJSON(streams, captured, failed)
			}
			return writeHuman(streams, captured)
		},
	}
	cmd.Flags().StringVar(&harness, "harness", "", "capture only this source's sessions, bypassing exclusion and the network opt-in")
	// OutputSchema stays unfilled until a consumer exists (C3.7/V35).
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// reportOutcome discloses one run's non-fatal outcomes to stderr and
// returns the invocation's verdict error. Ordering is deliberate:
// accumulated diagnostics always print first, then — on the sweep path
// — one "capture: <source>: <err>" line per source that could not
// enumerate at all (the run still exits 0: V13 holds that a source
// outage is never a failed run, but no reported success may hide
// incomplete work). Only then does a fatal error propagate, so an abort
// never swallows the disclosures already collected.
//
// The explicit --harness path reports its single source's failure as
// the verdict instead — capture.source-unavailable (exit 1) naming the
// source and its error — rather than as a continue-past diagnostic:
// one named source, one answer.
func reportOutcome(streams *iostreams.Streams, named source.Source, diags []source.Diagnostic, failed []SourceFailure, runErr error) error {
	for _, d := range diags {
		if _, err := fmt.Fprintf(streams.Err, "capture: %s: %v\n", d.Path, d.Err); err != nil {
			return err
		}
	}
	if named == nil {
		for _, f := range failed {
			if _, err := fmt.Fprintf(streams.Err, "capture: %s: %v\n", f.Source, f.Err); err != nil {
				return err
			}
		}
	}
	if runErr != nil {
		return runErr
	}
	if named != nil && len(failed) > 0 {
		return clasterr.New("capture.source-unavailable",
			fmt.Sprintf("source %q unavailable: %v", failed[0].Source, failed[0].Err))
	}
	return nil
}

// namedSource validates the --harness flag against the registry —
// validation.unknown-harness naming the implemented set when the name
// isn't implemented (V30). Called only when the flag is set, and before
// config.Load so a bad name reports even under malformed config.
func namedSource(harness string) (source.Source, error) {
	src, ok := sourceregistry.Lookup(harness)
	if !ok {
		return nil, clasterr.New("validation.unknown-harness",
			fmt.Sprintf("unknown harness %q; implemented: %s", harness, strings.Join(sourceregistry.Names, ", ")))
	}
	return src, nil
}

// checkPresence probes an explicitly requested source through the
// OPTIONAL source.Presence interface — the only place the probe runs —
// so `capture --harness <name>` on absent storage fails as
// capture.source-unavailable naming the source and the probed root
// (Present's error, verbatim) instead of silently exiting. A source
// that does not implement the probe degrades to pre-contract behavior:
// its empty Discover stays a quiet exit 0.
func checkPresence(ctx context.Context, src source.Source) error {
	// Cancellation is fatal here as in the sweep — never disclosed as a
	// source failure. A probe may consult ctx (a network source's
	// credential/endpoint check, unlike claude's local readdir), so both
	// seams are checked: a dead ctx before the probe, and a probe error
	// that IS context teardown. Both classify internal.capture, the same
	// verdict Run gives cancellation inside the sweep.
	if err := ctx.Err(); err != nil {
		return clasterr.New("internal.capture", err.Error())
	}
	p, ok := src.(source.Presence)
	if !ok {
		return nil
	}
	if err := p.Present(ctx); err != nil {
		if isCancellation(err) {
			return clasterr.New("internal.capture", err.Error())
		}
		return clasterr.New("capture.source-unavailable",
			fmt.Sprintf("source %q unavailable: %v", src.Name(), err))
	}
	return nil
}

// sweepSources builds the walk set for a bare invocation: the registry
// table filtered by the settled inclusion predicate — implemented &&
// !excluded && (local || capture.<name>.auto).
func sweepSources(cfg config.Config) ([]source.Source, error) {
	excluded, auto, err := capturePolicy(cfg)
	if err != nil {
		return nil, err
	}
	return selectSweep(sourceregistry.All, excluded, auto), nil
}

// selectSweep applies the sweep inclusion predicate over the given rows
// and returns a NEW slice. registry.All is the shared authority for
// implemented sources and is never mutated or filtered in place here —
// sessions/show/analyze resolve through it regardless of exclusion, so
// selection can never disable a reader (the contract's reader-
// independence constraint).
func selectSweep(all []source.Source, excluded, auto map[string]bool) []source.Source {
	set := make([]source.Source, 0, len(all))
	for _, src := range all {
		if excluded[src.Name()] {
			continue
		}
		if src.Model() == source.Network && !auto[src.Name()] {
			continue
		}
		set = append(set, src)
	}
	return set
}

// capturePolicy reads capture's selection keys out of the merged config:
// "exclude" (implemented source names a bare sweep skips) and per-source
// sections "capture.<name>.auto" (the opt-in that admits a network-model
// source to sweeps; inert on local sources). An unimplemented name in
// either position is an error naming the value and the implemented set
// — a typo'd source name must never sit silently accepted. Both follow
// the per-key read-time-default pattern autoDismissNoop set: top-level
// merge replacement means a user config carrying only one capture key
// must not lose the others' defaults.
func capturePolicy(cfg config.Config) (excluded, auto map[string]bool, err error) {
	section, err := captureSection(cfg)
	if err != nil {
		return nil, nil, err
	}

	excluded = map[string]bool{}
	if raw, present := section["exclude"]; present && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("capture: config key \"capture.exclude\" must be a list of source names, got %T", raw)
		}
		for _, item := range list {
			name, ok := item.(string)
			if !ok {
				return nil, nil, fmt.Errorf("capture: config key \"capture.exclude\" must be a list of source names, got an item of type %T", item)
			}
			if _, ok := sourceregistry.Lookup(name); !ok {
				return nil, nil, fmt.Errorf("capture: config key \"capture.exclude\" names unimplemented source %q; implemented: %s",
					name, strings.Join(sourceregistry.Names, ", "))
			}
			excluded[name] = true
		}
	}

	auto = map[string]bool{}
	for key, raw := range section {
		if key == "auto_dismiss_noop" || key == "exclude" {
			continue
		}
		// A mapping under capture: is a per-source section — the only
		// thing a "capture.<name>" mapping can mean — so its key is a
		// source name and is validated as one. Non-mapping keys are
		// unknown config keys, which stay silently ignored (P6).
		var m map[string]any
		switch sub := raw.(type) {
		case map[string]any:
			m = sub
		case config.Config:
			m = sub
		default:
			continue
		}
		if _, ok := sourceregistry.Lookup(key); !ok {
			return nil, nil, fmt.Errorf("capture: config section \"capture.%s\" names an unimplemented source; implemented: %s",
				key, strings.Join(sourceregistry.Names, ", "))
		}
		if v, present := m["auto"]; present && v != nil {
			b, ok := v.(bool)
			if !ok {
				return nil, nil, fmt.Errorf("capture: config key \"capture.%s.auto\" must be a boolean, got %T", key, v)
			}
			auto[key] = b
		}
	}
	return excluded, auto, nil
}

// captureSection returns config's "capture" section as a plain mapping
// (nil, nil when the key is absent). yaml.v3 decodes a nested mapping
// into its parent map's own type, so the section arrives as
// config.Config (not map[string]any) from config.Load — accept both
// spellings of the same underlying map.
func captureSection(cfg config.Config) (map[string]any, error) {
	raw, present := cfg["capture"]
	if !present || raw == nil {
		return nil, nil
	}
	switch m := raw.(type) {
	case map[string]any:
		return m, nil
	case config.Config:
		return m, nil
	default:
		return nil, fmt.Errorf("capture: config key \"capture\" must be a mapping, got %T", raw)
	}
}

// autoDismissNoop reads config capture.auto_dismiss_noop (M3), default
// true. A present-but-mistyped value is an error naming the key, never a
// silent fallback (the ConfiguredCutoff posture).
func autoDismissNoop(cfg config.Config) (bool, error) {
	section, err := captureSection(cfg)
	if err != nil {
		return false, err
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

// unavailableJSON is one row of the --json payload's "unavailable"
// list: one source that could not enumerate at all during the sweep.
type unavailableJSON struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

func writeJSON(streams *iostreams.Streams, captured []Captured, failed []SourceFailure) error {
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
	var unavailable []unavailableJSON
	for _, f := range failed {
		unavailable = append(unavailable, unavailableJSON{Source: f.Source, Error: f.Err.Error()})
	}
	payload := struct {
		Captured []capturedJSON `json:"captured"`
		// Additive, present only when non-empty: a clean sweep's payload
		// is exactly {"captured": [...]} as before.
		Unavailable []unavailableJSON `json:"unavailable,omitempty"`
	}{Captured: rows, Unavailable: unavailable}
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
