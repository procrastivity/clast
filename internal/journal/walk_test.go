package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTestSession writes session.json (and, when requested, curation.json
// and entry.md) for key under shard, via the package's own write
// primitives — the fixture-building style the store brief settles on
// (small Go builders, not hand-typed JSON, so fixtures can't drift from
// the schemas).
func writeTestSession(t *testing.T, root, shard string, key SessionKey, transcript TranscriptFingerprint) {
	t.Helper()
	sess := Session{
		Harness:    key.Harness,
		SessionID:  key.NativeID,
		Machine:    "framework",
		Transcript: transcript,
	}
	if err := WriteSession(root, shard, key, sess); err != nil {
		t.Fatalf("WriteSession(%s/%s): %v", shard, key.DirName(), err)
	}
}

func writeTestCuration(t *testing.T, root, shard string, key SessionKey, c Curation) {
	t.Helper()
	if err := WriteCuration(root, shard, key, c); err != nil {
		t.Fatalf("WriteCuration(%s/%s): %v", shard, key.DirName(), err)
	}
}

func writeTestEntry(t *testing.T, root, shard string, key SessionKey) {
	t.Helper()
	if err := os.WriteFile(EntryPath(root, shard, key), []byte("---\ntitle: t\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write entry.md: %v", err)
	}
}

func findItem(t *testing.T, items []WalkItem, dirName string) WalkItem {
	t.Helper()
	for _, it := range items {
		if it.Key.DirName() == dirName {
			return it
		}
	}
	t.Fatalf("no item named %q in %+v", dirName, items)
	return WalkItem{}
}

func TestWalk_MissingJournalIsEmptyNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk on a missing journal returned an error: %v", err)
	}
	if len(items) != 0 || len(diags) != 0 {
		t.Errorf("Walk = items:%+v diags:%+v, want both empty", items, diags)
	}
}

func TestWalk_MissingSessionsDirIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRoot(root); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	// Root exists (marker written) but sessions/ never created.
	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(items) != 0 || len(diags) != 0 {
		t.Errorf("Walk = items:%+v diags:%+v, want both empty", items, diags)
	}
}

func TestWalk_FullEnumerationDeterministicOrderAndStates(t *testing.T) {
	root := t.TempDir()

	captured := SessionKey{Harness: "claude", NativeID: "1111"}
	curated := SessionKey{Harness: "claude", NativeID: "2222"}
	dismissed := SessionKey{Harness: "codex", NativeID: "3333-uuid-with-dashes"}

	writeTestSession(t, root, "2026-09-12", captured, TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "a"})

	writeTestSession(t, root, "2026-09-11", curated, TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "b"})
	writeTestCuration(t, root, "2026-09-11", curated, Curation{
		State:                StateCurated,
		At:                   mustParseTime(t, "2026-09-12T08:00:00-05:00"),
		Machine:              "laptop",
		TranscriptAtCuration: &TranscriptStamp{Lines: 20, SHA256: "b"},
	})
	writeTestEntry(t, root, "2026-09-11", curated)

	reason := "auto:no-op"
	writeTestSession(t, root, "2026-09-11", dismissed, TranscriptFingerprint{Format: "codex-jsonl", Lines: 1, SHA256: "c"})
	writeTestCuration(t, root, "2026-09-11", dismissed, Curation{
		State:   StateDismissed,
		At:      mustParseTime(t, "2026-09-12T08:00:00-05:00"),
		Machine: "laptop",
		Reason:  &reason,
	})

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3", items)
	}

	// Deterministic order: shard 2026-09-11 before 2026-09-12, and within
	// a shard, directory name order (codex-... before claude-... would
	// sort alphabetically; here claude-2222 vs codex-3333-uuid-with-dashes).
	wantOrder := []string{curated.DirName(), dismissed.DirName(), captured.DirName()}
	for i, want := range wantOrder {
		if items[i].Key.DirName() != want {
			t.Errorf("items[%d] = %q, want %q (order: %v)", i, items[i].Key.DirName(), want, itemNames(items))
		}
	}

	c := findItem(t, items, captured.DirName())
	if c.State() != StateCaptured {
		t.Errorf("captured item State() = %q, want %q", c.State(), StateCaptured)
	}
	if c.CurationPresent {
		t.Errorf("captured item CurationPresent = true, want false")
	}
	if c.Stale() {
		t.Errorf("captured item Stale() = true, want false")
	}
	if c.EntryExists {
		t.Errorf("captured item EntryExists = true, want false")
	}
	if c.Shard != "2026-09-12" {
		t.Errorf("captured item Shard = %q, want %q", c.Shard, "2026-09-12")
	}

	cu := findItem(t, items, curated.DirName())
	if cu.State() != StateCurated {
		t.Errorf("curated item State() = %q, want %q", cu.State(), StateCurated)
	}
	if !cu.EntryExists {
		t.Errorf("curated item EntryExists = false, want true")
	}
	if cu.Stale() {
		t.Errorf("curated item Stale() = true, want false (fingerprints match)")
	}

	d := findItem(t, items, dismissed.DirName())
	if d.State() != StateDismissed {
		t.Errorf("dismissed item State() = %q, want %q", d.State(), StateDismissed)
	}
	if d.Stale() {
		t.Errorf("dismissed item Stale() = true, want false")
	}
}

