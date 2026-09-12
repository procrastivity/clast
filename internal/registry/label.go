package registry

import "path/filepath"

// DefaultLabel is a Clone's label at registration time: the clone
// directory's basename (M16).
//
// Ported from wip's internal/tiers/label.go DefaultLabel.
func DefaultLabel(dir string) string {
	return filepath.Base(filepath.Clean(dir))
}

// SuggestLabel runs M16's collision-suggestion ladder: try the parent
// directory's name first, then `<basename>-<parent>` if that also
// collides. It is a pure function of the path and the taken set — it does
// not depend on registration order — and it never invents a third form:
// the false result means both attempts also collide, and the caller
// refuses rather than falling back to anything else (no `-2`, `-3`,
// ever). A caller checking a candidate label against IsIdentityShaped
// before ever reaching here is what keeps a ULID-shaped label from
// entering the ladder at all (M16); SuggestLabel itself only ever
// produces directory-name-derived strings, never a bare id.
//
// The same algorithm serves two callers a later step wires up: silent
// resolution of the default label on first registration, and naming the
// one alternative a relabel refusal offers.
//
// Ported from wip's internal/tiers/label.go SuggestLabel.
func SuggestLabel(dir string, taken map[string]bool) (string, bool) {
	clean := filepath.Clean(dir)
	base := filepath.Base(clean)
	parent := filepath.Base(filepath.Dir(clean))

	if parent != "" && parent != "." && parent != base && !taken[parent] {
		return parent, true
	}
	combined := base + "-" + parent
	if !taken[combined] {
		return combined, true
	}
	return "", false
}
