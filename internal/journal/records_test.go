package journal

import (
	"encoding/json"
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

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}
