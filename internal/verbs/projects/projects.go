// Package projects implements `clast plumbing projects` (SURFACE V23): a
// read-only listing of every project clast knows about — slug, remote (or
// empty for a keyless project), and its clone count across every machine
// that has registered one (M5). It never mutates the journal; the
// mutating registry verb, `init`, is porcelain (V26).
package projects

import (
	"github.com/procrastivity/clast/internal/registry"
)

// Row is one project as Run lists it.
type Row struct {
	Slug string
	// Remote is "" for a keyless (remoteless) project (M15) — legal, not
	// an error; the caller decides how to render that.
	Remote string
	// CloneCount is the number of clones registered across every machine
	// (journal.ListClones already globs every clones.*.json for this
	// project's slug).
	CloneCount int
}

// Run lists every project registered under root, in registry.Load's own
// deterministic (slug) order.
func Run(root string) ([]Row, error) {
	view, _, err := registry.Load(root)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(view.Projects))
	for _, p := range view.Projects {
		rows = append(rows, Row{
			Slug:       p.Project.Slug,
			Remote:     p.Project.Remote,
			CloneCount: len(p.Clones),
		})
	}
	return rows, nil
}
