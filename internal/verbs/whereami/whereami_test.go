package whereami

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

var ctx = context.Background()

// --- git fixture helpers, ported from registry's testutil_test.go (no
// mocks for anything git-backed) ---

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

func addWorktree(t *testing.T, mainDir, worktreeDir, branch string) string {
	t.Helper()
	runGit(t, mainDir, "commit", "--allow-empty", "-q", "-m", "init")
	runGit(t, mainDir, "worktree", "add", "-q", "-b", branch, worktreeDir)
	return worktreeDir
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

func registerClone(t *testing.T, root, slug, machine, commonDir, label string) journal.Project {
	t.Helper()
	project := journal.Project{ID: registry.NewSource().Next(), Slug: slug}
	if err := journal.WriteProject(root, slug, project); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	clone := journal.Clone{ID: registry.NewSource().Next(), GitCommonDir: commonDir, Label: label}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{Machine: machine, Clones: []journal.Clone{clone}}); err != nil {
		t.Fatalf("WriteClones: %v", err)
	}
	return project
}

// TestRun_MainWorktree confirms a registered clone's main worktree
// resolves with Worktree "" and a real branch name.
func TestRun_MainWorktree(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")

	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	machine := localMachine(t)
	registerClone(t, root, "widget", machine, commonDir, "dev")

	result, err := Run(ctx, root, dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Project.Slug != "widget" {
		t.Errorf("Project.Slug = %q, want %q", result.Project.Slug, "widget")
	}
	if result.Clone.Label != "dev" {
		t.Errorf("Clone.Label = %q, want %q", result.Clone.Label, "dev")
	}
	if result.Worktree != "" {
		t.Errorf("Worktree = %q, want \"\" (main worktree)", result.Worktree)
	}
	if result.Branch == "" {
		t.Error("Branch = \"\", want the repo's default branch name")
	}
	if result.Machine != machine {
		t.Errorf("Machine = %q, want %q", result.Machine, machine)
	}
}

// TestRun_LinkedWorktree confirms a linked worktree resolves to the same
// owning clone, with Worktree filled by GitDir's own basename (M17).
func TestRun_LinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := newRepo(t, "widget")

	commonDir, err := registry.CommonDir(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	machine := localMachine(t)
	registerClone(t, root, "widget", machine, commonDir, "dev")

	wt := addWorktree(t, main, main+"-feature", "feature")

	result, err := Run(ctx, root, wt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Worktree != filepath.Base(wt) {
		t.Errorf("Worktree = %q, want %q", result.Worktree, filepath.Base(wt))
	}
	if result.Branch != "feature" {
		t.Errorf("Branch = %q, want %q", result.Branch, "feature")
	}
}

// TestRun_DetachedHEAD confirms Branch's "" sentinel surfaces through Run
// unchanged.
func TestRun_DetachedHEAD(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	runGit(t, dir, "checkout", "-q", "--detach", "HEAD")

	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	registerClone(t, root, "widget", localMachine(t), commonDir, "dev")

	result, err := Run(ctx, root, dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Branch != "" {
		t.Errorf("Branch = %q, want \"\" (detached HEAD)", result.Branch)
	}
}

// TestRun_UnregisteredClone_RefusesUnknownClone confirms a real git repo
// that was never registered maps to refusal.unknown-clone, naming
// `clast init` as the fix.
func TestRun_UnregisteredClone_RefusesUnknownClone(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "unregistered")

	_, err := Run(ctx, root, dir)
	if err == nil {
		t.Fatal("Run succeeded on an unregistered clone, want refusal.unknown-clone")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != "refusal.unknown-clone" {
		t.Errorf("error code = %q, want %q", cerr.Code, "refusal.unknown-clone")
	}
	if !strings.Contains(cerr.Message, "clast init") {
		t.Errorf("message = %q, want it to name `clast init`", cerr.Message)
	}
}

// TestRun_NotAGitRepo confirms a cwd outside any git repository maps to a
// structured validation.not-a-git-repo, the same code init raises, rather
// than surfacing the raw git error.
func TestRun_NotAGitRepo(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	_, err := Run(ctx, root, dir)
	if err == nil {
		t.Fatal("Run succeeded outside a git repository, want validation.not-a-git-repo")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != "validation.not-a-git-repo" {
		t.Errorf("error code = %q, want %q", cerr.Code, "validation.not-a-git-repo")
	}
}
