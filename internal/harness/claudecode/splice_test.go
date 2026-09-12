package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/claudecode"
)

// clasterrCode extracts a *clasterr.Error's Code from err, failing the
// test if err is not one.
func clasterrCode(t *testing.T, err error) string {
	t.Helper()
	var terr *clasterr.Error
	if !errors.As(err, &terr) {
		t.Fatalf("error %v is not a *clasterr.Error", err)
	}
	return terr.Code
}

// settingsFixture points claudecode.SettingsPath() at a fresh path under a
// temp dir (claudecode's own test seam, C2.6) and, when initial is
// non-nil, writes it there before returning the path — nil means "no
// settings.json exists yet" (Splice's missing-file case, C4.8).
func settingsFixture(t *testing.T, initial []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(claudecode.SettingsPathEnv, path)
	if initial != nil {
		if err := os.WriteFile(path, initial, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestSplice_Golden_NoSettingsFile is the "no settings.json" variant
// (SURFACE V32/C4.8): Splice treats a missing file as `{}` and writes a
// fresh one carrying only the spliced hook. No .bak: there was nothing to
// back up.
func TestSplice_Golden_NoSettingsFile(t *testing.T) {
	path := settingsFixture(t, nil)

	result, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if result.Status != claudecode.SpliceStatusSpliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.SpliceStatusSpliced)
	}
	if result.Path != path {
		t.Errorf("Path = %q, want %q", result.Path, path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "splice/no_settings_file.json", got)

	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf(".bak stat = %v, want IsNotExist (nothing existed to back up)", err)
	}
}

// TestSplice_Golden_EmptyObject is the "empty object" variant: a
// settings.json that already exists but is exactly `{}`. A .bak is
// written (the file did exist), carrying the pre-splice bytes verbatim.
func TestSplice_Golden_EmptyObject(t *testing.T) {
	const initial = "{}"
	path := settingsFixture(t, []byte(initial))

	result, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if result.Status != claudecode.SpliceStatusSpliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.SpliceStatusSpliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "splice/empty_object.json", got)

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("reading .bak: %v", err)
	}
	if string(bak) != initial {
		t.Errorf(".bak = %q, want the pre-splice bytes %q verbatim", bak, initial)
	}
}

// unrelatedHooksFixture is a settings.json carrying an unrelated top-level
// key and an existing PreToolUse hook — every byte of both must survive
// the splice untouched (C4.8: "preserve every unrelated key byte-for-
// byte").
const unrelatedHooksFixture = `{
  "env": {
    "FOO": "bar"
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "echo pre"
          }
        ]
      }
    ]
  }
}
`

// TestSplice_Golden_ExistingUnrelatedHooks is the "existing unrelated
// hooks" variant: settings.json already carries a PreToolUse hook and an
// unrelated env key, both of which must come through byte-for-byte
// (C4.8), with the SessionStart splice appended alongside.
func TestSplice_Golden_ExistingUnrelatedHooks(t *testing.T) {
	path := settingsFixture(t, []byte(unrelatedHooksFixture))

	result, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if result.Status != claudecode.SpliceStatusSpliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.SpliceStatusSpliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "splice/existing_unrelated_hooks.json", got)

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("reading .bak: %v", err)
	}
	if string(bak) != unrelatedHooksFixture {
		t.Errorf(".bak = %q, want the pre-splice bytes verbatim", bak)
	}
}

// TestSplice_Golden_ReinstallIsIdempotent is the re-install variant
// (C4.8): splicing an already-spliced settings.json reports
// already-spliced, writes nothing, and never overwrites the .bak a first
// splice already wrote.
func TestSplice_Golden_ReinstallIsIdempotent(t *testing.T) {
	path := settingsFixture(t, []byte(unrelatedHooksFixture))

	first, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("first Splice: %v", err)
	}
	if first.Status != claudecode.SpliceStatusSpliced {
		t.Fatalf("first Status = %q, want %q", first.Status, claudecode.SpliceStatusSpliced)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bakAfterFirst, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}

	second, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("second Splice: %v", err)
	}
	if second.Status != claudecode.SpliceStatusAlreadySpliced {
		t.Fatalf("second Status = %q, want %q (idempotent re-install)", second.Status, claudecode.SpliceStatusAlreadySpliced)
	}

	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSecond) != string(afterFirst) {
		t.Errorf("settings.json changed on a re-install: after first splice = %s, after second = %s", afterFirst, afterSecond)
	}
	goldenCompare(t, "splice/reinstall_idempotent.json", afterSecond)

	bakAfterSecond, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(bakAfterSecond) != string(bakAfterFirst) {
		t.Errorf(".bak changed on a re-install (must never be overwritten once written): after first = %s, after second = %s", bakAfterFirst, bakAfterSecond)
	}
	if string(bakAfterFirst) != unrelatedHooksFixture {
		t.Errorf(".bak = %q, want the pre-splice bytes verbatim", bakAfterFirst)
	}
}

