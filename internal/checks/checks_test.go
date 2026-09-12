package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/claudecode"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// newFixture points claude-code's install dir at a fresh temp dir (via its
// SkillsDirEnv test seam, C2.6) and returns a bare root command plus a
// buildinfo.Info, the two HarnessTargets needs.
func newFixture(t *testing.T) (root *cobra.Command, build buildinfo.Info) {
	t.Helper()
	t.Setenv(claudecode.SkillsDirEnv, t.TempDir())
	t.Setenv(claudecode.SettingsPathEnv, filepath.Join(t.TempDir(), "settings.json"))
	return &cobra.Command{Use: "clast"}, buildinfo.Info{Version: "v0.0.0-test"}
}

// installClean renders and stamps every one of claude-code's three skills
// under root's current manifest, plus the settings.json splice (SURFACE
// V28's splice-drift checks would otherwise flag a fresh install missing
// its shim), returning the wake skill's install dir — the Current fixture
// every other state's test starts from and mutates. wake stands in for
// "the" claude-code target the way the chassis's one skill used to: the
// other two skills install alongside it, untouched by whatever the test
// does to wake's tree.
func installClean(t *testing.T, root *cobra.Command, build buildinfo.Info) string {
	t.Helper()
	m, err := manifest.Build(root, build)
	if err != nil {
		t.Fatalf("manifest.Build: %v", err)
	}
	var wakeDir string
	for _, name := range claudecode.SkillNames {
		dir, err := claudecode.InstallSkill(name, m)
		if err != nil {
			t.Fatalf("claudecode.InstallSkill(%q): %v", name, err)
		}
		if name == "wake" {
			wakeDir = dir
		}
	}
	if _, err := claudecode.Splice(); err != nil {
		t.Fatalf("claudecode.Splice: %v", err)
	}
	return wakeDir
}

// addVerb registers a new verb under root's `plumbing` namespace, after a
// clean install — the manifest, and so claudecode.Generate's output, now
// differs from what got stamped (a verb added since install), Status's
// binary-vs-stamp question (C4.6), independent of whatever the disk says.
//
// It nests the new verb under a `plumbing` group command (creating one if
// this fixture's root does not already carry one) rather than adding it
// directly to root: harness.Projectable's filter is structural (SURFACE
// V32 — exactly the `plumbing` namespace, keyed off verb name position),
// not kind-based, so a bare top-level command would never be projected
// regardless of its kind annotation, and this fixture's staleness would
// stop proving anything about a real namespace verb being added.
func addVerb(root *cobra.Command) {
	group := plumbingGroup(root)
	extra := &cobra.Command{Use: "extra", Run: func(*cobra.Command, []string) {}}
	surface.Annotate(extra, surface.Plumbing)
	group.AddCommand(extra)
}

// plumbingGroup returns root's `plumbing` child command, creating a bare
// one (no RunE, matching the real internal/verbs/plumbing group's shape)
// if root does not already carry one.
func plumbingGroup(root *cobra.Command) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == "plumbing" {
			return c
		}
	}
	group := &cobra.Command{Use: "plumbing"}
	root.AddCommand(group)
	return group
}

// findTarget returns the TargetState named (harnessName, target), failing
// the test if no such row exists.
func findTarget(t *testing.T, targets []TargetState, harnessName, target string) TargetState {
	t.Helper()
	for _, tg := range targets {
		if tg.Harness == harnessName && tg.Target == target {
			return tg
		}
	}
	t.Fatalf("no target named %q/%q in %+v", harnessName, target, targets)
	return TargetState{}
}

func TestHarnessTargets_Current(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Current {
		t.Errorf("state = %q, want %q", got, harness.Current)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
}

// TestHarnessTargets_ListsEveryProjectedSkill asserts HarnessTargets emits
// one row per claude-code skill (SURFACE V32: three, not one), each
// independently reporting Current once installed.
func TestHarnessTargets_ListsEveryProjectedSkill(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)

	_, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if len(targets) != len(claudecode.SkillNames) {
		t.Fatalf("targets = %+v, want exactly %d (one per skill)", targets, len(claudecode.SkillNames))
	}
	for _, name := range claudecode.SkillNames {
		if got := findTarget(t, targets, claudecode.Name, name).State; got != harness.Current {
			t.Errorf("%s state = %q, want %q", name, got, harness.Current)
		}
	}
}

func TestHarnessTargets_Missing(t *testing.T) {
	root, build := newFixture(t)
	// Nothing installed.

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Missing {
		t.Errorf("state = %q, want %q", got, harness.Missing)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
}

func TestHarnessTargets_Stale(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)
	addVerb(root)

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Stale {
		t.Errorf("state = %q, want %q", got, harness.Stale)
	}
	if len(findings) == 0 {
		t.Fatalf("findings = %+v, want at least one stale finding", findings)
	}
	for _, f := range findings {
		if f.Code != StaleHarnessCode {
			t.Errorf("finding code = %q, want %q", f.Code, StaleHarnessCode)
		}
	}
}

