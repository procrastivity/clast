// Package prompt renders the system/user prompt pairs the wake/brief/retro
// flows use (SURFACE V8/V10-V11, MODEL M18): given a pair of asset paths
// and a data payload, resolve both templates through internal/asset (never
// an embedded string constant) and fill each template's placeholders from
// the payload.
//
// Templating mechanism: the six carried templates (main:lib/clast/prompts,
// step llm-verbs/step-02) use bare `{{name}}` tokens, not Go text/template's
// dot-prefixed field/pipeline syntax — so this package matches that with
// literal string substitution (strings.Replace-level, via
// strings.Replacer) rather than reaching for text/template, which would
// force every carried template to be rewritten into a richer syntax it
// never asked for.
package prompt

import (
	"fmt"
	"strings"

	"github.com/procrastivity/clast/internal/asset"
)

// Pair names one prompt pair's two asset-relative paths — always resolved
// through internal/asset's override -> shipped -> embedded chain (M18),
// never read any other way.
type Pair struct {
	SystemPath string
	UserPath   string
}

// The three prompt pairs the shape-documents Matter's flow assets bind by
// name (llm-verbs/step-02, carrying the six filenames verbatim from
// main:lib/clast/prompts): flows/wake.md §3, flows/brief.md §3, and
// flows/retro.md §2 each resolve exactly one of these pairs through
// `plumbing asset`. Declared here so a caller (a future llm verb) never
// re-spells these paths as its own string literals.
var (
	// WakeDraft is the wake flow's draft prompt pair (flows/wake.md §3).
	WakeDraft = Pair{SystemPath: "prompts/wake-draft-system.md", UserPath: "prompts/wake-draft-user.md"}
	// Brief is the brief flow's synthesis prompt pair (flows/brief.md §3).
	Brief = Pair{SystemPath: "prompts/brief-system.md", UserPath: "prompts/brief-user.md"}
	// RetroSummary is the retro flow's per-entry condensation prompt pair
	// (flows/retro.md §2).
	RetroSummary = Pair{SystemPath: "prompts/retro-summary-system.md", UserPath: "prompts/retro-summary-user.md"}
)

// Rendered is one prompt pair, resolved and filled, ready for
// llm.Client.Complete(ctx, Rendered.System, Rendered.User).
type Rendered struct {
	System string
	User   string
	// SystemSource/UserSource name which asset-chain link answered each
	// half (M18: "the verb also names which link answered" — carried here
	// so a future caller can report it the same way `plumbing asset`
	// already does per file).
	SystemSource asset.Source
	UserSource   asset.Source
}

// Render resolves pair's two asset paths through internal/asset.Resolve
// and fills each template's {{name}} placeholders from data (see Fill).
// None of the six carried templates put a placeholder in the system half,
// but Fill runs over both sides uniformly rather than special-casing that
// away — a future prompt pair is free to use one.
func Render(pair Pair, data map[string]string) (Rendered, error) {
	sys, err := asset.Resolve(pair.SystemPath)
	if err != nil {
		return Rendered{}, fmt.Errorf("prompt: resolving %s: %w", pair.SystemPath, err)
	}
	usr, err := asset.Resolve(pair.UserPath)
	if err != nil {
		return Rendered{}, fmt.Errorf("prompt: resolving %s: %w", pair.UserPath, err)
	}

	return Rendered{
		System:       Fill(string(sys.Bytes()), data),
		User:         Fill(string(usr.Bytes()), data),
		SystemSource: sys.Source,
		UserSource:   usr.Source,
	}, nil
}

// Fill substitutes every occurrence of "{{name}}" in tmpl with data[name]
// — plain, non-overlapping literal string replacement (strings.Replace-
// level), the mechanism the six carried templates already assume (no
// dot-prefixed field access, no pipelines, no control flow). A name absent
// from data is left as a literal "{{name}}" in the output rather than
// silently blanked or causing a panic — a caller that forgot a field gets
// a visible marker in the rendered prompt.
func Fill(tmpl string, data map[string]string) string {
	if len(data) == 0 {
		return tmpl
	}
	pairs := make([]string, 0, len(data)*2)
	for name, value := range data {
		pairs = append(pairs, "{{"+name+"}}", value)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}
