// Package breadcrumb implements the porcelain `clast breadcrumb <text>`
// (SURFACE V27, write side): a human's most frequent deliberate act in
// clast, so it belongs on the short top-level list rather than under
// `plumbing` (the list side, `plumbing breadcrumbs`, is V19). Business
// logic (Run) is kept separate from Cobra wiring (command.go), the same
// split internal/verbs/initverb and internal/verbs/whereami draw.
package breadcrumb

import (
	"context"
	"errors"
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
// session capture use. Only registry.ErrUnknownClone — dir resolves to no
// clone registered under this machine's name — is treated as the
// "unregistered cwd" case V27 names: refuse (refusal.unknown-clone)
// naming --global and `clast init` as the two outs. Every other error
// (dir is not even inside a git repository, or some other lookup
// failure) passes through unchanged, mirroring whereami's own split
// between refusal.unknown-clone and everything else (M16: refuse over
// guess, but do not mislabel an unrelated failure as this one).
func Run(ctx context.Context, root, dir, text string, global bool, now time.Time) (journal.Breadcrumb, error) {
	var slug *string
	if !global {
		current, err := registry.ResolveCurrentClone(ctx, root, dir)
		if err != nil {
			if errors.Is(err, registry.ErrUnknownClone) {
				return journal.Breadcrumb{}, unknownClone(dir)
			}
			return journal.Breadcrumb{}, err
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
