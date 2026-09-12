package retro_test

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/verbs/retro"
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

func project(slug, clone, label, path string) journal.SessionProject {
	return journal.SessionProject{ID: "p-" + slug, Slug: slug, Clone: clone, Label: label, Path: path}
}

// writeTornEntry writes entry.md bytes that fail entry.Parse directly
// through journal.WriteEntry — journaltest's own Curated helper always
// writes a well-formed entry, so a torn one is authored by hand here
// (mirrors wake_test.go's/brief_test.go's own writeTornEntry).
func writeTornEntry(root, shard string, key journal.SessionKey) error {
	return journal.WriteEntry(root, shard, key, []byte("not an entry document at all"))
}

// TestRun_DefaultDay_SingleDayWindow confirms the default (no --since,
// windowStart == day) case scopes strictly to that one day: a session the
// day before is excluded even though it is only 24h earlier.
func TestRun_DefaultDay_SingleDayWindow(t *testing.T) {
	fx := journaltest.New(t)
	inDay := journal.SessionKey{Harness: "claude", NativeID: "in-day"}
	dayBefore := journal.SessionKey{Harness: "claude", NativeID: "day-before"}

	fx.Captured("2026-09-11", inDay,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
	).WithProject("2026-09-11", inDay, project("widget", "c1", "dev", "/dev"))
	fx.Captured("2026-09-10", dayBefore,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithProject("2026-09-10", dayBefore, project("widget", "c1", "dev", "/dev"))

	cutoff := mustCutoff(t, "04:00")
	day := journal.Day("2026-09-11")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.WindowStart != result.Day {
		t.Fatalf("WindowStart = %s, Day = %s, want equal (no widening)", result.WindowStart, result.Day)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups = %+v, want 1 project", result.Groups)
	}
	if len(result.Groups[0].Sessions) != 1 || result.Groups[0].Sessions[0].Item.Key != inDay {
		t.Fatalf("Sessions = %+v, want only %s", result.Groups[0].Sessions, inDay.DirName())
	}
}

// TestRun_ExplicitDay_ResolvesThatDayOnly confirms an explicit day
// argument (not the default) scopes to exactly that day too.
func TestRun_ExplicitDay_ResolvesThatDayOnly(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "s1"}
	fx.Captured("2026-09-05", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-05T09:00:00-05:00"),
	).WithProject("2026-09-05", key, project("widget", "c1", "dev", "/dev"))
	// Noise on a different day, must not appear.
	other := journal.SessionKey{Harness: "claude", NativeID: "other-day"}
	fx.Captured("2026-09-06", other,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-06T09:00:00-05:00"),
	).WithProject("2026-09-06", other, project("widget", "c1", "dev", "/dev"))

	cutoff := mustCutoff(t, "04:00")
	day := journal.Day("2026-09-05")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 || len(result.Groups[0].Sessions) != 1 || result.Groups[0].Sessions[0].Item.Key != key {
		t.Fatalf("Groups = %+v, want only %s on 2026-09-05", result.Groups, key.DirName())
	}
}

