package claudecode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/cli"
	"github.com/procrastivity/clast/internal/harness/claudecode"
	"github.com/procrastivity/clast/internal/iostreams"
	toolmanifest "github.com/procrastivity/clast/internal/manifest"
)

// fixedBuild is the buildinfo.Info every SKILL.md golden renders against —
// a fixed version string, since the manifest's tool.version rides into
// SKILL.md's own generated header (claudecode.go's renderSkillMD) and
// must stay constant for the golden's bytes to be reproducible across
// runs and machines.
var fixedBuild = buildinfo.Info{Version: "v0.0.0-test"}

// skillMDManifest builds the exact manifest.Manifest `clast manifest
// --json` emits, off the real root command (internal/cli.NewRootCommand,
// the chassis's single registration point) — the same helper
// skilltable_test.go's realManifest builds, kept local to this file since
// that one is unexported.
func skillMDManifest(t *testing.T) toolmanifest.Manifest {
	t.Helper()
	// F3: GenerateSkill resolves each skill's description/judgment
	// through the asset chain (C5.1), which falls back to
	// $XDG_CONFIG_HOME/clast (or $HOME/.config/clast when unset) when no
	// override is set here. Left unpinned, a host that happens to carry
	// its own claude-code skill override under that directory would
	// silently change this golden's bytes — isolate every caller of this
	// helper (both tests in this file) to a fresh, empty override dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := cli.NewRootCommand(iostreams.System(), fixedBuild)
	m, err := toolmanifest.Build(root, fixedBuild)
	if err != nil {
		t.Fatalf("toolmanifest.Build: %v", err)
	}
	return m
}

// TestGenerateSkill_Golden renders every projected skill's SKILL.md
// (GenerateSkill) against the real manifest and pins each one's assembled
// bytes — frontmatter, generated header, this skill's own judgment
// prose, and the shared plumbing verb table — against a golden file
// (SURFACE V32, C4.4). Regenerate with:
//
//	go test ./internal/harness/claudecode -run TestGenerateSkill_Golden -update
func TestGenerateSkill_Golden(t *testing.T) {
	m := skillMDManifest(t)

	for _, name := range claudecode.SkillNames {
		t.Run(name, func(t *testing.T) {
			files, err := claudecode.GenerateSkill(name, m)
			if err != nil {
				t.Fatalf("GenerateSkill(%q): %v", name, err)
			}
			got, ok := files["SKILL.md"]
			if !ok {
				t.Fatalf("GenerateSkill(%q) = %+v, want a \"SKILL.md\" entry", name, files)
			}
			goldenCompare(t, "skillmd/"+name+".SKILL.md", got)
		})
	}
}

// TestGenerateSkill_DescriptionWithColon_YAMLQuoted is F5's regression
// test: renderSkillMD used to emit `description: %s` unquoted, so a
// description override containing ": " (allowed — resolveSkillDescription
// only rejects empty or multi-line values) would reparse as a nested YAML
// mapping key rather than plain scalar text, breaking frontmatter for any
// YAML-aware consumer. This plants such an override on wake and asserts
// the rendered frontmatter still parses as YAML with description carried
// through exactly, byte for byte.
func TestGenerateSkill_DescriptionWithColon_YAMLQuoted(t *testing.T) {
	xdg := t.TempDir()
	override := filepath.Join(xdg, "clast", "claude-code", "skills", "wake", "description.txt")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatal(err)
	}
	const desc = `Use when the task needs: focused review, or a "quoted" phrase, or a \backslash.`
	if err := os.WriteFile(override, []byte(desc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not skillMDManifest(t): that helper pins its own fresh
	// XDG_CONFIG_HOME (F3), which would clobber the override just
	// written above. This test needs the opposite — its override must
	// win — so it builds the manifest inline instead.
	t.Setenv("XDG_CONFIG_HOME", xdg)
	root := cli.NewRootCommand(iostreams.System(), fixedBuild)
	m, err := toolmanifest.Build(root, fixedBuild)
	if err != nil {
		t.Fatalf("toolmanifest.Build: %v", err)
	}
	files, err := claudecode.GenerateSkill("wake", m)
	if err != nil {
		t.Fatalf("GenerateSkill: %v", err)
	}
	got, ok := files["SKILL.md"]
	if !ok {
		t.Fatalf("GenerateSkill(wake) = %+v, want a SKILL.md entry", files)
	}

	parts := strings.SplitN(string(got), "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("SKILL.md = %s, want a --- delimited frontmatter block", got)
	}
	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		t.Fatalf("frontmatter did not parse as YAML: %v\nfrontmatter:\n%s", err, parts[1])
	}
	if frontmatter.Description != desc {
		t.Errorf("parsed description = %q, want %q", frontmatter.Description, desc)
	}
}

// TestGenerateSkill_UnknownSkill asserts GenerateSkill refuses a name
// outside SkillNames rather than silently rendering something.
func TestGenerateSkill_UnknownSkill(t *testing.T) {
	m := skillMDManifest(t)
	if _, err := claudecode.GenerateSkill("no-such-skill", m); err == nil {
		t.Fatal("GenerateSkill(\"no-such-skill\") returned no error")
	}
}
