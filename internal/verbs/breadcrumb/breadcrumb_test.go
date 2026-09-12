package breadcrumb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

var ctx = context.Background()

// --- git fixture helpers, ported per-package the way whereami's own
// tests do (no mocks for anything git-backed) ---

func newRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func localMachine(t *testing.T) string {
	t.Helper()
	m, err := journal.Hostname()
	if err != nil {
		t.Fatalf("journal.Hostname: %v", err)
	}
	return m
}

func registerClone(t *testing.T, root, slug, machine, commonDir, label string) {
	t.Helper()
	project := journal.Project{ID: registry.NewSource().Next(), Slug: slug}
	if err := journal.WriteProject(root, slug, project); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	clone := journal.Clone{ID: registry.NewSource().Next(), GitCommonDir: commonDir, Label: label}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{Machine: machine, Clones: []journal.Clone{clone}}); err != nil {
		t.Fatalf("WriteClones: %v", err)
	}
}

func TestRun_RegisteredClone_ScopesToItsProject(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")

	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	registerClone(t, root, "widget", localMachine(t), commonDir, "dev")

	now := time.Date(2026, 9, 11, 10, 22, 0, 0, time.UTC)
	b, err := Run(ctx, root, dir, "check migration before deploy", false, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if b.Slug == nil || *b.Slug != "widget" {
		t.Errorf("Slug = %v, want %q", b.Slug, "widget")
	}
	if b.Text != "check migration before deploy" {
		t.Errorf("Text = %q, want %q", b.Text, "check migration before deploy")
	}

	entries, _, err := journal.ReadBreadcrumbs(root, "2026-09-11")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "check migration before deploy" {
		t.Fatalf("ReadBreadcrumbs = %+v, want the one crumb just written", entries)
	}
}

func TestRun_Global_WritesSlugNullRegardlessOfCwd(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	registerClone(t, root, "widget", localMachine(t), commonDir, "dev")

	now := time.Date(2026, 9, 11, 14, 2, 0, 0, time.UTC)
	b, err := Run(ctx, root, dir, "bump the cache version", true, now)
	if err != nil {
		t.Fatalf("Run(global=true): %v", err)
	}
	if b.Slug != nil {
		t.Errorf("Slug = %v, want nil (global)", b.Slug)
	}
}

func TestRun_UnregisteredClone_RefusesUnknownClone(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "unregistered")

	_, err := Run(ctx, root, dir, "a note", false, time.Now())
	assertUnknownClone(t, err)
}

func TestRun_NotAGitRepo_AlsoRefusesUnknownClone(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	_, err := Run(ctx, root, dir, "a note", false, time.Now())
	assertUnknownClone(t, err)
}

func TestRun_Global_NeverFailsEvenOutsideAnyGitRepo(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	_, err := Run(ctx, root, dir, "a note", true, time.Now())
	if err != nil {
		t.Fatalf("Run(global=true) outside any git repo: %v, want success", err)
	}
}

func assertUnknownClone(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Run: want refusal.unknown-clone")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "refusal.unknown-clone" {
		t.Errorf("error code = %q, want refusal.unknown-clone", ce.Code)
	}
	if !strings.Contains(ce.Message, "--global") || !strings.Contains(ce.Message, "clast init") {
		t.Errorf("message = %q, want it to name both --global and `clast init`", ce.Message)
	}
}
