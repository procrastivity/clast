package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// journalFixture authors a small, deliberately-readable on-disk journal
// tree through this package's OWN write primitives (WriteSession,
// WriteCuration, WriteProject, WriteClones, AppendBreadcrumb) — the store
// brief's resolved fixture posture: a fixture built through the same
// functions production code uses can never silently drift from the
// schemas those functions already round-trip, the way a hand-typed JSON
// fixture could.
type journalFixture struct {
	t    *testing.T
	root string
}

// newJournalFixture starts a fixture rooted at a fresh temporary
// directory.
func newJournalFixture(t *testing.T) *journalFixture {
	t.Helper()
	return &journalFixture{t: t, root: t.TempDir()}
}

// Root returns the fixture's journal root.
func (f *journalFixture) Root() string { return f.root }

func (f *journalFixture) writeSession(shard string, key SessionKey, transcript TranscriptFingerprint, startedAt time.Time) {
	f.t.Helper()
	sess := Session{
		Harness:      key.Harness,
		SessionID:    key.NativeID,
		Machine:      "framework",
		StartedAt:    startedAt,
		LastActiveAt: startedAt.Add(20 * time.Minute),
		CapturedAt:   startedAt.Add(25 * time.Minute),
		SourcePath:   "/home/dev/.claude/projects/-home-dev-Code-clast/" + key.NativeID + ".jsonl",
		Counts:       SessionCounts{User: 3, Assistant: 3},
		Substantive:  true,
		Transcript:   transcript,
	}
	if err := WriteSession(f.root, shard, key, sess); err != nil {
		f.t.Fatalf("fixture: WriteSession(%s/%s): %v", shard, key.DirName(), err)
	}
}

func (f *journalFixture) writeCuration(shard string, key SessionKey, c Curation) {
	f.t.Helper()
	if err := WriteCuration(f.root, shard, key, c); err != nil {
		f.t.Fatalf("fixture: WriteCuration(%s/%s): %v", shard, key.DirName(), err)
	}
}

func (f *journalFixture) writeEntry(shard string, key SessionKey, title string) {
	f.t.Helper()
	body := "---\ntitle: " + title + "\ntags: []\n---\n\n" + title + "\n"
	if err := os.WriteFile(EntryPath(f.root, shard, key), []byte(body), 0o644); err != nil {
		f.t.Fatalf("fixture: writing entry.md for %s/%s: %v", shard, key.DirName(), err)
	}
}

// writeTranscript writes data verbatim to the transcript copy — this
// package never parses it either way (M9), so the fixture is free to put
// deliberately garbage bytes there for the seal sweep's M9 check.
func (f *journalFixture) writeTranscript(shard string, key SessionKey, data []byte) {
	f.t.Helper()
	path := TranscriptPath(f.root, shard, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("fixture: mkdir for transcript at %s/%s: %v", shard, key.DirName(), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		f.t.Fatalf("fixture: writing transcript for %s/%s: %v", shard, key.DirName(), err)
	}
}

// Captured authors a captured session: session.json only — no
// curation.json (MODEL §2's default state) and no entry.md.
func (f *journalFixture) Captured(shard string, key SessionKey, transcript TranscriptFingerprint, startedAt time.Time) *journalFixture {
	f.writeSession(shard, key, transcript, startedAt)
	return f
}

// Curated authors a curated session: session.json, an entry.md, and a
// curation.json whose transcript_at_curation matches transcript exactly
// — not stale (M7).
func (f *journalFixture) Curated(shard string, key SessionKey, transcript TranscriptFingerprint, startedAt, curatedAt time.Time, machine, title string) *journalFixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, Curation{
		State:                StateCurated,
		At:                   curatedAt,
		Machine:              machine,
		TranscriptAtCuration: &TranscriptStamp{Lines: transcript.Lines, SHA256: transcript.SHA256},
	})
	f.writeEntry(shard, key, title)
	return f
}

// CuratedStale authors a curated session whose recorded
// transcript_at_curation deliberately differs from the current
// session.json transcript — stale per M7 (the session grew after it was
// curated).
func (f *journalFixture) CuratedStale(shard string, key SessionKey, transcript TranscriptFingerprint, curatedFingerprint TranscriptStamp, startedAt, curatedAt time.Time, machine, title string) *journalFixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, Curation{
		State:                StateCurated,
		At:                   curatedAt,
		Machine:              machine,
		TranscriptAtCuration: &curatedFingerprint,
	})
	f.writeEntry(shard, key, title)
	return f
}

// Dismissed authors a dismissed session: session.json plus a
// curation.json recording reason (either MODEL M3's "auto:no-op" or a
// free-text reason), no entry.md.
func (f *journalFixture) Dismissed(shard string, key SessionKey, transcript TranscriptFingerprint, startedAt, dismissedAt time.Time, machine, reason string) *journalFixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, Curation{
		State:   StateDismissed,
		At:      dismissedAt,
		Machine: machine,
		Reason:  &reason,
	})
	return f
}

// WithTranscript writes data verbatim as key's transcript copy — a
// fluent wrapper over writeTranscript for chaining after any of the
// session methods above.
func (f *journalFixture) WithTranscript(shard string, key SessionKey, data []byte) *journalFixture {
	f.writeTranscript(shard, key, data)
	return f
}

// Project authors projects/<slug>/project.json.
func (f *journalFixture) Project(slug string, p Project) *journalFixture {
	f.t.Helper()
	if err := WriteProject(f.root, slug, p); err != nil {
		f.t.Fatalf("fixture: WriteProject(%s): %v", slug, err)
	}
	return f
}

// Clones authors projects/<slug>/clones.<machine>.json.
func (f *journalFixture) Clones(slug string, c ClonesFile) *journalFixture {
	f.t.Helper()
	if err := WriteClones(f.root, slug, c); err != nil {
		f.t.Fatalf("fixture: WriteClones(%s/%s): %v", slug, c.Machine, err)
	}
	return f
}

// Breadcrumb appends one breadcrumb as machine, at the given instant —
// swapping the hostname/now package seams for the one call, so a fixture
// can author crumbs from more than one machine without its caller
// juggling those seams directly.
func (f *journalFixture) Breadcrumb(machine string, at time.Time, slug *string, text string) *journalFixture {
	f.t.Helper()
	origHostname, origNow := hostname, now
	hostname = func() (string, error) { return machine, nil }
	now = func() time.Time { return at }
	defer func() { hostname, now = origHostname, origNow }()

	if err := AppendBreadcrumb(f.root, Breadcrumb{At: at, Slug: slug, Text: text}); err != nil {
		f.t.Fatalf("fixture: AppendBreadcrumb(%s): %v", machine, err)
	}
	return f
}
