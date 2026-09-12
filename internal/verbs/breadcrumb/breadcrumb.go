// Package breadcrumb implements the porcelain `clast breadcrumb <text>`
// (SURFACE V27, write side): a human's most frequent deliberate act in
// clast, so it belongs on the short top-level list rather than under
// `plumbing` (the list side, `plumbing breadcrumbs`, is V19). Business
// logic (Run) is kept separate from Cobra wiring (command.go), the same
// split internal/verbs/initverb and internal/verbs/whereami draw.
package breadcrumb

import (
	"context"
	"fmt"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

// Run appends one breadcrumb: scoped by dir's registered project unless
// global, in which case it writes slug: null regardless of dir.
//
// When global is false, dir is resolved through
// registry.ResolveCurrentClone — the same lookup `clast init` and
// session capture use. ANY failure to resolve a registered clone at dir
// (dir is not even inside a git repository, or it is one nobody has
// registered yet) is treated as the same "unregistered cwd" case V27
// names: refuse (refusal.unknown-clone) naming --global and `clast init`
// as the two outs, rather than distinguishing sub-cases the spec does
// not ask this verb to (a judgment call — whereami's own refusal keeps
// "not a git repo" as a separate validation.not-a-git-repo code, but
// breadcrumb has no reason to draw that distinction: refuse over guess
// either way, M16).
func Run(ctx context.Context, root, dir, text string, global bool, now time.Time) (journal.Breadcrumb, error) {
	var slug *string
	if !global {
		current, err := registry.ResolveCurrentClone(ctx, root, dir)
		if err != nil {
			return journal.Breadcrumb{}, unknownClone(dir)
		}
		slug = &current.Project.Slug
	}

	b := journal.Breadcrumb{At: now, Slug: slug, Text: text}
	if err := journal.AppendBreadcrumb(root, b); err != nil {
		return journal.Breadcrumb{}, err
	}
	return b, nil
}

// unknownClone is breadcrumb's refusal for a cwd that resolves to no
// registered clone: refusal.unknown-clone (V27/V34), naming --global and
// `clast init` as the two outs — refuse over guess, M16.
func unknownClone(dir string) error {
	return clasterr.New("refusal.unknown-clone",
		fmt.Sprintf("refused — %s is not a registered clast clone; use --global, or run `clast init` here to register it", dir))
}
