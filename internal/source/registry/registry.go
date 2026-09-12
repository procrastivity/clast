// Package registry holds the one table of implemented sources that
// capture and the query verbs all read (M12; C4.2's one-list-many-readers
// shape) — --harness validation, long-help source lists, and the capture
// walk itself all dispatch through it, so no two verbs can disagree about
// what a source is. A source is in the table or it does not exist: no
// disabled rows, no feature flags; planned sources are prose in long help
// (V30). It is a leaf for the same cycle reason as
// internal/harness/registry: internal/source cannot hold a table whose
// rows it would have to import.
package registry

import (
	"fmt"

	"github.com/procrastivity/clast/internal/source"
)

// All lists every implemented source, in the order help text and the
// capture walk use. claude lands first (MODEL §7); pi and devin join as
// rows when they land (MODEL §9), behind the same interface.
var All = []source.Source{}

// Names lists every source name in All's order — validation errors and
// long help read this rather than walking All themselves.
var Names = namesOf(All)

// namesOf panics on a duplicate Name, the same static-construction guard
// as internal/harness/registry: a row in All is program construction,
// not user input, and the panic surfaces at startup instead of letting
// two rows collapse into one name.
func namesOf(all []source.Source) []string {
	names := make([]string, len(all))
	seen := make(map[string]struct{}, len(all))
	for i, s := range all {
		if _, dup := seen[s.Name()]; dup {
			panic(fmt.Sprintf("registry: duplicate source name %q in All", s.Name()))
		}
		seen[s.Name()] = struct{}{}
		names[i] = s.Name()
	}
	return names
}

// Lookup finds the source registered under name. It reports false for an
// unknown name — the validation.unknown-harness case the verbs turn that
// into (V30).
func Lookup(name string) (source.Source, bool) {
	for _, s := range All {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}
