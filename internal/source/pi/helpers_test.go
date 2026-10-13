package pi

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

const idPrefix = "01900000-0000-7000-8000-"

func fixtureRoots(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "roots"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// sessionBytes returns a minimal valid v3 session file.
func sessionBytes(id, cwd string) []byte {
	h := fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-05-01T10:00:00.000Z","cwd":%q}`, id, cwd)
	e := `{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2026-05-01T10:00:01.000Z","message":{"role":"user","content":"FIXTURE-OK"}}`
	return []byte(h + "\n" + e + "\n")
}

// writeSession writes dir/<ts>_<id>.jsonl and returns its path.
func writeSession(t *testing.T, dir, id string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "2026-05-01T10-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(p, sessionBytes(id, "/home/user/Code/demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not deny access")
	}
}

func discover(t *testing.T, s *Source) ([]source.Discovered, []source.Diagnostic, error) {
	t.Helper()
	return s.Discover(t.Context())
}

// rel returns paths relative to base, slash-separated and sorted.
func rel(t *testing.T, base string, found []source.Discovered) []string {
	t.Helper()
	var out []string
	for _, d := range found {
		r, err := filepath.Rel(base, d.Path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

func ids(found []source.Discovered) []string {
	var out []string
	for _, d := range found {
		out = append(out, d.NativeID)
	}
	sort.Strings(out)
	return out
}

func diagStrings(diags []source.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s | %v", d.Path, d.Err))
	}
	return out
}

// hasDiag reports whether some diagnostic is for a path ending in
// pathSuffix and its error contains sub.
func hasDiag(diags []source.Diagnostic, pathSuffix, sub string) bool {
	for _, d := range diags {
		if strings.HasSuffix(d.Path, pathSuffix) && d.Err != nil && strings.Contains(d.Err.Error(), sub) {
			return true
		}
	}
	return false
}

type snapEntry struct {
	size  int64
	mtime time.Time
	mode  fs.FileMode
}

func snapshot(t *testing.T, root string) map[string]snapEntry {
	t.Helper()
	m := map[string]snapEntry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		m[p] = snapEntry{info.Size(), info.ModTime(), info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, r)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// envOf builds an injectable Env over a map and a home directory.
func envOf(home string, vars map[string]string) Env {
	return Env{
		Getenv:      func(k string) string { return vars[k] },
		UserHomeDir: func() (string, error) { return home, nil },
	}
}
