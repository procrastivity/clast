package devin_test

// Small public-seam helpers for the step-09 convergence tests — the
// same shapes the capture verb's own tests use, rebuilt here because
// test helpers cannot cross package boundaries. Everything routes
// through journal/registry public functions, never private internals.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

// gitRepo creates a real git repo so ResolveCurrentClone's
// git-common-dir evidence resolves — the same way `clast init` sees it.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	return dir
}

// registerClone registers dir as a clone of a fresh project — project.json
// plus this machine's clones file, keyed by dir's git-common-dir.
func registerClone(t *testing.T, root, slug, dir string) {
	t.Helper()
	commonDir, err := registry.CommonDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := journal.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteProject(root, slug, journal.Project{
		ID: "01TESTPROJECT0000000000000", Slug: slug,
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{
		Machine: machine,
		Clones:  []journal.Clone{{ID: "01TESTCLONE00000000000000", GitCommonDir: commonDir, Label: slug}},
	}); err != nil {
		t.Fatal(err)
	}
}

// sessionDir locates the committed session directory and its shard.
func sessionDir(t *testing.T, root, nativeID string) (dir, shard string) {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(root, "sessions", "*", "devin-"+nativeID))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("session dir for %s = %v, %v", nativeID, dirs, err)
	}
	return dirs[0], filepath.Base(filepath.Dir(dirs[0]))
}

// treeHash digests every file under root — byte-stability's measure.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h, "%s\n", path); err != nil {
			return err
		}
		_, err = h.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
