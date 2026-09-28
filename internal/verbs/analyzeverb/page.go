// Package analyzeverb implements `clast analyze [<day>]`: an HTML
// explorer over a journal window — an overview table, a day→project
// sidebar, each curated session's entry draft beside its cached retro
// summary, and each day's breadcrumbs by project.
//
// This file holds the page: the model the template renders (Page and
// the types under it) and Render, which resolves the template asset
// (analyze/index.html, overridable under $XDG_CONFIG_HOME/clast/) and
// executes it through html/template. The model carries raw facts; the
// derived figures the template shows (word counts, labels, averages) are
// methods, so an override template can reach every fact the shipped one
// uses.
package analyzeverb

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
)

// pageAsset is the template's path under the asset tree (assets/).
const pageAsset = "analyze/index.html"

// Page is the whole explorer for one window.
type Page struct {
	WindowStart journal.Day
	Day         journal.Day
	// Days is every day in the window with a session or a breadcrumb,
	// oldest first.
	Days []Day
}

// Day is one day's sidebar block and breadcrumbs view.
type Day struct {
	Day journal.Day
	// Projects is every project with a session on this day, in display
	// order; the no-project bucket reads as "(no project)".
	Projects []Project
	// Breadcrumbs is this day's breadcrumbs grouped by project; the
	// global group (no project) has an empty Slug.
	Breadcrumbs []CrumbGroup
}

// Project is one project's sessions on one day.
type Project struct {
	Name     string
	Sessions []Session
}

// Session is one captured session and what the journal and the retro
// cache hold for it.
type Session struct {
	ID              string
	Day             journal.Day
	Project         string
	Branch          string
	State           journal.CurationState
	Reason          string
	StartedAt       time.Time
	LastActiveAt    time.Time
	UserMsgs        int
	AssistantMsgs   int
	TranscriptLines int
	Title           string
	Tags            []string
	// Entry is entry.md's body (markdown); HasEntry is false when the
	// session has no readable entry.md.
	Entry    string
	HasEntry bool
	// Summary is the cached retro summary (markdown); HasSummary is false
	// on a cache miss. analyze never calls the LLM to fill one.
	Summary    string
	HasSummary bool
}

// CrumbGroup is one project's breadcrumbs on one day.
type CrumbGroup struct {
	Slug   string
	Crumbs []Crumb
}

// Crumb is one breadcrumb line.
type Crumb struct {
	At      time.Time
	Text    string
	Machine string
}

// Stats is the overview's summary row.
type Stats struct {
	Curated         int
	Uncurated       int
	AvgEntryWords   int
	AvgSummaryWords int
	Unsummarized    int
	Breadcrumbs     int
}

// Sessions is every session in the window in overview order: by day,
// then by project, then as each project lists them.
func (p Page) Sessions() []Session {
	var out []Session
	for _, d := range p.Days {
		for _, pr := range d.Projects {
			out = append(out, pr.Sessions...)
		}
	}
	return out
}

// Stats totals the window. The averages are over curated sessions that
// have the text in question, so a missing summary does not pull the
// retro average down.
func (p Page) Stats() Stats {
	var st Stats
	var entryWords, entries, summaryWords, summaries int
	for _, s := range p.Sessions() {
		if !s.Curated() {
			st.Uncurated++
			continue
		}
		st.Curated++
		if s.HasEntry {
			entryWords += s.EntryWords()
			entries++
			if !s.HasSummary {
				st.Unsummarized++
			}
		}
		if s.HasSummary {
			summaryWords += s.SummaryWords()
			summaries++
		}
	}
	st.AvgEntryWords = roundedMean(entryWords, entries)
	st.AvgSummaryWords = roundedMean(summaryWords, summaries)
	for _, d := range p.Days {
		st.Breadcrumbs += d.CrumbCount()
	}
	return st
}

func roundedMean(sum, n int) int {
	if n == 0 {
		return 0
	}
	return (sum + n/2) / n
}

// Label is the day as the sidebar reads it: "Mon, Sep 21".
func (d Day) Label() string { return dayLabel(d.Day) }

