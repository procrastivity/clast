package briefverb

import (
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/verbs/brief"
)

// TestRenderEntries_SingleWorkspace_NoHeader pins brief-system.md/brief-
// user.md's own stated convention: "a single-workspace project has no
// headers" — exactly one group renders with no "## Workspace:" line at
// all, just the entry itself.
func TestRenderEntries_SingleWorkspace_NoHeader(t *testing.T) {
	groups := []brief.Group{
		{
			Workspace: "dev",
			Entries: []brief.EntryRow{
				{Day: journal.Day("2026-09-11"), Entry: entry.Entry{
					Frontmatter: entry.Frontmatter{Title: "one entry", Tags: []string{"a"}},
					Body:        "body text",
				}},
			},
		},
	}
	got := renderEntries(groups)
	if strings.Contains(got, "## Workspace:") {
		t.Errorf("renderEntries(single group) = %q, want no workspace header", got)
	}
	if !strings.Contains(got, "one entry") || !strings.Contains(got, "body text") {
		t.Errorf("renderEntries(single group) = %q, want the entry's title and body", got)
	}
}

// TestRenderEntries_MultiWorkspace_Headers confirms two-or-more groups do
// get a "## Workspace: <label>" header per group, each naming its own
// workspace.
func TestRenderEntries_MultiWorkspace_Headers(t *testing.T) {
	groups := []brief.Group{
		{Workspace: "dev", Entries: []brief.EntryRow{
			{Day: journal.Day("2026-09-11"), Entry: entry.Entry{Frontmatter: entry.Frontmatter{Title: "dev entry"}, Body: "dev body"}},
		}},
		{Workspace: "other", Entries: []brief.EntryRow{
			{Day: journal.Day("2026-09-10"), Entry: entry.Entry{Frontmatter: entry.Frontmatter{Title: "other entry"}, Body: "other body"}},
		}},
	}
	got := renderEntries(groups)
	if !strings.Contains(got, "## Workspace: dev") {
		t.Errorf("renderEntries(multi group) = %q, want a header for dev", got)
	}
	if !strings.Contains(got, "## Workspace: other") {
		t.Errorf("renderEntries(multi group) = %q, want a header for other", got)
	}
}

// TestRenderEntries_Empty confirms an empty slice of groups renders a
// plain placeholder rather than an empty string (so the filled template
// never shows a bare, confusing gap).
func TestRenderEntries_Empty(t *testing.T) {
	if got := renderEntries(nil); got != "(none)" {
		t.Errorf("renderEntries(nil) = %q, want %q", got, "(none)")
	}
}

// TestRenderEntries_NoTags confirms an entry with no tags renders "none"
// rather than an empty Tags: line.
func TestRenderEntries_NoTags(t *testing.T) {
	groups := []brief.Group{
		{Workspace: "dev", Entries: []brief.EntryRow{
			{Day: journal.Day("2026-09-11"), Entry: entry.Entry{Frontmatter: entry.Frontmatter{Title: "t"}, Body: "b"}},
		}},
	}
	got := renderEntries(groups)
	if !strings.Contains(got, "Tags: none") {
		t.Errorf("renderEntries(no tags) = %q, want %q", got, "Tags: none")
	}
}

func TestRenderBreadcrumbs_EmptyAndPopulated(t *testing.T) {
	if got := renderBreadcrumbs(nil); got != "(none)" {
		t.Errorf("renderBreadcrumbs(nil) = %q, want %q", got, "(none)")
	}
	slug := "widget"
	crumbs := []journal.BreadcrumbEntry{
		{Breadcrumb: journal.Breadcrumb{Text: "first crumb", Slug: &slug}},
		{Breadcrumb: journal.Breadcrumb{Text: "second crumb", Slug: &slug}},
	}
	got := renderBreadcrumbs(crumbs)
	if !strings.Contains(got, "first crumb") || !strings.Contains(got, "second crumb") {
		t.Errorf("renderBreadcrumbs = %q, want both crumbs' text", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("renderBreadcrumbs = %q, want exactly one newline between two bullets", got)
	}
}

func TestRenderSessions_EmptyAndPopulated(t *testing.T) {
	if got := renderSessions(nil); got != "(none)" {
		t.Errorf("renderSessions(nil) = %q, want %q", got, "(none)")
	}
	rows := []brief.SessionRow{
		{Item: journal.WalkItem{Session: journal.Session{Branch: "go", Counts: journal.SessionCounts{User: 2, Assistant: 3}}}},
	}
	got := renderSessions(rows)
	if !strings.Contains(got, "go") || !strings.Contains(got, "5 messages") {
		t.Errorf("renderSessions = %q, want branch %q and %q", got, "go", "5 messages")
	}
}
