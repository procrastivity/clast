package retroverb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/prompt"
)

func TestCacheDir_XDGSet_UsesIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg-cache")
	dir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	want := filepath.Join("/xdg-cache", "clast", "retro")
	if dir != want {
		t.Errorf("CacheDir = %q, want %q", dir, want)
	}
}

func TestCacheDir_XDGUnset_FallsBackToDotCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	want := filepath.Join(home, ".cache", "clast", "retro")
	if dir != want {
		t.Errorf("CacheDir = %q, want %q (~/.cache fallback)", dir, want)
	}
}

func TestFingerprint_SameInputSameFingerprint(t *testing.T) {
	r := prompt.Rendered{System: "sys", User: "user body"}
	a := Fingerprint(r)
	b := Fingerprint(r)
	if a != b {
		t.Errorf("Fingerprint not stable: %q != %q", a, b)
	}
}

func TestFingerprint_DifferentBodyDifferentFingerprint(t *testing.T) {
	a := Fingerprint(prompt.Rendered{System: "sys", User: "body one"})
	b := Fingerprint(prompt.Rendered{System: "sys", User: "body two"})
	if a == b {
		t.Error("Fingerprint identical for two different user prompts, want distinct")
	}
}

// TestFingerprint_NoSeparatorCollision confirms the two halves are joined
// with a byte that can't appear from plain concatenation drift: "sys" +
// "combined" and "syscom" + "bined" must not fingerprint the same, even
// though naive string concatenation would make them equal.
func TestFingerprint_NoSeparatorCollision(t *testing.T) {
	a := Fingerprint(prompt.Rendered{System: "sys", User: "combined"})
	b := Fingerprint(prompt.Rendered{System: "syscom", User: "bined"})
	if a == b {
		t.Error("Fingerprint collided across a system/user boundary shift, want the NUL separator to prevent this")
	}
}

func TestFingerprint_TemplateChangeChangesFingerprint(t *testing.T) {
	// Same "content" (User) but a different System half (standing in for
	// an edited prompt-pair template) must fingerprint differently — a
	// template edit must bust the cache.
	a := Fingerprint(prompt.Rendered{System: "system v1", User: "same body"})
	b := Fingerprint(prompt.Rendered{System: "system v2", User: "same body"})
	if a == b {
		t.Error("Fingerprint identical despite a changed system prompt, want it to change")
	}
}

func TestCacheGetPut_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := cacheGet(dir, "missing"); ok {
		t.Error("cacheGet on an empty dir = hit, want miss")
	}
	if err := cachePut(dir, "fp1", "the summary text"); err != nil {
		t.Fatalf("cachePut: %v", err)
	}
	got, ok := cacheGet(dir, "fp1")
	if !ok || got != "the summary text" {
		t.Errorf("cacheGet after cachePut = (%q, %v), want (%q, true)", got, ok, "the summary text")
	}
}

func TestCacheGet_CorruptFile_TreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fp2.json"), []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("seeding corrupt cache file: %v", err)
	}
	if _, ok := cacheGet(dir, "fp2"); ok {
		t.Error("cacheGet on a corrupt file = hit, want miss")
	}
}

func TestCacheGet_UnreadableDir_TreatedAsMiss(t *testing.T) {
	// A cache directory that doesn't exist at all (never written to) is
	// exactly the "cold cache" case every first run hits — must be a
	// plain miss, never an error.
	if _, ok := cacheGet(filepath.Join(t.TempDir(), "does-not-exist"), "fp3"); ok {
		t.Error("cacheGet on a missing directory = hit, want miss")
	}
}
