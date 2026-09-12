package stats_test

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/verbs/stats"
)

func mustCutoff(t *testing.T, s string) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

func buildFixture(t *testing.T) string {
	t.Helper()
	fx := journaltest.New(t)

	a := journal.SessionKey{Harness: "claude", NativeID: "a"}
	b := journal.SessionKey{Harness: "claude", NativeID: "b"}
	c := journal.SessionKey{Harness: "codex", NativeID: "c"}

	fx.Captured("2026-09-10", a,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)
	fx.Curated("2026-09-11", b,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "b"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
		mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		"framework", "title",
	).WithProject("2026-09-11", b, journal.SessionProject{ID: "01P", Slug: "clast", Clone: "01C", Label: "dev", Path: "/x"})
	fx.Dismissed("2026-09-12", c,
		journal.TranscriptFingerprint{Format: "codex-jsonl", Lines: 1, SHA256: "c"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
		mustParseTime(t, "2026-09-12T09:00:05-05:00"),
		"framework", "auto:no-op",
	)

	return fx.Root()
}

func TestRun_CountsAllFourAxes(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	st, err := stats.Run(root, nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Total != 3 {
		t.Fatalf("Total = %d, want 3", st.Total)
	}
	if st.ByState["captured"] != 1 || st.ByState["curated"] != 1 || st.ByState["dismissed"] != 1 {
		t.Errorf("ByState = %+v, want one of each", st.ByState)
	}
	if st.ByHarness["claude"] != 2 || st.ByHarness["codex"] != 1 {
		t.Errorf("ByHarness = %+v, want claude=2 codex=1", st.ByHarness)
	}
	if st.ByProject["clast"] != 1 || st.ByProject["-"] != 2 {
		t.Errorf("ByProject = %+v, want clast=1 -=2", st.ByProject)
	}
	if st.ByDay["2026-09-10"] != 1 || st.ByDay["2026-09-11"] != 1 || st.ByDay["2026-09-12"] != 1 {
		t.Errorf("ByDay = %+v, want one per day", st.ByDay)
	}
}

func TestRun_SinceRestrictsTheWalk(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")
	since := journal.Day("2026-09-11")

	st, err := stats.Run(root, &since, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Total != 2 {
		t.Fatalf("Total = %d, want 2 (excludes the 09-10 session)", st.Total)
	}
}

func TestRun_NilSinceIsAll(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	st, err := stats.Run(root, nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Total != 3 {
		t.Errorf("Total = %d, want 3 (nil since = all)", st.Total)
	}
}

func TestRun_EmptyJournalIsEmptyNotError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	st, err := stats.Run(fx.Root(), nil, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Total != 0 {
		t.Errorf("Total = %d, want 0", st.Total)
	}
}