func TestHarnessTargets_Modified(t *testing.T) {
	root, build := newFixture(t)
	dir := installClean(t, root, build)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Modified {
		t.Errorf("state = %q, want %q", got, harness.Modified)
	}
	if len(findings) != 1 || findings[0].Code != "advisory.modified-harness-target" {
		t.Errorf("findings = %+v, want exactly one advisory.modified-harness-target", findings)
	}
}

// TestHarnessTargets_ModifiedAndStale is both at once: a hand-edited file
// plus a verb added since install. Modified wins the state (Status checks
// disk-vs-stamp before binary-vs-stamp), but the per-file stale findings
// still appear beside the modified finding, because binary-vs-stamp is
// independent of the disk (C4.6, design decisions.md §1.5).
func TestHarnessTargets_ModifiedAndStale(t *testing.T) {
	root, build := newFixture(t)
	dir := installClean(t, root, build)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addVerb(root)

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Modified {
		t.Errorf("state = %q, want %q", got, harness.Modified)
	}

	var sawModified, sawStale bool
	for _, f := range findings {
		switch f.Code {
		case "advisory.modified-harness-target":
			sawModified = true
		case StaleHarnessCode:
			sawStale = true
		default:
			t.Errorf("unexpected finding code %q", f.Code)
		}
	}
	if !sawModified {
		t.Errorf("findings = %+v, want an advisory.modified-harness-target finding", findings)
	}
	if !sawStale {
		t.Errorf("findings = %+v, want the per-file stale findings too", findings)
	}
}

func TestHarnessTargets_UnownedConflict(t *testing.T) {
	root, build := newFixture(t)
	dir, err := claudecode.SkillDir("wake")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "foreign.txt"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.UnownedConflict {
		t.Errorf("state = %q, want %q", got, harness.UnownedConflict)
	}
	if len(findings) != 1 || findings[0].Code != "advisory.unowned-harness-target" {
		t.Errorf("findings = %+v, want exactly one advisory.unowned-harness-target", findings)
	}
}

// TestHarnessTargets_IncompatibleUnparseable and
// TestHarnessTargets_IncompatibleSchemaVersion both assert the same thing
// the task calls out explicitly: an incompatible target gives exactly one
// failing finding, and HarnessTargets itself returns no hard error — the
// unparseable stamp used to make doctor exit with a hard error before this
// change (design decisions.md §1.5).

