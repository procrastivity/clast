package claudecode_test

import (
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/buildinfo"
	"github.com/procrastivity/clast/internal/cli"
	"github.com/procrastivity/clast/internal/harness/claudecode"
	"github.com/procrastivity/clast/internal/iostreams"
	toolmanifest "github.com/procrastivity/clast/internal/manifest"
)

// realManifest builds the exact manifest.Manifest `clast manifest --json`
// emits, off the real root command (internal/cli.NewRootCommand, the
// chassis's single registration point) — so this test pins the generated
// table against the same source of truth the binary's own manifest walk
// uses, never a hand-maintained stand-in list of verbs.
func realManifest(t *testing.T) toolmanifest.Manifest {
	t.Helper()
	root := cli.NewRootCommand(iostreams.System(), buildinfo.Info{Version: "test"})
	m, err := toolmanifest.Build(root, buildinfo.Info{Version: "test"})
	if err != nil {
		t.Fatalf("toolmanifest.Build: %v", err)
	}
	return m
}

// wantPlumbingVerbs is every verb SURFACE V32 places in the `plumbing`
// namespace as of this Matter's seed cards (query-verbs, state-verbs,
// registry, shape-documents) — the exhaustive namespace list, so
// TestPlumbingVerbTable_EveryNamespaceVerbPresent fails the moment a
// listed verb goes missing from the table. That test only checks presence
// of what is listed here, so it does not by itself catch a real namespace
// verb added since without this list catching up to name it; that
// guarantee comes from skillmd_test.go's SKILL.md byte-goldens instead —
// a new verb changes PlumbingVerbTable's rendered bytes, which are
// embedded verbatim in every skill's golden, so those tests fail first
// and force this list to catch up.
var wantPlumbingVerbs = []string{
	"plumbing capture",
	"plumbing whereami",
	"plumbing projects",
	"plumbing clones",
	"plumbing sessions",
	"plumbing show",
	"plumbing breadcrumbs",
	"plumbing stats",
	"plumbing curate",
	"plumbing dismiss",
	"plumbing undismiss",
	"plumbing asset",
	"plumbing wake",
	"plumbing brief",
	"plumbing retro",
}

// nonPlumbingVerbs are top-level porcelain verbs that must never appear in
// the generated table, several of which are deliberately kind=plumbing
// (deterministic) despite sitting outside the namespace (V32) — the exact
// case harness.Projectable's structural filter exists to get right.
var nonPlumbingVerbs = []string{
	"init",
	"breadcrumb",
	"label",
	"adopt",
	"relink",
	"doctor",
	"install",
	"uninstall",
	"version",
	"manifest",
}

// TestPlumbingVerbTable_EveryNamespaceVerbPresent pins the table against
// the real manifest: every verb under the `plumbing` namespace appears as
// its own row, and none is missing.
func TestPlumbingVerbTable_EveryNamespaceVerbPresent(t *testing.T) {
	m := realManifest(t)
	table := claudecode.PlumbingVerbTable(m)

	for _, name := range wantPlumbingVerbs {
		invocation := "`" + m.Tool.Name + " " + name + "`"
		if !strings.Contains(table, invocation) {
			t.Errorf("table missing a row for %q (looked for %s)\n%s", name, invocation, table)
		}
	}
}

// TestPlumbingVerbTable_UsageVerbatim asserts each row's usage cell
// reproduces the manifest's own Usage string exactly (C3.8) — a consumer
// reading only the generated skill must see the same positional-argument
// declaration `clast manifest --json` does.
func TestPlumbingVerbTable_UsageVerbatim(t *testing.T) {
	m := realManifest(t)
	table := claudecode.PlumbingVerbTable(m)

	byName := map[string]toolmanifest.Verb{}
	for _, v := range m.Verbs {
		byName[v.Name] = v
	}

	for _, name := range wantPlumbingVerbs {
		v, ok := byName[name]
		if !ok {
			t.Fatalf("manifest carries no verb %q — wantPlumbingVerbs is out of date", name)
		}

		row := rowFor(t, table, m.Tool.Name+" "+name)
		if v.Usage == "" {
			continue
		}
		wantCell := "`" + v.Usage + "`"
		if !strings.Contains(row, wantCell) {
			t.Errorf("row for %q = %q, want usage cell %s verbatim", name, row, wantCell)
		}
	}
}

// TestPlumbingVerbTable_ExcludesNonNamespaceVerbs asserts the table never
// carries a row for a top-level porcelain verb, kind=plumbing ones
// included — the structural (V32), not kind-based, reading of the
// projectable filter.
func TestPlumbingVerbTable_ExcludesNonNamespaceVerbs(t *testing.T) {
	m := realManifest(t)
	table := claudecode.PlumbingVerbTable(m)

	for _, name := range nonPlumbingVerbs {
		invocation := "`" + m.Tool.Name + " " + name + "`"
		if strings.Contains(table, invocation) {
			t.Errorf("table carries a row for %q, which sits outside the plumbing namespace (V32)", name)
		}
	}
}

// TestPlumbingVerbTable_Empty pins the empty-manifest case: no placeholder
// row, no panic.
func TestPlumbingVerbTable_Empty(t *testing.T) {
	table := claudecode.PlumbingVerbTable(toolmanifest.Manifest{Tool: toolmanifest.Tool{Name: "clast"}})
	if !strings.Contains(table, "no plumbing verbs registered yet") {
		t.Fatalf("PlumbingVerbTable(empty) = %q, want the empty-namespace message", table)
	}
}

// rowFor returns the one markdown table line naming invocation, failing
// the test if it is missing or duplicated.
func rowFor(t *testing.T, table, invocation string) string {
	t.Helper()
	var found string
	count := 0
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "`"+invocation+"`") {
			found = line
			count++
		}
	}
	if count != 1 {
		t.Fatalf("table has %d row(s) naming %q, want exactly 1\n%s", count, invocation, table)
	}
	return found
}
