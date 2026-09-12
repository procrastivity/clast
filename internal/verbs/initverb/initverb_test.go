package initverb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
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

func addRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	runGit(t, dir, "remote", "add", name, url)
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

func wantClasterr(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatal("got nil error, want a *clasterr.Error")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != code {
		t.Fatalf("error code = %q, want %q (message: %s)", cerr.Code, code, cerr.Message)
	}
}

// hashTree hashes every file's relative path and content under root,
// deterministically, so a test can assert a registered write did (or did
// not) change anything on disk.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		files[rel] = data
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return "empty"
		}
		t.Fatalf("hashTree: %v", err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(files[p])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestInit_FreshCloneWithRemote_CreatesProjectAndClone is worked example 1's
// setup: a brand-new remote clone creates both a project and a clone.
func TestInit_FreshCloneWithRemote_CreatesProjectAndClone(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	result, err := Run(ctx, root, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCreated || !result.ProjectCreated {
		t.Fatalf("Outcome=%s ProjectCreated=%v, want created/true", result.Outcome, result.ProjectCreated)
	}
	if result.Project.Remote != "github.com/acme/widget" {
		t.Errorf("Remote = %q, want the normalized origin", result.Project.Remote)
	}
	if result.Project.Slug != "widget" {
		t.Errorf("Slug = %q, want %q (repo basename)", result.Project.Slug, "widget")
	}
	if result.Clone.Label != "widget" {
		t.Errorf("Label = %q, want %q (clone dir basename)", result.Clone.Label, "widget")
	}
	if result.Project.ID >= result.Clone.ID {
		t.Errorf("project id %q should sort before clone id %q (project minted first)", result.Project.ID, result.Clone.ID)
	}

	proj, ok, err := journal.ReadProject(root, "widget")
	if err != nil || !ok {
		t.Fatalf("ReadProject: ok=%v err=%v", ok, err)
	}
	if proj.ID != result.Project.ID {
		t.Errorf("persisted project id = %q, want %q", proj.ID, result.Project.ID)
	}
}

// TestInit_SecondCloneSameRemote_ReusesProject is worked example 1's
// widget-a/widget-b shape: a second clone of the same normalized remote
// joins the same project rather than minting a new one.
func TestInit_SecondCloneSameRemote_ReusesProject(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, "widget-a")
	addRemote(t, a, "origin", "git@github.com:acme/widget.git")
	b := newRepo(t, "widget-b")
	addRemote(t, b, "origin", "https://github.com/acme/widget.git")

	first, err := Run(ctx, root, a, Options{})
	if err != nil {
		t.Fatalf("Run(a): %v", err)
	}
	second, err := Run(ctx, root, b, Options{})
	if err != nil {
		t.Fatalf("Run(b): %v", err)
	}
	if second.ProjectCreated {
		t.Errorf("second run ProjectCreated = %v, want false (same project)", second.ProjectCreated)
	}
	if second.Outcome != OutcomeRegistered {
		t.Errorf("second run Outcome = %s, want %s", second.Outcome, OutcomeRegistered)
	}
	if first.Project.ID != second.Project.ID {
		t.Fatalf("two clones of the same normalized remote got different projects: %s vs %s", first.Project.ID, second.Project.ID)
	}
	if first.Clone.ID == second.Clone.ID {
		t.Fatal("two different clones ended up sharing one clone id")
	}
	if second.Clone.Label != "widget-b" {
		t.Errorf("second clone label = %q, want %q (its own dir basename)", second.Clone.Label, "widget-b")
	}
}

// TestInit_ForkDifferentRemote_TwoProjects is worked example 5: two clones
// of genuinely different remotes, neither using --identity-remote, resolve
// to two distinct projects.
func TestInit_ForkDifferentRemote_TwoProjects(t *testing.T) {
	root := t.TempDir()
	upstream := newRepo(t, "widget-upstream")
	addRemote(t, upstream, "origin", "git@github.com:acme/widget.git")
	fork := newRepo(t, "widget-fork")
	addRemote(t, fork, "origin", "git@github.com:someone/widget-fork.git")

	a, err := Run(ctx, root, upstream, Options{})
	if err != nil {
		t.Fatalf("Run(upstream): %v", err)
	}
	b, err := Run(ctx, root, fork, Options{})
	if err != nil {
		t.Fatalf("Run(fork): %v", err)
	}
	if a.Project.ID == b.Project.ID {
		t.Fatal("upstream and fork resolved to the same project, want distinct projects")
	}
}

// TestInit_LocalOnly_KeylessProject is worked example 4's setup: a repo
// with no remotes at all is the unambiguous local-only case — no refusal,
// an empty (keyless) Remote, IdentityRemote still recorded as the default.
func TestInit_LocalOnly_KeylessProject(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "local-only")

	result, err := Run(ctx, root, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.ProjectCreated {
		t.Error("ProjectCreated = false, want true")
	}
	if result.Project.Remote != "" {
		t.Errorf("Remote = %q, want empty (keyless local-only)", result.Project.Remote)
	}
	if result.Project.IdentityRemote != "origin" {
		t.Errorf("IdentityRemote = %q, want %q recorded even with no matching remote", result.Project.IdentityRemote, "origin")
	}
}

// TestInit_IdentityRemoteOverride is the origin-is-my-fork case:
// --identity-remote names which remote provides the project's identity.
func TestInit_IdentityRemoteOverride(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:me/widget-fork.git")
	addRemote(t, dir, "upstream", "git@github.com:acme/widget.git")

	result, err := Run(ctx, root, dir, Options{IdentityRemote: "upstream"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Project.Remote != "github.com/acme/widget" {
		t.Errorf("Remote = %q, want upstream's normal form", result.Project.Remote)
	}
	if result.Project.IdentityRemote != "upstream" {
		t.Errorf("IdentityRemote = %q, want %q", result.Project.IdentityRemote, "upstream")
	}
}

// TestInit_NoIdentityRemote_Refuses covers the ambiguous case: remotes
// exist, but none is named origin and no override was given.
func TestInit_NoIdentityRemote_Refuses(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "upstream", "git@github.com:acme/widget.git")

	_, err := Run(ctx, root, dir, Options{})
	wantClasterr(t, err, "validation.no-identity-remote")
}

// TestInit_UnresolvableIdentityRemote_Refuses covers --identity-remote
// naming a remote that does not exist on this clone.
func TestInit_UnresolvableIdentityRemote_Refuses(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	_, err := Run(ctx, root, dir, Options{IdentityRemote: "nonexistent"})
	wantClasterr(t, err, "validation.unresolvable-identity-remote")
}

// TestInit_LabelCollision_FallsToParentName confirms init runs the M16
// collision-suggestion ladder silently rather than refusing outright, when
// the default basename is already taken in the target project.
func TestInit_LabelCollision_FallsToParentName(t *testing.T) {
	root := t.TempDir()
	parentA := t.TempDir()
	first := filepath.Join(parentA, "widget")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, first, "init", "-q")
	runGit(t, first, "config", "user.email", "test@example.com")
	runGit(t, first, "config", "user.name", "test")
	addRemote(t, first, "origin", "git@github.com:acme/widget.git")
	if _, err := Run(ctx, root, first, Options{}); err != nil {
		t.Fatalf("Run(first): %v", err)
	}

	// A second clone whose directory basename also happens to be "widget"
	// (a sibling checkout under a differently-named parent) collides on
	// the default label and must fall through to the parent-dir tier.
	collisionParent := filepath.Join(t.TempDir(), "widget-collision-parent")
	if err := os.MkdirAll(collisionParent, 0o755); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(collisionParent, "widget")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, second, "init", "-q")
	runGit(t, second, "config", "user.email", "test@example.com")
	runGit(t, second, "config", "user.name", "test")
	addRemote(t, second, "origin", "https://github.com/acme/widget.git")

	result, err := Run(ctx, root, second, Options{})
	if err != nil {
		t.Fatalf("Run(second): %v", err)
	}
	if result.Clone.Label == "widget" {
		t.Fatal("second clone kept the colliding label, want the collision-suggestion ladder to have fired")
	}
	if !strings.Contains(result.Clone.Label, "widget-collision-parent") {
		t.Errorf("resolved label = %q, want it derived from the parent dir", result.Clone.Label)
	}
}

