// Package install implements the `clast install [harness]` verb: it
// runs the manifest pipeline, filters to plumbing verbs, and writes the
// result to the target harness's install path, stamped (C4). Given a
// harness name, it installs (or reinstalls) into just that target; the
// registry's harnesses (internal/harness/registry) are the recognized
// targets, and an unrecognized name fails validation rather than silently
// no-op'ing. Run bare, it instead detects every harness available on this
// host and installs into each one in turn, reporting a per-harness result
// rather than aborting the whole run for one harness's refusal.
//
// A harness may carry more than one stamped target (SURFACE V32:
// claude-code projects three skills, each its own independently stamped
// tree) plus one splice target (C4.8, a foreign-file path edit with no
// stamp of its own). Before writing to any stamped target, this package
// asks internal/harness.Status and refuses (internal/harness.Refusal) on
// any of its three unsafe states — a target tree a human edited by hand,
// foreign unstamped content, or a stamp this binary cannot use; a tree
// already byte-identical to what this binary would generate is reported
// current rather than rewritten; --force skips Status and overwrites
// unconditionally, in both modes. The first refusing target stops that
// harness's install, same as a single-target harness always worked (C4.7:
// doctor, not install, is exhaustive). A splice target has no refusal of
// its own in this build (C4.8's idempotency check stands in for it): it is
// applied after every stamped target succeeds.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/registry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// Command constructs the `clast install <harness>` verb. root is the
// *cobra.Command NewRootCommand is assembling, captured by reference so
// the manifest it builds at RunE time reflects every verb ultimately
// registered on it (see internal/verbs/manifest for the same pattern).
func Command(streams *iostreams.Streams, build buildinfo.Info, root *cobra.Command) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install <harness>",
		Short: "render and install clast's self-projection into an agent harness",
		Long: "render and install clast's self-projection into an agent harness.\n\n" +
			"With no harness name, clast install detects every harness available on this host and installs into each one, reporting a result per harness. Naming a harness installs (or reinstalls) only that one.\n\n" +
			"Available harnesses: " + strings.Join(registry.Names, ", ") + ".\n\n" +
			"Refuses to overwrite a target that was hand-edited or holds unstamped content; pass --force to overwrite it anyway, in either mode. A tree that already matches what this binary would write is reported as current and left untouched; --force rewrites it anyway.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			if len(args) == 0 {
				m, err := manifest.Build(root, build)
				if err != nil {
					return err
				}
				return installAll(streams, flags, m, force)
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

			m, err := manifest.Build(root, build)
			if err != nil {
				return err
			}

			// harnessName was already validated against registry.Names
			// above, so Lookup is guaranteed to find it here.
			h, _ := registry.Lookup(harnessName)

			targets, splice, err := installHarness(h, m, force)
			if err != nil {
				return err
			}

			return writeTargetedResult(streams, flags, harnessName, targets, splice)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite a target tree even if it was hand-edited or not written by clast install")
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// targetResult is one of a harness's stamped targets' outcome: "current"
// (already matched what this binary would generate, nothing written) or
// "installed" (written just now).
type targetResult struct {
	Target string `json:"target"`
	Dir    string `json:"dir"`
	Status string `json:"status"`
}

// spliceResult is a harness's splice target's outcome (C4.8): "spliced"
// (the hook entry was just written, with a one-time .bak if the file
// already existed) or "already-spliced" (idempotent re-install, nothing
// written).
type spliceResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// installHarness drives every one of h's stamped Targets in order — the
// same Status/Refusal/Install sequence a single-target harness always ran,
// just once per target — stopping at the first refusal (C4.7: install is
// not exhaustive; doctor is). Once every target succeeds, it applies h's
// splice, if it has one. force skips the Status/Refusal check entirely and
// always calls Target.Install, matching --force's existing meaning.
func installHarness(h registry.Harness, m manifest.Manifest, force bool) ([]targetResult, *spliceResult, error) {
	results := make([]targetResult, 0, len(h.Targets))

	for _, target := range h.Targets {
		installDir, err := target.InstallDir()
		if err != nil {
			return nil, nil, err
		}

		if !force {
			files, err := target.Generate(m)
			if err != nil {
				return nil, nil, err
			}
			s, err := harness.Status(installDir, files)
			if err != nil {
				return nil, nil, err
			}
			if err := harness.Refusal(h.Name, installDir, s, harness.ForceRemedy(h.Name, s)); err != nil {
				return nil, nil, err
			}
			if s == harness.Current {
				results = append(results, targetResult{Target: target.Label, Dir: installDir, Status: "current"})
				continue
			}
			// Missing or Stale: fall through and install below.
		}

		dir, err := target.Install(m)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, targetResult{Target: target.Label, Dir: dir, Status: "installed"})
	}

	var splice *spliceResult
	if h.Splice != nil {
		outcome, err := h.Splice()
		if err != nil {
			return results, nil, err
		}
		splice = &spliceResult{Path: outcome.Path, Status: outcome.Status}
	}

	return results, splice, nil
}

// writeTargetedResult renders the targeted (single-harness) mode's outcome
// to streams.Out: under --json, {"harness","targets","splice"}; in human
// mode, one line per target and one for the splice, if any.
func writeTargetedResult(streams *iostreams.Streams, flags cliflags.Flags, harnessName string, targets []targetResult, splice *spliceResult) error {
	if flags.JSON {
		payload := struct {
			Harness string         `json:"harness"`
			Targets []targetResult `json:"targets"`
			Splice  *spliceResult  `json:"splice,omitempty"`
		}{Harness: harnessName, Targets: targets, Splice: splice}
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(streams.Out, string(b))
		return err
	}

	for _, t := range targets {
		var line string
		if t.Status == "current" {
			line = fmt.Sprintf("%s %s skill at %s is already current", harnessName, t.Target, t.Dir)
		} else {
			line = fmt.Sprintf("installed %s %s skill at %s", harnessName, t.Target, t.Dir)
		}
		if _, err := fmt.Fprintln(streams.Out, line); err != nil {
			return err
		}
	}
	if splice != nil {
		line := spliceLine(splice)
		if _, err := fmt.Fprintln(streams.Out, line); err != nil {
			return err
		}
	}
	return nil
}

