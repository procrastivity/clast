package claudecode_test

import (
	"errors"
	"os"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/harness/claudecode"
)

// TestUnsplice_NoSettingsFile is the "no settings.json" variant — Unsplice
// treats a missing file as nothing to remove (symmetric with Splice's own
// missing-file handling, but reporting a no-op rather than writing
// anything): no file is created, and the call is not a diagnostic.
func TestUnsplice_NoSettingsFile(t *testing.T) {
	path := settingsFixture(t, nil)

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusNotSpliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusNotSpliced)
	}
	if result.Path != path {
		t.Errorf("Path = %q, want %q", result.Path, path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Unsplice created %s where none existed", path)
	}
}

// TestUnsplice_AbsentHook_NoOp covers a settings.json that exists but
// carries no hooks.SessionStart entry matching ShimCommand at all — an
// empty object, and one with only unrelated hooks. Both are a no-op, not
// a diagnostic, and the file is left byte-for-byte untouched.
func TestUnsplice_AbsentHook_NoOp(t *testing.T) {
	cases := map[string]string{
		"empty object":    "{}",
		"unrelated hooks": unrelatedHooksFixture,
	}
	for name, initial := range cases {
		t.Run(name, func(t *testing.T) {
			path := settingsFixture(t, []byte(initial))

			result, err := claudecode.Unsplice()
			if err != nil {
				t.Fatalf("Unsplice: %v", err)
			}
			if result.Status != claudecode.UnspliceStatusNotSpliced {
				t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusNotSpliced)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != initial {
				t.Errorf("settings.json changed on a no-op: got %s, want %s untouched", got, initial)
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Errorf(".bak stat = %v, want IsNotExist — Unsplice never writes a .bak", err)
			}
		})
	}
}

// TestUnsplice_Golden_EmptyObjectSpliced removes the shim hook from a
// settings.json that, before install, was exactly `{}` (this package's
// own splice/empty_object.json golden as the starting fixture) — the
// emptied-group cascade removes the group, hooks.SessionStart, and hooks
// itself, leaving `{}` behind again.
func TestUnsplice_Golden_EmptyObjectSpliced(t *testing.T) {
	spliced, err := os.ReadFile(goldenPath("splice/empty_object.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := settingsFixture(t, spliced)

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusUnspliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusUnspliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "unsplice/empty_object_spliced.json", got)

	if string(got) != "{}" {
		t.Errorf("settings.json = %s, want the cascade to leave exactly {} behind", got)
	}
}

// TestUnsplice_Golden_ExistingUnrelatedHooks removes the shim hook from a
// settings.json that already carried an unrelated PreToolUse hook and an
// unrelated top-level env key before the splice (splice/
// existing_unrelated_hooks.json). The emptied SessionStart group and its
// now-empty hooks.SessionStart key are removed, but "hooks" itself stays
// (PreToolUse still lives there) and every unrelated byte survives.
func TestUnsplice_Golden_ExistingUnrelatedHooks(t *testing.T) {
	spliced, err := os.ReadFile(goldenPath("splice/existing_unrelated_hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := settingsFixture(t, spliced)

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusUnspliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusUnspliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "unsplice/existing_unrelated_hooks.json", got)

	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf(".bak stat = %v, want IsNotExist — Unsplice never writes a .bak of its own", err)
	}
}

// otherGroupFixture carries the spliced shim hook alongside a second,
// independent hooks.SessionStart group a human (or another tool) added —
// the case the emptied-group rule exists to protect: removing the shim's
// own group must never touch a sibling group's hooks.
const otherGroupFixture = `{"hooks":{"SessionStart":[{"matcher":"other","hooks":[{"type":"command","command":"echo other"}]},{"matcher":"","hooks":[{"type":"command","command":"command -v clast >/dev/null 2>&1 && (clast plumbing capture >/dev/null 2>&1 &); exit 0"}]}]}}`

// TestUnsplice_Golden_SiblingGroupUntouched proves the emptied-group rule
// is scoped to the shim's own group: a sibling hooks.SessionStart group
// survives byte-for-byte, and hooks.SessionStart itself is kept (non-
// empty: one group remains) rather than deleted.
func TestUnsplice_Golden_SiblingGroupUntouched(t *testing.T) {
	path := settingsFixture(t, []byte(otherGroupFixture))

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusUnspliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusUnspliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "unsplice/sibling_group_untouched.json", got)
}

// sharedGroupFixture carries the shim hook as one of two hooks inside the
// *same* SessionStart group — the other-hooks-in-the-group half of the
// emptied-group rule: removing the shim hook must leave the group in
// place (its hooks array is not empty) and the sibling hook untouched.
const sharedGroupFixture = `{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"echo sibling"},{"type":"command","command":"command -v clast >/dev/null 2>&1 && (clast plumbing capture >/dev/null 2>&1 &); exit 0"}]}]}}`

// TestUnsplice_Golden_SiblingHookInSameGroupUntouched covers a
// SessionStart group that carries the shim hook plus another hook: the
// group survives (not emptied) and the sibling hook is untouched.
func TestUnsplice_Golden_SiblingHookInSameGroupUntouched(t *testing.T) {
	path := settingsFixture(t, []byte(sharedGroupFixture))

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusUnspliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusUnspliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "unsplice/sibling_hook_in_same_group_untouched.json", got)
}

// TestUnsplice_ReinstallThenUninstallIsClean covers the "uninstall after a
// re-installed (idempotent) splice" case: Splice runs twice (idempotent,
// C4.8), then one Unsplice call fully removes the single hook entry that
// exists regardless of how many times Splice ran.
func TestUnsplice_ReinstallThenUninstallIsClean(t *testing.T) {
	path := settingsFixture(t, []byte(unrelatedHooksFixture))

	if _, err := claudecode.Splice(); err != nil {
		t.Fatalf("first Splice: %v", err)
	}
	second, err := claudecode.Splice()
	if err != nil {
		t.Fatalf("second Splice: %v", err)
	}
	if second.Status != claudecode.SpliceStatusAlreadySpliced {
		t.Fatalf("second Splice status = %q, want %q", second.Status, claudecode.SpliceStatusAlreadySpliced)
	}

	result, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("Unsplice: %v", err)
	}
	if result.Status != claudecode.UnspliceStatusUnspliced {
		t.Errorf("Status = %q, want %q", result.Status, claudecode.UnspliceStatusUnspliced)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != unrelatedHooksFixture {
		t.Errorf("settings.json after Unsplice = %s, want the pre-splice bytes restored verbatim", got)
	}

	again, err := claudecode.Unsplice()
	if err != nil {
		t.Fatalf("second Unsplice: %v", err)
	}
	if again.Status != claudecode.UnspliceStatusNotSpliced {
		t.Errorf("second Unsplice status = %q, want %q — nothing left to remove", again.Status, claudecode.UnspliceStatusNotSpliced)
	}
}

// TestUnsplice_MalformedSettings_ValidationError asserts an unparseable
// settings.json refuses with a structured validation code rather than
// silently mangling the file — the same posture Splice takes.
func TestUnsplice_MalformedSettings_ValidationError(t *testing.T) {
	settingsFixture(t, []byte("not json"))

	_, err := claudecode.Unsplice()
	if err == nil {
		t.Fatal("Unsplice over malformed settings.json returned no error")
	}
	var terr *clasterr.Error
	if !errors.As(err, &terr) {
		t.Fatalf("error %v is not a *clasterr.Error", err)
	}
	if terr.Code != "validation.malformed-settings" {
		t.Errorf("error code = %q, want validation.malformed-settings", terr.Code)
	}
}
