package analyzeverb

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/retrocache"
	retroplumbing "github.com/procrastivity/clast/internal/verbs/retro"
)

func strp(s string) *string { return &s }

func walkItem(id string, state journal.CurationState, started time.Time, reason *string) journal.WalkItem {
	it := journal.WalkItem{
		Key:     journal.SessionKey{Harness: "claude", NativeID: id},
		Session: journal.Session{SessionID: id, Branch: "main", StartedAt: started, LastActiveAt: started.Add(30 * time.Minute)},
	}
	it.Session.Counts.User, it.Session.Counts.Assistant = 2, 5
	it.Session.Transcript.Lines = 40
	if state != journal.StateCaptured {
		it.CurationPresent = true
		it.Curation = journal.Curation{State: state, Reason: reason}
	}
	return it
}

func withLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func TestBuildPage_MapsResult(t *testing.T) {
	withLocal(t, time.FixedZone("TST", -5*3600))
	cutoff, err := journal.ParseCutoff("00:00")
	if err != nil {
		t.Fatal(err)
	}

	// 02:00 UTC on the 22nd is 21:00 on the 21st locally.
	early := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	later := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	curated := walkItem("aaaaaaaa", journal.StateCurated, early, nil)
	missed := walkItem("bbbbbbbb", journal.StateCurated, later, nil)
	dismissed := walkItem("cccccccc", journal.StateDismissed, later, strp("auto:no-op"))
	captured := walkItem("dddddddd", journal.StateCaptured, later, nil)
	proj := "clast"

	result := retroplumbing.Result{
		WindowStart: "2026-09-21",
		Day:         "2026-09-22",
		Groups: []retroplumbing.ProjectGroup{
			{
				Slug: "-",
				Sessions: []retroplumbing.SessionRow{
					{Item: dismissed, Day: "2026-09-22"},
					{Item: captured, Day: "2026-09-22"},
				},
			},
			{
				Slug: "clast",
				Sessions: []retroplumbing.SessionRow{
					{Item: curated, Day: "2026-09-21", Title: "Cached one"},
					{Item: missed, Day: "2026-09-22", Title: "Uncached one"},
				},
				Entries: []retroplumbing.EntryRow{
					{Item: curated, Day: "2026-09-21", Entry: entry.Entry{Frontmatter: entry.Frontmatter{Title: "Cached one", Tags: []string{"x"}}, Body: "body one"}},
					{Item: missed, Day: "2026-09-22", Entry: entry.Entry{Frontmatter: entry.Frontmatter{Title: "Uncached one"}, Body: "body two"}},
				},
				Breadcrumbs: []journal.BreadcrumbEntry{
					{Breadcrumb: journal.Breadcrumb{At: later, Slug: &proj, Text: "project crumb"}, Machine: "BOX"},
				},
			},
		},
		GlobalBreadcrumbs: []journal.BreadcrumbEntry{
			{Breadcrumb: journal.Breadcrumb{At: later, Text: "global crumb"}},
		},
	}

	dir := t.TempDir()
	e := retrocache.Entry{Project: "clast", Day: "2026-09-21", StartedAt: early, SessionID: "aaaaaaaa", Body: "body one"}
	key, err := retrocache.EntryKey(e, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if err := retrocache.Put(dir, key, "the summary"); err != nil {
		t.Fatal(err)
	}

	page := buildPage(result, "/j", cutoff, cachedSummary(dir, "m1"))

	if len(page.Days) != 2 || page.Days[0].Day != "2026-09-21" || page.Days[1].Day != "2026-09-22" {
		t.Fatalf("days = %+v", page.Days)
	}

	first := page.Days[0].Projects
	if len(first) != 1 || first[0].Name != "clast" || len(first[0].Sessions) != 1 {
		t.Fatalf("day 1 projects = %+v", first)
	}
	hit := first[0].Sessions[0]
	if !hit.HasSummary || hit.Summary != "the summary" {
		t.Errorf("cache hit: summary = %q, %v", hit.Summary, hit.HasSummary)
	}
	if !hit.HasEntry || hit.Entry != "body one" || hit.Title != "Cached one" || len(hit.Tags) != 1 {
		t.Errorf("hit entry facts = %+v", hit)
	}
	if hit.StartedAt.Location().String() != "TST" || hit.Started() != "21:00 TST" {
		t.Errorf("StartedAt not local: %s / %s", hit.StartedAt, hit.Started())
	}
	if hit.UserMsgs != 2 || hit.AssistantMsgs != 5 || hit.TranscriptLines != 40 || hit.Branch != "main" {
		t.Errorf("counts = %+v", hit)
	}

	second := page.Days[1].Projects
	if len(second) != 2 || second[0].Name != "(no project)" || second[1].Name != "clast" {
		t.Fatalf("day 2 projects = %+v", second)
	}
	miss := second[1].Sessions[0]
	if !miss.HasEntry || miss.HasSummary || miss.Summary != "" {
		t.Errorf("cache miss: %+v", miss)
	}
	gone, plain := second[0].Sessions[0], second[0].Sessions[1]
	if gone.State != journal.StateDismissed || gone.Reason != "auto:no-op" || gone.HasEntry {
		t.Errorf("dismissed = %+v", gone)
	}
	if plain.State != journal.StateCaptured || plain.Reason != "" {
		t.Errorf("captured = %+v", plain)
	}

	// A different model keys differently: the same cache misses.
	if other := buildPage(result, "/j", cutoff, cachedSummary(dir, "m2")); other.Days[0].Projects[0].Sessions[0].HasSummary {
		t.Error("summary served under a different model")
	}

	crumbs := page.Days[1].Breadcrumbs
	if len(crumbs) != 2 || crumbs[0].Slug != "" || crumbs[1].Slug != "clast" {
		t.Fatalf("crumb groups = %+v", crumbs)
	}
	if crumbs[1].Crumbs[0].Machine != "BOX" || crumbs[0].Crumbs[0].Time() != "10:00" {
		t.Errorf("crumbs = %+v", crumbs)
	}
	if len(page.Days[0].Breadcrumbs) != 0 {
		t.Errorf("day 1 crumbs = %+v", page.Days[0].Breadcrumbs)
	}
}

func TestBuildPage_BreadcrumbOnlyDay(t *testing.T) {
	cutoff, _ := journal.ParseCutoff("00:00")
	proj := "wip"
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	result := retroplumbing.Result{
		WindowStart: "2026-09-20", Day: "2026-09-20",
		Groups: []retroplumbing.ProjectGroup{{Slug: "wip", Breadcrumbs: []journal.BreadcrumbEntry{
			{Breadcrumb: journal.Breadcrumb{At: at, Slug: &proj, Text: "solo"}},
		}}},
	}
	page := buildPage(result, "/j", cutoff, func(retrocache.Entry) (string, bool) { return "", false })
	if len(page.Days) != 1 || len(page.Days[0].Projects) != 0 || page.Days[0].CrumbCount() != 1 {
		t.Fatalf("page = %+v", page)
	}
}
