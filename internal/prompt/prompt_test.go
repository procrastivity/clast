package prompt_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/prompt"
)

// update regenerates every golden file from the current asset content and
// fixture data instead of comparing against them — `go test ./internal/prompt/... -run TestRender -update`.
// A run with -update always passing is not itself a green test: it exists
// so a deliberate template or fixture change can refresh the goldens in
// one step, the diff of which is exactly the drift this suite exists to
// make visible (llm-verbs/step-02).
var update = flag.Bool("update", false, "update golden files")

// goldenPairCase is one of the three shipped prompt pairs under golden
// coverage: its Pair, a small fixed fixture payload standing in for the
// real plumbing wake/brief/retro --json facts (SURFACE V8), and where its
// golden files live.
type goldenPairCase struct {
	name string
	pair prompt.Pair
	data map[string]string
}

func goldenCases() []goldenPairCase {
	return []goldenPairCase{
		{
			name: "wake-draft",
			pair: prompt.WakeDraft,
			data: map[string]string{
				"project":     "clast",
				"branch":      "go",
				"start":       "2026-09-10T09:00:00Z",
				"end":         "2026-09-10T11:30:00Z",
				"msg_count":   "42",
				"transcript":  "USER: fix the wake ordering bug\nASSISTANT: found it — groupByProject sorted by first appearance, not recency. Fixed.",
				"breadcrumbs": "- 09:15 remember to check the stale-curated path before lunch",
			},
		},
		{
			name: "brief",
			pair: prompt.Brief,
			data: map[string]string{
				"project":       "clast",
				"current_label": "dev",
				"entries":       "## Workspace: dev\n\n### Fixed wake ordering (2026-09-09, sess-01)\n\nGroups now sort by most-recently-active project.",
				"breadcrumbs":   "- 09:15 remember to check the stale-curated path before lunch",
				"sessions":      "- sess-02  curated  Wired the brief empty-state guard",
			},
		},
		{
			name: "retro-summary",
			pair: prompt.RetroSummary,
			data: map[string]string{
				"project":    "clast",
				"started_at": "2026-09-10T09:00:00Z",
				"day":        "2026-09-10",
				"session_id": "sess-01",
				"body":       "## What shipped\n- Fixed wake ordering\n\n## Open threads\n- brief empty-state guard still needs a test",
			},
		},
	}
}

func goldenDir(name string) string {
	return filepath.Join("testdata", name)
}

// TestRender_Golden renders each shipped prompt pair against its fixture
// payload and compares the result byte-for-byte against testdata/<pair>/
// {system,user}.golden — so a diff in either the shipped template content
// (assets/prompts/*.md) or Render/Fill's own logic shows up as a failing
// test, per llm-verbs/step-02's whole point.
func TestRender_Golden(t *testing.T) {
	// Isolate from whatever the host running this suite has at its own
	// $XDG_CONFIG_HOME/clast/prompts/ — this test pins the embedded/shipped
	// content, not a real user's override (internal/verbs/asset's own
	// tests take the same precaution).
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := prompt.Render(tc.pair, tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			systemGolden := filepath.Join(goldenDir(tc.name), "system.golden")
			userGolden := filepath.Join(goldenDir(tc.name), "user.golden")

			if *update {
				writeGolden(t, systemGolden, rendered.System)
				writeGolden(t, userGolden, rendered.User)
				return
			}

			wantSystem := readGolden(t, systemGolden)
			if rendered.System != wantSystem {
				t.Errorf("system prompt drifted from %s\ngot:\n%s\nwant:\n%s", systemGolden, rendered.System, wantSystem)
			}

			wantUser := readGolden(t, userGolden)
			if rendered.User != wantUser {
				t.Errorf("user prompt drifted from %s\ngot:\n%s\nwant:\n%s", userGolden, rendered.User, wantUser)
			}

			if rendered.SystemSource != asset.SourceEmbedded {
				t.Errorf("SystemSource = %v, want %v (no installed share tree or override in this test)", rendered.SystemSource, asset.SourceEmbedded)
			}
			if rendered.UserSource != asset.SourceEmbedded {
				t.Errorf("UserSource = %v, want %v (no installed share tree or override in this test)", rendered.UserSource, asset.SourceEmbedded)
			}
		})
	}
}

// TestRender_OverrideLiveness asserts the M18 chain-liveness fact this step
// is asked to pin down specifically for prompts: a template resolved
// through an $XDG_CONFIG_HOME override renders the override's own content,
// with no re-install and no code change — the same posture
// internal/verbs/asset's own override tests already pin for `plumbing
// asset`, exercised here through Render instead.
func TestRender_OverrideLiveness(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	overridePath := filepath.Join(xdg, "clast", "prompts", "wake-draft-system.md")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o755); err != nil {
		t.Fatal(err)
	}
	overrideContent := "override system prompt — a user's own drafting instructions.\n"
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	rendered, err := prompt.Render(prompt.WakeDraft, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rendered.SystemSource != asset.SourceOverride {
		t.Errorf("SystemSource = %v, want %v", rendered.SystemSource, asset.SourceOverride)
	}
	if rendered.System != overrideContent {
		t.Errorf("System = %q, want the override's own content %q", rendered.System, overrideContent)
	}
	// The user half has no override planted, so it still resolves through
	// to the embedded shipped default — shadow by name only touches the
	// one file actually overridden (C5.2), never the whole pair.
	if rendered.UserSource != asset.SourceEmbedded {
		t.Errorf("UserSource = %v, want %v (no override planted for the user half)", rendered.UserSource, asset.SourceEmbedded)
	}
}

// TestFill_UnknownNameLeftLiteral asserts Fill's own documented posture for
// a data key absent from the template's placeholder set: nothing panics,
// nothing is silently blanked, the unmatched token survives verbatim.
func TestFill_UnknownNameLeftLiteral(t *testing.T) {
	got := prompt.Fill("hello {{name}}, your {{thing}} is ready", map[string]string{"name": "beau"})
	want := "hello beau, your {{thing}} is ready"
	if got != want {
		t.Errorf("Fill = %q, want %q", got, want)
	}
}

func readGolden(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with -update to create it)", path, err)
	}
	return string(b)
}

func writeGolden(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating golden dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing golden %s: %v", path, err)
	}
}