// TestRun_SinceWidensToWindow confirms --since's resolved windowStart
// (day.AddDays(-N), computed by command.go's resolveWindowStart — here
// passed directly) includes sessions across the whole [windowStart, day]
// range, not just day itself.
func TestRun_SinceWidensToWindow(t *testing.T) {
	fx := journaltest.New(t)
	tooOld := journal.SessionKey{Harness: "claude", NativeID: "too-old"}
	inWindowEarly := journal.SessionKey{Harness: "claude", NativeID: "in-window-early"}
	onDay := journal.SessionKey{Harness: "claude", NativeID: "on-day"}
	afterDay := journal.SessionKey{Harness: "claude", NativeID: "after-day"}

	fx.Captured("2026-09-07", tooOld,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-07T09:00:00-05:00"),
	).WithProject("2026-09-07", tooOld, project("widget", "c1", "dev", "/dev"))
	fx.Captured("2026-09-08", inWindowEarly,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-08T09:00:00-05:00"),
	).WithProject("2026-09-08", inWindowEarly, project("widget", "c1", "dev", "/dev"))
	fx.Captured("2026-09-10", onDay,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "c"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithProject("2026-09-10", onDay, project("widget", "c1", "dev", "/dev"))
	fx.Captured("2026-09-11", afterDay,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "d"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
	).WithProject("2026-09-11", afterDay, project("widget", "c1", "dev", "/dev"))

	cutoff := mustCutoff(t, "04:00")
	day := journal.Day("2026-09-10")
	windowStart := journal.Day("2026-09-08") // day - 2 days, as -2d would resolve
	result, err := retro.Run(fx.Root(), day, windowStart, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups = %+v, want 1 project", result.Groups)
	}
	got := map[journal.SessionKey]bool{}
	for _, r := range result.Groups[0].Sessions {
		got[r.Item.Key] = true
	}
	if !got[inWindowEarly] || !got[onDay] {
		t.Errorf("Sessions = %+v, want both %s and %s", result.Groups[0].Sessions, inWindowEarly.DirName(), onDay.DirName())
	}
	if got[tooOld] {
		t.Error("too-old session included, want excluded (before windowStart)")
	}
	if got[afterDay] {
		t.Error("after-day session included, want excluded (day is the window's fixed upper bound)")
	}
}

// TestRun_GroupsPerProject_SessionsEntriesBreadcrumbs seeds two projects
// with a curated session (state+title+entry body) and a project-scoped
// breadcrumb each, confirming V8's full per-project shape: sessions,
// entries, and breadcrumbs all land under the right group.
func TestRun_GroupsPerProject_SessionsEntriesBreadcrumbs(t *testing.T) {
	fx := journaltest.New(t)
	day := journal.Day("2026-09-11")

	widgetKey := journal.SessionKey{Harness: "claude", NativeID: "widget-01"}
	fx.Curated("2026-09-11", widgetKey,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"), mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		"framework", "widget's entry",
	).WithProject("2026-09-11", widgetKey, project("widget", "c1", "dev", "/dev"))

	acmeKey := journal.SessionKey{Harness: "claude", NativeID: "acme-01"}
	fx.Curated("2026-09-11", acmeKey,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-11T11:00:00-05:00"), mustParseTime(t, "2026-09-11T12:00:00-05:00"),
		"framework", "acme's entry",
	).WithProject("2026-09-11", acmeKey, project("acme", "c2", "dev", "/acme"))

	widgetSlug := "widget"
	acmeSlug := "acme"
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-11T08:00:00-05:00"), &widgetSlug, "widget crumb")
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-11T08:30:00-05:00"), &acmeSlug, "acme crumb")

	cutoff := mustCutoff(t, "04:00")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("Groups = %+v, want 2 (acme, widget — alphabetical)", result.Groups)
	}
	// Alphabetical: acme, then widget.
	if result.Groups[0].Slug != "acme" || result.Groups[1].Slug != "widget" {
		t.Fatalf("Groups slugs = [%s, %s], want [acme, widget]", result.Groups[0].Slug, result.Groups[1].Slug)
	}

	acme := result.Groups[0]
	if len(acme.Sessions) != 1 || acme.Sessions[0].Title != "acme's entry" {
		t.Errorf("acme.Sessions = %+v, want one session titled %q", acme.Sessions, "acme's entry")
	}
	if len(acme.Entries) != 1 || acme.Entries[0].Entry.Title != "acme's entry" {
		t.Errorf("acme.Entries = %+v, want one entry titled %q", acme.Entries, "acme's entry")
	}
	if len(acme.Breadcrumbs) != 1 || acme.Breadcrumbs[0].Text != "acme crumb" {
		t.Errorf("acme.Breadcrumbs = %+v, want one crumb %q", acme.Breadcrumbs, "acme crumb")
	}

	widget := result.Groups[1]
	if len(widget.Sessions) != 1 || widget.Sessions[0].Title != "widget's entry" {
		t.Errorf("widget.Sessions = %+v, want one session titled %q", widget.Sessions, "widget's entry")
	}
	if len(widget.Breadcrumbs) != 1 || widget.Breadcrumbs[0].Text != "widget crumb" {
		t.Errorf("widget.Breadcrumbs = %+v, want one crumb %q", widget.Breadcrumbs, "widget crumb")
	}
}

// TestRun_UnprojectedSessions_OwnBucket confirms a session with no frozen
// project still appears, in its own group.
func TestRun_UnprojectedSessions_OwnBucket(t *testing.T) {
	fx := journaltest.New(t)
	day := journal.Day("2026-09-11")
	key := journal.SessionKey{Harness: "claude", NativeID: "no-project"}
	fx.Captured("2026-09-11", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
	)

	cutoff := mustCutoff(t, "04:00")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 || len(result.Groups[0].Sessions) != 1 || result.Groups[0].Sessions[0].Item.Key != key {
		t.Fatalf("Groups = %+v, want one unprojected group with %s", result.Groups, key.DirName())
	}
}

