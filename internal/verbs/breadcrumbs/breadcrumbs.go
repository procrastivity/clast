// Package breadcrumbs implements `clast plumbing breadcrumbs` (SURFACE
// V19, list side): a day's crumbs, cross-machine (journal.
// ReadBreadcrumbsForDay, M5's glob), scoped by --project/--global. The
// write verb, `breadcrumb`, is porcelain (V27) — a separate package.
package breadcrumbs

import (
	"sort"

	"github.com/procrastivity/clast/internal/journal"
)

// Run lists day's breadcrumbs (M8 day-bucket semantics, via
// journal.ReadBreadcrumbsForDay) scoped per V19's settled list-side call:
//   - global: slug-null crumbs only.
//   - project != "": that project's crumbs only.
//   - neither: every crumb in the bucket.
//
// project and global are mutually exclusive — the command layer's job to
// enforce (cobra flag groups), never this function's. Results are sorted
// by At, ascending: a day's crumbs read as one chronological log
// regardless of which machine wrote which entry.
func Run(root string, day journal.Day, cutoff journal.Cutoff, project string, global bool) ([]journal.BreadcrumbEntry, error) {
	entries, _, err := journal.ReadBreadcrumbsForDay(root, day, cutoff)
	if err != nil {
		return nil, err
	}

	var out []journal.BreadcrumbEntry
	for _, e := range entries {
		switch {
		case global:
			if e.Slug == nil {
				out = append(out, e)
			}
		case project != "":
			if e.Slug != nil && *e.Slug == project {
				out = append(out, e)
			}
		default:
			out = append(out, e)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}
