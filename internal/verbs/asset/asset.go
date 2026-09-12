// Package asset implements `clast plumbing asset <path>` (SURFACE V25): the
// verb every flow and skill reads clast's tunable text through. It carries
// no resolution logic of its own — internal/asset already implements the
// override -> shipped -> embedded chain (M18) — this package only adds the
// verb's own concerns: the surface vocabulary for which link answered, and
// the resolved content's sha256.
package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	assetchain "github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
)

// Result is what Run resolved for one asset path.
type Result struct {
	Path string
	// Link is the surface word for the chain link that answered: "override",
	// "shipped", or "embedded" (see link below — the TRAP this guards
	// against is assetchain.SourceDefault.String()'s own "default" leaking
	// here instead of "shipped").
	Link string
	// ResolvedFrom is the filesystem path the asset was read from, or
	// "embedded" for the fallback link, which lives compiled into the
	// binary rather than on disk (V25).
	ResolvedFrom string
	SHA256       string
	Content      []byte
}

// Run resolves path through the asset-resolution chain (M18) and reports
// which link answered. An unknown path is not-found.asset (V25):
// assetchain.Resolve's own error — neither a shipped default nor the
// embedded fallback has path — is the only failure this can raise. A
// failure to even reach the override or default directory (e.g. an
// unreadable $XDG_CONFIG_HOME) is swallowed inside the chain and treated as
// that link's absence, not surfaced as a distinct error — this verb has no
// occasion to see it separately from not-found.
func Run(path string) (Result, error) {
	resolved, err := assetchain.Resolve(path)
	if err != nil {
		return Result{}, clasterr.New("not-found.asset", fmt.Sprintf("no asset at %q: %v", path, err))
	}

	sum := sha256.Sum256(resolved.Bytes())
	return Result{
		Path:         path,
		Link:         link(resolved.Source),
		ResolvedFrom: resolvedFrom(resolved),
		SHA256:       hex.EncodeToString(sum[:]),
		Content:      resolved.Bytes(),
	}, nil
}

// link maps an internal assetchain.Source to V25's surface vocabulary.
// assetchain.SourceDefault.String() returns "default" — the chain's own
// internal name for the shipped-default link — which must never leak to
// the surface; V25 names that link "shipped".
func link(s assetchain.Source) string {
	switch s {
	case assetchain.SourceOverride:
		return "override"
	case assetchain.SourceDefault:
		return "shipped"
	case assetchain.SourceEmbedded:
		return "embedded"
	default:
		return "unknown"
	}
}

// resolvedFrom is r's disk path, or "embedded" for the fallback link (V25:
// resolved_from is "embedded" for the embedded link).
func resolvedFrom(r assetchain.Resolved) string {
	if r.Source == assetchain.SourceEmbedded {
		return "embedded"
	}
	return r.Path
}
