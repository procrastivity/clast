// This package's golden suite pins the two byte-promise spots SURFACE
// names as needing tests from day one (D5/C4.8): the settings.json splice
// (splice_test.go) and each skill's assembled SKILL.md (skillmd_test.go).
// The shim's own exact command string (shim_test.go) is a third,
// simpler byte promise — a literal constant, so it is pinned by direct
// equality rather than a golden file — but its bytes are also embedded
// inside every splice golden's expected output, so the splice goldens
// prove it too (SURFACE V33: "pinned by the byte-golden splice tests").
//
// Regenerate every golden in this package with:
//
//	go test ./internal/harness/claudecode -run TestGolden -update
package claudecode_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "write testdata/golden/*'s files from the current run instead of comparing to them")

// goldenPath resolves rel under this package's testdata/golden directory.
func goldenPath(rel string) string {
	return filepath.Join("testdata", "golden", rel)
}

// goldenCompare compares got against the golden file at rel (relative to
// testdata/golden), or writes got there under -update.
func goldenCompare(t *testing.T, rel string, got []byte) {
	t.Helper()
	path := goldenPath(rel)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with -update to create it)", path, err)
	}
	if string(want) != string(got) {
		t.Errorf("%s mismatch:\n--- want (golden) ---\n%s\n--- got ---\n%s", rel, want, got)
	}
}
