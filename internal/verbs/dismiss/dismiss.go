// Package dismiss implements `clast plumbing dismiss` (SURFACE V15): the
// state transition that marks a session deliberately excluded, with a
// reason. It refuses a curated session outright (the entry would be
// orphaned) and refuses the reason capture reserves to itself (M3); a
// re-dismiss of an already-dismissed session succeeds and replaces the
// prior reason (a matter finding: V15 names exactly one refusal —
// curated — and V34 commits no code for "already dismissed", so refusing
// it would invent a refusal the surface never drew).
package dismiss

import (
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
)

// reservedReason is M3's reserved dismissal reason, capture's own —
// dismiss refuses a caller who names it explicitly (V15). Matches
// capture's own private autoNoopReason constant; kept as an independent
// literal here rather than exporting capture's (capture is a sealed,
// unrelated Matter's package, and one repeated string constant across two
// packages that both need to name M3's reserved value is a smaller cost
// than a new cross-package dependency for it).
const reservedReason = "auto:no-op"

// DefaultReason is dismiss's --reason default when the caller supplies
// none (V15).
const DefaultReason = "manual"

// Result is what Run dismissed: the session's identity and shard, and
// whether this call replaced an existing dismissal (re-dismiss) rather
// than dismissing a freshly captured session.
type Result struct {
	Key       journal.SessionKey
	Shard     string
	Redismiss bool
}

// Run resolves locator (V4) against root's journal and dismisses it with
// reason, refusing a curated session (validation.curated — the entry
// would be orphaned; delete is not a v1 surface) and refusing the
// reserved reason regardless of the session's current state
// (validation.reserved-reason). A session already dismissed is
// re-dismissed: the new reason replaces the old one outright.
func Run(root, locator, reason string, now time.Time, machine string) (Result, error) {
	if reason == reservedReason {
		return Result{}, clasterr.New("validation.reserved-reason",
			"reason \""+reservedReason+"\" is reserved to capture's own auto-dismissal (M3)")
	}

	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}
	item, err := journal.Resolve(items, locator)
	if err != nil {
		return Result{}, err
	}

	// Curated sessions are refused outright, and so is a session whose
	// entry.md exists without curation.json: curate writes entry.md
	// before curation.json (its own doc comment), so a crash between the
	// two writes leaves exactly that shape — read back as "captured", but
	// with an entry already on disk that dismiss would otherwise orphan.
	// Same code (validation.curated) and rationale (V15) either way: the
	// entry would be orphaned.
	if item.State() == journal.StateCurated || item.EntryExists {
		return Result{}, clasterr.New("validation.curated",
			"refused — "+item.Key.DirName()+" has an entry.md on disk; dismissing it would orphan the entry")
	}

	redismiss := item.State() == journal.StateDismissed

	if err := journal.WriteCuration(root, item.Shard, item.Key, journal.Curation{
		State:   journal.StateDismissed,
		At:      now,
		Machine: machine,
		Reason:  &reason,
	}); err != nil {
		return Result{}, err
	}

	return Result{Key: item.Key, Shard: item.Shard, Redismiss: redismiss}, nil
}
