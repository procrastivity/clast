package asset

import (
	"testing"

	assetchain "github.com/procrastivity/clast/internal/asset"
)

// TestLink_MapsEveryChainSourceToItsSurfaceWord table-drives the V25
// vocabulary: "shipped" for assetchain.SourceDefault is the TRAP — the
// internal name for that same link is "default" (asset_test.go's
// TestSourceDefaultStillSaysDefault pins that this stays true) — and must
// never leak to the surface.
func TestLink_MapsEveryChainSourceToItsSurfaceWord(t *testing.T) {
	cases := []struct {
		source assetchain.Source
		want   string
	}{
		{assetchain.SourceOverride, "override"},
		{assetchain.SourceDefault, "shipped"},
		{assetchain.SourceEmbedded, "embedded"},
	}
	for _, c := range cases {
		if got := link(c.source); got != c.want {
			t.Errorf("link(%v) = %q, want %q", c.source, got, c.want)
		}
	}
}

// TestResolvedFrom_EmbeddedIsTheSentinel asserts the "embedded" string V25
// fixes for the fallback link, and that every other link reports its real
// disk path unchanged.
func TestResolvedFrom_EmbeddedIsTheSentinel(t *testing.T) {
	embedded := assetchain.Resolved{Source: assetchain.SourceEmbedded, Path: ""}
	if got := resolvedFrom(embedded); got != "embedded" {
		t.Errorf("resolvedFrom(embedded) = %q, want %q", got, "embedded")
	}

	onDisk := assetchain.Resolved{Source: assetchain.SourceOverride, Path: "/home/dev/.config/clast/flows/retro.md"}
	if got := resolvedFrom(onDisk); got != onDisk.Path {
		t.Errorf("resolvedFrom(override) = %q, want its own disk path %q", got, onDisk.Path)
	}
}
