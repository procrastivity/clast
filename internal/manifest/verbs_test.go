package manifest

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/surface"
)

// TestUsageArgsTable pins that usage carries the positional portion of a
// verb's Use line and nothing else — never the verb's own name, never a
// stray separator. The multi-argument row is the one a rule that took only
// the last word would get wrong.
func TestUsageArgsTable(t *testing.T) {
	cases := []struct{ use, want string }{
		{"new <name>", "<name>"},
		{"check [path]", "[path]"},
		{"install <harness>", "<harness>"},
		{"version", ""},
		{"copy <src> <dst>", "<src> <dst>"},
	}
	for _, c := range cases {
		cmd := &cobra.Command{Use: c.use}
		if got := usageArgs(cmd); got != c.want {
			t.Errorf("usageArgs(%q) = %q, want %q", c.use, got, c.want)
		}
	}
}

// TestWalkVerbs_RecordsUsage checks the field reaches the manifest through
// the walk, not just through the helper: a verb that declares a positional
// must be distinguishable in the manifest from one that takes none. That
// distinction is the whole reason C3.8 exists — the harness projection
// renders from this field, and without it an agent reading the generated
// skill cannot tell that a required argument is required.
func TestWalkVerbs_RecordsUsage(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	for _, use := range []string{"new <name>", "version"} {
		child := &cobra.Command{Use: use, Run: func(*cobra.Command, []string) {}}
		surface.Annotate(child, surface.Plumbing)
		root.AddCommand(child)
	}

	verbs, err := walkVerbs(root)
	if err != nil {
		t.Fatalf("walkVerbs: %v", err)
	}
	got := map[string]string{}
	for _, v := range verbs {
		got[v.Name] = v.Usage
	}
	if got["new"] != "<name>" {
		t.Errorf("verb new: usage = %q, want %q", got["new"], "<name>")
	}
	if got["version"] != "" {
		t.Errorf("verb version: usage = %q, want empty", got["version"])
	}
}

// TestWalkVerbs_RefusesAnUnannotatedVerb covers C3.2's hard error, the
// clause's actual enforcement point. The manifest e2e test can only assert
// that every registered verb carries a kind; it cannot reach the refusal,
// because a verb missing one never gets committed.
func TestWalkVerbs_RefusesAnUnannotatedVerb(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.AddCommand(&cobra.Command{Use: "orphan", Run: func(*cobra.Command, []string) {}})

	if _, err := walkVerbs(root); err == nil {
		t.Fatalf("walkVerbs accepted a verb with no surface kind; C3.2 requires a hard error")
	}
}
