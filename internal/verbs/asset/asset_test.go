package asset_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	assetchain "github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/verbs/asset"
)

// writeOverride plants relName under a fresh $XDG_CONFIG_HOME/clast tree and
// points the process there for the duration of the test (t.Setenv restores
// the prior value on cleanup) — the override link of the chain (M18).
func writeOverride(t *testing.T, relName, content string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "clast", relName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRun_OverridePresentWinsOverEmbedded plants an override at a path that
// also exists in the embedded fallback (agent-guidance.md) and asserts the
// override's own content and link answer — shadow by name, override
// replaces the whole file (C5.2), never merged.
func TestRun_OverridePresentWinsOverEmbedded(t *testing.T) {
	writeOverride(t, "agent-guidance.md", "override content\n")

	result, err := asset.Run("agent-guidance.md")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Link != "override" {
		t.Errorf("Link = %q, want %q", result.Link, "override")
	}
	if string(result.Content) != "override content\n" {
		t.Errorf("Content = %q, want the override's own content", result.Content)
	}
	if result.ResolvedFrom == "" || result.ResolvedFrom == "embedded" {
		t.Errorf("ResolvedFrom = %q, want the override's disk path", result.ResolvedFrom)
	}
	wantSum := sha256.Sum256([]byte("override content\n"))
	if result.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("SHA256 = %q, want the digest of the override's content", result.SHA256)
	}
}

// TestRun_NoOverride_FallsToEmbedded resolves a path with no override and no
// installed share tree (this test binary's own layout has neither) — the
// chain's last-resort link, compiled into the binary (M18).
func TestRun_NoOverride_FallsToEmbedded(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	result, err := asset.Run("agent-guidance.md")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Link != "embedded" {
		t.Errorf("Link = %q, want %q", result.Link, "embedded")
	}
	if result.ResolvedFrom != "embedded" {
		t.Errorf("ResolvedFrom = %q, want %q (V25: embedded for the embedded link)", result.ResolvedFrom, "embedded")
	}
	if len(result.Content) == 0 {
		t.Error("Content is empty, want the embedded asset's bytes")
	}
	wantSum := sha256.Sum256(result.Content)
	if result.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("SHA256 does not match the content it rides alongside")
	}
}

// TestRun_UnknownPath_NotFoundAsset asserts the not-found.asset code (V25)
// for a path absent from every link of the chain.
func TestRun_UnknownPath_NotFoundAsset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := asset.Run("no/such/asset.md")
	if err == nil {
		t.Fatal("Run: want an error for an unknown path")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "not-found.asset" {
		t.Errorf("error code = %q, want not-found.asset", ce.Code)
	}
}

// TestRun_PathEchoedVerbatim asserts Result.Path is exactly the caller's
// positional argument, not a normalized or resolved form.
func TestRun_PathEchoedVerbatim(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	result, err := asset.Run("agent-guidance.md")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Path != "agent-guidance.md" {
		t.Errorf("Path = %q, want the caller's own path", result.Path)
	}
}

// TestRun_PathTraversal_NotFoundAsset asserts that a positional which
// escapes the asset chain roots — by climbing above them with ".." (however
// deep) or by naming an absolute path — is refused with the same
// not-found.asset posture as any other unresolvable path (V25's own error
// code, never a distinct "forbidden" code), rather than being joined onto a
// chain root and read. A benign interior ".." that Clean cancels away
// entirely (no leading ".." survives) is left to resolve normally.
func TestRun_PathTraversal_NotFoundAsset(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"shallow traversal", "../x"},
		{"deep traversal that still climbs above root", "a/../../x"},
		{"absolute path", "/etc/hostname"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			_, err := asset.Run(c.path)
			if err == nil {
				t.Fatalf("Run(%q): want an error, got nil", c.path)
			}
			ce, ok := err.(*clasterr.Error)
			if !ok {
				t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
			}
			if ce.Code != "not-found.asset" {
				t.Errorf("error code = %q, want not-found.asset", ce.Code)
			}
		})
	}
}

// TestRun_InteriorDotDot_CancelsAndResolves asserts the posture for a
// benign interior ".." that filepath.Clean cancels away entirely, leaving
// no leading ".." — it is not treated as a traversal, and resolves like the
// clean path it's equivalent to. The override link is used (rather than
// embedded) because the disk join (filepath.Join, which Clean's internally)
// tolerates the raw uncleaned positional the same way this fix does, while
// the embedded fallback's fs.FS requires an already-valid path and would
// fail this positional for a reason unrelated to containment — not the
// posture this test is pinning down.
func TestRun_InteriorDotDot_CancelsAndResolves(t *testing.T) {
	writeOverride(t, "flows/wake.md", "override wake content\n")

	result, err := asset.Run("flows/../flows/wake.md")
	if err != nil {
		t.Fatalf("Run(interior ..): want no error, got %v", err)
	}
	if result.Link != "override" {
		t.Errorf("Link = %q, want %q", result.Link, "override")
	}
	if string(result.Content) != "override wake content\n" {
		t.Errorf("Content = %q, want the planted override's own content", result.Content)
	}
}

// TestSourceDefaultStillSaysDefault pins the TRAP V25 calls out:
// assetchain.SourceDefault.String() must keep returning "default" — that is
// not a bug to fix in internal/asset, it is the exact word this verb's own
// link mapping (link_test.go, same package) must never let leak to the
// surface. A change here is a signal to re-check that mapping.
func TestSourceDefaultStillSaysDefault(t *testing.T) {
	if got := assetchain.SourceDefault.String(); got != "default" {
		t.Fatalf("assetchain.SourceDefault.String() = %q, want %q", got, "default")
	}
}