// TestInit_LabelLadderExhausted_Refuses confirms exhaustion of both ladder
// tiers is a hard refusal, never a third auto-suffixed form. The two
// colliding rows are injected directly through journal.WriteClones (the
// production write primitive, same "fixture built through production's own
// functions" posture registry's own tests take) under a different
// machine — cheaper and just as real as three more git repos, since the
// ladder only ever reads labels, never common-dirs.
func TestInit_LabelLadderExhausted_Refuses(t *testing.T) {
	root := t.TempDir()
	first := newRepo(t, "widget")
	addRemote(t, first, "origin", "git@github.com:acme/widget.git")
	firstResult, err := Run(ctx, root, first, Options{})
	if err != nil {
		t.Fatalf("Run(first): %v", err)
	}

	const parentName = "collision-parent"
	const comboName = "widget-" + parentName
	if err := journal.WriteClones(root, firstResult.Project.Slug, journal.ClonesFile{
		Machine: "other-machine",
		Clones: []journal.Clone{
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5FA1", GitCommonDir: "/fake/a/.git", Label: parentName},
			{ID: "01ARZ3NDEKTSV4RRFFQ69G5FA2", GitCommonDir: "/fake/b/.git", Label: comboName},
		},
	}); err != nil {
		t.Fatalf("WriteClones (fixture): %v", err)
	}

	fourthParent := filepath.Join(t.TempDir(), parentName)
	fourth := filepath.Join(fourthParent, "widget")
	if err := os.MkdirAll(fourth, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fourth, "init", "-q")
	runGit(t, fourth, "config", "user.email", "test@example.com")
	runGit(t, fourth, "config", "user.name", "test")
	addRemote(t, fourth, "origin", "https://github.com/acme/widget.git")

	before := hashTree(t, root)
	_, err = Run(ctx, root, fourth, Options{})
	wantClasterr(t, err, "validation.label-collision")
	after := hashTree(t, root)
	if before != after {
		t.Error("a refused init wrote to the journal tree; want no write on refusal")
	}
}

