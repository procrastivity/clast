package wake_test

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/verbs/wake"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

func mustCutoff(t *testing.T, s string) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

// TestRun_IncludesCapturedAndStaleCurated_ExcludesFreshCuratedAndDismissed
// covers V8's own working-set membership test: captured and
// curated-and-stale (M7) are in; a fresh (non-stale) curated session and a
// dismissed one are both out.
func TestRun_IncludesCapturedAndStaleCurated_ExcludesFreshCuratedAndDismissed(t *testing.T) {
	fx := journaltest.New(t)

	captured := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	staleCurated := journal.SessionKey{Harness: "claude", NativeID: "stale-curated-01"}
	freshCurated := journal.SessionKey{Harness: "claude", NativeID: "fresh-curated-01"}
	dismissed := journal.SessionKey{Harness: "claude", NativeID: "dismissed-01"}

	fx.Captured("2026-09-10", captured,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)
	fx.CuratedStale("2026-09-10", staleCurated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 10, SHA256: "original"},
		mustParseTime(t, "2026-09-10T10:00:00-05:00"),
		mustParseTime(t, "2026-09-10T11:00:00-05:00"),
		"framework", "a stale entry",
	)
	fx.Curated("2026-09-10", freshCurated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		mustParseTime(t, "2026-09-10T12:00:00-05:00"),
		mustParseTime(t, "2026-09-10T13:00:00-05:00"),
		"framework", "a fresh entry",
	)
	fx.Dismissed("2026-09-10", dismissed,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "c"},
		mustParseTime(t, "2026-09-10T14:00:00-05:00"),
		mustParseTime(t, "2026-09-10T15:00:00-05:00"),
		"framework", "manual",
	)

	cutoff := mustCutoff(t, "04:00")
	rows, err := wake.Run(fx.Root(), nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("Run = %d rows, want 2 (captured + stale-curated only); got %+v", len(rows), rows)
	}
	var gotCaptured, gotStale bool
	for _, r := range rows {
		switch r.Item.Key {
		case captured:
			gotCaptured = true
			if r.Item.State() != journal.StateCaptured {
				t.Errorf("captured row state = %s, want captured", r.Item.State())
			}
		case staleCurated:
			gotStale = true
			if !r.Item.Stale() {
				t.Error("staleCurated row Stale() = false, want true")
			}
			if r.Title != "a stale entry" {
				t.Errorf("staleCurated row Title = %q, want %q", r.Title, "a stale entry")
			}
		case freshCurated, dismissed:
			t.Errorf("row %s must be excluded from the working set", r.Item.Key.DirName())
		}
	}
	if !gotCaptured {
		t.Error("captured session missing from the working set")
	}
	if !gotStale {
		t.Error("stale curated session missing from the working set")
	}
}

// TestRun_OrdersProjectGroupedThenChronological covers V8/V7's ordering
// disposition: groups by project, most-recently-active project first,
// chronological (oldest first) within each group.
func TestRun_OrdersProjectGroupedThenChronological(t *testing.T) {
	fx := journaltest.New(t)

	// Project "alpha": most recent session at 09:00 on 09-12.
	alphaOld := journal.SessionKey{Harness: "claude", NativeID: "alpha-old"}
	alphaNew := journal.SessionKey{Harness: "claude", NativeID: "alpha-new"}
	// Project "beta": most recent session at 08:00 on 09-11 — earlier than
	// alpha's, so beta's group must sort after alpha's.
	betaOnly := journal.SessionKey{Harness: "claude", NativeID: "beta-only"}
	// No project at all: bucketed on its own, ordered by its own recency
	// like any other group.
	unprojected := journal.SessionKey{Harness: "claude", NativeID: "unprojected"}

	fx.Captured("2026-09-10", alphaOld,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a1"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithProject("2026-09-10", alphaOld, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	fx.Captured("2026-09-12", alphaNew,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a2"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
	).WithProject("2026-09-12", alphaNew, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	fx.Captured("2026-09-11", betaOnly,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b1"},
		mustParseTime(t, "2026-09-11T08:00:00-05:00"),
	).WithProject("2026-09-11", betaOnly, journal.SessionProject{ID: "p-beta", Slug: "beta", Clone: "c", Label: "beta", Path: "/beta"})

	fx.Captured("2026-09-09", unprojected,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "u1"},
		mustParseTime(t, "2026-09-09T07:00:00-05:00"),
	)

	cutoff := mustCutoff(t, "04:00")
	rows, err := wake.Run(fx.Root(), nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("Run = %d rows, want 4", len(rows))
	}

	want := []journal.SessionKey{alphaOld, alphaNew, betaOnly, unprojected}
	for i, w := range want {
		if rows[i].Item.Key != w {
			t.Errorf("rows[%d] = %s, want %s (project-grouped, most-recent project first, chronological within group)",
				i, rows[i].Item.Key.DirName(), w.DirName())
		}
	}
}

