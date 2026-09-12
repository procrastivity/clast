// Package sessions implements `clast plumbing sessions` (SURFACE V17): the
// one query verb over the journal tree. Filters compose over a single
// journal.Walk (internal/query, M4 — no side index); results list
// newest-first by started_at.
package sessions

import (
	"sort"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
)

// Row is one session as Run lists it: the raw WalkItem, the M8 day bucket
// derived under the current cutoff (never the shard name — MODEL M8), and
// the curated entry's title, when one exists.
type Row struct {
	Item  journal.WalkItem
	Day   journal.Day
	Title string
}

// Run lists sessions per V17: one journal.Walk, filter applied through
// internal/query (the shared layer every query verb composes over),
// newest-first by started_at. Walk's own diagnostics (a malformed
// session directory) are tolerated here exactly as Walk tolerates them
// itself — `doctor` (V28) is the verb that reports journal health, not
// this listing. A curated session whose entry.md fails to read (a torn
// document — present but malformed) gets the same tolerant treatment:
// the row still lists, just without a title, rather than an entry.Read
// error killing the whole listing.
func Run(root string, filter query.Filter, cutoff journal.Cutoff) ([]Row, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return nil, err
	}
	matched := query.Apply(items, filter, cutoff)

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].Session.StartedAt.After(matched[j].Session.StartedAt)
	})

	rows := make([]Row, len(matched))
	for i, it := range matched {
		row := Row{Item: it, Day: cutoff.DayOf(it.Session.StartedAt)}
		if it.State() == journal.StateCurated {
			// A read error (a torn entry.md) is tolerated, not
			// propagated: drop the title, keep the row.
			if e, ok, err := entry.Read(journal.EntryPath(root, it.Shard, it.Key)); err == nil && ok {
				row.Title = e.Title
			}
		}
		rows[i] = row
	}
	return rows, nil
}
