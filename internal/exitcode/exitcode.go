// Package exitcode centralizes the contract's small, closed exit-code table
// (CONTRACT.md C2.4). A new code gets added only when a real case doesn't
// fit one of these — never speculatively.
package exitcode

import (
	"fmt"
	"strings"

	"github.com/procrastivity/clast/internal/clasterr"
)

const (
	// Success is the verb's happy path.
	Success = 0
	// UserFail is validation, not-found, any error the verb itself raises
	// that isn't one of the more specific categories below.
	UserFail = 1
	// Usage is a bad-flags/args failure — Cobra's own argument-parsing path.
	Usage = 2
	// Refusal is a guard tripping: the tool declines to act on principle.
	Refusal = 3
	// Internal is an unexpected internal failure — I/O, corruption.
	Internal = 4
)

// SilentError exits with Code and writes nothing to stderr. It exists for
// verbs whose output bytes are owned by a parity contract with another
// implementation (ratified from ste9, T5/C2.4): the standard error renderer
// would break byte parity, so the verb returns Silent(code) and Execute
// exits without rendering. Use it only where a parity oracle owns the
// bytes — everywhere else, raise a clasterr.Error.
type SilentError struct {
	Code int
}

func (e *SilentError) Error() string {
	return fmt.Sprintf("silent exit %d", e.Code)
}

// Silent constructs a SilentError carrying code.
func Silent(code int) *SilentError {
	return &SilentError{Code: code}
}

// FromError reads err's own Code field to pick the exit code: a
// "refusal."-prefixed code is a refusal (3), an "internal."-prefixed code is
// an internal error (4), anything else is a plain user-facing failure (1).
// Cobra usage errors (2) never reach this function — Execute's return paths
// handle that distinction before FromError is called.
func FromError(err *clasterr.Error) int {
	switch {
	case strings.HasPrefix(err.Code, "refusal."):
		return Refusal
	case strings.HasPrefix(err.Code, "internal."):
		return Internal
	default:
		return UserFail
	}
}
