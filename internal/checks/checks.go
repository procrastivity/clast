// Package checks is doctor's pluggable check registry (C4.7): doctor grows
// by registering a new Check here and in the doctor verb's call site, never
// by a second command or a second output path. Findings are flat
// {code, message} pairs with no severity levels; a code with the
// "advisory." prefix never fails the run.
package checks

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/registry"
	"github.com/procrastivity/clast/internal/manifest"
)

// Finding is one reported condition: a stable dotted machine code and the
// human-readable message.
type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Check is one doctor check. Every check reports every instance of its
// condition present at the time of the run — never just the first (C4.7).
type Check func() ([]Finding, error)

// Run executes checks in order and concatenates their findings. A check
// error aborts the run — that is an I/O failure, not a finding.
func Run(cs ...Check) ([]Finding, error) {
	var findings []Finding
	for _, c := range cs {
		fs, err := c()
		if err != nil {
			return nil, err
		}
		findings = append(findings, fs...)
	}
	return findings, nil
}

// StaleHarnessCode is the advisory code for a generated harness artifact
// the current binary would render differently — drift the user resolves by
// re-running install, so it never fails doctor.
const StaleHarnessCode = "advisory.stale-harness-artifact"

// TargetState is one registered harness's reported drift state — the
// per-target fact doctor's --json and text output carry beside findings
// (C4.5).
type TargetState struct {
	Harness string        `json:"harness"`
	Dir     string        `json:"dir"`
	State   harness.State `json:"state"`
}

// HarnessTargets derives every registered harness's drift state
// (harness.Status, C4.6's three comparisons folded into C4.5's six
// states) in registry.All's order, and returns both doctor's findings and
// the per-target states doctor reports beside them. root is the
// *cobra.Command NewRootCommand is assembling, captured by reference — the
// same pattern install uses — so the manifest this builds reflects every
// verb actually registered.
//
// One registry walk gives both, because the state already needs each
// target's install dir and generated files.
//
// Findings by state (C4.7: advisory codes never fail the run;
// docs/contract-v1-2-reconcile/decisions.md §1.5):
//   - Current, Missing: no finding.
//   - Stale: the per-file advisory.stale-harness-artifact findings, one
//     per drifted file, exhaustively.
//   - Modified: those same per-file findings too (binary-vs-stamp is
//     independent of disk, C4.6), plus one
//     advisory.modified-harness-target finding.
//   - UnownedConflict: one advisory.unowned-harness-target finding.
//   - Incompatible: one refusal.incompatible-harness-target finding, and
//     no per-file findings — a stamp Status could not trust cannot be
//     diffed against either. This is the one state that fails the run,
//     because its code keeps the "refusal." prefix.
func HarnessTargets(root *cobra.Command, build buildinfo.Info) ([]Finding, []TargetState, error) {
	m, err := manifest.Build(root, build)
	if err != nil {
		return nil, nil, err
	}

	var findings []Finding
	targets := make([]TargetState, 0, len(registry.All))

	for _, h := range registry.All {
		dir, err := h.InstallDir()
		if err != nil {
			return nil, nil, err
		}
		files, err := h.Generate(m)
		if err != nil {
			return nil, nil, err
		}
		state, err := harness.Status(dir, files)
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, TargetState{Harness: h.Name, Dir: dir, State: state})

		switch state {
		case harness.Stale:
			fs, err := staleFileFindings(h.Name, dir, files)
			if err != nil {
				return nil, nil, err
			}
			findings = append(findings, fs...)
		case harness.Modified:
			fs, err := staleFileFindings(h.Name, dir, files)
			if err != nil {
				return nil, nil, err
			}
			findings = append(findings, fs...)
			findings = append(findings, driftFinding(h.Name, dir, state))
		case harness.UnownedConflict, harness.Incompatible:
			findings = append(findings, driftFinding(h.Name, dir, state))
		}
	}

	return findings, targets, nil
}

// staleFileFindings reports the per-file advisory.stale-harness-artifact
// findings for a target whose stamp is known to parse and match
// manifest.SchemaVersion (Stale and Modified both establish that before
// calling this) — the binary-vs-stamp comparison, independent of whatever
// disk-vs-stamp said (C4.6). files is the current binary's generated
// output, already rendered by the caller, so this never re-generates it.
func staleFileFindings(harnessName, dir string, files map[string][]byte) ([]Finding, error) {
	stamp, ok, err := manifest.ReadStamp(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	want := manifest.ChecksumFiles(files)
	drifted := manifest.Drift(want, stamp)
	findings := make([]Finding, 0, len(drifted))
	for _, d := range drifted {
		findings = append(findings, Finding{
			Code: StaleHarnessCode,
			Message: fmt.Sprintf("found: %s harness artifact %s %s since last install; run `clast install %s` to refresh it",
				harnessName, d.Path, d.Reason, harnessName),
		})
	}
	return findings, nil
}

// driftFindingCodes gives doctor's finding code for each unsafe state.
// Only incompatible fails the run, so it alone keeps the refusal code;
// the other two are advisory (C4.7,
// docs/contract-v1-2-reconcile/decisions.md §1.5).
var driftFindingCodes = map[harness.State]string{
	harness.UnownedConflict: "advisory.unowned-harness-target",
	harness.Modified:        "advisory.modified-harness-target",
	harness.Incompatible:    harness.CodeIncompatible,
}

// driftFinding reports an unsafe state with the same fact and remedy that
// install's refusal names (harness.Risk, harness.ForceRemedy).
func driftFinding(harnessName, dir string, state harness.State) Finding {
	return Finding{
		Code:    driftFindingCodes[state],
		Message: fmt.Sprintf("found: %s — %s", harness.Risk(harnessName, dir, state), harness.ForceRemedy(harnessName, state)),
	}
}
