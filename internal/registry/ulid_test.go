package registry

import (
	"testing"
	"time"
)

// TestIsIdentityShaped confirms the shape test a candidate label or
// locator is checked against (M16): a real 26-character Crockford ULID is
// identity-shaped; an ordinary label, an empty string, a wrong-length
// string, and a string carrying a non-Crockford letter (lowercase l here,
// which Crockford's alphabet omits) are not.
//
// Ported from wip's internal/tiers/resolve_test.go TestIsIdentityShaped.
func TestIsIdentityShaped(t *testing.T) {
	if !IsIdentityShaped("01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Error("a real 26-char Crockford ULID was not recognized as identity-shaped")
	}
	for _, s := range []string{"widget-a", "", "01ARZ3NDEKTSV4RRFFQ69G5FA", "01ARZ3NDEKTSV4RRFFQ69G5FAVX", "0lARZ3NDEKTSV4RRFFQ69G5FAV"} {
		if IsIdentityShaped(s) {
			t.Errorf("%q was recognized as identity-shaped, want not", s)
		}
	}
}

// TestSourceNext_Monotonic confirms successive ids from one Source sort
// strictly ascending as strings, across many calls against the real
// clock.
func TestSourceNext_Monotonic(t *testing.T) {
	src := NewSource()
	prev := src.Next()
	for i := 0; i < 1000; i++ {
		next := src.Next()
		if next <= prev {
			t.Fatalf("Source.Next() produced %q after %q, want strictly greater", next, prev)
		}
		prev = next
	}
}

// TestSourceNext_SameMillisecondIncrementsRandomHalf forces the
// same-millisecond path with an injected clock: two ids minted in the same
// instant still sort strictly ascending, and still share the same
// timestamp half, because the random half increments rather than being
// redrawn.
func TestSourceNext_SameMillisecondIncrementsRandomHalf(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := newSourceWithClock(func() time.Time { return fixed })
	a := src.Next()
	b := src.Next()
	if a >= b {
		t.Fatalf("same-millisecond ids not strictly ascending: %q then %q", a, b)
	}
	if a[:10] != b[:10] {
		t.Errorf("same-millisecond ids carry different timestamp halves: %q vs %q", a[:10], b[:10])
	}
}

// TestSourceNext_Shape confirms every id Next produces is identity-shaped.
func TestSourceNext_Shape(t *testing.T) {
	src := NewSource()
	for i := 0; i < 10; i++ {
		id := src.Next()
		if !IsIdentityShaped(id) {
			t.Errorf("Source.Next() = %q, not identity-shaped", id)
		}
	}
}
