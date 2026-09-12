package journal

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"
)

// mustParseTime parses an RFC3339 timestamp with the fixed offsets MODEL §4
// examples use, failing the test on a bad literal.
func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

// keySet unmarshals data (one JSON object) and returns its top-level key
// set, sorted — used to assert a document's key names against the MODEL
// §4 examples independent of Go field ordering.
func keySet(t *testing.T, data []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertKeys(t *testing.T, data []byte, want []string) {
	t.Helper()
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedWant)
	got := keySet(t, data)
	if len(got) != len(sortedWant) {
		t.Fatalf("keys = %v, want %v", got, sortedWant)
	}
	for i := range got {
		if got[i] != sortedWant[i] {
			t.Fatalf("keys = %v, want %v", got, sortedWant)
		}
	}
}

func TestSession_RoundTrip(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "8f3a"}
	shard := "2026-09-11"

	want := Session{
		SchemaVersion: recordSchemaVersion,
		Harness:       "claude",
		SessionID:     "8f3a",
		Machine:       "framework",
		Project: &SessionProject{
			ID:    "01J9WXYZ",
			Slug:  "clast",
			Clone: "01J9WABC",
			Label: "dev",
			Path:  "/home/dev/Code/clast",
		},
		Worktree:     "",
		Branch:       "main",
		StartedAt:    mustParseTime(t, "2026-09-11T09:14:02-05:00"),
		LastActiveAt: mustParseTime(t, "2026-09-11T11:40:51-05:00"),
		CapturedAt:   mustParseTime(t, "2026-09-11T12:00:03-05:00"),
		SourcePath:   "/home/dev/.claude/projects/-home-dev-Code-clast/x.jsonl",
		Counts:       SessionCounts{User: 14, Assistant: 13},
		Substantive:  true,
		Transcript:   TranscriptFingerprint{Format: "claude-jsonl", Lines: 412, SHA256: "abc123"},
	}

	if err := WriteSession(root, shard, key, want); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	got, ok, err := ReadSession(root, shard, key)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if !ok {
		t.Fatalf("ReadSession: ok = false, want true")
	}

	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip changed the document:\n got = %s\nwant = %s", gotJSON, wantJSON)
	}

	assertKeys(t, gotJSON, []string{
		"schema_version", "harness", "session_id", "machine", "project",
		"worktree", "branch", "started_at", "last_active_at", "captured_at",
		"source_path", "counts", "substantive", "transcript",
	})
	assertKeys(t, mustMarshal(t, got.Project), []string{"id", "slug", "clone", "label", "path"})

	if got.SchemaVersion != recordSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, recordSchemaVersion)
	}

	// WriteSession is a write path: it must have triggered EnsureRoot's
	// init-on-first-write (MODEL M6), the same primitive step-01 landed.
	if _, ok, err := ReadMarker(root); err != nil || !ok {
		t.Errorf("ReadMarker after WriteSession: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
}

func TestSession_NoProjectOmitsKey(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "no-project"}
	shard := "2026-09-11"

	s := Session{Harness: "claude", SessionID: "no-project"}
	if err := WriteSession(root, shard, key, s); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	got, ok, err := ReadSession(root, shard, key)
	if err != nil || !ok {
		t.Fatalf("ReadSession: ok=%v err=%v", ok, err)
	}
	if got.Project != nil {
		t.Errorf("Project = %+v, want nil", got.Project)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["project"]; present {
		t.Errorf(`"project" key present with no project, want omitted`)
	}
}

func TestCuration_RoundTrip_Curated(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "8f3a"}
	shard := "2026-09-11"

	want := Curation{
		SchemaVersion: recordSchemaVersion,
		State:         StateCurated,
		At:            mustParseTime(t, "2026-09-12T08:05:00-05:00"),
		Machine:       "laptop",
		Reason:        nil,
		TranscriptAtCuration: &TranscriptStamp{
			Lines:  412,
			SHA256: "abc123",
		},
	}

	if err := WriteCuration(root, shard, key, want); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	got, ok, err := ReadCuration(root, shard, key)
	if err != nil {
		t.Fatalf("ReadCuration: %v", err)
	}
	if !ok {
		t.Fatalf("ReadCuration: ok = false, want true")
	}

	gotJSON := mustMarshal(t, got)
	wantJSON := mustMarshal(t, want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip changed the document:\n got = %s\nwant = %s", gotJSON, wantJSON)
	}

	assertKeys(t, gotJSON, []string{
		"schema_version", "state", "at", "machine", "reason", "transcript_at_curation",
	})

	// reason: null must be an explicit key, not an omitted one (MODEL §4
	// example shows "reason": null for the curated case).
	var m map[string]any
	if err := json.Unmarshal(gotJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, present := m["reason"]; !present || v != nil {
		t.Errorf(`"reason" = %v (present=%v), want explicit null`, v, present)
	}
}

func TestCuration_RoundTrip_Dismissed(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "dismissed-one"}
	shard := "2026-09-11"

	reason := "auto:no-op"
	want := Curation{
		State:   StateDismissed,
		At:      mustParseTime(t, "2026-09-12T08:05:00-05:00"),
		Machine: "laptop",
		Reason:  &reason,
		// No TranscriptAtCuration: dismissed sessions never record one.
	}

	if err := WriteCuration(root, shard, key, want); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	got, ok, err := ReadCuration(root, shard, key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if got.TranscriptAtCuration != nil {
		t.Errorf("TranscriptAtCuration = %+v, want nil for dismissed", got.TranscriptAtCuration)
	}

	gotJSON := mustMarshal(t, got)
	assertKeys(t, gotJSON, []string{"schema_version", "state", "at", "machine", "reason"})
}

func TestCuration_MissingMeansCaptured(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "never-curated"}
	shard := "2026-09-11"

	// A session can exist (session.json written) with no curation.json at
	// all — the MODEL §2 default state, `captured`.
	if err := WriteSession(root, shard, key, Session{Harness: "claude", SessionID: "never-curated"}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	got, ok, err := ReadCuration(root, shard, key)
	if err != nil {
		t.Fatalf("ReadCuration on missing curation.json returned an error: %v", err)
	}
	if ok {
		t.Errorf("ReadCuration on missing curation.json: ok = true, want false")
	}
	if got != (Curation{}) {
		t.Errorf("ReadCuration on missing curation.json: got %+v, want zero value", got)
	}
}

func TestWriteCuration_RejectsStateCaptured(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "guard-captured"}
	shard := "2026-09-11"

	err := WriteCuration(root, shard, key, Curation{State: StateCaptured})
	if err == nil {
		t.Fatal("WriteCuration(StateCaptured): want error, got nil")
	}
	if _, ok, readErr := ReadCuration(root, shard, key); ok || readErr != nil {
		t.Errorf("WriteCuration must not have written a file: ok=%v err=%v", ok, readErr)
	}
}

