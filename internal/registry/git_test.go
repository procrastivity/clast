package registry

import "testing"

// TestCommonDir_WorktreeInvariant confirms CommonDir resolves identically
// from the main clone and from a linked worktree of it — the property M15
// and M17 rest a Clone's key on.
func TestCommonDir_WorktreeInvariant(t *testing.T) {
	main := newRepo(t, "main")
	wt := addWorktree(t, main, main+"-feature", "feature")

	mainCommon, err := CommonDir(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	wtCommon, err := CommonDir(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if mainCommon != wtCommon {
		t.Errorf("CommonDir differs between main (%q) and linked worktree (%q)", mainCommon, wtCommon)
	}
}

// TestGitDir_DiffersForLinkedWorktree confirms GitDir agrees with
// CommonDir from the main worktree, but differs from inside a linked
// worktree — the comparison a later step (registration) uses to derive
// M17's worktree name.
func TestGitDir_DiffersForLinkedWorktree(t *testing.T) {
	main := newRepo(t, "main")
	wt := addWorktree(t, main, main+"-feature", "feature")

	commonDir, err := CommonDir(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	mainGitDir, err := GitDir(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if mainGitDir != commonDir {
		t.Errorf("GitDir from the main worktree = %q, want it to equal CommonDir %q", mainGitDir, commonDir)
	}

	wtGitDir, err := GitDir(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if wtGitDir == commonDir {
		t.Error("GitDir from the linked worktree equals CommonDir, want it to differ")
	}
}

// TestBranch_NormalBranch confirms Branch reports the checked-out branch
// name, including on a brand-new repo with no commits yet (an unborn
// HEAD, still resolvable to its default branch by --show-current).
func TestBranch_NormalBranch(t *testing.T) {
	dir := newRepo(t, "repo")
	branch, err := Branch(ctx, dir)
	if err != nil {
		t.Fatalf("Branch on an unborn HEAD: %v", err)
	}
	if branch == "" {
		t.Error("Branch on a fresh repo's default branch = \"\", want a real name")
	}

	run(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	run(t, dir, "checkout", "-q", "-b", "feature")
	branch, err = Branch(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "feature" {
		t.Errorf("Branch = %q, want %q", branch, "feature")
	}
}

// TestBranch_DetachedHEAD confirms Branch reports "" for a detached HEAD —
// this package's deliberate choice (git.go's own comment), distinct from
// `git rev-parse --abbrev-ref HEAD`'s literal "HEAD".
func TestBranch_DetachedHEAD(t *testing.T) {
	dir := newRepo(t, "repo")
	run(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	run(t, dir, "checkout", "-q", "--detach", "HEAD")

	branch, err := Branch(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" {
		t.Errorf("Branch on a detached HEAD = %q, want empty", branch)
	}
}

// TestRemotes confirms Remotes reports no remotes on a fresh repo, and the
// configured fetch URL by name once one is added.
func TestRemotes(t *testing.T) {
	dir := newRepo(t, "repo")
	if remotes, err := Remotes(ctx, dir); err != nil {
		t.Fatal(err)
	} else if len(remotes) != 0 {
		t.Errorf("Remotes on a fresh repo = %v, want empty", remotes)
	}

	addRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	remotes, err := Remotes(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := remotes["origin"]; got != "git@github.com:acme/widget.git" {
		t.Errorf(`Remotes()["origin"] = %q, want %q`, got, "git@github.com:acme/widget.git")
	}
}
