// Package retro implements `clast plumbing retro [<day>]` (SURFACE
// V8/V20): the retro shape's day→project document. It is a pure query —
// one journal.Walk plus one journal.ReadBreadcrumbsForDay per day in the
// window, no writes, no LLM calls (V7/V11: the fingerprinted summary
// cache is llm-verbs' own private state, never this verb's) — composing
// over internal/query the same way every other query verb does (M4: no
// side index).
package retro

import (
	"sort"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
)

// unprojectedKey groups every session with no frozen project (MODEL §4: a
// captured session need not belong to one) into one bucket, keyed
// distinctly from any real slug — wake's own convention (V8/V20),
// reused here rather than invented afresh.
const unprojectedKey = "-"

// SessionRow is one of the window's sessions: state and, when curated,
// its entry's title (v8: "the day's sessions with state and titles"). A
// curated session whose entry.md fails to parse still lists, just
// without a title — the same tolerant posture wake/sessions already take
// (MODEL §7), not brief's own drop-the-row posture for gathered content.
type SessionRow struct {
	Item  journal.WalkItem
	Day   journal.Day
	Title string
}

// EntryRow is one curated session's entry body within the window (V8:
// "entries — the entry body for sessions that have one"). A torn
// entry.md contributes no row at all here — brief's own posture for
// gathered content (internal/verbs/brief/brief.go's gatherEntries): there
// is nothing for the flow to read from an unreadable document, even
// though the same session still lists in Sessions above.
type EntryRow struct {
	Item  journal.WalkItem
	Day   journal.Day
	Entry entry.Entry
}

// ProjectGroup is one project's slice of the window: its sessions,
// entries, and breadcrumbs (V8's "per project" grouping). Slug is
// unprojectedKey for the no-project bucket; command.go renders that as
// "" (no project) on the way out, the same "" convention brief's own
// CurrentWorkspace already uses for "no value".
type ProjectGroup struct {
	Slug        string
	Sessions    []SessionRow
	Entries     []EntryRow
	Breadcrumbs []journal.BreadcrumbEntry
}

// Result is plumbing retro's day→project document (SURFACE V8/V11).
type Result struct {
	// Day is the resolved target day — the positional, default yesterday
	// (command.go resolves it through journal.ParseDay before Run ever
	// sees it, the same seam sessions/breadcrumbs already use).
	Day journal.Day
	// WindowStart is the window's lower bound, inclusive. Equal to Day
	// when --since was not given: V8's "--since <dur> widens to a
	// window" reads as widening AWAY from the single day, so the day
	// itself is always the window's upper bound and --since's absence is
	// a one-day window, not an unrestricted one (finding — see
	// command.go's flag doc for the full reasoning, including why "all"
	// is not accepted here the way sessions'/wake's/brief's own --since
	// is).
	WindowStart journal.Day
	// Groups is every project with activity in the window (a session, or
	// a breadcrumb naming it), ordered alphabetically by slug — the
	// no-project bucket sorts first ("-" < any letter/digit). Finding:
	// unlike wake's own "most-recently-active project first", retro has
	// no natural recency to sort a breadcrumb-only group against a
	// session-bearing one on equal footing, so alphabetical is the
	// simpler, still-deterministic call.
	Groups []ProjectGroup
	// GlobalBreadcrumbs is every slug-null breadcrumb in the window —
	// not attached to any project group. Finding: V8 groups sessions/
	// entries/breadcrumbs "per project", and a global crumb by
	// definition names none; brief's own precedent (gatherBreadcrumbs)
	// scopes breadcrumbs to one already-resolved project only, so there
	// is no existing per-project home for a global crumb to fall back
	// to here either.
	GlobalBreadcrumbs []journal.BreadcrumbEntry
}

// Run assembles retro's day→project document for the window
// [windowStart, day] (inclusive; windowStart == day when --since was not
// given) — one journal.Walk for sessions, one journal.ReadBreadcrumbsForDay
// per day in the window for breadcrumbs.
func Run(root string, day, windowStart journal.Day, cutoff journal.Cutoff) (Result, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}
	windowed := windowSessions(items, windowStart, day, cutoff)

	crumbs, err := gatherBreadcrumbs(root, windowStart, day, cutoff)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Day:               day,
		WindowStart:       windowStart,
		Groups:            buildGroups(root, windowed, crumbs, cutoff),
		GlobalBreadcrumbs: globalBreadcrumbs(crumbs),
	}, nil
}

