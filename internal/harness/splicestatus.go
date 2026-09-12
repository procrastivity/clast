package harness

// SpliceState is the drift state doctor derives for a harness's splice
// target (C4.8, SURFACE V28 step-04): a foreign-file path edit that carries
// no stamp of its own (registry.SpliceOutcome's doc comment), so it is
// described with its own small vocabulary rather than State's six
// stamped-target states.
type SpliceState string

const (
	// SpliceCurrent means the pinned shim command is present, byte for
	// byte, among the foreign file's hooks.
	SpliceCurrent SpliceState = "current"
	// SpliceAbsent means no entry recognizable as the shim is present at
	// all — including a wholly missing foreign file, treated the same as
	// an empty one (Splice's own "missing settings.json is `{}`" rule).
	// Whether that counts as drift depends on whether any of the
	// harness's stamped targets are installed: an absent shim alongside a
	// fully-uninstalled harness is the clean state, not drift, the same
	// reading Status's own Missing gives a lone stamped target — checks.go
	// decides that by combining this with the harness's TargetStates; this
	// package only reports the bare fact.
	SpliceAbsent SpliceState = "absent"
	// SpliceTampered means an entry recognizable as "meant to be" the
	// shim is present, but its command bytes no longer match the pinned
	// string exactly — drift regardless of whether any skill is
	// installed, since the entry's mere presence proves install wrote it.
	SpliceTampered SpliceState = "tampered"
	// SpliceMalformed means the foreign file exists but cannot be parsed
	// as JSON at all — the splice target's analog of a stamped target's
	// Incompatible: a condition install and uninstall both already refuse
	// on (claudecode.CodeMalformedSplice), so doctor treats it with the
	// same severity.
	SpliceMalformed SpliceState = "malformed"
)

// SpliceProbe is one harness's splice target's read-only status: the
// foreign file's path plus its SpliceState (SURFACE V28). Unlike Status,
// whose caller may go on to write based on what it returns, a SpliceProbe's
// only caller (doctor) never writes at all — probing it must never risk
// installing or repairing anything as a side effect of reporting on it.
type SpliceProbe struct {
	Path  string
	State SpliceState
}

// CodeMalformedSplice is the validation code a harness's splice target
// carries when its foreign file exists but cannot be parsed as JSON —
// shared by claudecode.Splice/Unsplice's own refusal and doctor's finding
// for SpliceMalformed (SURFACE V28), the same one-code-two-callers pattern
// CodeIncompatible already established for stamped targets (refusal.go).
const CodeMalformedSplice = "validation.malformed-settings"
