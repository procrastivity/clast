// Package journaltest builds small, deliberately-readable on-disk fixture
// journals through internal/journal's own write primitives (WriteSession,
// WriteCuration, WriteProject, WriteClones, AppendBreadcrumbAs) — never by
// hand-typing JSON. A fixture built through the same functions production
// code uses can never silently drift from the schemas those functions
// already round-trip, the way a hand-typed JSON fixture could.
//
// This package is the store Matter's original package-local builder
// (internal/journal/fixture_test.go), promoted here in query-verbs' step 01
// so every verb Matter that needs a fixture journal — query-verbs' own
// tests and state-verbs, run in a parallel worktree — shares one builder
// instead of forking a second copy (H4: fixtures are authored fresh, never
// copied from a live journal, but that doesn't mean re-invented per
// Matter).
package journaltest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
)

// Fixture authors a fixture journal at a fresh temporary root, one fluent
// call per session/document.
type Fixture struct {
	t    testing.TB
	root string
}

// New starts a fixture rooted at a fresh temporary directory.
func New(t testing.TB) *Fixture {
	t.Helper()
	return &Fixture{t: t, root: t.TempDir()}
}

// Root returns the fixture's journal root.
func (f *Fixture) Root() string { return f.root }

func (f *Fixture) writeSession(shard string, key journal.SessionKey, transcript journal.TranscriptFingerprint, startedAt time.Time) {
	f.t.Helper()
	sess := journal.Session{
		Harness:      key.Harness,
		SessionID:    key.NativeID,
		Machine:      "framework",
		StartedAt:    startedAt,
		LastActiveAt: startedAt.Add(20 * time.Minute),
		CapturedAt:   startedAt.Add(25 * time.Minute),
		SourcePath:   "/home/dev/.claude/projects/-home-dev-Code-clast/" + key.NativeID + ".jsonl",
		Counts:       journal.SessionCounts{User: 3, Assistant: 3},
		Substantive:  true,
		Transcript:   transcript,
	}
	if err := journal.WriteSession(f.root, shard, key, sess); err != nil {
		f.t.Fatalf("fixture: WriteSession(%s/%s): %v", shard, key.DirName(), err)
	}
}

func (f *Fixture) writeCuration(shard string, key journal.SessionKey, c journal.Curation) {
	f.t.Helper()
	if err := journal.WriteCuration(f.root, shard, key, c); err != nil {
		f.t.Fatalf("fixture: WriteCuration(%s/%s): %v", shard, key.DirName(), err)
	}
}

// writeEntry writes a minimal entry.md directly (not through the entry
// package's Write, which validates a caller-supplied document): the
// fixture is authoring its own known-good bytes, so there is nothing to
// validate against.
func (f *Fixture) writeEntry(shard string, key journal.SessionKey, title string) {
	f.t.Helper()
	body := "---\ntitle: " + title + "\ntags: []\n---\n\n" + title + "\n"
	if err := os.WriteFile(journal.EntryPath(f.root, shard, key), []byte(body), 0o644); err != nil {
		f.t.Fatalf("fixture: writing entry.md for %s/%s: %v", shard, key.DirName(), err)
	}
}

// writeTranscript writes data verbatim to the transcript copy — the store
// package never parses it either way (M9), so the fixture is free to put
// deliberately garbage bytes there for an M9 check.
func (f *Fixture) writeTranscript(shard string, key journal.SessionKey, data []byte) {
	f.t.Helper()
	path := journal.TranscriptPath(f.root, shard, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("fixture: mkdir for transcript at %s/%s: %v", shard, key.DirName(), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		f.t.Fatalf("fixture: writing transcript for %s/%s: %v", shard, key.DirName(), err)
	}
}

// Captured authors a captured session: session.json only — no
// curation.json (MODEL §2's default state) and no entry.md.
func (f *Fixture) Captured(shard string, key journal.SessionKey, transcript journal.TranscriptFingerprint, startedAt time.Time) *Fixture {
	f.writeSession(shard, key, transcript, startedAt)
	return f
}

// Curated authors a curated session: session.json, an entry.md, and a
// curation.json whose transcript_at_curation matches transcript exactly —
// not stale (M7).
func (f *Fixture) Curated(shard string, key journal.SessionKey, transcript journal.TranscriptFingerprint, startedAt, curatedAt time.Time, machine, title string) *Fixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, journal.Curation{
		State:                journal.StateCurated,
		At:                   curatedAt,
		Machine:              machine,
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: transcript.Lines, SHA256: transcript.SHA256},
	})
	f.writeEntry(shard, key, title)
	return f
}