// windowSessions restricts items to the window [windowStart, day]:
// query.Apply's own Since gives the lower bound (day bucket >=
// windowStart, the same shared machinery sessions/wake/brief already
// compose over), and the loop below adds the upper bound query.Filter has
// no field for — retro is anchored to a fixed day, never to "now", so
// there is no existing helper for this half.
func windowSessions(items []journal.WalkItem, windowStart, day journal.Day, cutoff journal.Cutoff) []journal.WalkItem {
	matched := query.Apply(items, query.Filter{Since: &windowStart}, cutoff)
	var out []journal.WalkItem
	for _, it := range matched {
		if cutoff.DayOf(it.Session.StartedAt) <= day {
			out = append(out, it)
		}
	}
	return out
}

// gatherBreadcrumbs reads every day in [windowStart, day] through
// journal.ReadBreadcrumbsForDay (M8's own day-bucketed reader, already
// tolerant of a torn line — diags are dropped here exactly as brief's own
// gatherBreadcrumbs already drops them) and merges the results in day
// order.
func gatherBreadcrumbs(root string, windowStart, day journal.Day, cutoff journal.Cutoff) ([]journal.BreadcrumbEntry, error) {
	var all []journal.BreadcrumbEntry
	d := windowStart
	for {
		entries, _, err := journal.ReadBreadcrumbsForDay(root, d, cutoff)
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
		if d == day {
			return all, nil
		}
		next, err := d.AddDays(1)
		if err != nil {
			return nil, err
		}
		d = next
	}
}

// projectKey is a session's grouping key: its frozen project's slug, or
// unprojectedKey when it has none — wake's own projectKey, repeated here
// (no verb package imports another verb's package in this codebase; see
// brief.go's workspaceKey for the same recurring duplication).
func projectKey(it journal.WalkItem) string {
	if it.Session.Project == nil {
		return unprojectedKey
	}
	return it.Session.Project.Slug
}

// buildGroups assembles every ProjectGroup: sessions and entries from
// windowed (sorted chronologically once, up front, so each group's own
// slice comes out already oldest-first — the same "sort once" approach
// wake's buildRows takes), and breadcrumbs from crumbs, keyed by
// *Breadcrumb.Slug. A project named only by a breadcrumb (no session in
// the window) still gets its own group — V8 lists breadcrumbs as one more
// per-project fact alongside sessions/entries, not something gated on a
// session existing first.
func buildGroups(root string, windowed []journal.WalkItem, crumbs []journal.BreadcrumbEntry, cutoff journal.Cutoff) []ProjectGroup {
	type building struct {
		sessions    []SessionRow
		entries     []EntryRow
		breadcrumbs []journal.BreadcrumbEntry
	}
	byKey := map[string]*building{}
	ensure := func(key string) *building {
		g, ok := byKey[key]
		if !ok {
			g = &building{}
			byKey[key] = g
		}
		return g
	}

	sortedItems := make([]journal.WalkItem, len(windowed))
	copy(sortedItems, windowed)
	sort.SliceStable(sortedItems, func(i, j int) bool {
		return sortedItems[i].Session.StartedAt.Before(sortedItems[j].Session.StartedAt)
	})
	for _, it := range sortedItems {
		g := ensure(projectKey(it))
		row := SessionRow{Item: it, Day: cutoff.DayOf(it.Session.StartedAt)}
		if it.State() == journal.StateCurated {
			if e, ok, err := entry.Read(journal.EntryPath(root, it.Shard, it.Key)); err == nil && ok {
				row.Title = e.Title
				g.entries = append(g.entries, EntryRow{Item: it, Day: row.Day, Entry: e})
			}
		}
		g.sessions = append(g.sessions, row)
	}

	sortedCrumbs := make([]journal.BreadcrumbEntry, len(crumbs))
	copy(sortedCrumbs, crumbs)
	sort.SliceStable(sortedCrumbs, func(i, j int) bool { return sortedCrumbs[i].At.Before(sortedCrumbs[j].At) })
	for _, c := range sortedCrumbs {
		if c.Slug == nil {
			continue // global; gathered separately by globalBreadcrumbs.
		}
		g := ensure(*c.Slug)
		g.breadcrumbs = append(g.breadcrumbs, c)
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	groups := make([]ProjectGroup, 0, len(keys))
	for _, k := range keys {
		g := byKey[k]
		groups = append(groups, ProjectGroup{Slug: k, Sessions: g.sessions, Entries: g.entries, Breadcrumbs: g.breadcrumbs})
	}
	return groups
}

// globalBreadcrumbs returns every slug-null breadcrumb in crumbs,
// chronological — brief's own gatherBreadcrumbs sort, repeated here.
func globalBreadcrumbs(crumbs []journal.BreadcrumbEntry) []journal.BreadcrumbEntry {
	var out []journal.BreadcrumbEntry
	for _, c := range crumbs {
		if c.Slug == nil {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
