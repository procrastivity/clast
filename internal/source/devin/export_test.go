package devin

import (
	"context"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

// Test-only exports for the external devin_test package: consumer-level
// tests (capture.Run, registry lookup, show/analyze) must live in
// package devin_test — once the registry row lands, registry will
// import this package, and internal test files importing verbs would
// become an import cycle.
var (
	BuildStoreForTest   = buildStore
	BuildVariantForTest = buildStoreWithInsertOrder
	MutateStoreForTest  = mutate
)

// CaptureForTest runs Capture against a scratch artifact sink — the
// exported equivalent of the internal artifacts helper.
func CaptureForTest(t *testing.T, s *Source, id string) (source.Facts, map[string][]byte, []source.Diagnostic, error) {
	t.Helper()
	a := artifacts{}
	facts, diags, err := s.Capture(context.Background(), source.Discovered{NativeID: id}, a.writer)
	return facts, map[string][]byte(a), diags, err
}
