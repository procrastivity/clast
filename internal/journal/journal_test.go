package journal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/config"
)

// clearEnvSeams unsets the environment inputs Root consults, so a test
// controls resolution purely through its own setup (config, fake home,
// fake XDG) rather than whatever happens to be set in the ambient
// environment this test binary runs under.
func clearEnvSeams(t *testing.T) {
	t.Helper()
	t.Setenv(RootDirEnv, "")
	t.Setenv("XDG_DATA_HOME", "")
}

// fakeHome points userHomeDir at dir for the duration of the test (C2.6
// seam), restoring the previous value on cleanup.
func fakeHome(t *testing.T, dir string) {
	t.Helper()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userHomeDir = orig })
}

func TestRoot_DefaultUnderFakeHome(t *testing.T) {
	clearEnvSeams(t)
	fakeHome(t, "/fake/home")

	got, err := Root(config.Config{})
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join("/fake/home", ".local", "share", "clast", "journal")
	if got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

func TestRoot_DefaultUnderFakeXDG(t *testing.T) {
	clearEnvSeams(t)
	t.Setenv("XDG_DATA_HOME", "/fake/xdg-data")
	// No home fallback should be needed; fail loudly if it is consulted.
	fakeHome(t, "")

	got, err := Root(config.Config{})
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join("/fake/xdg-data", "clast", "journal")
	if got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

func TestRoot_ConfigOverride(t *testing.T) {
	clearEnvSeams(t)
	fakeHome(t, "/fake/home")

	cfg := config.Config{"journal_dir": "/configured/journal"}
	got, err := Root(cfg)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/configured/journal" {
		t.Errorf("Root = %q, want %q", got, "/configured/journal")
	}
}

func TestRoot_EnvSeamWinsOverConfig(t *testing.T) {
	clearEnvSeams(t)
	t.Setenv(RootDirEnv, "/seam/journal")
	fakeHome(t, "/fake/home")

	cfg := config.Config{"journal_dir": "/configured/journal"}
	got, err := Root(cfg)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/seam/journal" {
		t.Errorf("Root = %q, want %q (seam must win)", got, "/seam/journal")
	}
}

func TestEnsureRoot_CreatesMarkerOnce(t *testing.T) {
	root := t.TempDir()
	root = filepath.Join(root, "journal") // root itself must not pre-exist.

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	origNow := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = origNow })

	if err := EnsureRoot(root); err != nil {
		t.Fatalf("EnsureRoot (first): %v", err)
	}
	marker, ok, err := ReadMarker(root)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if !ok {
		t.Fatalf("ReadMarker: ok = false, want true after EnsureRoot")
	}
	if marker.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", marker.SchemaVersion)
	}
	if !marker.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt = %v, want %v", marker.CreatedAt, fixed)
	}

	// Advance the clock and ensure a second EnsureRoot call is a no-op:
	// created_at must not move.
	later := fixed.Add(time.Hour)
	now = func() time.Time { return later }

	if err := EnsureRoot(root); err != nil {
		t.Fatalf("EnsureRoot (second): %v", err)
	}
	marker2, ok, err := ReadMarker(root)
	if err != nil {
		t.Fatalf("ReadMarker (second): %v", err)
	}
	if !ok {
		t.Fatalf("ReadMarker (second): ok = false, want true")
	}
	if !marker2.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt after second EnsureRoot = %v, want unchanged %v", marker2.CreatedAt, fixed)
	}
}

func TestReadMarker_MissingJournalIsEmptyNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")

	marker, ok, err := ReadMarker(root)
	if err != nil {
		t.Fatalf("ReadMarker on missing journal returned an error: %v", err)
	}
	if ok {
		t.Errorf("ReadMarker on missing journal: ok = true, want false")
	}
	if marker != (Marker{}) {
		t.Errorf("ReadMarker on missing journal: marker = %+v, want zero value", marker)
	}
}
