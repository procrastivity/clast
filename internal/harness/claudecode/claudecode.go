// Package claudecode renders the tool's manifest into Claude Code's own
// skills directory and splices a SessionStart hook into Claude Code's
// settings.json (SURFACE V32/V33). Three thin skills project — wake, brief,
// retro — one per shape; each is its own independently stamped tree
// (C4.4), exactly the single-tree convention the chassis established for
// its one skill, just run three times over. Every installed skill traces
// back to the manifest or to an asset: the per-skill judgment paragraph and
// skillDescriptionAsset ride the asset chain (C5.1, C4.4) as this harness's
// own hand-authored prose; the verb table (skilltable.go) is generated,
// derived from the same manifest.Manifest `clast manifest --json` emits, so
// a skill can never disagree with the binary about what a verb takes.
//
// Prompts and flows are never rendered into a skill (M18/V6): each skill's
// core instruction is to read `clast plumbing asset flows/<shape>.md` live
// and follow it, so an override applies with no re-install.
package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/manifest"
)

// Name is this harness's install-target name, as passed to `clast
// install <harness>` / `clast uninstall <harness>`.
const Name = "claude-code"

// SkillNames lists the three skills projected into Claude Code, in
// SURFACE V32's order — the exhaustive set install/uninstall/doctor walk.
// Each is its own install target (internal/harness/registry.Target),
// independently stamped.
var SkillNames = []string{"wake", "brief", "retro"}

// SkillsDirEnv is an environment variable that, when set, overrides
// SkillsDir()'s result — a test seam only (C2.6), never read for any other
// purpose.
const SkillsDirEnv = "CLAST_CLAUDE_SKILLS_DIR"

var userHomeDir = os.UserHomeDir

// rootDir returns Claude Code's own root config directory under home,
// ~/.claude — the directory both SkillsDir and Available key off of, and
// SettingsPath's parent.
func rootDir(home string) string {
	return filepath.Join(home, ".claude")
}

// SkillsDir returns the directory Claude Code loads skills from,
// ~/.claude/skills.
func SkillsDir() (string, error) {
	if dir := os.Getenv(SkillsDirEnv); dir != "" {
		return dir, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudecode: locating home directory: %w", err)
	}
	return filepath.Join(rootDir(home), "skills"), nil
}

