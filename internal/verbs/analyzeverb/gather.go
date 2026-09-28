package analyzeverb

import (
	"fmt"
	"sort"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/retrocache"
	retroplumbing "github.com/procrastivity/clast/internal/verbs/retro"
)

// unprojectedSlug is plumbing retro's no-project grouping key ("-"),
// repeated here as retroverb does (no verb package imports another
// verb's unexported surface); unprojectedLabel is how it reads on the
// page, and in the retro-summary prompt retroverb renders.
const (
	unprojectedSlug  = "-"
	unprojectedLabel = "(no project)"
)

// Gather builds the Page for the window ending at dayArg and reaching
// back by since (retroplumbing.ResolveWindowStart's grammar; "" is the
// single day). It reads config, journal and retro cache afresh on every
// call, so a server can call it once per request. It never calls the LLM:
// a session whose summary is not already cached has an empty Summary.
func Gather(dayArg, since string) (Page, error) {
	cfg, err := config.Load()
	if err != nil {
		return Page{}, clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
	}
	root, err := journal.Root(cfg)
	if err != nil {
		return Page{}, clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
	}
	cutoff, err := journal.ConfiguredCutoff(cfg)
	if err != nil {
		return Page{}, clasterr.New("validation.config", fmt.Sprintf("resolving day cutoff: %v", err))
	}
	day, err := journal.ParseDay(dayArg, cutoff)
	if err != nil {
		return Page{}, err
	}
	windowStart, err := retroplumbing.ResolveWindowStart(day, since)
	if err != nil {
		return Page{}, err
	}
	model, err := llm.ConfiguredModel(cfg)
	if err != nil {
		return Page{}, clasterr.New("validation.config", fmt.Sprintf("resolving llm model: %v", err))
	}
	cacheDir, err := retrocache.Dir()
	if err != nil {
		return Page{}, clasterr.New("validation.config", fmt.Sprintf("resolving retro cache directory: %v", err))
	}

	result, err := retroplumbing.Run(root, day, windowStart, cutoff)
	if err != nil {
		return Page{}, err
	}
	return buildPage(result, cutoff, cachedSummary(cacheDir, model)), nil
}

// cachedSummary looks an entry's summary up in the retro cache under
// model, keyed exactly as retro writes it (retrocache.EntryKey). Any
// failure to key or read is a miss.
func cachedSummary(cacheDir, model string) func(retrocache.Entry) (string, bool) {
	return func(e retrocache.Entry) (string, bool) {
		key, err := retrocache.EntryKey(e, model)
		if err != nil {
			return "", false
		}
		return retrocache.Get(cacheDir, key)
	}
}

// buildPage maps a retro Result onto the Page model. summary looks up
// the cached retro summary for one entry. Times are converted to local
// time here: the template formats in the zone a time carries.
func buildPage(result retroplumbing.Result, cutoff journal.Cutoff, summary func(retrocache.Entry) (string, bool)) Page {
	page := Page{WindowStart: result.WindowStart, Day: result.Day}

	type dayBuild struct {
		projects []Project
		crumbs   map[string][]Crumb // by slug, "" for global
	}
	days := map[journal.Day]*dayBuild{}
	ensure := func(d journal.Day) *dayBuild {
		b, ok := days[d]
		if !ok {
			b = &dayBuild{crumbs: map[string][]Crumb{}}
			days[d] = b
		}
		return b
	}
	addCrumb := func(c journal.BreadcrumbEntry) {
		slug := ""
		if c.Slug != nil {
			slug = *c.Slug
		}
		b := ensure(cutoff.DayOf(c.At))
		b.crumbs[slug] = append(b.crumbs[slug], Crumb{At: c.At.Local(), Text: c.Text, Machine: c.Machine})
	}

	for _, g := range result.Groups {
		name := g.Slug
		if g.Slug == unprojectedSlug {
			name = unprojectedLabel
		}
		bodies := map[string]entryBody{}
		for _, e := range g.Entries {
			bodies[e.Item.Key.DirName()] = entryBody{title: e.Entry.Title, tags: e.Entry.Tags, body: e.Entry.Body}
		}

		// Sessions arrive oldest first, so each day's slice stays ordered.
		byDay := map[journal.Day]*Project{}
		for _, r := range g.Sessions {
			s := sessionFor(r, name)
			if eb, ok := bodies[r.Item.Key.DirName()]; ok {
				s.Title, s.Tags, s.Entry, s.HasEntry = eb.title, eb.tags, eb.body, true
				s.Summary, s.HasSummary = summary(retrocache.Entry{
					Project:   name,
					Day:       r.Day,
					StartedAt: r.Item.Session.StartedAt,
					SessionID: r.Item.Session.SessionID,
					Body:      eb.body,
				})
			}
			p, ok := byDay[r.Day]
			if !ok {
				b := ensure(r.Day)
				b.projects = append(b.projects, Project{Name: name})
				p = &b.projects[len(b.projects)-1]
				byDay[r.Day] = p
			}
			p.Sessions = append(p.Sessions, s)
		}
		for _, c := range g.Breadcrumbs {
			addCrumb(c)
		}
	}
	for _, c := range result.GlobalBreadcrumbs {
		addCrumb(c)
	}

	order := make([]journal.Day, 0, len(days))
	for d := range days {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, d := range order {
		b := days[d]
		slugs := make([]string, 0, len(b.crumbs))
		for slug := range b.crumbs {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs) // the global group ("") sorts first
		groups := make([]CrumbGroup, len(slugs))
		for i, slug := range slugs {
			groups[i] = CrumbGroup{Slug: slug, Crumbs: b.crumbs[slug]}
		}
		page.Days = append(page.Days, Day{Day: d, Projects: b.projects, Breadcrumbs: groups})
	}
	return page
}

// entryBody is the entry.md facts a session row is joined with.
type entryBody struct {
	title string
	tags  []string
	body  string
}

// sessionFor maps one retro session row onto a Session, before its entry
// and summary are attached.
func sessionFor(r retroplumbing.SessionRow, project string) Session {
	sess := r.Item.Session
	s := Session{
		ID:              sess.SessionID,
		Day:             r.Day,
		Project:         project,
		Branch:          sess.Branch,
		State:           r.Item.State(),
		StartedAt:       sess.StartedAt.Local(),
		LastActiveAt:    sess.LastActiveAt.Local(),
		UserMsgs:        sess.Counts.User,
		AssistantMsgs:   sess.Counts.Assistant,
		TranscriptLines: sess.Transcript.Lines,
		Title:           r.Title,
	}
	if s.State == journal.StateDismissed && r.Item.Curation.Reason != nil {
		s.Reason = *r.Item.Curation.Reason
	}
	return s
}