func TestWriteCuration_RejectsEmptyState(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "guard-empty"}
	shard := "2026-09-11"

	err := WriteCuration(root, shard, key, Curation{})
	if err == nil {
		t.Fatal("WriteCuration(empty state): want error, got nil")
	}
}

func TestRemoveCuration_PresentAndAbsent(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "undismiss-me"}
	shard := "2026-09-11"

	reason := "not useful"
	if err := WriteCuration(root, shard, key, Curation{State: StateDismissed, Reason: &reason, Machine: "framework"}); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	if _, ok, err := ReadCuration(root, shard, key); err != nil || !ok {
		t.Fatalf("precondition: ReadCuration ok=%v err=%v, want ok=true", ok, err)
	}

	if err := RemoveCuration(root, shard, key); err != nil {
		t.Fatalf("RemoveCuration (present): %v", err)
	}
	if _, ok, err := ReadCuration(root, shard, key); err != nil || ok {
		t.Fatalf("after RemoveCuration: ok=%v err=%v, want ok=false err=nil (back to captured)", ok, err)
	}

	// Removing an already-absent curation.json is a no-op, not an error.
	if err := RemoveCuration(root, shard, key); err != nil {
		t.Fatalf("RemoveCuration (already absent): %v", err)
	}
}

func TestProject_RoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Project{
		SchemaVersion:  recordSchemaVersion,
		ID:             "01J9WXYZ",
		Slug:           "clast",
		Remote:         "github.com/procrastivity/clast",
		IdentityRemote: "origin",
	}

	if err := WriteProject(root, want.Slug, want); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	got, ok, err := ReadProject(root, want.Slug)
	if err != nil || !ok {
		t.Fatalf("ReadProject: ok=%v err=%v", ok, err)
	}

	gotJSON := mustMarshal(t, got)
	wantJSON := mustMarshal(t, want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip changed the document:\n got = %s\nwant = %s", gotJSON, wantJSON)
	}
	assertKeys(t, gotJSON, []string{"schema_version", "id", "slug", "remote", "identity_remote"})
}

func TestProject_MissingReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, ok, err := ReadProject(root, "never-created")
	if err != nil {
		t.Fatalf("ReadProject on missing project returned an error: %v", err)
	}
	if ok {
		t.Errorf("ReadProject on missing project: ok = true, want false")
	}
	if got != (Project{}) {
		t.Errorf("ReadProject on missing project: got %+v, want zero value", got)
	}
}

