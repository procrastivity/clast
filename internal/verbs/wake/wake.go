// Package wake implements `clast plumbing wake` (SURFACE V8/V20): the wake
// shape's deterministic working set. It is a pure query — one journal.Walk,
// no writes, no LLM calls — composing over internal/query the same way
// every other query verb does (M4: no side index).
package wake

import (
	"sort"
	"time"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
)

// unprojectedKey groups every session with no frozen project (MODEL §4: a
// captured session need not belong to one) into one bucket, keyed
// distinctly from any real slug — mirrors stats' own convention (SURFACE
// V21) rather than inventing a second one.
const unprojectedKey = "-"

// Row is one session in the wake shape's working set: the raw WalkItem,
// its M8 day bucket, and the curated entry's title for a stale
// re-curation candidate (a fresh `captured` session carries no entry, so
// Title is "" there) — the same three facts sessions.Row carries (V17),
// reused rather than forked.
type Row struct {
	Item  journal.WalkItem
	Day   journal.Day
	Title string
}

// Run assembles the wake shape's working set (SURFACE V8): every session
// in state `captured`, plus every `curated` session that is stale (MODEL
// M7 — its transcript grew since curation), restricted to since's recent
// window (nil = no restriction, the "all" case) — one journal.Walk. A
// curated-but-fresh or dismissed session never appears; wake offers
// re-curation, it does not revoke an entry (MODEL §2).
//
// Order is a plumbing fact this verb owns (SURFACE V7's disposition
// table, "skill-only per-project ordering"): project-grouped, the
// most-recently-active project first, chronological (oldest first) within
// each group — the order the old wake skill wanted of its own porcelain
// and never got.
func Run(root string, since *journal.Day, cutoff journal.Cutoff) ([]Row, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return nil, err
	}
	windowed := query.Apply(items, query.Filter{Since: since}, cutoff)

	var selected []journal.WalkItem
	for _, it := range windowed {
		if inWorkingSet(it) {
			selected = append(selected, it)
		}
	}

	return buildRows(root, selected, cutoff), nil
}

// inWorkingSet is V8's own working-set membership test: captured, or
// curated-and-stale (M7). Everything else (a fresh curated session, a
// dismissed one) is excluded.
func inWorkingSet(it journal.WalkItem) bool {
	switch it.State() {
	case journal.StateCaptured:
		return true
	case journal.StateCurated:
		return it.Stale()
	default:
		return false
	}
}

// group is one project's slice of the working set, mid-assembly.
type group struct {
	key        string
	items      []journal.WalkItem
	mostRecent time.Time
}

// buildRows groups selected by project (groupByProject), then reads each
// stale curated row's entry title — tolerant of a torn entry.md, exactly
// sessions.Run's own read posture (MODEL §7: `doctor` reports journal
// health, not a listing; a failed read drops the title, not the row).
func buildRows(root string, selected []journal.WalkItem, cutoff journal.Cutoff) []Row {
	groups := groupByProject(selected)

	rows := make([]Row, 0, len(selected))
	for _, g := range groups {
		for _, it := range g.items {
			row := Row{Item: it, Day: cutoff.DayOf(it.Session.StartedAt)}
			if it.State() == journal.StateCurated {
				if e, ok, err := entry.Read(journal.EntryPath(root, it.Shard, it.Key)); err == nil && ok {
					row.Title = e.Title
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// groupByProject buckets items by project slug (unprojectedKey for a
// projectless session), sorts each bucket chronologically (oldest
// first — V8's "chronological within each group"), and returns the
// buckets ordered most-recently-active project first: the group whose
// latest session started latest sorts first, ties broken by key for
// determinism. bucket order of first appearance is otherwise irrelevant,
// since mostRecent is what orders the result.
func groupByProject(items []journal.WalkItem) []group {
	byKey := map[string]*group{}
	var order []string
	for _, it := range items {
		key := projectKey(it)
		g, ok := byKey[key]
		if !ok {
			g = &group{key: key}
			byKey[key] = g
			order = append(order, key)
		}
		g.items = append(g.items, it)
	}

	groups := make([]group, 0, len(order))
	for _, key := range order {
		g := byKey[key]
		sort.SliceStable(g.items, func(i, j int) bool {
			return g.items[i].Session.StartedAt.Before(g.items[j].Session.StartedAt)
		})
		g.mostRecent = g.items[len(g.items)-1].Session.StartedAt
		groups = append(groups, *g)
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if !groups[i].mostRecent.Equal(groups[j].mostRecent) {
			return groups[i].mostRecent.After(groups[j].mostRecent)
		}
		return groups[i].key < groups[j].key
	})
	return groups
}

// projectKey is a session's grouping key: its frozen project's slug, or
// unprojectedKey when it has none.
func projectKey(it journal.WalkItem) string {
	if it.Session.Project == nil {
		return unprojectedKey
	}
	return it.Session.Project.Slug
}
