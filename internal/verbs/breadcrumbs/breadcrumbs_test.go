package breadcrumbs_test

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/verbs/breadcrumbs"
)

func mustCutoff(t *testing.T, s string) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

func buildFixture(t *testing.T) string {
	t.Helper()
	fx := journaltest.New(t)
	slug := "clast"
	other := "other"

	fx.Breadcrumb("framework", time.Date(2026, 9, 11, 10, 22, 0, 0, time.Local), &slug, "check migration before deploy")
	fx.Breadcrumb("laptop", time.Date(2026, 9, 11, 9, 0, 0, 0, time.Local), nil, "bump the cache version")
	fx.Breadcrumb("laptop", time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local), &other, "unrelated project note")
	// A different day's crumb must never leak into 2026-09-11's bucket.
	fx.Breadcrumb("framework", time.Date(2026, 9, 12, 9, 0, 0, 0, time.Local), &slug, "next day")

	return fx.Root()
}

func TestRun_NoFlagsListsEveryCrumbInBucket(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(root, journal.Day("2026-09-11"), cutoff, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("Run(no flags) = %d entries, want 3", len(entries))
	}
}

func TestRun_ProjectScopesToThatSlug(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(root, journal.Day("2026-09-11"), cutoff, "clast", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "check migration before deploy" {
		t.Fatalf("Run(project=clast) = %+v, want exactly the clast-scoped crumb", entries)
	}
}

func TestRun_GlobalScopesToSlugNullOnly(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(root, journal.Day("2026-09-11"), cutoff, "", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "bump the cache version" {
		t.Fatalf("Run(global) = %+v, want exactly the global crumb", entries)
	}
}

func TestRun_NeitherFlagNeverListsAnotherDay(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(root, journal.Day("2026-09-11"), cutoff, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, e := range entries {
		if e.Text == "next day" {
			t.Errorf("Run leaked a 2026-09-12 crumb into the 2026-09-11 bucket: %+v", e)
		}
	}
}

func TestRun_ResultsSortedChronologically(t *testing.T) {
	root := buildFixture(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(root, journal.Day("2026-09-11"), cutoff, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].At.Before(entries[i-1].At) {
			t.Fatalf("entries not sorted chronologically: %+v", entries)
		}
	}
}

func TestRun_EmptyBucketIsEmptyNotError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	entries, err := breadcrumbs.Run(fx.Root(), journal.Day("2026-01-01"), cutoff, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Run on an empty journal = %+v, want none", entries)
	}
}
