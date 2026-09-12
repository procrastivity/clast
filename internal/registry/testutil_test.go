package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo creates a fresh git repo (no remotes) under t's temp dir, at the
// given relative name, and returns its absolute path.
//
// Ported from wip's internal/tiers/testutil_test.go newRepo (the store
// fixture helper there has no caller in this package's pure core and is
// dropped).
func newRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "test")
	return dir
}

// addRemote configures a remote in dir the way a user's `git remote add`
// would.
//
// Ported from wip's internal/tiers/testutil_test.go addRemote.
func addRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	run(t, dir, "remote", "add", name, url)
}

// addWorktree adds a linked worktree of mainDir at worktreeDir, on a new
// branch, and returns worktreeDir.
//
// Ported from wip's internal/tiers/testutil_test.go addWorktree.
func addWorktree(t *testing.T, mainDir, worktreeDir, branch string) string {
	t.Helper()
	run(t, mainDir, "commit", "--allow-empty", "-q", "-m", "init")
	run(t, mainDir, "worktree", "add", "-q", "-b", branch, worktreeDir)
	return worktreeDir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

var ctx = context.Background()
