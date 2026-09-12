package claudecode_test

import (
	"testing"

	"github.com/procrastivity/clast/internal/harness/claudecode"
)

// TestShimCommand_ExactBytes pins the V33 shim's exact quoting: a PATH
// check that exits silently if clast is missing, a backgrounded
// `clast plumbing capture` with output discarded, and an unconditional
// `exit 0` — see shim.go's own comment for why each clause is there. This
// literal is the byte promise; splice_test.go's goldens additionally prove
// it survives unchanged inside a spliced settings.json.
func TestShimCommand_ExactBytes(t *testing.T) {
	const want = `command -v clast >/dev/null 2>&1 && (clast plumbing capture >/dev/null 2>&1 &); exit 0`
	if claudecode.ShimCommand != want {
		t.Fatalf("ShimCommand = %q, want %q", claudecode.ShimCommand, want)
	}
}
