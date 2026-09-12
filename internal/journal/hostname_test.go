package journal

import "testing"

// TestHostname_ResolvesThroughTheSharedSeam stays in the internal test
// package: it swaps the unexported hostname var directly, which an
// external (journal_test) test file cannot reach.
func TestHostname_ResolvesThroughTheSharedSeam(t *testing.T) {
	origHostname := hostname
	hostname = func() (string, error) { return "fixture-machine", nil }
	defer func() { hostname = origHostname }()

	got, err := Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}
	if got != "fixture-machine" {
		t.Errorf("Hostname() = %q, want %q", got, "fixture-machine")
	}
}
