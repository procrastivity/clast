// Package stats implements `clast plumbing stats` (SURFACE V21): a cheap
// tree walk, counting on four fixed axes — state, harness, project, and
// day range. One walk over the small JSON documents (M9); no cache, no
// side index (M4).
package stats

import (
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
)

// unprojectedKey is ByProject's key for a session with no frozen project
// (MODEL §4: a captured session need not belong to one) — deliberately
// not "" so it prints as a real row rather than a blank one.
const unprojectedKey = "-"

// Stats is V21's fixed count breakdown over the sessions Run walked.
type Stats struct {
	Total     int
	ByState   map[string]int
	ByHarness map[string]int
	ByProject map[string]int
	ByDay     map[string]int
}

// Run walks root's journal once and counts on the four fixed axes,
// restricted to sessions whose M8 day bucket is on or after since (nil =
// "all", V21's own default — deliberately not the `since` config key
// SURFACE V17/V31 use for `sessions`, since a health-at-a-glance count
// should cover everything by default).
func Run(root string, since *journal.Day, cutoff journal.Cutoff) (Stats, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return Stats{}, err
	}
	matched := query.Apply(items, query.Filter{Since: since}, cutoff)

	st := Stats{
		ByState:   map[string]int{},
		ByHarness: map[string]int{},
		ByProject: map[string]int{},
		ByDay:     map[string]int{},
	}
	for _, it := range matched {
		st.Total++
		st.ByState[string(it.State())]++
		st.ByHarness[it.Session.Harness]++
		st.ByProject[projectKey(it)]++
		st.ByDay[string(cutoff.DayOf(it.Session.StartedAt))]++
	}
	return st, nil
}

func projectKey(item journal.WalkItem) string {
	if item.Session.Project == nil {
		return unprojectedKey
	}
	return item.Session.Project.Slug
}
