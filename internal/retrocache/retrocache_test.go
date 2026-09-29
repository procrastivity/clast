package retrocache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/prompt"
)

func TestDir_XDGSet_UsesIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg-cache")
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join("/xdg-cache", "clast", "retro")
	if dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}
}

func TestDir_XDGUnset_FallsBackToDotCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, ".cache", "clast", "retro")
	if dir != want {
		t.Errorf("Dir = %q, want %q (~/.cache fallback)", dir, want)
	}
}

func TestFingerprint_SameInputSameFingerprint(t *testing.T) {
	r := prompt.Rendered{System: "sys", User: "user body"}
	a := Fingerprint(r, "gpt-test")
	b := Fingerprint(r, "gpt-test")
	if a != b {
		t.Errorf("Fingerprint not stable: %q != %q", a, b)
	}
}

func TestFingerprint_DifferentBodyDifferentFingerprint(t *testing.T) {
	a := Fingerprint(prompt.Rendered{System: "sys", User: "body one"}, "gpt-test")
	b := Fingerprint(prompt.Rendered{System: "sys", User: "body two"}, "gpt-test")
	if a == b {
		t.Error("Fingerprint identical for two different user prompts, want distinct")
	}
}

// TestFingerprint_NoSeparatorCollision confirms the two halves are joined
// with a byte that can't appear from plain concatenation drift: "sys" +
// "combined" and "syscom" + "bined" must not fingerprint the same, even
// though naive string concatenation would make them equal.
func TestFingerprint_NoSeparatorCollision(t *testing.T) {
	a := Fingerprint(prompt.Rendered{System: "sys", User: "combined"}, "gpt-test")
	b := Fingerprint(prompt.Rendered{System: "syscom", User: "bined"}, "gpt-test")
	if a == b {
		t.Error("Fingerprint collided across a system/user boundary shift, want the NUL separator to prevent this")
	}
}

func TestFingerprint_TemplateChangeChangesFingerprint(t *testing.T) {
	// Same "content" (User) but a different System half (standing in for
	// an edited prompt-pair template) must fingerprint differently — a
	// template edit must bust the cache.
	a := Fingerprint(prompt.Rendered{System: "system v1", User: "same body"}, "gpt-test")
	b := Fingerprint(prompt.Rendered{System: "system v2", User: "same body"}, "gpt-test")
	if a == b {
		t.Error("Fingerprint identical despite a changed system prompt, want it to change")
	}
}

// TestFingerprint_ModelChangeChangesFingerprint pins F5 (llm-verbs seal
// sweep): the exact same rendered pair under two different llm.model
// values must fingerprint differently, so a config change from one model
// to another never serves the old model's cached summary. Old cache
// entries (written before this field existed in the recipe) simply miss
// once under the new fingerprint — the correct, disposable-cache
// behavior (MODEL M-§6).
func TestFingerprint_ModelChangeChangesFingerprint(t *testing.T) {
	r := prompt.Rendered{System: "sys", User: "same body"}
	a := Fingerprint(r, "gpt-4")
	b := Fingerprint(r, "gpt-5")
	if a == b {
		t.Error("Fingerprint identical despite a changed model, want it to change (F5)")
	}
}

func TestGetPut_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Get(dir, "missing"); ok {
		t.Error("Get on an empty dir = hit, want miss")
	}
	if err := Put(dir, "fp1", "the summary text"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := Get(dir, "fp1")
	if !ok || got != "the summary text" {
		t.Errorf("Get after Put = (%q, %v), want (%q, true)", got, ok, "the summary text")
	}
}

func TestGet_CorruptFile_TreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fp2.json"), []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("seeding corrupt cache file: %v", err)
	}
	if _, ok := Get(dir, "fp2"); ok {
		t.Error("Get on a corrupt file = hit, want miss")
	}
}

func TestGet_UnreadableDir_TreatedAsMiss(t *testing.T) {
	// A cache directory that doesn't exist at all (never written to) is
	// exactly the "cold cache" case every first run hits — must be a
	// plain miss, never an error.
	if _, ok := Get(filepath.Join(t.TempDir(), "does-not-exist"), "fp3"); ok {
		t.Error("Get on a missing directory = hit, want miss")
	}
}

func fixtureEntry() Entry {
	return Entry{
		Project:   "clast",
		Day:       "2026-09-22",
		StartedAt: time.Date(2026, 9, 22, 14, 3, 0, 0, time.UTC),
		SessionID: "abc-123",
		Body:      "did a thing\n\nand another",
	}
}

// TestEntryKey_IgnoresProject pins the backfill rule: the same entry
// under a different project — "(no project)" later resolved to a clone —
// keys the same, so its cached summary survives the backfill.
func TestEntryKey_IgnoresProject(t *testing.T) {
	a, b := fixtureEntry(), fixtureEntry()
	b.Project = "(no project)"
	ka, err := EntryKey(a, "m1")
	if err != nil {
		t.Fatalf("EntryKey: %v", err)
	}
	kb, err := EntryKey(b, "m1")
	if err != nil {
		t.Fatalf("EntryKey: %v", err)
	}
	if ka != kb {
		t.Errorf("EntryKey differs across projects: %q vs %q, want equal", ka, kb)
	}
}

// TestEntryKey_IsFingerprintOfBlankedPair pins EntryKey to the recipe
// retro used before the extraction: Fingerprint over the pair rendered
// from PromptData with {{project}} blanked. A drift here would orphan
// every summary already on disk.
func TestEntryKey_IsFingerprintOfBlankedPair(t *testing.T) {
	e := fixtureEntry()
	data := e.PromptData()
	data["project"] = ""
	rendered, err := prompt.Render(prompt.RetroSummary, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := EntryKey(e, "m1")
	if err != nil {
		t.Fatalf("EntryKey: %v", err)
	}
	if want := Fingerprint(rendered, "m1"); got != want {
		t.Errorf("EntryKey = %q, want %q", got, want)
	}
}

func TestEntryKey_BodyAndModelChangeKey(t *testing.T) {
	base, err := EntryKey(fixtureEntry(), "m1")
	if err != nil {
		t.Fatalf("EntryKey: %v", err)
	}
	body := fixtureEntry()
	body.Body = "a re-curated body"
	if k, _ := EntryKey(body, "m1"); k == base {
		t.Error("EntryKey unchanged after a body edit, want a new key")
	}
	if k, _ := EntryKey(fixtureEntry(), "m2"); k == base {
		t.Error("EntryKey unchanged after a model change, want a new key")
	}
}

func TestPromptData_FormatsStartedAtUTC(t *testing.T) {
	e := fixtureEntry()
	e.StartedAt = time.Date(2026, 9, 22, 9, 3, 0, 0, time.FixedZone("CDT", -5*3600))
	if got, want := e.PromptData()["started_at"], "2026-09-22T14:03:00Z"; got != want {
		t.Errorf("started_at = %q, want %q", got, want)
	}
}
