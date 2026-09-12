// Package curate implements `clast plumbing curate` (SURFACE V14): the
// entry writer, valid from every curation state. It reads a complete
// entry.md document — frontmatter included — from stdin or --file,
// validates it, and writes it atomically alongside a fresh curation.json
// carrying the M7 transcript fingerprint.
package curate

import (
	"errors"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/entry"
)

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
