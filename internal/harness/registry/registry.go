// Package registry holds the one table of harnesses that install,
// uninstall, and doctor all render from (C4.2) — help text, bare-invocation
// listings, unknown-harness errors, and the stale-artifact checks all read
// it, so those verbs can never disagree about what is installable or how
// to drive it. It is a leaf: it imports each harness subpackage for its
// Name constant and function set, and nothing imports it but the verbs and
// checks. internal/harness itself cannot hold this table because every
// subpackage imports it (a cycle), and neither verb package should own a
// fact both read.
package registry

import (
	"fmt"

	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/harness/claudecode"
	"github.com/procrastivity/clast/internal/manifest"
)

// Target is one independently stamped, installable subtree within a
// harness's projection (C4.4) — the shape a single-target harness used to
// expose directly at the Harness level, before SURFACE V32 gave
// claude-code three of them (one per skill). Label distinguishes a
// harness's targets from one another for reporting (install, uninstall,
// doctor); every other field is the same {InstallDir, Generate, Install,
// Uninstall} tuple the chassis always had.
type Target struct {
	Label      string
	InstallDir func() (string, error)
	Generate   func(manifest.Manifest) (map[string][]byte, error)
	Install    func(manifest.Manifest) (string, error)
	Uninstall  func() (string, error)
}

// SpliceOutcome reports what a harness's Splice call did to its splice
// target (C4.8) — a foreign-file path edit, not a stamped generated tree,
// so it carries no drift state of its own; see SpliceStatus and
// harness.SpliceProbe for that (SURFACE V28, step-04).
type SpliceOutcome struct {
	Path   string
	Status string
}

// Harness is one row of the install/uninstall/doctor table: a harness's
// name, its projected targets, and the functions that probe it and
// splice/unsplice its foreign-file targets. Available is required —
// Lookup's callers dispatch through it unconditionally. Targets may be
// empty and Splice/Unsplice may be nil for a harness with neither kind of
// target (none exist yet, but C4.8 rates both as ordinary extensions of
// the model).
type Harness struct {
	Name      string
	Available func() bool
	Targets   []Target
	// Splice installs this harness's splice target, if it has one (C4.8) —
	// nil for a harness with none. Only claude-code sets it today.
	Splice func() (SpliceOutcome, error)
	// Unsplice reverses Splice (step-03): removes exactly the entry Splice
	// added, leaving every unrelated byte alone. nil for a harness with no
	// splice target. Idempotent and symmetric with Splice: a missing file
	// or an already-absent entry is a no-op, not a diagnostic (see
	// claudecode.Unsplice's doc comment for the full reasoning).
	Unsplice func() (SpliceOutcome, error)
	// SpliceStatus probes this harness's splice target's drift state,
	// read-only (SURFACE V28, step-04) — nil for a harness with no splice
	// target. Unlike Splice/Unsplice, it never writes: doctor is its only
	// caller, and a bare `clast doctor` run must never install or repair
	// anything as a side effect of reporting on it. Returns the shared
	// harness.SpliceProbe shape directly (no adapter, unlike
	// Splice/Unsplice's SpliceOutcome): that type already lives in the
	// harness-agnostic internal/harness package, the same place State
	// lives for stamped targets, so there is no harness-specific shape to
	// translate out of the way here.
	SpliceStatus func() (harness.SpliceProbe, error)
}

// All lists every harness this tool can project itself into. The skeleton
// ships exactly one worked target (claude-code, T18); add a row here per
// additional harness — toolsmith doc playbook/new-harness-target.md
// walks every call site a new target touches.
var All = []Harness{
	{
		Name:         claudecode.Name,
		Available:    claudecode.Available,
		Targets:      claudecodeTargets(),
		Splice:       claudecodeSplice,
		Unsplice:     claudecodeUnsplice,
		SpliceStatus: claudecode.SpliceStatus,
	},
}

// claudecodeTargets builds claude-code's three skill targets (SURFACE
// V32), one per claudecode.SkillNames entry, each closing over its own
// name so InstallDir/Generate/Install/Uninstall all act on that one
// skill's tree.
func claudecodeTargets() []Target {
	out := make([]Target, 0, len(claudecode.SkillNames))
	for _, name := range claudecode.SkillNames {
		out = append(out, Target{
			Label:      name,
			InstallDir: func() (string, error) { return claudecode.SkillDir(name) },
			Generate: func(m manifest.Manifest) (map[string][]byte, error) {
				return claudecode.GenerateSkill(name, m)
			},
			Install: func(m manifest.Manifest) (string, error) {
				return claudecode.InstallSkill(name, m)
			},
			Uninstall: func() (string, error) {
				return claudecode.UninstallSkill(name)
			},
		})
	}
	return out
}

// claudecodeSplice adapts claudecode.Splice's result into the registry's
// own SpliceOutcome shape, so this package stays the sole shape install,
// uninstall, and checks read (the same reason Harness itself is defined
// here rather than in each harness subpackage).
func claudecodeSplice() (SpliceOutcome, error) {
	r, err := claudecode.Splice()
	if err != nil {
		return SpliceOutcome{}, err
	}
	return SpliceOutcome{Path: r.Path, Status: r.Status}, nil
}

// claudecodeUnsplice adapts claudecode.Unsplice's result into the
// registry's own SpliceOutcome shape, the same reason claudecodeSplice
// does — one shape install, uninstall, and checks all read.
func claudecodeUnsplice() (SpliceOutcome, error) {
	r, err := claudecode.Unsplice()
	if err != nil {
		return SpliceOutcome{}, err
	}
	return SpliceOutcome{Path: r.Path, Status: r.Status}, nil
}

// Names lists every harness in All's order — help text, bare-invocation
// listings, and unknown-harness errors all read this rather than walking
// All themselves.
var Names = namesOf(All)

// namesOf panics on a duplicate Name (C4.2: registration panics on
// duplicates, because a row in All is static program construction, not
// user input). It runs once, as Names's own initializer, so the panic
// surfaces at program startup rather than letting two rows silently
// collapse into one name that install, uninstall, and doctor would then
// read inconsistently. Rejected: returning an error instead — nothing at
// this call site could act on it, and the guard is cheap enough to pay
// unconditionally for the next harness row.
func namesOf(all []Harness) []string {
	names := make([]string, len(all))
	seen := make(map[string]struct{}, len(all))
	for i, h := range all {
		if _, dup := seen[h.Name]; dup {
			panic(fmt.Sprintf("registry: duplicate harness name %q in All", h.Name))
		}
		seen[h.Name] = struct{}{}
		names[i] = h.Name
	}
	return names
}

// Lookup finds the Harness registered under name, in All's order. It
// reports false for any name not in All — the same unknown-harness case
// install and uninstall already validate against Names before calling
// Lookup.
func Lookup(name string) (Harness, bool) {
	for _, h := range All {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}
