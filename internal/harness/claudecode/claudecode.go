// Package claudecode renders the tool's manifest into a Claude Code skill
// directory plus a .claude-plugin/plugin.json in the same directory — the
// "skills-dir as plugin" mechanism: no marketplace manifest, no separate
// registry entry. Everything in the generated tree traces back to the
// manifest or to an asset. Three hand-authored texts ride the asset chain
// as C4.4 describes: the per-harness judgment paragraph (this harness's
// own to revise), the shared agent-guidance paragraph (shared across every
// future harness target, and not this package's to rewrite), and
// skillDescriptionAsset below, the most tunable of the three, since it
// decides whether an agent loads the skill at all (C5.1). The rest is
// fixed template text around manifest fields — the SKILL.md headings and
// renderPluginJSON's "...'s own generated plumbing-verb skill." format
// string — which C4.4 counts as generated output, not hand-written
// content (T27). Rejected: moving that format string to an asset. It is
// not prose, and an override of it could make plugin.json invalid.
package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/harness"
	"github.com/procrastivity/clast/internal/manifest"
)

// Name is this harness's install-target name, as passed to `clast
// install <harness>` / `clast uninstall <harness>`.
const Name = "claude-code"

// skillName is the directory name the generated skill is installed under.
const skillName = "clast"

// skillDescriptionAsset is the SKILL.md frontmatter description: one
// sentence naming the situations this skill triggers on, not the verbs it
// exposes (those are generated, in the table below). Judgment prose by
// function, resolved through the asset chain like judgmentAsset (C4.4)
// rather than kept inline as a Go constant — it is the most tunable prose
// in the projection, since it alone decides whether an agent loads the
// skill at all (C5.1).
const skillDescriptionAsset = "templates/skills/claude-code/description.txt"

// judgmentAsset is the per-harness judgment paragraph, this harness's own
// hand-authored prose, resolved through the asset chain (C4.4).
const judgmentAsset = "templates/skills/claude-code/judgment.md"

// guidanceAsset is the shared agent-guidance paragraph, seeded at the top
// level of the shipped asset tree and projected here verbatim. It is
// hand-written like judgmentAsset and skillDescriptionAsset, but it is
// shared across every harness target rather than owned by this one.
const guidanceAsset = "agent-guidance.md"

// SkillsDirEnv is an environment variable that, when set, overrides
// SkillsDir()'s result — a test seam only (C2.6), never read for any other
// purpose.
const SkillsDirEnv = "CLAST_CLAUDE_SKILLS_DIR"

var userHomeDir = os.UserHomeDir

// rootDir returns Claude Code's own root config directory under home,
// ~/.claude — the directory both SkillsDir and Available key off of.
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

// InstallDir returns the directory the generated skill is written to and
// read back from: SkillsDir()/clast.
func InstallDir() (string, error) {
	dir, err := SkillsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, skillName), nil
}

// Generate renders m into the generated tree's content, keyed by path
// relative to InstallDir(). It filters m's verbs to the plumbing subset
// (harness.Projectable, C4.3) before rendering — the manifest's own full
// verb list is never projected as-is.
func Generate(m manifest.Manifest) (map[string][]byte, error) {
	verbs := harness.Projectable(m.Verbs)

	description, err := resolveSkillDescription()
	if err != nil {
		return nil, err
	}
	judgment, err := asset.Resolve(judgmentAsset)
	if err != nil {
		return nil, fmt.Errorf("claudecode: resolving judgment template: %w", err)
	}
	guidance, err := asset.Resolve(guidanceAsset)
	if err != nil {
		return nil, fmt.Errorf("claudecode: resolving agent-guidance partial: %w", err)
	}

	pluginJSON, err := renderPluginJSON(m)
	if err != nil {
		return nil, fmt.Errorf("claudecode: rendering plugin.json: %w", err)
	}

	return map[string][]byte{
		"SKILL.md":                   renderSkillMD(m, verbs, description, judgment.Bytes(), guidance.Bytes()),
		".claude-plugin/plugin.json": pluginJSON,
	}, nil
}

// resolveSkillDescription resolves skillDescriptionAsset and returns the
// value of SKILL.md's `description:` frontmatter key. That key holds one
// line, so an empty or multi-line override is an error rather than broken
// frontmatter. Rejected: joining the lines with spaces, which would install
// a trigger sentence the override's author never wrote. Only trailing
// whitespace, such as the newline a text file ends with, is trimmed.
//
// A malformed value is validation.skill-description (exit 1, C2.5): the
// shipped asset is one line, so only a user override can break it.
func resolveSkillDescription() (string, error) {
	resolved, err := asset.Resolve(skillDescriptionAsset)
	if err != nil {
		return "", fmt.Errorf("claudecode: resolving skill description: %w", err)
	}
	desc := strings.TrimRight(string(resolved.Bytes()), "\n\r\t ")
	if desc == "" || strings.ContainsAny(desc, "\n\r") {
		return "", clasterr.New("validation.skill-description",
			fmt.Sprintf("skill description asset %q must be exactly one non-empty line; fix or remove the override under the clast config directory", skillDescriptionAsset))
	}
	return desc, nil
}

func renderSkillMD(m manifest.Manifest, verbs []manifest.Verb, description string, judgment, guidance []byte) []byte {
	var b strings.Builder

	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %s\n", skillName)
	fmt.Fprintf(&b, "description: %s\n", description)
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", m.Tool.Name)
	fmt.Fprintf(&b, "Generated from %s %s (schema %d) — never hand-edit; re-run `%s install %s` after upgrading.\n\n",
		m.Tool.Name, m.Tool.Version, m.SchemaVersion, m.Tool.Name, Name)

	b.Write(judgment)
	fmt.Fprintf(&b, "\n\n")

	fmt.Fprintf(&b, "## Verbs\n\n")
	if len(verbs) == 0 {
		fmt.Fprintf(&b, "(none registered yet)\n\n")
	} else {
		fmt.Fprintf(&b, "| Verb | Description |\n|---|---|\n")
		for _, v := range verbs {
			desc := v.Description
			if desc == "" {
				desc = "(no description)"
			}
			invocation := strings.TrimSpace(m.Tool.Name + " " + v.Name + " " + v.Usage)
			fmt.Fprintf(&b, "| `%s` | %s |\n", invocation, desc)
		}
		fmt.Fprintf(&b, "\n")
	}

	b.Write(guidance)
	fmt.Fprintf(&b, "\n")

	return []byte(b.String())
}

// pluginManifest is the minimal .claude-plugin/plugin.json shape (the
// skills-dir-as-plugin mechanism). Hooks/agents/MCP sections are left
// absent until a real need exists — the mechanism costs nothing to leave
// empty. A lifecycle hook, when one is earned, rides here as the smallest
// shim that invokes the binary (C4.10).
type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

func renderPluginJSON(m manifest.Manifest) ([]byte, error) {
	p := pluginManifest{
		Name:        skillName,
		Description: fmt.Sprintf("%s's own generated plumbing-verb skill.", m.Tool.Name),
		Version:     m.Tool.Version,
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