// spliceLine renders one splice outcome as a human-mode line.
func spliceLine(splice *spliceResult) string {
	if splice.Status == "already-spliced" {
		return fmt.Sprintf("%s already has the clast SessionStart hook", splice.Path)
	}
	return fmt.Sprintf("spliced the clast SessionStart hook into %s", splice.Path)
}

// harnessResult is one row of the bare-invocation report: what happened
// when installAll considered a single harness. Exactly one of
// {Targets/Splice}, Reason, or Error is populated, matching Status.
type harnessResult struct {
	Harness string              `json:"harness"`
	Status  string              `json:"status"`
	Targets []targetResult      `json:"targets,omitempty"`
	Splice  *spliceResult       `json:"splice,omitempty"`
	Reason  string              `json:"reason,omitempty"`
	Error   *harnessResultError `json:"error,omitempty"`
}

// harnessResultError is a refusal's code and message, carried inside a
// harnessResult exactly as clasterr.Error would render them under
// --json.
type harnessResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// aggregateStatus folds a harness's per-target and splice outcomes into
// the one status word installAll reports for that harness: "installed" if
// anything was actually written, "current" if every target and the splice
// (if any) were already up to date.
func aggregateStatus(targets []targetResult, splice *spliceResult) string {
	for _, t := range targets {
		if t.Status == "installed" {
			return "installed"
		}
	}
	if splice != nil && splice.Status == "spliced" {
		return "installed"
	}
	return "current"
}

// installAll is the bare `clast install` (no harness argument) path: it
// walks registry.All in order, installing into every harness Available
// reports present on this host and recording one result per harness —
// installed, current (every target and splice already matched what this
// binary would generate), skipped (not detected), or refused (a target's
// Status found one of its three unsafe states, without --force; each
// carries its own code, per harness.Refusal). A refusal on one harness
// does not stop the run; any other error (an I/O failure reading or
// writing a harness's install dir) does, since that is not a policy
// decision this loop can route around. After printing every result, it
// returns a single refusal.harness-targets-refused error naming every
// refused harness so the process still exits non-zero, unless nothing was
// refused.
func installAll(streams *iostreams.Streams, flags cliflags.Flags, m manifest.Manifest, force bool) error {
	results := make([]harnessResult, 0, len(registry.All))
	var refused []string

	for _, h := range registry.All {
		if !h.Available() {
			results = append(results, harnessResult{Harness: h.Name, Status: "skipped", Reason: "not detected"})
			continue
		}

		targets, splice, err := installHarness(h, m, force)
		if err != nil {
			var terr *clasterr.Error
			if errors.As(err, &terr) {
				results = append(results, harnessResult{
					Harness: h.Name,
					Status:  "refused",
					Error:   &harnessResultError{Code: terr.Code, Message: terr.Message},
				})
				refused = append(refused, h.Name)
				continue
			}
			return err
		}
		results = append(results, harnessResult{
			Harness: h.Name,
			Status:  aggregateStatus(targets, splice),
			Targets: targets,
			Splice:  splice,
		})
	}

	if err := writeInstallAllResults(streams, flags, results); err != nil {
		return err
	}

	if len(refused) > 0 {
		return clasterr.New("refusal.harness-targets-refused",
			fmt.Sprintf("refused — %d harness target(s) hold hand-edited, unstamped, or incompatible content (%s); re-run `clast install <harness> --force` for each to overwrite",
				len(refused), strings.Join(refused, ", ")))
	}
	return nil
}

// writeInstallAllResults renders installAll's per-harness results to
// streams.Out: one JSON value under --json, or one line per harness in
// human mode, followed by a hint when every harness was skipped.
func writeInstallAllResults(streams *iostreams.Streams, flags cliflags.Flags, results []harnessResult) error {
	if flags.JSON {
		payload := struct {
			Results []harnessResult `json:"results"`
		}{Results: results}
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(streams.Out, string(b))
		return err
	}

	anyDetected := false
	for _, r := range results {
		var line string
		switch r.Status {
		case "installed", "current":
			anyDetected = true
			var parts []string
			for _, t := range r.Targets {
				parts = append(parts, fmt.Sprintf("%s %s at %s", t.Status, t.Target, t.Dir))
			}
			if r.Splice != nil {
				parts = append(parts, spliceLine(r.Splice))
			}
			line = fmt.Sprintf("%s: %s", r.Harness, strings.Join(parts, "; "))
		case "refused":
			anyDetected = true
			// The refusal message already opens with "refused — "; the
			// line's own leading "refused <harness>" carries that word.
			line = fmt.Sprintf("refused %s — %s", r.Harness, strings.TrimPrefix(r.Error.Message, "refused — "))
		default: // "skipped"
			line = fmt.Sprintf("skipped %s — %s", r.Harness, r.Reason)
		}
		if _, err := fmt.Fprintln(streams.Out, line); err != nil {
			return err
		}
	}

	if !anyDetected {
		if _, err := fmt.Fprintln(streams.Out, "no harness detected on this host; install one explicitly: clast install <harness>"); err != nil {
			return err
		}
	}
	return nil
}
