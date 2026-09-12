// Package journal owns the durable store: layout, record formats, atomic
// writes, day-bucket math, the tree walk, and session-locator resolution
// (MODEL.md §1-§6; the name follows §1 vocabulary — the Journal is the
// durable store, "store" stays a design-doc word). No code outside this
// package touches journal paths.
//
// This file resolves the journal root (Root) and, in store.go, its
// journal.json marker (EnsureRoot/ReadMarker). records.go types every
// MODEL §4 document (Session, Curation, Project, ClonesFile, Breadcrumb);
// paths.go names where each one lives on disk. breadcrumbs.go adds the
// one line-appended exception to whole-document writes
// (AppendBreadcrumb/ReadBreadcrumbs, M5). day.go parses day_cutoff and
// the V5 day/duration grammar (Cutoff, ParseDay, ParseDuration). walk.go
// enumerates the tree (Walk) and derives curation state and staleness
// from what it finds (WalkItem.State/Stale, M7). locator.go resolves a
// V4 session locator (Resolve) against a Walk result. projects.go
// enumerates the registry side of the tree — every project.json
// (ListProjects) and, per project, every clones.<machine>.json across
// every machine that has one (ListClones, M5) — the read side a later
// registry resolution layer (internal/registry) builds on; this package
// still owns every path those reads touch.
//
// One posture holds across every read in the package: a missing document
// returns empty, never an error; a malformed one is a counted
// Diagnostic, never fatal (MODEL §7's tolerance, applied to this
// package's own tree). And one boundary holds across every write and
// read: this package never opens the transcript copy or parses entry.md's
// body (M9) — every fact a query needs already lives in the small JSON
// documents, so no truth-layer fact here ever requires reading a
// transcript.
package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
//
// journal_dir must be a string; a present-and-non-nil value of any other
// YAML type (a number, a bool, a mapping — someone's config typo) is an
// error naming the key and the value's Go type, never a silent fallback
// to the default. It may be absolute, or start with "~/" (or be the bare
// "~"), expanded against $HOME the same way a shell would — MODEL M6's
// own example config value is "~/Sync/clast/journal". After expansion the
// path must be absolute: a relative journal_dir would make the journal's
// location depend on whatever directory the caller happened to be
// running in, which a fixed store root must never do (refuse over
// guess).
func Root(cfg config.Config) (string, error) {
	if dir := journalDirEnv(); dir != "" {
		return dir, nil
	}

	if raw, present := cfg[journalDirConfigKey]; present && raw != nil {
		dir, isString := raw.(string)
		if !isString {
			return "", fmt.Errorf("journal: config key %q must be a string, got %T", journalDirConfigKey, raw)
		}
		if dir != "" {
			return expandJournalDir(dir)
		}
	}
	return defaultRoot()
}

// expandJournalDir expands a leading "~/" (or a bare "~") in dir against
// $HOME, via the userHomeDir seam, then requires the result to be
// absolute.
func expandJournalDir(dir string) (string, error) {
	switch {
	case dir == "~":
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("journal: expanding %q: resolving home directory: %w", dir, err)
		}
		dir = home
	case strings.HasPrefix(dir, "~/"):
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("journal: expanding %q: resolving home directory: %w", dir, err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
	}

	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("journal: config key %q must be an absolute path (or start with \"~/\"), got %q", journalDirConfigKey, dir)
	}
	return dir, nil
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
