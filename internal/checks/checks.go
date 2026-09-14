// Package checks is doctor's pluggable check registry (C4.7): doctor grows
// by registering a new Check here and in the doctor verb's call site, never
// by a second command or a second output path. Findings are flat
// {code, message} pairs with no severity levels; a code with the
// "advisory." prefix never fails the run.
package checks

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/registry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	tierregistry "github.com/procrastivity/clast/internal/registry"
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

// MissingHarnessTargetCode is the advisory code for a stamped harness
// target that is Missing while a sibling target under the same harness is
// not (SURFACE V28, step-04) — partial-install drift: install.go's
// install/uninstall.go both walk a harness's targets and stop at the
// first refusal, so a run interrupted partway through, or a tree deleted
// by hand afterward, can leave exactly this shape. See
// missingTargetFindings for the all-Missing case this deliberately leaves
// clean.
const MissingHarnessTargetCode = "advisory.missing-harness-target"

// TamperedHarnessSpliceCode is the advisory code for a harness's splice
// target whose shim entry is present but no longer matches the pinned
// command string exactly (harness.SpliceTampered, SURFACE V28, step-04).
const TamperedHarnessSpliceCode = "advisory.tampered-harness-splice"

// MissingHarnessSpliceCode is the advisory code for a harness's splice
// target with no recognizable shim entry at all (harness.SpliceAbsent),
// reported only when at least one of that harness's stamped targets is
// installed (SURFACE V28, step-04) — see spliceDriftFindings.
const MissingHarnessSpliceCode = "advisory.missing-harness-splice"

// OrphanedHarnessSpliceCode is the advisory code for a harness's splice
// target whose shim entry is present and current (harness.SpliceCurrent)
// while every one of the harness's stamped targets is Missing (F1 seal
// finding). This is the shape an interrupted `clast uninstall <harness>`
// leaves behind: uninstall walks a harness's targets in order and only
// calls Unsplice once every one of them has succeeded
// (internal/verbs/uninstall.go); a target that refuses, or was already
// gone, before Unsplice runs stops the whole command first, so the hook
// is never reached and survives. Once that happens every later
// `clast uninstall <harness>` aborts the same way (the same missing
// target refuses again), so no clast verb can ever reach Unsplice for
// that hook again — doctor is the only place left that can even notice
// it, and its message says so plainly rather than naming a verb that
// does not exist: hand-removal of the SessionStart hook from
// settings.json is the only remedy. See spliceDriftFindings' SpliceCurrent
// case.
const OrphanedHarnessSpliceCode = "advisory.orphaned-harness-splice"

// RegistryMovedCloneCode is the report-only offer for a current directory
// whose remote belongs to a known project but whose git common-dir is not
// registered on this machine. The message names the concrete relink
// candidates when one exists and always names init as the alternative.
const RegistryMovedCloneCode = "advisory.unknown-clone-remote-known"

// RegistryAdoptionCode is the report-only offer for a registered, keyless
// project whose recorded identity remote is now present in the current
// clone.
const RegistryAdoptionCode = "advisory.keyless-project-remote-known"

// TargetState is one registered harness target's reported drift state —
// the per-target fact doctor's --json and text output carry beside
// findings (C4.5). A harness may carry more than one stamped target
// (SURFACE V32: claude-code projects three skills, each its own target),
// so Harness alone does not identify a row; Target does, within that
// harness.
type TargetState struct {
	Harness string        `json:"harness"`
	Target  string        `json:"target"`
	Dir     string        `json:"dir"`
	State   harness.State `json:"state"`
}

