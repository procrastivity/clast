package claudecode_test

import (
	"testing"

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

// TestGenerateSkill_UnknownSkill asserts GenerateSkill refuses a name
// outside SkillNames rather than silently rendering something.
func TestGenerateSkill_UnknownSkill(t *testing.T) {
	m := skillMDManifest(t)
	if _, err := claudecode.GenerateSkill("no-such-skill", m); err == nil {
		t.Fatal("GenerateSkill(\"no-such-skill\") returned no error")
	}
}