func itemNames(items []WalkItem) []string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Shard + "/" + it.Key.DirName()
	}
	return names
}

func TestWalk_StaleFlipsWhenFingerprintsDiffer(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	key := SessionKey{Harness: "claude", NativeID: "stale-one"}

	// session.json now shows a longer transcript than what curation.json
	// recorded at curation time: M7's stale = curated && fingerprints
	// differ.
	writeTestSession(t, root, shard, key, TranscriptFingerprint{Format: "claude-jsonl", Lines: 500, SHA256: "new-hash"})
	writeTestCuration(t, root, shard, key, Curation{
		State:                StateCurated,
		At:                   mustParseTime(t, "2026-09-12T08:00:00-05:00"),
		Machine:              "laptop",
		TranscriptAtCuration: &TranscriptStamp{Lines: 412, SHA256: "old-hash"},
	})

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	item := findItem(t, items, key.DirName())
	if !item.Stale() {
		t.Errorf("Stale() = false, want true (fingerprints differ)")
	}
}

func TestWalk_MalformedSessionJSONCountedAndSkipped(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	good := SessionKey{Harness: "claude", NativeID: "good"}
	bad := SessionKey{Harness: "claude", NativeID: "bad"}

	writeTestSession(t, root, shard, good, TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"})

	// Fabricate a garbage session.json directly, bypassing WriteSession.
	badDir := SessionDir(root, shard, bad)
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(SessionJSONPath(root, shard, bad), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage session.json: %v", err)
	}

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk returned a hard error for a malformed session.json: %v", err)
	}
	if len(items) != 1 || items[0].Key.DirName() != good.DirName() {
		t.Errorf("items = %+v, want only %q", items, good.DirName())
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	if diags[0].Path != SessionJSONPath(root, shard, bad) {
		t.Errorf("diags[0].Path = %q, want %q", diags[0].Path, SessionJSONPath(root, shard, bad))
	}
	if diags[0].Err == nil {
		t.Errorf("diags[0].Err = nil, want a parse error")
	}
}

func TestWalk_MissingSessionJSONCountedAndSkipped(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	key := SessionKey{Harness: "claude", NativeID: "empty-dir"}

	// A session directory exists (so it looks like a session) but nothing
	// was ever written into it.
	if err := os.MkdirAll(SessionDir(root, shard, key), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %+v, want none", items)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
}

func TestWalk_MalformedShardAndSessionDirNamesCountedAndSkipped(t *testing.T) {
	root := t.TempDir()
	good := SessionKey{Harness: "claude", NativeID: "good"}
	writeTestSession(t, root, "2026-09-11", good, TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"})

	// A shard directory not shaped YYYY-MM-DD.
	if err := os.MkdirAll(filepath.Join(root, "sessions", "not-a-date"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A session directory not shaped <harness>-<native-id> (no dash at all).
	if err := os.MkdirAll(filepath.Join(root, "sessions", "2026-09-11", "nodash"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(items) != 1 || items[0].Key.DirName() != good.DirName() {
		t.Errorf("items = %+v, want only %q", items, good.DirName())
	}
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want exactly 2 (bad shard + bad session dir)", diags)
	}
}

func TestWalk_EntryPresenceFlag(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	withEntry := SessionKey{Harness: "claude", NativeID: "has-entry"}
	withoutEntry := SessionKey{Harness: "claude", NativeID: "no-entry"}

	writeTestSession(t, root, shard, withEntry, TranscriptFingerprint{Lines: 1, SHA256: "a"})
	writeTestEntry(t, root, shard, withEntry)
	writeTestSession(t, root, shard, withoutEntry, TranscriptFingerprint{Lines: 1, SHA256: "b"})

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}

	if got := findItem(t, items, withEntry.DirName()); !got.EntryExists {
		t.Errorf("EntryExists = false for %q, want true", withEntry.DirName())
	}
	if got := findItem(t, items, withoutEntry.DirName()); got.EntryExists {
		t.Errorf("EntryExists = true for %q, want false", withoutEntry.DirName())
	}
}

// TestWalk_DashedHarnessNameUsesSessionJSONIdentity is the M11 identity
// fix's regression test: a naive first-dash split of the directory name
// "claude-code-abc123" would wrongly read Harness="claude",
// NativeID="code-abc123". session.json is the only authority for
// identity (loadWalkItem), so Walk must recover Harness="claude-code",
// NativeID="abc123" instead — a dashed harness name is real (claude-code
// in this repo, cursor-agent in the design record), not hypothetical.
func TestWalk_DashedHarnessNameUsesSessionJSONIdentity(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	key := SessionKey{Harness: "claude-code", NativeID: "abc123"}

	writeTestSession(t, root, shard, key, TranscriptFingerprint{Format: "claude-code-jsonl", Lines: 5, SHA256: "a"})

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1", items)
	}
	if items[0].Key != key {
		t.Errorf("Key = %+v, want %+v (a naive first-dash split would wrongly give %+v)",
			items[0].Key, key, SessionKey{Harness: "claude", NativeID: "code-abc123"})
	}
}

// TestWalk_SessionJSONIdentityMismatchCountedAndSkipped covers the other
// side of the M11 fix: when session.json's own identity genuinely
// disagrees with the directory it was found in (a hand-edited or
// corrupted document — not just a dashed harness name confusing a
// splitter), Walk must not trust either side blindly; it skips the
// session and counts a diagnostic rather than returning a mismatched or
// guessed-at item.
func TestWalk_SessionJSONIdentityMismatchCountedAndSkipped(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"
	key := SessionKey{Harness: "claude", NativeID: "dirname-says-this"}
	writeTestSession(t, root, shard, key, TranscriptFingerprint{Lines: 1, SHA256: "a"})

	// Hand-edit session.json's identity to disagree with its directory,
	// bypassing WriteSession's own stamping (which would just re-align it).
	sess, ok, err := ReadSession(root, shard, key)
	if err != nil || !ok {
		t.Fatalf("ReadSession: ok=%v err=%v", ok, err)
	}
	sess.SessionID = "session-json-says-this"
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(SessionJSONPath(root, shard, key), data, 0o644); err != nil {
		t.Fatalf("write mismatched session.json: %v", err)
	}

	items, diags, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %+v, want none (identity mismatch)", items)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	if diags[0].Path != SessionJSONPath(root, shard, key) {
		t.Errorf("diags[0].Path = %q, want %q", diags[0].Path, SessionJSONPath(root, shard, key))
	}
}
