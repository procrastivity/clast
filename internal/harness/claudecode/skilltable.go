package claudecode

import (
	"fmt"
	"strings"

	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/manifest"
)

// PlumbingVerbTable renders the generated markdown table every projected
// skill carries (SURFACE V32): one row per verb in the `plumbing`
// namespace — name, usage verbatim (C3.8), one-line purpose — selected by
// harness.Projectable, the same structural filter internal/checks and any
// other harness consumer read. It is binary-derived output, not an asset
// (C4.4: "fixed template text in a generator ... is generated output, not
// hand-written content", T27) — every installed skill's table comes from
// this same walk of m, the identical manifest.Manifest `clast manifest
// --json` emits, so a skill can never disagree with the binary about what
// a verb takes.
//
// The table is shared verbatim across every one of the three projected
// skills (wake, brief, retro): V32 names the projected content as "a
// generated table of the namespace verbs", not a per-shape subset, so
// each skill's SKILL.md carries the whole plumbing namespace regardless
// of which shape it fronts.
func PlumbingVerbTable(m manifest.Manifest) string {
	verbs := harness.Projectable(m.Verbs)

	var b strings.Builder
	if len(verbs) == 0 {
		b.WriteString("(no plumbing verbs registered yet)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "| Verb | Usage | Purpose |\n|---|---|---|\n")
	for _, v := range verbs {
		purpose := v.Description
		if purpose == "" {
			purpose = "(no description)"
		}
		invocation := strings.TrimSpace(m.Tool.Name + " " + v.Name)
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", invocation, mdUsageCell(v.Usage), purpose)
	}
	return b.String()
}

// mdUsageCell renders a verb's usage (C3.8: recorded verbatim, empty for a
// verb with no positional) as a markdown table cell — backtick-quoted when
// present, an empty cell (never a placeholder guess) when the verb takes
// nothing.
func mdUsageCell(usage string) string {
	if usage == "" {
		return ""
	}
	return "`" + usage + "`"
}