// SkillDir returns the directory a named skill (one of SkillNames) is
// installed to and read back from: SkillsDir()/<name>. Each skill's
// directory is exclusively clast's — nothing else may live there — so
// harness.Status's generic ChecksumTree walk (a whole-directory comparison)
// never mistakes a sibling skill under SkillsDir() for drift in this one.
func SkillDir(name string) (string, error) {
	dir, err := SkillsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Available reports whether this harness appears to be present on this
// host: its root config directory (~/.claude) exists, or — when
// SkillsDirEnv overrides SkillsDir — that override directory exists. The
// root, not the skills subdirectory, is the signal: a fresh harness
// install may not have created its skills directory yet. A home-dir
// lookup failure counts as not available rather than an error — the bare
// install run treats an unavailable harness as a skip, and a probe should
// never abort that run.
func Available() bool {
	dir := os.Getenv(SkillsDirEnv)
	if dir == "" {
		home, err := userHomeDir()
		if err != nil {
			return false
		}
		dir = rootDir(home)
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// skillDescriptionAssetPath is the SKILL.md frontmatter description asset
// for the named skill: one sentence naming the situations this skill
// triggers on, not the verbs it exposes (those are generated, in the table
// PlumbingVerbTable renders). Judgment prose by function, resolved through
// the asset chain (C4.4) rather than kept inline as a Go constant — it is
// the most tunable prose in the projection, since it alone decides whether
// an agent loads the skill at all (C5.1).
func skillDescriptionAssetPath(name string) string {
	return "claude-code/skills/" + name + "/description.txt"
}

// skillJudgmentAssetPath is the named skill's own hand-authored judgment
// paragraph, resolved through the asset chain (C4.4).
func skillJudgmentAssetPath(name string) string {
	return "claude-code/skills/" + name + "/judgment.md"
}

// resolveSkillDescription resolves name's description asset and returns the
// value of its SKILL.md's `description:` frontmatter key. That key holds one
// line, so an empty or multi-line override is an error rather than broken
// frontmatter. Rejected: joining the lines with spaces, which would install
// a trigger sentence the override's author never wrote. Only trailing
// whitespace, such as the newline a text file ends with, is trimmed.
//
// A malformed value is validation.skill-description (exit 1, C2.5): the
// shipped asset is one line, so only a user override can break it.
func resolveSkillDescription(name string) (string, error) {
	assetPath := skillDescriptionAssetPath(name)
	resolved, err := asset.Resolve(assetPath)
	if err != nil {
		return "", fmt.Errorf("claudecode: resolving %s skill description: %w", name, err)
	}
	desc := strings.TrimRight(string(resolved.Bytes()), "\n\r\t ")
	if desc == "" || strings.ContainsAny(desc, "\n\r") {
		return "", clasterr.New("validation.skill-description",
			fmt.Sprintf("skill description asset %q must be exactly one non-empty line; fix or remove the override under the clast config directory", assetPath))
	}
	return desc, nil
}

// GenerateSkill renders name's SKILL.md content, keyed "SKILL.md" (relative
// to that skill's own SkillDir) — this skill's description and judgment
// assets, plus the generated plumbing verb table shared verbatim across
// every skill (skilltable.go). name must be one of SkillNames.
func GenerateSkill(name string, m manifest.Manifest) (map[string][]byte, error) {
	if !slices.Contains(SkillNames, name) {
		return nil, fmt.Errorf("claudecode: %q is not a projected skill (want one of %v)", name, SkillNames)
	}

	description, err := resolveSkillDescription(name)
	if err != nil {
		return nil, err
	}
	judgment, err := asset.Resolve(skillJudgmentAssetPath(name))
	if err != nil {
		return nil, fmt.Errorf("claudecode: resolving %s judgment template: %w", name, err)
	}

	return map[string][]byte{
		"SKILL.md": renderSkillMD(name, m, description, judgment.Bytes()),
	}, nil
}

// yamlQuoteScalar renders s as a YAML double-quoted flow scalar (F5):
// description rides into SKILL.md's YAML frontmatter unquoted before this
// fix, but description is not a Go constant — it resolves through the
// asset chain (C5.1), so a user override can contain arbitrary one-line
// text (resolveSkillDescription only rejects empty or multi-line values).
// An override containing ": " — e.g. "Use when the task needs: focused
// review" — would reparse as a nested YAML mapping under the description
// key instead of a plain scalar, breaking frontmatter a YAML-aware
// consumer needs to parse. Double-quoting is the cheapest sound fix here:
// tightening resolveSkillDescription's validation instead would mean
// deciding which of YAML's many special leading/embedded characters
// (":", "#", "&", "*", "!", "|", ">", "%", "@", leading "-", "?") to ban,
// an open-ended and still-incomplete list, versus quoting once here and
// being correct for all of them. Only backslash and the double-quote
// itself need escaping: resolveSkillDescription has already ruled out
// embedded newlines/carriage returns, the only other characters YAML's
// double-quoted style would otherwise ask to be escaped.
func yamlQuoteScalar(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// renderSkillMD assembles one skill's SKILL.md: frontmatter (name +
// description), a generated header naming the source manifest, this
// skill's judgment prose, and the generated plumbing verb table. Headings
// and the format strings around manifest fields are generated output, not
// hand-written content (C4.4, T27) — the only hand-authored bytes here are
// description and judgment, both resolved through the asset chain.
func renderSkillMD(name string, m manifest.Manifest, description string, judgment []byte) []byte {
	var b strings.Builder

	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", yamlQuoteScalar(description))
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# %s %s\n\n", m.Tool.Name, name)
	fmt.Fprintf(&b, "Generated from %s %s (schema %d) — never hand-edit; re-run `%s install %s` after upgrading.\n\n",
		m.Tool.Name, m.Tool.Version, m.SchemaVersion, m.Tool.Name, Name)

	b.Write(judgment)
	fmt.Fprintf(&b, "\n\n")

	fmt.Fprintf(&b, "## Verbs\n\n")
	b.WriteString(PlumbingVerbTable(m))

	return []byte(b.String())
}