func TestClonesFile_RoundTrip(t *testing.T) {
	root := t.TempDir()
	want := ClonesFile{
		SchemaVersion: recordSchemaVersion,
		Machine:       "framework",
		Clones: []Clone{
			{ID: "01J9WABC", GitCommonDir: "/home/dev/Code/clast/.git", Label: "dev"},
			{ID: "01J9WDEF", GitCommonDir: "/home/dev/Code/clast-perf/.git", Label: "perf"},
		},
	}

	if err := WriteClones(root, "clast", want); err != nil {
		t.Fatalf("WriteClones: %v", err)
	}
	got, ok, err := ReadClones(root, "clast", want.Machine)
	if err != nil || !ok {
		t.Fatalf("ReadClones: ok=%v err=%v", ok, err)
	}

	gotJSON := mustMarshal(t, got)
	wantJSON := mustMarshal(t, want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip changed the document:\n got = %s\nwant = %s", gotJSON, wantJSON)
	}
	assertKeys(t, gotJSON, []string{"schema_version", "machine", "clones"})
	if len(got.Clones) != 2 {
		t.Fatalf("Clones = %+v, want 2 entries", got.Clones)
	}
	assertKeys(t, mustMarshal(t, got.Clones[0]), []string{"id", "git_common_dir", "label"})
}

func TestClonesFile_MissingReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, ok, err := ReadClones(root, "clast", "never-registered")
	if err != nil {
		t.Fatalf("ReadClones on missing file returned an error: %v", err)
	}
	if ok {
		t.Errorf("ReadClones on missing file: ok = true, want false")
	}
	if got.SchemaVersion != 0 || got.Machine != "" || len(got.Clones) != 0 {
		t.Errorf("ReadClones on missing file: got %+v, want zero value", got)
	}
}

func TestBreadcrumb_RoundTrip(t *testing.T) {
	at := mustParseTime(t, "2026-09-11T10:22:00-05:00")

	slug := "clast"
	scoped := Breadcrumb{At: at, Slug: &slug, Text: "check migration before deploy"}
	scopedJSON := mustMarshal(t, scoped)
	assertKeys(t, scopedJSON, []string{"at", "slug", "text"})

	var gotScoped Breadcrumb
	if err := json.Unmarshal(scopedJSON, &gotScoped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if gotScoped.Slug == nil || *gotScoped.Slug != "clast" {
		t.Errorf("Slug = %v, want \"clast\"", gotScoped.Slug)
	}

	global := Breadcrumb{At: at, Slug: nil, Text: "bump the cache version"}
	globalJSON := mustMarshal(t, global)
	assertKeys(t, globalJSON, []string{"at", "slug", "text"})

	var m map[string]any
	if err := json.Unmarshal(globalJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, present := m["slug"]; !present || v != nil {
		t.Errorf(`global crumb "slug" = %v (present=%v), want explicit null`, v, present)
	}
}

// TestWriteEntry_RoundTrip_OpaqueBytes asserts WriteEntry writes exactly
// the bytes it was given, verbatim, to EntryPath's location — the M9-clean
// posture (state-verbs step 01): this package never parses or otherwise
// touches entry.md's content, only stores it.
func TestWriteEntry_RoundTrip_OpaqueBytes(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "8f3a"}
	shard := "2026-09-11"

	data := []byte("---\ntitle: fixing the flaky test\ntags: []\n---\n\nbody text\n")
	if err := WriteEntry(root, shard, key, data); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	got, err := os.ReadFile(EntryPath(root, shard, key))
	if err != nil {
		t.Fatalf("reading entry.md after WriteEntry: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("entry.md content = %q, want %q verbatim", got, data)
	}

	// WriteEntry is a write path: it must have triggered EnsureRoot's
	// init-on-first-write (MODEL M6), same as every other write primitive.
	if _, ok, err := ReadMarker(root); err != nil || !ok {
		t.Errorf("ReadMarker after WriteEntry: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
}

// TestWriteEntry_Overwrites confirms a second WriteEntry call replaces the
// first document's bytes entirely — the re-curate case (SURFACE V14) and
// curate-from-dismissed both depend on entry.md being rewritten whole, not
// merged.
func TestWriteEntry_Overwrites(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "8f3a"}
	shard := "2026-09-11"

	if err := WriteEntry(root, shard, key, []byte("---\ntitle: first\n---\n\nfirst body\n")); err != nil {
		t.Fatalf("WriteEntry (first): %v", err)
	}
	second := []byte("---\ntitle: second\n---\n\nsecond body\n")
	if err := WriteEntry(root, shard, key, second); err != nil {
		t.Fatalf("WriteEntry (second): %v", err)
	}

	got, err := os.ReadFile(EntryPath(root, shard, key))
	if err != nil {
		t.Fatalf("reading entry.md: %v", err)
	}
	if string(got) != string(second) {
		t.Errorf("entry.md content = %q, want the second write's bytes %q", got, second)
	}
}

// TestWriteEntry_NoStrayTempFileLeftBehind mirrors the entry package's own
// Write test for the same property, over this package's own primitive.
func TestWriteEntry_NoStrayTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Harness: "claude", NativeID: "8f3a"}
	shard := "2026-09-11"

	if err := WriteEntry(root, shard, key, []byte("---\ntitle: clean\n---\n\nbody\n")); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	dir := SessionDir(root, shard, key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if len(e.Name()) >= 5 && e.Name()[:5] == ".tmp-" {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}