// TestInit_LabelFlagOverride confirms --label chooses this clone's
// registered label outright.
func TestInit_LabelFlagOverride(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	result, err := Run(ctx, root, dir, Options{Label: "my-custom-label"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Clone.Label != "my-custom-label" {
		t.Errorf("Label = %q, want %q", result.Clone.Label, "my-custom-label")
	}
}

// TestInit_LabelFlag_ULIDShaped_Refuses confirms a ULID-shaped --label
// refuses outright (M16 shape rule).
func TestInit_LabelFlag_ULIDShaped_Refuses(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	_, err := Run(ctx, root, dir, Options{Label: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	wantClasterr(t, err, "validation.label-ulid-shaped")
}

// TestInit_SlugFlagOverride confirms --slug names a newly-created
// project's slug outright.
func TestInit_SlugFlagOverride(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	result, err := Run(ctx, root, dir, Options{Slug: "my-custom-slug"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Project.Slug != "my-custom-slug" {
		t.Errorf("Slug = %q, want %q", result.Project.Slug, "my-custom-slug")
	}
	if _, ok, _ := journal.ReadProject(root, "my-custom-slug"); !ok {
		t.Error("project.json not written at the overridden slug's directory")
	}
}

// TestInit_SlugFlag_ULIDShaped_Refuses confirms a ULID-shaped --slug
// refuses outright (M16 shape rule, ported to the slug axis).
func TestInit_SlugFlag_ULIDShaped_Refuses(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	_, err := Run(ctx, root, dir, Options{Slug: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	wantClasterr(t, err, "validation.slug-ulid-shaped")
}

// TestInit_SlugCollisionDifferentRemote_Refuses: refuse-over-guess when a
// new clone's default (or explicit) slug names a directory already taken
// by an unrelated project (a different, or absent, remote) — never guess
// that they're the same project.
func TestInit_SlugCollisionDifferentRemote_Refuses(t *testing.T) {
	root := t.TempDir()
	first := newRepo(t, "widget")
	addRemote(t, first, "origin", "git@github.com:acme/widget.git")
	if _, err := Run(ctx, root, first, Options{}); err != nil {
		t.Fatalf("Run(first): %v", err)
	}

	// A second, unrelated repo whose directory also happens to be named
	// "widget" but points at a different remote.
	otherParent := t.TempDir()
	second := filepath.Join(otherParent, "widget")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, second, "init", "-q")
	runGit(t, second, "config", "user.email", "test@example.com")
	runGit(t, second, "config", "user.name", "test")
	addRemote(t, second, "origin", "git@github.com:someone-else/widget.git")

	before := hashTree(t, root)
	_, err := Run(ctx, root, second, Options{})
	wantClasterr(t, err, "validation.slug-collision")
	after := hashTree(t, root)
	if before != after {
		t.Error("a refused init wrote to the journal tree; want no write on refusal")
	}
	if !strings.Contains(err.(*clasterr.Error).Message, "--slug") {
		t.Errorf("slug-collision message = %q, want it to name --slug as the out", err.(*clasterr.Error).Message)
	}
}

// TestInit_FromLinkedWorktree_RegistersOwningClone confirms init run from a
// linked worktree of a not-yet-registered clone registers the OWNING
// clone (keyed by the worktree-invariant common-dir, labeled from the
// owning directory, not the worktree's own directory name) and creates no
// worktree row anywhere in the journal tree (S7/M17).
func TestInit_FromLinkedWorktree_RegistersOwningClone(t *testing.T) {
	root := t.TempDir()
	main := newRepo(t, "widget")
	addRemote(t, main, "origin", "git@github.com:acme/widget.git")
	wt := addWorktree(t, main, main+"-feature", "feature")

	result, err := Run(ctx, root, wt, Options{})
	if err != nil {
		t.Fatalf("Run(worktree): %v", err)
	}
	if result.Clone.Label != "widget" {
		t.Errorf("Label = %q, want %q (the owning clone dir's basename, not the worktree dir's)", result.Clone.Label, "widget")
	}
	if result.Worktree == "" {
		t.Error("Worktree = \"\", want the linked worktree's own name recorded as a fact")
	}

	// No worktree row anywhere: grep the whole journal tree's JSON content
	// for the word "worktree" as a KEY — journal.Clone/journal.Project
	// carry no such field, so this is a straightforward absence check
	// against what got persisted (not this package's own in-memory Result,
	// which legitimately carries a Worktree fact per call, never stored).
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), `"worktree"`) {
			t.Errorf("journal file %s mentions a worktree row; clast never births one (S7)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking journal tree: %v", err)
	}

	// Running init again from the main worktree resolves to the SAME
	// clone (current), confirming only one clone row exists for both
	// worktrees of this repo.
	again, err := Run(ctx, root, main, Options{})
	if err != nil {
		t.Fatalf("Run(main): %v", err)
	}
	if again.Outcome != OutcomeCurrent {
		t.Errorf("Outcome = %s, want current (same clone as the worktree run)", again.Outcome)
	}
	if again.Clone.ID != result.Clone.ID {
		t.Errorf("main worktree resolved to a different clone (%s) than the linked worktree's run (%s)", again.Clone.ID, result.Clone.ID)
	}
}

// TestInit_Rerun_ReportsCurrent_WritesNothing is the registry Matter's seal
// condition: re-running init in an already-registered clone reports
// current, exit 0, and the journal tree's bytes are provably unchanged.
func TestInit_Rerun_ReportsCurrent_WritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := newRepo(t, "widget")
	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	first, err := Run(ctx, root, dir, Options{})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	before := hashTree(t, root)
	second, err := Run(ctx, root, dir, Options{})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	after := hashTree(t, root)

	if second.Outcome != OutcomeCurrent {
		t.Errorf("Outcome = %s, want %s", second.Outcome, OutcomeCurrent)
	}
	if second.Clone.ID != first.Clone.ID || second.Project.ID != first.Project.ID {
		t.Errorf("re-run resolved to a different project/clone: %+v vs %+v", second, first)
	}
	if before != after {
		t.Error("re-running init changed the journal tree's bytes; want no write on the current path")
	}
}

// TestInit_NotAGitRepo_Refuses confirms init on a directory with no git
// repository at all refuses cleanly rather than surfacing a raw git error.
func TestInit_NotAGitRepo_Refuses(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	_, err := Run(ctx, root, dir, Options{})
	wantClasterr(t, err, "validation.not-a-git-repo")
}
