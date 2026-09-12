package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/procrastivity/clast/internal/asset"

	rootassets "github.com/procrastivity/clast/assets"
)

// walkAssets lists every file under the shipped assets/ tree with a sha256
// checksum — the same tree the asset-resolution chain reads
// (<prefix>/share/clast/assets at runtime if installed, else the
// embedded fallback baked in at build time; never the user-override
// directory, which is not shipped content). Adding an asset file changes
// this list; editing one changes only its checksum.
func walkAssets() ([]Asset, error) {
	if dir, err := asset.DefaultDir(); err == nil {
		if entries, ok, err := walkDiskDir(dir); err != nil {
			return nil, err
		} else if ok {
			return entries, nil
		}
	}
	return walkEmbedded()
}

func walkDiskDir(dir string) ([]Asset, bool, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, false, nil
	}

	var out []Asset
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if !isShippedAsset(relSlash) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, Asset{Path: relSlash, SHA256: Checksum(data)})
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("manifest: walking shipped asset dir %q: %w", dir, err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, true, nil
}

func walkEmbedded() ([]Asset, error) {
	var out []Asset
	err := fs.WalkDir(rootassets.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isShippedAsset(path) {
			return nil
		}
		data, err := rootassets.FS.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, Asset{Path: path, SHA256: Checksum(data)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("manifest: walking embedded asset fallback: %w", err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// isShippedAsset excludes the assets package's own Go source — assets.go,
// whose //go:embed directive matches every file in the directory including
// itself, and any future doc.go — as package infrastructure rather than
// shipped asset content.
//
// The exclusion is deliberately top-level-only. A .go file inside a
// subdirectory is shipped content, not infrastructure — a verb that writes
// a tree ships that tree's sources as assets. Excluding every path ending
// in .go once left a whole skeleton payload out of the checksum list C3.3
// keeps to make drift and tampering detectable, and out of the
// manifest_digest computed over it (C3.4).
//
// path is relative to the tree being walked and slash-separated, so
// "assets.go" is the package's own source and "templates/x/main.go" is
// content. Both callers must pass the relative form: an absolute
// filesystem path always contains a separator and would defeat this test.
func isShippedAsset(path string) bool {
	inSubdir := strings.Contains(path, "/")
	return inSubdir || !strings.HasSuffix(path, ".go")
}

// Checksum returns data's sha256 hex digest — the checksum shape used
// throughout this package (the asset list, ChecksumFiles, Drift) and by
// harness generators verifying an installed tree against its Stamp.
func Checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