// HarnessTargets derives every registered harness's stamped targets'
// drift state (harness.Status, C4.6's three comparisons folded into
// C4.5's six states) in registry.All's order, and returns both doctor's
// findings and the per-target states doctor reports beside them. root is
// the *cobra.Command NewRootCommand is assembling, captured by reference —
// the same pattern install uses — so the manifest this builds reflects
// every verb actually registered.
//
// A harness's splice target (C4.8), if it has one, is folded in below via
// spliceDriftFindings (SURFACE V28, step-04): its own small vocabulary
// (harness.SpliceState), since a foreign file carries no stamp to diff the
// way a stamped target does.
//
// One registry walk gives both, because the state already needs each
// target's install dir and generated files.
//
// Findings by state (C4.7: advisory codes never fail the run; toolsmith's
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
//
// Beside every target's own state, two more install-drift questions are
// asked once per harness, after its targets are known (SURFACE V28,
// step-04): missingTargetFindings (a target Missing while a sibling is
// not — see its own doc comment for the all-Missing exception) and, for a
// harness with a splice target, spliceDriftFindings (the shim entry's own
// three-state drift). Both follow the same advisory-unless-untrustworthy
// severity split incompatible already established: recoverable-by-
// reinstall conditions stay advisory (never fail the run); a settings.json
// that will not even parse keeps harness.CodeMalformedSplice's
// "validation." prefix, the splice target's analog of Incompatible, and so
// is the one splice-drift finding that does fail it.
func HarnessTargets(root *cobra.Command, build buildinfo.Info) ([]Finding, []TargetState, error) {
	m, err := manifest.Build(root, build)
	if err != nil {
		return nil, nil, err
	}

	var findings []Finding
	var targets []TargetState

	for _, h := range registry.All {
		harnessTargets := make([]TargetState, 0, len(h.Targets))

		for _, target := range h.Targets {
			dir, err := target.InstallDir()
			if err != nil {
				return nil, nil, err
			}
			files, err := target.Generate(m)
			if err != nil {
				return nil, nil, err
			}
			state, err := harness.Status(dir, files)
			if err != nil {
				return nil, nil, err
			}
			harnessTargets = append(harnessTargets, TargetState{Harness: h.Name, Target: target.Label, Dir: dir, State: state})

			// h.Name, not a per-target label, is what harness.Risk and
			// harness.ForceRemedy fold into a runnable `clast install
			// <harness> --force` suggestion below — dir already
			// disambiguates which of the harness's targets (e.g. which
			// claude-code skill) a finding is about.
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

		targets = append(targets, harnessTargets...)
		findings = append(findings, missingTargetFindings(h.Name, harnessTargets)...)

		if h.SpliceStatus != nil {
			fs, err := spliceDriftFindings(h.Name, harnessTargets, h.SpliceStatus)
			if err != nil {
				return nil, nil, err
			}
			findings = append(findings, fs...)
		}
	}

	return findings, targets, nil
}

// anyInstallEvidence reports whether states carries at least one target
// state that proves `clast install <harness>` (or a predecessor of it) ran
// against this target at some point — the signal both missingTargetFindings
// and spliceDriftFindings' SpliceAbsent case gate their own finding on, so a
// never-installed harness reads as clean rather than drifted (SURFACE V28,
// step-04).
//
// Current, Stale, and Modified all mean a stamp this binary trusts is
// present, so they count. Incompatible also counts — its stamp exists and
// once matched schemaVersion, or install would never have written it — the
// stamp merely being unreadable now does not erase that history. Missing
// obviously does not count. UnownedConflict is the one state that does
// not: it means content sits at the target path with no stamp at all,
// which proves nothing about clast's own install ever having run there.
func anyInstallEvidence(states []TargetState) bool {
	for _, s := range states {
		switch s.State {
		case harness.Current, harness.Stale, harness.Modified, harness.Incompatible:
			return true
		}
	}
	return false
}

// missingTargetFindings reports partial-install drift (SURFACE V28,
// step-04): a harness whose targets are a mix of installed (or drifted)
// and Missing. A skill tree absent while a sibling skill tree is present
// can only mean install stopped partway through (it walks targets in
// order and stops at the first refusal) or a tree was deleted by hand
// afterward — either way, `clast install <harness>` fixes it.
//
// When every one of the harness's targets is Missing, that reads as
// "never installed" instead, the same clean reading a lone Missing target
// already gets from harness.Status/C4.5, so this reports nothing for that
// all-Missing case — the reconciliation this Matter's step-04 was asked
// to record (matter.md): absence is drift only relative to a sibling that
// proves install was intended, never on its own.
func missingTargetFindings(harnessName string, states []TargetState) []Finding {
	if !anyInstallEvidence(states) {
		return nil
	}

	var findings []Finding
	for _, s := range states {
		if s.State != harness.Missing {
			continue
		}
		findings = append(findings, Finding{
			Code: MissingHarnessTargetCode,
			Message: fmt.Sprintf("found: %s %s skill tree is missing at %s while other %s skills are installed; run `clast install %s` to restore it",
				harnessName, s.Target, s.Dir, harnessName, harnessName),
		})
	}
	return findings
}

// spliceDriftFindings reports install-drift for a harness's splice target
// (C4.8, SURFACE V28 step-04), by probing it (probe, h.SpliceStatus) and
// reading its harness.SpliceState:
//
//   - SpliceMalformed: always a finding, and the one splice-drift finding
//     that fails the run — harness.CodeMalformedSplice keeps the
//     "validation." prefix, not "advisory.", because a settings.json that
//     will not parse blocks `clast install`/`clast uninstall` outright,
//     the same severity Incompatible carries for a stamped target.
//   - SpliceTampered: always a finding — an entry that looks like the
//     shim but no longer matches its pinned bytes is drift regardless of
//     whether any skill is installed, since the entry's mere presence
//     proves install wrote it.
//   - SpliceAbsent: a finding only when at least one of the harness's
//     stamped targets is not Missing — an absent shim alongside a
//     fully-uninstalled harness is the clean state, the same all-Missing
//     exception missingTargetFindings applies to its own targets.
//   - SpliceCurrent: no finding, unless every one of the harness's
//     stamped targets is Missing too (F1) — an orphaned hook an
//     interrupted uninstall left behind with no target left to prove it,
//     reported as advisory.orphaned-harness-splice. Unlike SpliceAbsent's
//     all-Missing case (genuinely clean: nothing was ever installed and
//     nothing was ever spliced), a *present, current* hook alongside
//     all-Missing targets proves install did run — the hook just outlived
//     the targets it was installed for.
func spliceDriftFindings(harnessName string, states []TargetState, probe func() (harness.SpliceProbe, error)) ([]Finding, error) {
	p, err := probe()
	if err != nil {
		return nil, err
	}

	switch p.State {
	case harness.SpliceMalformed:
		return []Finding{{
			Code: harness.CodeMalformedSplice,
			Message: fmt.Sprintf("found: %s is not valid JSON; fix it by hand, then re-run `clast install %s` or `clast uninstall %s`",
				p.Path, harnessName, harnessName),
		}}, nil
	case harness.SpliceTampered:
		return []Finding{{
			Code: TamperedHarnessSpliceCode,
			Message: fmt.Sprintf("found: %s's clast SessionStart hook in %s no longer matches the pinned shim command; run `clast install %s` to restore it",
				harnessName, p.Path, harnessName),
		}}, nil
	case harness.SpliceAbsent:
		if !anyInstallEvidence(states) {
			return nil, nil
		}
		return []Finding{{
			Code: MissingHarnessSpliceCode,
			Message: fmt.Sprintf("found: %s skills are installed but %s carries no clast SessionStart hook; run `clast install %s` to restore it",
				harnessName, p.Path, harnessName),
		}}, nil
	default: // harness.SpliceCurrent
		if anyInstallEvidence(states) {
			return nil, nil
		}
		return []Finding{{
			Code: OrphanedHarnessSpliceCode,
			Message: fmt.Sprintf("found: %s carries a clast SessionStart hook but no %s skills are installed; no clast verb can safely remove it — remove the hook from %s's hooks.SessionStart array by hand",
				p.Path, harnessName, p.Path),
		}}, nil
	}
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
// the other two are advisory (C4.7, toolsmith's
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

// RegistryFindings is the report-only registry check. A keyless project with
// its recorded identity remote present gets an adoption offer. A known remote
// on an unknown common-dir gets a relink-or-init offer, naming concrete local
// clone IDs when available. Neither case writes anything; the user must make
// the choice explicitly.
func RegistryFindings(ctx context.Context, root, dir string) ([]Finding, error) {
	common, err := tierregistry.CommonDir(ctx, dir)
	if err != nil {
		return nil, nil
	}
	machine, err := journal.Hostname()
	if err != nil {
		return nil, err
	}
	view, _, err := tierregistry.Load(root)
	if err != nil {
		return nil, err
	}
	var current []tierregistry.ProjectView
	for _, p := range view.Projects {
		for _, c := range p.Clones {
			if c.Machine == machine && c.GitCommonDir == common {
				current = append(current, p)
				break
			}
		}
	}
	remotes, err := tierregistry.Remotes(ctx, dir)
	if err != nil {
		return nil, err
	}
	if len(current) > 0 {
		findings := make([]Finding, 0, len(current))
		seen := make(map[string]bool)
		for _, p := range current {
			if p.Project.Remote != "" || seen[p.Project.ID] {
				continue
			}
			seen[p.Project.ID] = true
			name := p.Project.IdentityRemote
			if name == "" {
				name = "origin"
			}
			raw, ok := remotes[name]
			if !ok {
				continue
			}
			remote, e := tierregistry.NormalizeRemote(raw)
			if e != nil {
				continue
			}
			findings = append(findings, Finding{
				Code: RegistryAdoptionCode,
				Message: fmt.Sprintf("found: keyless project %q now has identity remote %q; choose `clast adopt` to record it (no automatic adoption)",
					p.Project.Slug, remote),
			})
		}
		return findings, nil
	}

	names := make([]string, 0, len(remotes))
	for n := range remotes {
		names = append(names, n)
	}
	sort.Strings(names)
	matchedRemotes := make(map[string]bool)
	matchedProjects := make(map[string]bool)
	var findings []Finding
	for _, n := range names {
		r, e := tierregistry.NormalizeRemote(remotes[n])
		if e != nil || matchedRemotes[r] {
			continue
		}
		matchedRemotes[r] = true
		for _, p := range view.Projects {
			if p.Project.Remote != r || matchedProjects[p.Project.ID] {
				continue
			}
			matchedProjects[p.Project.ID] = true
			ids := make([]string, 0)
			for _, c := range p.Clones {
				if c.Machine == machine {
					ids = append(ids, c.ID)
				}
			}
			sort.Strings(ids)
			message := fmt.Sprintf("found: this clone's remote matches project %q but its git common-dir is unknown; choose `clast init` to register a new clone (no automatic choice)", p.Project.Slug)
			if len(ids) > 0 {
				locators := make([]string, len(ids))
				for i, id := range ids {
					locators[i] = fmt.Sprintf("`clast relink %s`", id)
				}
				message = fmt.Sprintf("found: this clone's remote matches project %q but its git common-dir is unknown; choose %s to keep an existing clone identity, or `clast init` to register a new clone (no automatic choice)",
					p.Project.Slug, strings.Join(locators, " or "))
			}
			findings = append(findings, Finding{Code: RegistryMovedCloneCode, Message: message})
		}
	}
	return findings, nil
}

// CurrentDir is kept small so doctor remains flat and report-only while the
// check stays directly testable.
func CurrentDir() (string, error) { return os.Getwd() }