func TestHarnessTargets_IncompatibleUnparseable(t *testing.T) {
	root, build := newFixture(t)
	dir := installClean(t, root, build)
	if err := os.WriteFile(filepath.Join(dir, manifest.StampFileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets returned a hard error for an incompatible stamp: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Incompatible {
		t.Errorf("state = %q, want %q", got, harness.Incompatible)
	}
	if len(findings) != 1 || findings[0].Code != harness.CodeIncompatible {
		t.Errorf("findings = %+v, want exactly one %s", findings, harness.CodeIncompatible)
	}
}

func TestHarnessTargets_IncompatibleSchemaVersion(t *testing.T) {
	root, build := newFixture(t)
	dir := installClean(t, root, build)
	stampPath := filepath.Join(dir, manifest.StampFileName)
	raw, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	var stamp map[string]any
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	stamp["schemaVersion"] = 999
	out, err := json.Marshal(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets returned a hard error for an incompatible stamp: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Incompatible {
		t.Errorf("state = %q, want %q", got, harness.Incompatible)
	}
	if len(findings) != 1 || findings[0].Code != harness.CodeIncompatible {
		t.Errorf("findings = %+v, want exactly one %s", findings, harness.CodeIncompatible)
	}
}

// hasCode reports whether findings carries at least one finding with code.
func hasCode(findings []Finding, code string) bool {
	for _, f := range findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// TestHarnessTargets_MissingSkillTree_PartialInstall asserts SURFACE V28's
// partial-install reading: deleting one of three cleanly installed skill
// trees reports that one tree, and only that one, as
// MissingHarnessTargetCode — a per-tree finding, not one blanket row,
// naming `clast install claude-code` as the fix.
func TestHarnessTargets_MissingSkillTree_PartialInstall(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)

	wakeDir, err := claudecode.SkillDir("wake")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(wakeDir); err != nil {
		t.Fatal(err)
	}

	findings, targets, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if got := findTarget(t, targets, claudecode.Name, "wake").State; got != harness.Missing {
		t.Errorf("wake state = %q, want %q", got, harness.Missing)
	}
	if got := findTarget(t, targets, claudecode.Name, "brief").State; got != harness.Current {
		t.Errorf("brief state = %q, want %q (untouched sibling)", got, harness.Current)
	}

	var missingFindings []Finding
	for _, f := range findings {
		if f.Code == MissingHarnessTargetCode {
			missingFindings = append(missingFindings, f)
		}
	}
	if len(missingFindings) != 1 {
		t.Fatalf("findings = %+v, want exactly one %s (wake only, brief/retro untouched)", findings, MissingHarnessTargetCode)
	}
	if !strings.Contains(missingFindings[0].Message, "wake") || !strings.Contains(missingFindings[0].Message, "clast install "+claudecode.Name) {
		t.Errorf("finding message = %q, want it to name wake and `clast install %s`", missingFindings[0].Message, claudecode.Name)
	}
}

// TestHarnessTargets_MissingSkillTree_NeverInstalledIsClean asserts the
// absence-vs-drift reading SURFACE V28 step-04 records: every target
// Missing, with no sibling giving install evidence, reads as "never
// installed" — clean, not drift. TestHarnessTargets_Missing already covers
// the plain nothing-installed case with zero findings; this test states
// the same reading explicitly by the new codes' names.
func TestHarnessTargets_MissingSkillTree_NeverInstalledIsClean(t *testing.T) {
	root, build := newFixture(t)
	// Nothing installed, no splice — a bare, never-touched host.

	findings, _, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if hasCode(findings, MissingHarnessTargetCode) {
		t.Errorf("findings = %+v, want no %s on a never-installed host", findings, MissingHarnessTargetCode)
	}
	if hasCode(findings, MissingHarnessSpliceCode) {
		t.Errorf("findings = %+v, want no %s on a never-installed host", findings, MissingHarnessSpliceCode)
	}
}

// TestHarnessTargets_SpliceAbsentWhileSkillsInstalled asserts the shim-
// entry-absent splice-drift class: skills installed via InstallSkill
// directly, bypassing Splice, report MissingHarnessSpliceCode, naming
// `clast install claude-code` to restore the hook.
func TestHarnessTargets_SpliceAbsentWhileSkillsInstalled(t *testing.T) {
	root, build := newFixture(t)
	m, err := manifest.Build(root, build)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range claudecode.SkillNames {
		if _, err := claudecode.InstallSkill(name, m); err != nil {
			t.Fatalf("claudecode.InstallSkill(%q): %v", name, err)
		}
	}
	// No Splice call: settings.json is never written.

	findings, _, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if !hasCode(findings, MissingHarnessSpliceCode) {
		t.Errorf("findings = %+v, want a %s finding", findings, MissingHarnessSpliceCode)
	}
}

// TestHarnessTargets_SpliceTampered asserts the shim-command-bytes-differ
// splice-drift class: after a clean install (skills + splice), rewriting
// the hook's command field to something that still names clast's shim but
// no longer matches ShimCommand exactly reports TamperedHarnessSpliceCode.
func TestHarnessTargets_SpliceTampered(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)

	settingsPath, err := claudecode.SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "clast plumbing capture", "clast plumbing capture --tampered", 1)
	if tampered == string(raw) {
		t.Fatal("fixture did not find the shim command to tamper with")
	}
	if err := os.WriteFile(settingsPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, _, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if !hasCode(findings, TamperedHarnessSpliceCode) {
		t.Errorf("findings = %+v, want a %s finding", findings, TamperedHarnessSpliceCode)
	}
	if hasCode(findings, MissingHarnessSpliceCode) {
		t.Errorf("findings = %+v, want no %s alongside a tampered entry", findings, MissingHarnessSpliceCode)
	}
}

// TestHarnessTargets_SpliceMalformed asserts the settings.json-malformed
// splice-drift class: a settings.json that will not parse as JSON reports
// harness.CodeMalformedSplice — the one splice-drift finding that keeps a
// non-"advisory." prefix, since a broken settings.json blocks
// `clast install`/`clast uninstall` outright (the same severity
// Incompatible carries for a stamped target).
func TestHarnessTargets_SpliceMalformed(t *testing.T) {
	root, build := newFixture(t)
	installClean(t, root, build)

	settingsPath, err := claudecode.SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, _, err := HarnessTargets(root, build)
	if err != nil {
		t.Fatalf("HarnessTargets: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != harness.CodeMalformedSplice {
		t.Errorf("findings = %+v, want exactly one %s", findings, harness.CodeMalformedSplice)
	}
	if !strings.HasPrefix(findings[0].Code, "validation.") {
		t.Errorf("finding code = %q, want the validation. prefix (fails doctor's run, unlike the advisory splice-drift codes)", findings[0].Code)
	}
}