// CuratedStale authors a curated session whose recorded
// transcript_at_curation deliberately differs from the current
// session.json transcript — stale per M7 (the session grew after it was
// curated).
func (f *Fixture) CuratedStale(shard string, key journal.SessionKey, transcript journal.TranscriptFingerprint, curatedFingerprint journal.TranscriptStamp, startedAt, curatedAt time.Time, machine, title string) *Fixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, journal.Curation{
		State:                journal.StateCurated,
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
func (f *Fixture) Dismissed(shard string, key journal.SessionKey, transcript journal.TranscriptFingerprint, startedAt, dismissedAt time.Time, machine, reason string) *Fixture {
	f.writeSession(shard, key, transcript, startedAt)
	f.writeCuration(shard, key, journal.Curation{
		State:   journal.StateDismissed,
		At:      dismissedAt,
		Machine: machine,
		Reason:  &reason,
	})
	return f
}

// WithTranscript writes data verbatim as key's transcript copy — a
// fluent wrapper for chaining after any of the session methods above.
func (f *Fixture) WithTranscript(shard string, key journal.SessionKey, data []byte) *Fixture {
	f.writeTranscript(shard, key, data)
	return f
}

// WithProject re-reads shard/key's already-authored session.json, sets
// its Project field, and writes it back. A separate fluent step rather
// than a parameter on Captured/Curated/etc.: most fixture sessions don't
// need a project (a captured session need not belong to one, MODEL §4),
// so this layers one onto only the ones that do.
func (f *Fixture) WithProject(shard string, key journal.SessionKey, project journal.SessionProject) *Fixture {
	f.t.Helper()
	sess, ok, err := journal.ReadSession(f.root, shard, key)
	if err != nil || !ok {
		f.t.Fatalf("fixture: WithProject: ReadSession(%s/%s): ok=%v err=%v", shard, key.DirName(), ok, err)
	}
	sess.Project = &project
	if err := journal.WriteSession(f.root, shard, key, sess); err != nil {
		f.t.Fatalf("fixture: WithProject: WriteSession(%s/%s): %v", shard, key.DirName(), err)
	}
	return f
}

// WithMachine re-reads shard/key's already-authored session.json, sets its
// Machine field, and writes it back — the session-capture machine
// (MODEL §4's session.json "machine"), distinct from Curated/Dismissed's
// own machine parameter (who curated it, curation.json's "machine"). A
// separate fluent step for the same reason WithProject is: most fixture
// sessions are fine with the default ("framework"), so only a test that
// actually needs a session captured on a named machine reaches for this.
func (f *Fixture) WithMachine(shard string, key journal.SessionKey, machine string) *Fixture {
	f.t.Helper()
	sess, ok, err := journal.ReadSession(f.root, shard, key)
	if err != nil || !ok {
		f.t.Fatalf("fixture: WithMachine: ReadSession(%s/%s): ok=%v err=%v", shard, key.DirName(), ok, err)
	}
	sess.Machine = machine
	if err := journal.WriteSession(f.root, shard, key, sess); err != nil {
		f.t.Fatalf("fixture: WithMachine: WriteSession(%s/%s): %v", shard, key.DirName(), err)
	}
	return f
}

// Project authors projects/<slug>/project.json.
func (f *Fixture) Project(slug string, p journal.Project) *Fixture {
	f.t.Helper()
	if err := journal.WriteProject(f.root, slug, p); err != nil {
		f.t.Fatalf("fixture: WriteProject(%s): %v", slug, err)
	}
	return f
}

// Clones authors projects/<slug>/clones.<machine>.json.
func (f *Fixture) Clones(slug string, c journal.ClonesFile) *Fixture {
	f.t.Helper()
	if err := journal.WriteClones(f.root, slug, c); err != nil {
		f.t.Fatalf("fixture: WriteClones(%s/%s): %v", slug, c.Machine, err)
	}
	return f
}

// Breadcrumb appends one breadcrumb as machine, at the given instant, via
// journal.AppendBreadcrumbAs — journaltest lives outside internal/journal
// and so cannot swap its unexported hostname seam directly the way the
// original package-local fixture did; AppendBreadcrumbAs exists for
// exactly this. AppendBreadcrumbAs files the crumb under at's own local
// calendar date, so no now() seam is needed here either.
func (f *Fixture) Breadcrumb(machine string, at time.Time, slug *string, text string) *Fixture {
	f.t.Helper()
	if err := journal.AppendBreadcrumbAs(f.root, machine, journal.Breadcrumb{At: at, Slug: slug, Text: text}); err != nil {
		f.t.Fatalf("fixture: AppendBreadcrumbAs(%s): %v", machine, err)
	}
	return f
}