// TestRun_GlobalBreadcrumbs_ReportedSeparately confirms a slug-null
// breadcrumb lands in GlobalBreadcrumbs, not in any project group.
func TestRun_GlobalBreadcrumbs_ReportedSeparately(t *testing.T) {
	fx := journaltest.New(t)
	day := journal.Day("2026-09-11")
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-11T08:00:00-05:00"), nil, "a global note")

	cutoff := mustCutoff(t, "04:00")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 0 {
		t.Fatalf("Groups = %+v, want none (only a global crumb exists)", result.Groups)
	}
	if len(result.GlobalBreadcrumbs) != 1 || result.GlobalBreadcrumbs[0].Text != "a global note" {
		t.Fatalf("GlobalBreadcrumbs = %+v, want one crumb %q", result.GlobalBreadcrumbs, "a global note")
	}
}

// TestRun_ProjectNamedOnlyByBreadcrumb_StillGetsAGroup confirms a project
// with a breadcrumb but no session in the window still gets its own
// group (V8 lists breadcrumbs as one more per-project fact, not gated on
// a session existing first).
func TestRun_ProjectNamedOnlyByBreadcrumb_StillGetsAGroup(t *testing.T) {
	fx := journaltest.New(t)
	day := journal.Day("2026-09-11")
	slug := "widget"
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-11T08:00:00-05:00"), &slug, "widget crumb, no session")

	cutoff := mustCutoff(t, "04:00")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 || result.Groups[0].Slug != "widget" {
		t.Fatalf("Groups = %+v, want one group %q", result.Groups, "widget")
	}
	if len(result.Groups[0].Sessions) != 0 {
		t.Errorf("Sessions = %+v, want none", result.Groups[0].Sessions)
	}
	if len(result.Groups[0].Breadcrumbs) != 1 {
		t.Errorf("Breadcrumbs = %+v, want one", result.Groups[0].Breadcrumbs)
	}
}

// TestRun_TornEntry_SessionListedWithoutTitle_EntryDropped confirms the
// dual posture: a stale-worthy read failure still lists the session (per
// wake's tolerant posture) but contributes no EntryRow (per brief's
// gathered-content posture) — the same session, two different facts.
func TestRun_TornEntry_SessionListedWithoutTitle_EntryDropped(t *testing.T) {
	fx := journaltest.New(t)
	day := journal.Day("2026-09-11")
	torn := journal.SessionKey{Harness: "claude", NativeID: "torn-01"}
	if err := journal.WriteSession(fx.Root(), "2026-09-11", torn, journal.Session{
		Harness: torn.Harness, SessionID: torn.NativeID, Machine: "framework",
		Project:      &journal.SessionProject{ID: "p", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"},
		StartedAt:    mustParseTime(t, "2026-09-11T09:00:00-05:00"),
		LastActiveAt: mustParseTime(t, "2026-09-11T09:20:00-05:00"),
		CapturedAt:   mustParseTime(t, "2026-09-11T09:25:00-05:00"),
		Transcript:   journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "grown"},
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := journal.WriteCuration(fx.Root(), "2026-09-11", torn, journal.Curation{
		State: journal.StateCurated, At: mustParseTime(t, "2026-09-11T10:00:00-05:00"), Machine: "framework",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: 20, SHA256: "grown"},
	}); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	if err := writeTornEntry(fx.Root(), "2026-09-11", torn); err != nil {
		t.Fatalf("writeTornEntry: %v", err)
	}

	cutoff := mustCutoff(t, "04:00")
	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v, want the torn entry.md tolerated rather than propagated", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups = %+v, want 1", result.Groups)
	}
	g := result.Groups[0]
	if len(g.Sessions) != 1 || g.Sessions[0].Item.Key != torn || g.Sessions[0].Title != "" {
		t.Errorf("Sessions = %+v, want the torn session listed with an empty title", g.Sessions)
	}
	if len(g.Entries) != 0 {
		t.Errorf("Entries = %+v, want none (torn entry.md is dropped, not gathered)", g.Entries)
	}
}

// TestRun_EmptyDay_NoSessionsNoBreadcrumbs confirms an empty window is
// empty, not an error.
func TestRun_EmptyDay_NoSessionsNoBreadcrumbs(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")
	day := journal.Day("2026-09-20")

	result, err := retro.Run(fx.Root(), day, day, cutoff)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 0 {
		t.Errorf("Groups = %+v, want none", result.Groups)
	}
	if len(result.GlobalBreadcrumbs) != 0 {
		t.Errorf("GlobalBreadcrumbs = %+v, want none", result.GlobalBreadcrumbs)
	}
}
