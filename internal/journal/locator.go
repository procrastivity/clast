package journal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/procrastivity/clast/internal/clasterr"
)

// Resolve finds the one WalkItem in items whose session directory name
// (M11's <harness>-<native-id>) matches locator: the full name, or any
// prefix unique among items (V4).
//
// An exact full-name match always wins, even when it also happens to be a
// prefix of another item's name (e.g. locator "claude-8f3a" against both
// "claude-8f3a" and "claude-8f3abcd") — a judgment call this step settles:
// V4's refuse-over-guess is about never picking among several equally
// plausible candidates, not about making exact addressing by full name
// impossible just because a shorter session's name happens to also be a
// prefix of a longer one's.
//
// locator must be non-empty. An empty string is a prefix of every name,
// so it names nothing in particular rather than "everything" — this is
// validation.empty-locator, not the ambiguous case below (a judgment
// call: empty input is a different mistake than an under-specified one,
// and folding it into ambiguous-locator would print every session in
// the journal as "candidates" for a locator the caller never actually
// typed).
//
// Zero matches is not-found.session. Two or more prefix matches with no
// exact match to break the tie is validation.ambiguous-locator, naming
// every candidate directory name — V4: refuse over guess, never pick one.
func Resolve(items []WalkItem, locator string) (WalkItem, error) {
	if locator == "" {
		return WalkItem{}, clasterr.New("validation.empty-locator",
			"session locator must not be empty")
	}

	var matches []WalkItem
	for _, it := range items {
		name := it.Key.DirName()
		if name == locator {
			return it, nil // exact match always wins (see doc above).
		}
		if strings.HasPrefix(name, locator) {
			matches = append(matches, it)
		}
	}

	switch len(matches) {
	case 0:
		return WalkItem{}, clasterr.New("not-found.session",
			fmt.Sprintf("no session matches %q", locator))
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, it := range matches {
			names[i] = it.Key.DirName()
		}
		sort.Strings(names)
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = fmt.Sprintf("%q", n)
		}
		return WalkItem{}, clasterr.New("validation.ambiguous-locator",
			fmt.Sprintf("%q is ambiguous — matches %s", locator, strings.Join(quoted, ", ")))
	}
}
