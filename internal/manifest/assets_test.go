package manifest

import (
	"io/fs"
	"testing"

	rootassets "github.com/procrastivity/clast/assets"
)

// TestIsShippedAssetTable pins that the .go exclusion reaches exactly the
// assets package's own source and nothing under a subdirectory. The
// subdirectory rows are the regression: a blanket "ends in .go" test once
// dropped the whole Go payload a tree-writing verb ships, which put it
// outside the checksum list C3.3 keeps so drift and tampering are
// detectable, and outside the manifest_digest computed over it.
func TestIsShippedAssetTable(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"assets.go", false},
		{"doc.go", false},

		{"templates/skeleton/main.go", true},
		{"templates/skeleton/internal/cli/root.go", true},
		{"templates/skeleton/assets/assets.go", true},

		{"agent-guidance.md", true},
		{"config.default.yaml", true},
		{"templates/skills/claude-code/judgment.md", true},
	}
	for _, c := range cases {
		if got := isShippedAsset(c.path); got != c.want {
			t.Errorf("isShippedAsset(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestWalkEmbedded_ListsEveryShippedFile asserts the walk is exhaustive
// against the embedded tree rather than spot-checking it: every file in the
// fallback FS is listed with a checksum, except the package's own top-level
// Go source. An asset that reaches the binary but not the manifest is
// invisible to C3.3 and to the digest, which is the failure this guards.
func TestWalkEmbedded_ListsEveryShippedFile(t *testing.T) {
	got, err := walkEmbedded()
	if err != nil {
		t.Fatalf("walkEmbedded: %v", err)
	}

	listed := make(map[string]string, len(got))
	for _, a := range got {
		if a.SHA256 == "" {
			t.Errorf("asset %q carries no checksum (C3.3)", a.Path)
		}
		if _, dup := listed[a.Path]; dup {
			t.Errorf("asset %q listed twice", a.Path)
		}
		listed[a.Path] = a.SHA256
	}

	err = fs.WalkDir(rootassets.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want := isShippedAsset(path)
		if _, ok := listed[path]; ok != want {
			t.Errorf("embedded file %q: listed = %v, want %v", path, ok, want)
		}
		delete(listed, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded fallback: %v", err)
	}

	for path := range listed {
		t.Errorf("manifest lists %q, which is not in the embedded tree", path)
	}
}

// TestWalkEmbedded_SortedByPath pins the order manifestDigest's determinism
// argument depends on: two Build calls against the same binary must produce
// the same document, so the asset list may not vary with FS iteration order.
func TestWalkEmbedded_SortedByPath(t *testing.T) {
	got, err := walkEmbedded()
	if err != nil {
		t.Fatalf("walkEmbedded: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Path >= got[i].Path {
			t.Fatalf("asset list is not sorted by path: %q before %q", got[i-1].Path, got[i].Path)
		}
	}
}