// TestSplice_MalformedSettings_ValidationError asserts an unparseable
// settings.json refuses with a structured validation code rather than
// silently mangling the file.
func TestSplice_MalformedSettings_ValidationError(t *testing.T) {
	settingsFixture(t, []byte("not json"))

	_, err := claudecode.Splice()
	if err == nil {
		t.Fatal("Splice over malformed settings.json returned no error")
	}
	if got := clasterrCode(t, err); got != "validation.malformed-settings" {
		t.Errorf("error code = %q, want validation.malformed-settings", got)
	}
}

// TestSpliceStatus_NoSettingsFile asserts SpliceStatus reads a wholly
// absent settings.json the same way Splice treats it as `{}`: SpliceAbsent,
// not SpliceMalformed — checks.go, not this probe, decides whether that is
// drift (SURFACE V28, step-04).
func TestSpliceStatus_NoSettingsFile(t *testing.T) {
	path := settingsFixture(t, nil)

	got, err := claudecode.SpliceStatus()
	if err != nil {
		t.Fatalf("SpliceStatus: %v", err)
	}
	if got.State != harness.SpliceAbsent {
		t.Errorf("State = %q, want %q", got.State, harness.SpliceAbsent)
	}
	if got.Path != path {
		t.Errorf("Path = %q, want %q", got.Path, path)
	}
}

// TestSpliceStatus_AfterSplice_IsCurrent asserts SpliceStatus reads a
// freshly spliced settings.json as SpliceCurrent, and — the point of
// SpliceStatus existing at all — that probing it never writes anything.
func TestSpliceStatus_AfterSplice_IsCurrent(t *testing.T) {
	path := settingsFixture(t, nil)
	if _, err := claudecode.Splice(); err != nil {
		t.Fatalf("Splice: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := claudecode.SpliceStatus()
	if err != nil {
		t.Fatalf("SpliceStatus: %v", err)
	}
	if got.State != harness.SpliceCurrent {
		t.Errorf("State = %q, want %q", got.State, harness.SpliceCurrent)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("SpliceStatus wrote to %s: before=%s after=%s", path, before, after)
	}
}

// TestSpliceStatus_NoRecognizableHook_IsAbsent asserts a settings.json
// with unrelated hooks, but nothing naming clast's shim, reads as
// SpliceAbsent — the same state a wholly missing file gets.
func TestSpliceStatus_NoRecognizableHook_IsAbsent(t *testing.T) {
	settingsFixture(t, []byte(`{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"echo hi"}]}]}}`))

	got, err := claudecode.SpliceStatus()
	if err != nil {
		t.Fatalf("SpliceStatus: %v", err)
	}
	if got.State != harness.SpliceAbsent {
		t.Errorf("State = %q, want %q", got.State, harness.SpliceAbsent)
	}
}

// TestSpliceStatus_TamperedCommand_IsTampered asserts a hook entry whose
// command has been edited away from ShimCommand, but still names clast's
// shim, reads as SpliceTampered rather than SpliceAbsent.
func TestSpliceStatus_TamperedCommand_IsTampered(t *testing.T) {
	path := settingsFixture(t, nil)
	if _, err := claudecode.Splice(); err != nil {
		t.Fatalf("Splice: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "clast plumbing capture", "clast plumbing capture --tampered", 1)
	if tampered == string(raw) {
		t.Fatal("fixture did not find the shim command to tamper with")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := claudecode.SpliceStatus()
	if err != nil {
		t.Fatalf("SpliceStatus: %v", err)
	}
	if got.State != harness.SpliceTampered {
		t.Errorf("State = %q, want %q", got.State, harness.SpliceTampered)
	}
}

// TestSpliceStatus_MalformedSettings_IsMalformed asserts a settings.json
// that will not parse as JSON reads as SpliceMalformed — a read-only fact
// SpliceStatus reports rather than an error, unlike Splice/Unsplice, which
// refuse (harness.CodeMalformedSplice) on the same input.
func TestSpliceStatus_MalformedSettings_IsMalformed(t *testing.T) {
	settingsFixture(t, []byte("not json"))

	got, err := claudecode.SpliceStatus()
	if err != nil {
		t.Fatalf("SpliceStatus returned an error for malformed settings.json, want the SpliceMalformed state instead: %v", err)
	}
	if got.State != harness.SpliceMalformed {
		t.Errorf("State = %q, want %q", got.State, harness.SpliceMalformed)
	}
}
