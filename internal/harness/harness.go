// Package harness holds the pure filter that selects which verbs project
// into a harness (C3.2's mechanical enforcement point), plus the shared
// comparisons every per-harness generator's callers use. The manifest's
// own --json output is never filtered — this package is the only consumer
// of the filter, and each subpackage (e.g. claudecode) is the only
// consumer of that harness's rendered output.
package harness

import (
	"strings"

	"github.com/procrastivity/clast/internal/manifest"
)

// plumbingPrefix is the verb-name prefix every verb registered under the
// `plumbing` namespace carries in the manifest walk (internal/manifest's
// verbPath: "plumbing capture", "plumbing wake", …). The group command
// itself declares no RunE, so it is never Runnable and never appears as
// its own Verb row (internal/manifest/verbs.go's collect recurses through
// any command with children) — no separate "bare plumbing" case is
// needed.
const plumbingPrefix = "plumbing "

// Projectable selects exactly the `plumbing` namespace (SURFACE V32): the
// projectable set is structural, keyed off a verb's position under
// `plumbing` in the command tree, not off its C3.2 kind annotation.
//
// Earlier chassis-era code read this filter as kind == plumbing (C4.3's
// generic wording, "only plumbing verbs project"), but several top-level
// porcelain verbs — init, breadcrumb, doctor, install, uninstall, version,
// manifest — are themselves kind=plumbing (deterministic, no LLM) while
// sitting outside the namespace; SURFACE V32 resolves the ambiguity for
// clast by naming the filter structural, so a kind-only reading would
// wrongly project those porcelain verbs into every harness.
func Projectable(verbs []manifest.Verb) []manifest.Verb {
	out := make([]manifest.Verb, 0, len(verbs))
	for _, v := range verbs {
		if strings.HasPrefix(v.Name, plumbingPrefix) {
			out = append(out, v)
		}
	}
	return out
}
