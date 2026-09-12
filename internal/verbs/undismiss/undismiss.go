// Package undismiss implements `clast plumbing undismiss` (SURFACE V15):
// the reverse of dismiss, returning a dismissed session to captured.
// Its only precondition is state dismissed (V15) — it accepts a
// dismissal carrying capture's own reserved "auto:no-op" reason exactly
// as it accepts any other, per a matter finding: capture.go's own
// comment ("only the retraction below may remove a dismissal carrying
// it") describes capture's own writers, not a constraint on this verb. A
// session undismissed back to captured while still not substantive is
// re-dismissed by the next capture sweep (M3, applyAutoDismissal) — that
// is M3 working as designed, not a bug this package works around.
package undismiss

import (
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
)

// Result is what Run undismissed: the session's identity and shard.
type Result struct {
	Key   journal.SessionKey
	Shard string
}

// Run resolves locator (V4) against root's journal and removes its
// curation.json, returning it to captured — refusing
// (validation.not-dismissed) any session not currently in state
// dismissed.
func Run(root, locator string) (Result, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}
	item, err := journal.Resolve(items, locator)
	if err != nil {
		return Result{}, err
	}

	if item.State() != journal.StateDismissed {
		return Result{}, clasterr.New("validation.not-dismissed",
			"refused — "+item.Key.DirName()+" is not dismissed (state "+string(item.State())+")")
	}

	if err := journal.RemoveCuration(root, item.Shard, item.Key); err != nil {
		return Result{}, err
	}

	return Result{Key: item.Key, Shard: item.Shard}, nil
}
