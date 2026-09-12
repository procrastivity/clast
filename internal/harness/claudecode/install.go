package claudecode

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/manifest"
)

// InstallSkill renders name's content (GenerateSkill) into SkillDir(name)
// and stamps the result with tool.version, schemaVersion, and a per-file
// checksum (C4.4). It always overwrites whatever it finds and writes the
// stamp unconditionally — refusing to overwrite a hand-edited or unstamped
// target is the install verb's job (internal/harness.Status plus
// internal/harness.Refusal), not this function's, kept out of InstallSkill
// so every harness target stays policy-free (C4.3) and so a caller that has
// already decided to overwrite (the verb after --force) needs no second
// flag here.
func InstallSkill(name string, m manifest.Manifest) (string, error) {
	files, err := GenerateSkill(name, m)
	if err != nil {
		return "", err
	}

	dir, err := SkillDir(name)
	if err != nil {
		return "", err
	}

	for path, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", fmt.Errorf("claudecode: creating %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return "", fmt.Errorf("claudecode: writing %q: %w", full, err)
		}
	}

	stamp := manifest.Stamp{
		ToolVersion:   m.Tool.Version,
		SchemaVersion: m.SchemaVersion,
		Files:         manifest.ChecksumFiles(files),
	}
	if err := manifest.WriteStamp(dir, stamp); err != nil {
		return "", err
	}

	return dir, nil
}

// UninstallSkill removes exactly the stamped tree at SkillDir(name) — never
// more (C4.7). It refuses, rather than silently proceeding, on any of
// Status's three unsafe states (hand-edited, foreign, or unreadable
// content): projections are never hand-authored, and this must not
// silently delete something a human put there by hand. Status is asked
// with a nil files map — uninstall has nothing to generate, so a stale
// tree (C4.6's binary-vs-stamp question) reads as Current and is removed
// like any other current tree; uninstall has no --force, so Refusal's
// remedy here always names removing the tree by hand instead.
func UninstallSkill(name string) (string, error) {
	dir, err := SkillDir(name)
	if err != nil {
		return "", err
	}

	s, err := harness.Status(dir, nil)
	if err != nil {
		return "", err
	}
	if s == harness.Missing {
		return "", clasterr.New("not-found.harness-not-installed",
			fmt.Sprintf("not found — no %s %s skill is installed at %s", Name, name, dir))
	}
	if err := harness.Refusal(Name, dir, s, "remove it by hand if that was intentional"); err != nil {
		return "", err
	}

	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("claudecode: removing %q: %w", dir, err)
	}
	return dir, nil
}