// TestRun_SinceWindowRestricts covers V31's since resolution: a session
// before the bound is excluded even though it would otherwise qualify.
func TestRun_SinceWindowRestricts(t *testing.T) {
	fx := journaltest.New(t)

	tooOld := journal.SessionKey{Harness: "claude", NativeID: "too-old"}
	inWindow := journal.SessionKey{Harness: "claude", NativeID: "in-window"}

	fx.Captured("2026-09-01", tooOld,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-01T09:00:00-05:00"),
	)
	fx.Captured("2026-09-12", inWindow,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
	)

	cutoff := mustCutoff(t, "04:00")
	since := journal.Day("2026-09-10")
	rows, err := wake.Run(fx.Root(), &since, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 1 || rows[0].Item.Key != inWindow {
		t.Errorf("Run(since=2026-09-10) = %+v, want only %s", rows, inWindow.DirName())
	}
}

// TestRun_TornEntryMD_ListsRowWithoutTitle mirrors sessions' own tolerant
// read posture (MODEL §7): a stale curated session whose entry.md fails
// to parse still lists, just without a title.
func TestRun_TornEntryMD_ListsRowWithoutTitle(t *testing.T) {
	fx := journaltest.New(t)
	torn := journal.SessionKey{Harness: "claude", NativeID: "torn-01"}

	if err := journal.WriteSession(fx.Root(), "2026-09-10", torn, journal.Session{
		Harness:      torn.Harness,
		SessionID:    torn.NativeID,
		Machine:      "framework",
		StartedAt:    mustParseTime(t, "2026-09-10T09:00:00-05:00"),
		LastActiveAt: mustParseTime(t, "2026-09-10T09:20:00-05:00"),
		CapturedAt:   mustParseTime(t, "2026-09-10T09:25:00-05:00"),
		Transcript:   journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "grown"},
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := journal.WriteCuration(fx.Root(), "2026-09-10", torn, journal.Curation{
		State:                journal.StateCurated,
		At:                   mustParseTime(t, "2026-09-10T10:00:00-05:00"),
		Machine:              "framework",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: 10, SHA256: "original"},
	}); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	if err := writeTornEntry(fx.Root(), "2026-09-10", torn); err != nil {
		t.Fatalf("writing torn entry.md: %v", err)
	}

	cutoff := mustCutoff(t, "04:00")
	rows, err := wake.Run(fx.Root(), nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v, want the torn entry.md tolerated rather than propagated", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Run = %d rows, want 1 (the torn stale-curated session)", len(rows))
	}
	if rows[0].Item.Key != torn {
		t.Fatalf("Run = %+v, want the torn session", rows)
	}
	if rows[0].Title != "" {
		t.Errorf("torn session's Title = %q, want empty", rows[0].Title)
	}
}

// TestRun_EmptyJournalIsEmptyNotError mirrors sessions' own empty-journal
// posture.
func TestRun_EmptyJournalIsEmptyNotError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	rows, err := wake.Run(fx.Root(), nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Run on an empty journal = %+v, want none", rows)
	}
}

// writeTornEntry writes entry.md bytes that fail entry.Parse (no
// frontmatter delimiter at all) directly through journal.WriteEntry —
// journaltest's own Curated/CuratedStale helpers always write a
// well-formed entry, so a torn one is authored by hand here.
func writeTornEntry(root, shard string, key journal.SessionKey) error {
	return journal.WriteEntry(root, shard, key, []byte("not an entry document at all"))
}
