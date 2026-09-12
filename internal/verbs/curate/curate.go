// Package curate implements `clast plumbing curate` (SURFACE V14): the
// entry writer, valid from every curation state. It reads a complete
// entry.md document — frontmatter included — from stdin or --file,
// validates it, and writes it atomically alongside a fresh curation.json
// carrying the M7 transcript fingerprint.
package curate

import (
	"errors"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
)

// Result is what Run curated: the session's identity and shard, and the
// curation state it carried immediately before this call — a command
// layer can use PriorState to note "replaced a dismissal" without a
// second walk.
type Result struct {
	Key        journal.SessionKey
	Shard      string
	PriorState journal.CurationState
}

// Run resolves locator (V4) against root's journal, validates data as a
// complete entry.md document, and writes it — valid from every curation
// state (V14): from captured it curates, from curated it re-curates (the
// stale flow lands a fresh transcript_at_curation, per M7), from
// dismissed it revives straight to curated, replacing the dismissal in
// one write with no undismiss ceremony (the M-§2 diagram draws this edge
// directly; state-verbs step 03 settles it need not branch on prior
// state at all — WriteCuration always overwrites whole).
//
// Order matters on a crash: entry.md is written before curation.json, so
// a session interrupted mid-curate is still readable as its prior state
// (captured/curated/dismissed) rather than landing with a curation.json
// that claims curated over a half-written or absent entry.md.
func Run(root, locator string, data []byte, now time.Time) (Result, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}
	item, err := journal.Resolve(items, locator)
	if err != nil {
		return Result{}, err
	}

	if _, err := validate(data); err != nil {
		return Result{}, err
	}

	machine, err := journal.Hostname()
	if err != nil {
		return Result{}, err
	}

	if err := journal.WriteEntry(root, item.Shard, item.Key, data); err != nil {
		return Result{}, err
	}

	stamp := journal.TranscriptStamp{
		Lines:  item.Session.Transcript.Lines,
		SHA256: item.Session.Transcript.SHA256,
	}
	curation := journal.Curation{
		State:                journal.StateCurated,
		At:                   now,
		Machine:              machine,
		Reason:               nil,
		TranscriptAtCuration: &stamp,
	}
	if err := journal.WriteCuration(root, item.Shard, item.Key, curation); err != nil {
		return Result{}, err
	}

	return Result{Key: item.Key, Shard: item.Shard, PriorState: item.State()}, nil
}

// validate parses and validates a caller-supplied entry.md document per
// V14's posture: frontmatter must parse, and title must be present —
// nothing else. The two failure modes get distinct clasterr codes
// (validation.entry-frontmatter, validation.entry-title) rather than one
// shared one, because a skill acting on the failure wants to know which
// condition it was (matter finding) — internal/entry's ErrInvalidFrontmatter
// and ErrMissingTitle sentinels are what makes the distinction possible
// without this package re-parsing anything itself.
func validate(data []byte) (entry.Entry, error) {
	e, err := entry.Parse(data)
	if err == nil {
		return e, nil
	}
	switch {
	case errors.Is(err, entry.ErrMissingTitle):
		return entry.Entry{}, clasterr.New("validation.entry-title", err.Error())
	default:
		// Covers entry.ErrInvalidFrontmatter and, defensively, any other
		// Parse failure this package doesn't yet know to name separately —
		// frontmatter-shaped is the more general of the two conditions.
		return entry.Entry{}, clasterr.New("validation.entry-frontmatter", err.Error())
	}
}
