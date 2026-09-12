package journal

import (
	"os"
	"strings"
	"testing"
)

// sealFixture bundles a journalFixture with the session keys and shards
// it authored, so the seal sweep's tests can address specific fixture
// sessions by name instead of re-deriving them.
type sealFixture struct {
	*journalFixture
	Captured, Curated, Stale, DismissedAuto, DismissedNote SessionKey
}

// buildSealFixture authors the fixture journal the seal sweep runs
// against: three shards, one session per state (including a curated-
// stale one, M7, and both dismissed reasons, M3), a project with two
// machines' clone files, and breadcrumbs from two machines including one
// global crumb. Ids and dates are deliberately legible so a failing
// assertion's diff stays readable.
func buildSealFixture(t *testing.T) sealFixture {
	t.Helper()
	f := newJournalFixture(t)

	captured := SessionKey{Harness: "claude", NativeID: "captured-01"}
	curated := SessionKey{Harness: "claude", NativeID: "curated-01"}
	stale := SessionKey{Harness: "claude", NativeID: "stale-01"}
	dismissedAuto := SessionKey{Harness: "codex", NativeID: "dismissed-auto-01"}
	dismissedNote := SessionKey{Harness: "codex", NativeID: "dismissed-note-01"}

	f.Captured("2026-09-10", captured,
		TranscriptFingerprint{Format: "claude-jsonl", Lines: 8, SHA256: "captured-hash"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithTranscript("2026-09-10", captured, []byte(`{"role":"user","text":"hello"}`+"\n"))

	f.Curated("2026-09-11", curated,
		TranscriptFingerprint{Format: "claude-jsonl", Lines: 40, SHA256: "curated-hash"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
		mustParseTime(t, "2026-09-11T18:00:00-05:00"),
		"framework", "curated session title",
	).WithTranscript("2026-09-11", curated, []byte(`{"role":"user","text":"hello"}`+"\n"))

	f.CuratedStale("2026-09-11", stale,
		TranscriptFingerprint{Format: "claude-jsonl", Lines: 90, SHA256: "grown-hash"},
		TranscriptStamp{Lines: 40, SHA256: "original-hash"},
		mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		mustParseTime(t, "2026-09-11T12:00:00-05:00"),
		"laptop", "stale session title",
	).WithTranscript("2026-09-11", stale, []byte(`{"role":"user","text":"hello"}`+"\n"))

	f.Dismissed("2026-09-12", dismissedAuto,
		TranscriptFingerprint{Format: "codex-jsonl", Lines: 1, SHA256: "noop-hash"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
		mustParseTime(t, "2026-09-12T09:00:05-05:00"),
		"framework", "auto:no-op",
	).WithTranscript("2026-09-12", dismissedAuto, []byte(`{"role":"user","text":"one-liner"}`+"\n"))

	f.Dismissed("2026-09-12", dismissedNote,
		TranscriptFingerprint{Format: "codex-jsonl", Lines: 12, SHA256: "note-hash"},
		mustParseTime(t, "2026-09-12T10:00:00-05:00"),
		mustParseTime(t, "2026-09-12T14:00:00-05:00"),
		"laptop", "not useful, exploratory only",
	).WithTranscript("2026-09-12", dismissedNote, []byte(`{"role":"user","text":"nope"}`+"\n"))

	f.Project("clast", Project{
		ID:             "01J9WXYZ",
		Slug:           "clast",
		Remote:         "github.com/procrastivity/clast",
		IdentityRemote: "origin",
	})
	f.Clones("clast", ClonesFile{
		Machine: "framework",
		Clones:  []Clone{{ID: "01J9WABC", GitCommonDir: "/home/dev/Code/clast/.git", Label: "dev"}},
	})
	f.Clones("clast", ClonesFile{
		Machine: "laptop",
		Clones:  []Clone{{ID: "01J9WLAP", GitCommonDir: "/home/dev/laptop/clast/.git", Label: "laptop-dev"}},
	})

	slug := "clast"
	f.Breadcrumb("framework", mustParseTime(t, "2026-09-11T10:22:00-05:00"), &slug, "check migration before deploy")
	f.Breadcrumb("laptop", mustParseTime(t, "2026-09-11T14:02:00-05:00"), nil, "bump the cache version")

	return sealFixture{
		journalFixture: f,
		Captured:       captured,
		Curated:        curated,
		Stale:          stale,
		DismissedAuto:  dismissedAuto,
		DismissedNote:  dismissedNote,
	}
}

// TestSeal_DocumentsRoundTrip is the seal sweep's first leg: every MODEL
// §4 document the fixture authored round-trips, with the same key-set
// discipline step-02 established for a single document, now exercised
// across every document kind at once.
func TestSeal_DocumentsRoundTrip(t *testing.T) {
	sf := buildSealFixture(t)
	root := sf.Root()

	cases := []struct {
		shard       string
		key         SessionKey
		hasCuration bool
		hasEntry    bool
	}{
		{"2026-09-10", sf.Captured, false, false},
		{"2026-09-11", sf.Curated, true, true},
		{"2026-09-11", sf.Stale, true, true},
		{"2026-09-12", sf.DismissedAuto, true, false},
		{"2026-09-12", sf.DismissedNote, true, false},
	}
	for _, c := range cases {
		sess, ok, err := ReadSession(root, c.shard, c.key)
		if err != nil || !ok {
			t.Fatalf("ReadSession(%s): ok=%v err=%v", c.key.DirName(), ok, err)
		}
		if sess.Harness != c.key.Harness || sess.SessionID != c.key.NativeID {
			t.Errorf("session identity mismatch for %s: %+v", c.key.DirName(), sess)
		}
		assertKeys(t, mustMarshal(t, sess), []string{
			"schema_version", "harness", "session_id", "machine", "worktree",
			"branch", "started_at", "last_active_at", "captured_at",
			"source_path", "counts", "substantive", "transcript",
		}) // no "project" key: none of the fixture's sessions set one.

		curation, curationOK, err := ReadCuration(root, c.shard, c.key)
		if err != nil {
			t.Fatalf("ReadCuration(%s): %v", c.key.DirName(), err)
		}
		if curationOK != c.hasCuration {
			t.Errorf("%s: curationOK = %v, want %v", c.key.DirName(), curationOK, c.hasCuration)
		}
		if curationOK {
			wantKeys := []string{"schema_version", "state", "at", "machine", "reason"}
			if curation.State == StateCurated {
				wantKeys = append(wantKeys, "transcript_at_curation")
			}
			assertKeys(t, mustMarshal(t, curation), wantKeys)
		}

		_, statErr := os.Stat(EntryPath(root, c.shard, c.key))
		if hasEntry := statErr == nil; hasEntry != c.hasEntry {
			t.Errorf("%s: entry.md present = %v, want %v", c.key.DirName(), hasEntry, c.hasEntry)
		}
	}

	proj, ok, err := ReadProject(root, "clast")
	if err != nil || !ok {
		t.Fatalf("ReadProject: ok=%v err=%v", ok, err)
	}
	assertKeys(t, mustMarshal(t, proj), []string{"schema_version", "id", "slug", "remote", "identity_remote"})

	for _, machine := range []string{"framework", "laptop"} {
		cf, ok, err := ReadClones(root, "clast", machine)
		if err != nil || !ok {
			t.Fatalf("ReadClones(%s): ok=%v err=%v", machine, ok, err)
		}
		if len(cf.Clones) != 1 {
			t.Errorf("ReadClones(%s): Clones = %+v, want 1 entry", machine, cf.Clones)
		}
		assertKeys(t, mustMarshal(t, cf), []string{"schema_version", "machine", "clones"})
	}

	entries, diags, err := ReadBreadcrumbs(root, "2026-09-11")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("ReadBreadcrumbs diags = %+v, want none", diags)
	}
	if len(entries) != 2 {
		t.Fatalf("ReadBreadcrumbs entries = %+v, want 2", entries)
	}
	var sawGlobal, sawScoped bool
	for _, e := range entries {
		assertKeys(t, mustMarshal(t, e.Breadcrumb), []string{"at", "slug", "text"})
		switch {
		case e.Slug == nil:
			sawGlobal = true
		case *e.Slug == "clast":
			sawScoped = true
		}
	}
	if !sawGlobal || !sawScoped {
		t.Errorf("entries = %+v, want one global and one clast-scoped crumb", entries)
	}
}

// TestSeal_WalkMatchesExpectedStates is the seal sweep's second leg: a
// full Walk over the fixture returns every session with the right
// derived state and staleness, and zero diagnostics.
func TestSeal_WalkMatchesExpectedStates(t *testing.T) {
	sf := buildSealFixture(t)

	items, diags, err := Walk(sf.Root())
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("Walk diags = %+v, want none (a well-formed fixture)", diags)
	}
	if len(items) != 5 {
		t.Fatalf("Walk items = %d, want 5", len(items))
	}

	want := map[string]struct {
		state CurationState
		stale bool
		entry bool
	}{
		sf.Captured.DirName():      {StateCaptured, false, false},
		sf.Curated.DirName():       {StateCurated, false, true},
		sf.Stale.DirName():         {StateCurated, true, true},
		sf.DismissedAuto.DirName(): {StateDismissed, false, false},
		sf.DismissedNote.DirName(): {StateDismissed, false, false},
	}
	for _, it := range items {
		w, ok := want[it.Key.DirName()]
		if !ok {
			t.Errorf("unexpected item %q in walk result", it.Key.DirName())
			continue
		}
		if it.State() != w.state {
			t.Errorf("%s: State() = %q, want %q", it.Key.DirName(), it.State(), w.state)
		}
		if it.Stale() != w.stale {
			t.Errorf("%s: Stale() = %v, want %v", it.Key.DirName(), it.Stale(), w.stale)
		}
		if it.EntryExists != w.entry {
			t.Errorf("%s: EntryExists = %v, want %v", it.Key.DirName(), it.EntryExists, w.entry)
		}
	}

	auto := findItem(t, items, sf.DismissedAuto.DirName())
	if auto.Curation.Reason == nil || *auto.Curation.Reason != "auto:no-op" {
		t.Errorf("dismissedAuto reason = %v, want %q", auto.Curation.Reason, "auto:no-op")
	}
	note := findItem(t, items, sf.DismissedNote.DirName())
	if note.Curation.Reason == nil || *note.Curation.Reason != "not useful, exploratory only" {
		t.Errorf("dismissedNote reason = %v, want free text", note.Curation.Reason)
	}
}

// TestM9_GarbageTranscriptBytesNeverAffectReads is the seal sweep's M9
// leg, made mechanical rather than aspirational: it snapshots the full
// read surface (Walk, ReadBreadcrumbs) over the fixture, overwrites every
// session's transcript copy with bytes that aren't even valid JSONL, and
// asserts the snapshot is byte-identical afterward. It then spot-checks
// Resolve, State/Stale, and ReadSession/ReadCuration directly on the
// stale session — the derived fact (Stale) most likely to accidentally
// depend on transcript content if the M9 boundary ever broke.
func TestM9_GarbageTranscriptBytesNeverAffectReads(t *testing.T) {
	sf := buildSealFixture(t)
	root := sf.Root()

	sessions := []struct {
		shard string
		key   SessionKey
	}{
		{"2026-09-10", sf.Captured},
		{"2026-09-11", sf.Curated},
		{"2026-09-11", sf.Stale},
		{"2026-09-12", sf.DismissedAuto},
		{"2026-09-12", sf.DismissedNote},
	}

	snapshot := func() (walkJSON, crumbsJSON string) {
		t.Helper()
		items, diags, err := Walk(root)
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		if len(diags) != 0 {
			t.Fatalf("Walk diags = %+v, want none", diags)
		}
		entries, cdiags, err := ReadBreadcrumbs(root, "2026-09-11")
		if err != nil {
			t.Fatalf("ReadBreadcrumbs: %v", err)
		}
		if len(cdiags) != 0 {
			t.Fatalf("breadcrumb diags = %+v, want none", cdiags)
		}
		return string(mustMarshal(t, items)), string(mustMarshal(t, entries))
	}

	beforeWalk, beforeCrumbs := snapshot()

	for _, s := range sessions {
		sf.WithTranscript(s.shard, s.key, []byte("\x00not-json\xff,,,{{{garbage"))
	}

	afterWalk, afterCrumbs := snapshot()

	if beforeWalk != afterWalk {
		t.Errorf("Walk's result changed after the transcript copy was overwritten with garbage bytes:\nbefore = %s\nafter  = %s", beforeWalk, afterWalk)
	}
	if beforeCrumbs != afterCrumbs {
		t.Errorf("ReadBreadcrumbs' result changed after the transcript copy was overwritten with garbage bytes")
	}

	items, _, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	resolved, err := Resolve(items, sf.Stale.DirName())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.State() != StateCurated || !resolved.Stale() {
		t.Errorf("stale session's derived facts changed: State()=%q Stale()=%v", resolved.State(), resolved.Stale())
	}

	sess, ok, err := ReadSession(root, "2026-09-11", sf.Stale)
	if err != nil || !ok {
		t.Fatalf("ReadSession: ok=%v err=%v", ok, err)
	}
	if sess.Transcript.Lines != 90 || sess.Transcript.SHA256 != "grown-hash" {
		t.Errorf("ReadSession's fingerprint changed after garbage transcript bytes: %+v", sess.Transcript)
	}

	curation, ok, err := ReadCuration(root, "2026-09-11", sf.Stale)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.TranscriptAtCuration == nil || curation.TranscriptAtCuration.Lines != 40 {
		t.Errorf("ReadCuration's recorded fingerprint changed after garbage transcript bytes: %+v", curation.TranscriptAtCuration)
	}
}

// TestM9_OnlyPathsGoNamesTheTranscriptFile is the seal sweep's source-level
// M9 guard: it greps every non-test .go file in this package for the
// literal transcript filename and asserts only paths.go (TranscriptPath's
// path construction) ever mentions it. This is deliberately mechanical —
// a grep, not a read of the prose — so it keeps catching a regression
// even if a future change's comments claim otherwise.
func TestM9_OnlyPathsGoNamesTheTranscriptFile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "paths.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if strings.Contains(string(data), "transcript.jsonl") {
			t.Errorf("%s names the literal transcript filename; only paths.go's TranscriptPath may construct that path — M9 means every other read in this package stays on session.json, curation.json, and entry.md's presence", name)
		}
	}
}
