// Package journal owns the durable store: layout, record formats, atomic
// writes, the walk, and day math (MODEL.md §1-§6; the name follows §1
// vocabulary — the Journal is the durable store, "store" stays a
// design-doc word). No code outside this package touches journal paths.
//
// This file lands only root resolution. Record types, the walk,
// breadcrumbs, and day math arrive in later steps.
package journal

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/procrastivity/clast/internal/config"
)

// appName names the XDG data subdirectory: $XDG_DATA_HOME/clast/journal.
const appName = "clast"

// journalDirConfigKey is the tool-config key naming the journal root
// (SURFACE V31, MODEL M6). Empty or absent means the XDG default.
const journalDirConfigKey = "journal_dir"

// RootDirEnv overrides Root's result outright when set. It is a C2.6 test
// seam only: production deployments configure journal_dir instead, and no
// production code path may come to depend on this variable for anything
// other than letting tests redirect the root.
const RootDirEnv = "CLAST_JOURNAL_DIR"

// journalDirEnv reads RootDirEnv, indirected as a package-level var (C2.6)
// rather than a bare os.Getenv call at the use site — same shape as
// userHomeDir below — so the seam is visibly swappable from tests.
var journalDirEnv = func() string { return os.Getenv(RootDirEnv) }

// userHomeDir is os.UserHomeDir, indirected so tests can fake $HOME without
// a real one on disk (C2.6).
var userHomeDir = os.UserHomeDir

// Root resolves the journal root directory: cfg's journal_dir key when
// non-empty, otherwise $XDG_DATA_HOME/clast/journal, defaulting to
// ~/.local/share/clast/journal when XDG_DATA_HOME is unset (MODEL M6).
// CLAST_JOURNAL_DIR, when set, wins over both — the C2.6 test seam.
func Root(cfg config.Config) (string, error) {
	if dir := journalDirEnv(); dir != "" {
		return dir, nil
	}
	if dir, _ := cfg[journalDirConfigKey].(string); dir != "" {
		return dir, nil
	}
	return defaultRoot()
}

// defaultRoot computes $XDG_DATA_HOME/clast/journal, falling back to
// ~/.local/share when XDG_DATA_HOME is unset — the same fallback shape
// asset.OverrideDir uses for XDG_CONFIG_HOME.
func defaultRoot() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("journal: resolving XDG_DATA_HOME fallback: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, appName, "journal"), nil
}