// CrumbCount is the number of breadcrumbs on this day, across groups.
func (d Day) CrumbCount() int {
	n := 0
	for _, g := range d.Breadcrumbs {
		n += len(g.Crumbs)
	}
	return n
}

// Name is the group's heading: its project slug, or "(global)".
func (g CrumbGroup) Name() string {
	if g.Slug == "" {
		return "(global)"
	}
	return g.Slug
}

// Time is the breadcrumb's clock time, in the zone its At carries.
func (c Crumb) Time() string { return c.At.Format("15:04") }

// Curated reports whether the session is curated — the only state with
// an entry draft and a retro summary to compare.
func (s Session) Curated() bool { return s.State == journal.StateCurated }

// Short is the session id's first eight characters.
func (s Session) Short() string {
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

// Label is the session's sidebar line: its title, else its short id
// (with its state, when that is not curated).
func (s Session) Label() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.Curated():
		return s.Short()
	default:
		return s.Short() + " · " + string(s.State)
	}
}

// NavMeta is the sidebar's second line: word counts for a curated
// session, else the dismissal reason or the state.
func (s Session) NavMeta() string {
	if !s.Curated() {
		if s.Reason != "" {
			return s.Reason
		}
		return string(s.State)
	}
	if !s.HasEntry {
		return "no entry"
	}
	if !s.HasSummary {
		return fmt.Sprintf("%dw entry · not summarized", s.EntryWords())
	}
	return fmt.Sprintf("%dw entry · %dw retro", s.EntryWords(), s.SummaryWords())
}

// DayLabel is the session's day as the sidebar reads it.
func (s Session) DayLabel() string { return dayLabel(s.Day) }

// Started is the session's start time, in the zone its StartedAt
// carries: "14:03 CDT".
func (s Session) Started() string { return s.StartedAt.Format("15:04 MST") }

// Minutes is the session's span from start to last activity.
func (s Session) Minutes() int {
	return max(0, int(s.LastActiveAt.Sub(s.StartedAt)/time.Minute))
}

// EntryWords is the entry's word count. Word and character counts are
// over the raw markdown, markers included — the prototype viewer's
// method, which the summary-verbosity baselines were measured with.
func (s Session) EntryWords() int { return len(strings.Fields(s.Entry)) }

// EntryChars is the entry's length in bytes.
func (s Session) EntryChars() int { return len(s.Entry) }

// EntryHTML is the entry rendered from its markdown.
func (s Session) EntryHTML() template.HTML { return markdownHTML(s.Entry) }

// SummaryWords is the cached summary's word count (see EntryWords).
func (s Session) SummaryWords() int { return len(strings.Fields(s.Summary)) }

// SummaryChars is the cached summary's length in bytes.
func (s Session) SummaryChars() int { return len(s.Summary) }

// SummaryHTML is the cached summary rendered from its markdown.
func (s Session) SummaryHTML() template.HTML { return markdownHTML(s.Summary) }

func dayLabel(d journal.Day) string {
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil {
		return string(d)
	}
	return t.Format("Mon, Jan 2")
}

// Render writes p as the explorer page to w. The page is executed into
// memory first, so a template error never leaves a half-written page.
func Render(w io.Writer, p Page) error {
	res, err := asset.Resolve(pageAsset)
	if err != nil {
		return clasterr.New("analyze.page-unavailable",
			fmt.Sprintf("analyze: resolving the page template: %v", err))
	}
	where := res.Path
	if where == "" {
		where = "embedded " + pageAsset
	}
	tmpl, err := template.New(pageAsset).Parse(string(res.Bytes()))
	if err != nil {
		return clasterr.New("analyze.page-unavailable",
			fmt.Sprintf("analyze: parsing the page template (%s): %v", where, err))
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return clasterr.New("analyze.page-unavailable",
			fmt.Sprintf("analyze: rendering the page template (%s): %v", where, err))
	}
	_, err = w.Write(buf.Bytes())
	return err
}
