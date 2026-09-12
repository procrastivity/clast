package sessions_test

import (
	"os"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/query"
	"github.com/procrastivity/clast/internal/verbs/sessions"
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

func buildFixture(t *testing.T) (string, journal.SessionKey, journal.SessionKey, journal.SessionKey) {
	t.Helper()
	fx := journaltest.New(t)

	older := journal.SessionKey{Harness: "claude", NativeID: "older-01"}
	newer := journal.SessionKey{Harness: "claude", NativeID: "newer-01"}
	curated := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}

	fx.Captured("2026-09-10", older,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)
	fx.Captured("2026-09-11", newer,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
	)
	fx.Curated("2026-09-12", curated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "c"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
		mustParseTime(t, "2026-09-12T10:00:00-05:00"),
		"framework", "a curated session",
	)

	return fx.Root(), older, newer, curated
}

func TestRun_NewestFirst(t *testing.T) {
	root, older, newer, curated := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	rows, err := sessions.Run(root, query.Filter{}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("Run = %d rows, want 3", len(rows))
	}
	want := []journal.SessionKey{curated, newer, older}
	for i, w := range want {
		if rows[i].Item.Key != w {
			t.Errorf("rows[%d] = %s, want %s (newest-first by started_at)", i, rows[i].Item.Key.DirName(), w.DirName())
		}
	}
}

func TestRun_TitleOnlyForCurated(t *testing.T) {
	root, older, _, curated := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	rows, err := sessions.Run(root, query.Filter{}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var gotCurated, gotOlder *sessions.Row
	for i := range rows {
		switch rows[i].Item.Key {
		case curated:
			gotCurated = &rows[i]
		case older:
			gotOlder = &rows[i]
		}
	}
	if gotCurated == nil || gotCurated.Title != "a curated session" {
		t.Errorf("curated row title = %+v, want %q", gotCurated, "a curated session")
	}
	if gotOlder == nil || gotOlder.Title != "" {
		t.Errorf("captured row title = %+v, want empty", gotOlder)
	}
}

func TestRun_DayBucketDerivedNotFromShard(t *testing.T) {
	root, older, _, _ := buildFixture(t)
	// A 04:00 cutoff and a 09:00 local start time: the M8 bucket is the
	// same calendar day as the shard here, but Run must derive it from
	// StartedAt+Cutoff rather than merely echo the shard string — a
	// tighter cutoff proves the derivation is live.
	cutoff := mustCutoff(t, "23:00")

	rows, err := sessions.Run(root, query.Filter{}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, r := range rows {
		if r.Item.Key == older {
			// Under a 23:00 cutoff, a 09:00 start is before the cutoff and
			// so belongs to the PREVIOUS calendar day's bucket (M8).
			if r.Day != "2026-09-09" {
				t.Errorf("older's Day = %s, want 2026-09-09 under a 23:00 cutoff", r.Day)
			}
		}
	}
}

func TestRun_FilterAppliesBeforeSort(t *testing.T) {
	root, older, newer, curated := buildFixture(t)
	_ = curated
	cutoff := mustCutoff(t, "04:00")

	day := journal.Day("2026-09-10")
	rows, err := sessions.Run(root, query.Filter{Day: &day}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 1 || rows[0].Item.Key != older {
		t.Errorf("Run(day=2026-09-10) = %+v, want only %s", rows, older.DirName())
	}
	_ = newer
}

// TestRun_TornEntryMD_ListsRowWithoutTitle covers MODEL §7's tolerant-read
// posture (`doctor` reports journal health, not listings): a curated
// session whose entry.md fails to parse must not kill the whole listing —
// it still lists, just without a title, alongside otherwise-healthy
// sessions.
func TestRun_TornEntryMD_ListsRowWithoutTitle(t *testing.T) {
	root, _, _, curated := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	torn := journal.SessionKey{Harness: "claude", NativeID: "torn-01"}

	// Author a curated session directly against the shared root, then
	// clobber its entry.md with bytes that fail to parse (no frontmatter
	// delimiter at all) — a torn document.
	if err := journal.WriteSession(root, "2026-09-12", torn, journal.Session{
		Harness:      torn.Harness,
		SessionID:    torn.NativeID,
		Machine:      "framework",
		StartedAt:    mustParseTime(t, "2026-09-12T11:00:00-05:00"),
		LastActiveAt: mustParseTime(t, "2026-09-12T11:20:00-05:00"),
		CapturedAt:   mustParseTime(t, "2026-09-12T11:25:00-05:00"),
		Transcript:   journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "torn"},
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := journal.WriteCuration(root, "2026-09-12", torn, journal.Curation{
		State:   journal.StateCurated,
		At:      mustParseTime(t, "2026-09-12T11:30:00-05:00"),
		Machine: "framework",
	}); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	if err := os.WriteFile(journal.EntryPath(root, "2026-09-12", torn), []byte("not an entry document at all"), 0o644); err != nil {
		t.Fatalf("write torn entry.md: %v", err)
	}

	rows, err := sessions.Run(root, query.Filter{}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v, want the torn entry.md tolerated rather than propagated", err)
	}
	if len(rows) != 4 {
		t.Fatalf("Run = %d rows, want 4 (the 3 healthy sessions plus the torn one)", len(rows))
	}

	var gotTorn, gotHealthyCurated *sessions.Row
	for i := range rows {
		switch rows[i].Item.Key {
		case torn:
			gotTorn = &rows[i]
		case curated:
			gotHealthyCurated = &rows[i]
		}
	}
	if gotTorn == nil {
		t.Fatal("torn session missing from the listing entirely, want it present without a title")
	}
	if gotTorn.Title != "" {
		t.Errorf("torn session's Title = %q, want empty", gotTorn.Title)
	}
	if gotHealthyCurated == nil || gotHealthyCurated.Title != "a curated session" {
		t.Errorf("healthy curated row title = %+v, want %q unaffected by the torn sibling", gotHealthyCurated, "a curated session")
	}
}

func TestRun_EmptyJournalIsEmptyNotError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	rows, err := sessions.Run(fx.Root(), query.Filter{}, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Run on an empty journal = %+v, want none", rows)
	}
}
