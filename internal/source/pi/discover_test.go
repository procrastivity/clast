package pi_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source/pi"
)

func TestDiscoverFixtureRoots(t *testing.T) {
	type diagWant struct{ suffix, sub string }
	tests := []struct {
		root      string
		want      []string // relative to testdata/roots, sorted
		diags     []diagWant
		wantDiags int
	}{
		{
			root: "percwd",
			want: []string{
				"percwd/--home-user-Code-demo--/2026-01-10T10-00-00-000Z_01900000-0000-7000-8000-000000000001.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-10T11-00-00-000Z_01900000-0000-7000-8000-000000000002.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-11T08-00-00-000Z_01900000-0000-7000-8000-000000000003.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-11T12-00-00-000Z_01900000-0000-7000-8000-000000000004.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-11T13-00-00-000Z_01900000-0000-7000-8000-000000000005.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-13T09-00-00-000Z_01900000-0000-7000-8000-000000000006.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-14T09-00-00-000Z_01900000-0000-7000-8000-000000000007.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-14T10-00-00-000Z_01900000-0000-7000-8000-000000000008.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-14T11-00-00-000Z_01900000-0000-7000-8000-000000000009.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-14T12-00-00-000Z_01900000-0000-7000-8000-000000000010.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-15T09-00-00-000Z_01900000-0000-7000-8000-000000000011.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-15T10-00-00-000Z_01900000-0000-7000-8000-000000000012.jsonl",
				"percwd/--home-user-Code-demo--/2026-01-15T11-00-00-000Z_01900000-0000-7000-8000-000000000013.jsonl",
				"percwd/--home-user-Code-other--/2026-01-16T11-00-00-000Z_01900000-0000-7000-8000-000000000016.jsonl",
				"percwd/--no-cwd--/2026-01-16T09-00-00-000Z_01900000-0000-7000-8000-000000000014.jsonl",
				"percwd/--relative-cwd--/2026-01-16T10-00-00-000Z_01900000-0000-7000-8000-000000000015.jsonl",
			},
		},
		{
			root: "flat",
			want: []string{
				"flat/2026-02-01T09-00-00-000Z_01900000-0000-7000-8000-000000000020.jsonl",
				"flat/2026-02-01T10-00-00-000Z_01900000-0000-7000-8000-000000000021.jsonl",
			},
		},
		{
			root: "rejected",
			want: nil,
			diags: []diagWant{
				{"000000000030.jsonl", "unsupported session version 2 (supported: 3)"},
				{"000000000031.jsonl", "unsupported session version 4 (supported: 3)"},
				{"000000000032.jsonl", "unsupported session version missing (supported: 3)"},
				{"hostile-dotdot.jsonl", "not a valid id"},
				{"trailing.jsonl", "not a valid id"},
				{"no-header.jsonl", "first line is not a session header"},
				{"junk-first-line.jsonl", "first line is not a session header"},
			},
			wantDiags: 7, // zero-byte and header-inflight are silent
		},
		{
			root: "diagnosed",
			want: []string{
				"diagnosed/--home-user-Code-demo--/2026-03-02T09-00-00-000Z_01900000-0000-7000-8000-999999999999.jsonl",
				"diagnosed/--home-user-Code-demo--/no-underscore-name.jsonl",
			},
			diags: []diagWant{
				{"999999999999.jsonl", "does not match header id"},
				{"no-underscore-name.jsonl", "does not match header id"},
			},
			wantDiags: 2,
		},
		{
			root: "walkshape",
			want: []string{
				"walkshape/--home-user-Code-demo--/2026-03-04T09-00-00-000Z_01900000-0000-7000-8000-000000000060.jsonl",
			},
		},
	}
	base := fixtureRoots(t)
	for _, tc := range tests {
		t.Run(tc.root, func(t *testing.T) {
			before := snapshot(t, filepath.Join(base, tc.root))
			found, diags, err := discover(t, pi.NewAt(filepath.Join(base, tc.root)))
			if err != nil {
				t.Fatal(err)
			}
			if got := rel(t, base, found); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("paths:\n got %q\nwant %q", got, tc.want)
			}
			for _, d := range found {
				if tc.root != "diagnosed" && !strings.HasSuffix(d.Path, d.NativeID+".jsonl") {
					t.Errorf("native id %q does not match path %s", d.NativeID, d.Path)
				}
				if d.ModTime.IsZero() {
					t.Errorf("%s: zero ModTime", d.Path)
				}
			}
			for _, w := range tc.diags {
				if !hasDiag(diags, w.suffix, w.sub) {
					t.Errorf("missing diagnostic %q on *%s; got %q", w.sub, w.suffix, diagStrings(diags))
				}
			}
			if len(diags) != tc.wantDiags {
				t.Errorf("diagnostics = %d, want %d: %q", len(diags), tc.wantDiags, diagStrings(diags))
			}
			if after := snapshot(t, filepath.Join(base, tc.root)); !reflect.DeepEqual(before, after) {
				t.Error("discovery changed the fixture tree")
			}
		})
	}
}

func TestDiscoverHeaderIDWinsOverFilename(t *testing.T) {
	found, _, err := discover(t, pi.NewAt(filepath.Join(fixtureRoots(t), "diagnosed")))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{idPrefix + "000000000040", idPrefix + "000000000041"}
	if got := ids(found); !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
}

func TestDiscoverDuplicateIDs(t *testing.T) {
	base := fixtureRoots(t)
	const name = "2026-03-03T09-00-00-000Z_01900000-0000-7000-8000-000000000050.jsonl"
	tests := []struct {
		name         string
		demo, copyAt time.Time
		winner       string // dir name of the winner
	}{
		{"copy newest", time.Unix(1_000, 0), time.Unix(2_000, 0), "--home-user-Code-copy--"},
		{"original newest", time.Unix(2_000, 0), time.Unix(1_000, 0), "--home-user-Code-demo--"},
		{"tie goes to smallest path", time.Unix(1_000, 0), time.Unix(1_000, 0), "--home-user-Code-copy--"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			copyTree(t, filepath.Join(base, "duplicate"), root)
			demo := filepath.Join(root, "--home-user-Code-demo--", name)
			cp := filepath.Join(root, "--home-user-Code-copy--", name)
			for p, mt := range map[string]time.Time{demo: tc.demo, cp: tc.copyAt} {
				if err := os.Chtimes(p, mt, mt); err != nil {
					t.Fatal(err)
				}
			}
			found, diags, err := discover(t, pi.NewAt(root))
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 {
				t.Fatalf("found %d, want 1", len(found))
			}
			winner := filepath.Join(root, tc.winner, name)
			loser := demo
			if tc.winner == "--home-user-Code-demo--" {
				loser = cp
			}
			if found[0].Path != winner || found[0].NativeID != idPrefix+"000000000050" {
				t.Errorf("winner = %s (%s), want %s", found[0].Path, found[0].NativeID, winner)
			}
			if len(diags) != 1 || diags[0].Path != loser ||
				!strings.Contains(diags[0].Err.Error(), "duplicate session id "+idPrefix+"000000000050, using "+winner) {
				t.Errorf("diags = %q", diagStrings(diags))
			}
		})
	}
}

func TestDiscoverAbsentEmptyUnreadableRoot(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		s := pi.NewAt(filepath.Join(t.TempDir(), "nope"))
		found, diags, err := discover(t, s)
		if found != nil || diags != nil || err != nil {
			t.Fatalf("got %v %v %v, want nil nil nil", found, diags, err)
		}
		if err := s.Present(t.Context()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Present = %v, want not-exist", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		s := pi.NewAt(t.TempDir())
		found, diags, err := discover(t, s)
		if len(found) != 0 || len(diags) != 0 || err != nil {
			t.Fatalf("got %v %v %v", found, diags, err)
		}
		if err := s.Present(t.Context()); err != nil {
			t.Fatalf("Present = %v, want nil", err)
		}
	})
	t.Run("unreadable only root is a source failure", func(t *testing.T) {
		skipIfRoot(t)
		root := t.TempDir()
		writeSession(t, filepath.Join(root, "--x--"), idPrefix+"000000000001")
		if err := os.Chmod(root, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
		s := pi.NewAt(root)
		found, _, err := discover(t, s)
		if err == nil || !errors.Is(err, fs.ErrPermission) || found != nil {
			t.Fatalf("Discover = %v, %v; want permission error", found, err)
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) || pe.Path != root {
			t.Errorf("error does not name the root verbatim: %v", err)
		}
		if err := s.Present(t.Context()); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Present = %v, want permission error", err)
		}
	})
	t.Run("root is a file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file")
		writeFile(t, f, "x")
		if _, _, err := discover(t, pi.NewAt(f)); err == nil {
			t.Error("Discover on a non-directory root: want error")
		}
		if err := pi.NewAt(f).Present(t.Context()); err == nil {
			t.Error("Present on a non-directory root: want error")
		}
	})
}

func TestDiscoverUnreadableSubdirectory(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	good := writeSession(t, filepath.Join(root, "--good--"), idPrefix+"000000000001")
	writeSession(t, filepath.Join(root, "--bad--"), idPrefix+"000000000002")
	bad := filepath.Join(root, "--bad--")
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })
	found, diags, err := discover(t, pi.NewAt(root))
	if err != nil {
		t.Fatalf("an unreadable subdirectory must not fail the source: %v", err)
	}
	if len(found) != 1 || found[0].Path != good {
		t.Errorf("found = %v", found)
	}
	if len(diags) != 1 || diags[0].Path != bad || !errors.Is(diags[0].Err, fs.ErrPermission) {
		t.Errorf("diags = %q", diagStrings(diags))
	}
}

func TestDiscoverUnreadableFile(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	p := writeSession(t, filepath.Join(root, "--x--"), idPrefix+"000000000001")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	found, diags, err := discover(t, pi.NewAt(root))
	if err != nil || len(found) != 0 {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(diags) != 1 || diags[0].Path != p {
		t.Errorf("diags = %q", diagStrings(diags))
	}
}

func TestDiscoverHeaderLineBound(t *testing.T) {
	mk := func(contentLen int) string {
		prefix := `{"type":"session","version":3,"id":"` + idPrefix + `000000000001","timestamp":"2026-05-01T10:00:00.000Z","cwd":"/`
		suffix := `"}`
		pad := contentLen - len(prefix) - len(suffix)
		return prefix + strings.Repeat("a", pad) + suffix + "\n"
	}
	t.Run("exactly 1 MiB is accepted", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "x_"+idPrefix+"000000000001.jsonl"), mk(1<<20))
		found, diags, err := discover(t, pi.NewAt(root))
		if err != nil || len(found) != 1 || len(diags) != 0 {
			t.Fatalf("found=%v diags=%q err=%v", found, diagStrings(diags), err)
		}
	})
	t.Run("over 1 MiB is a diagnostic", func(t *testing.T) {
		root := t.TempDir()
		p := filepath.Join(root, "x_"+idPrefix+"000000000001.jsonl")
		writeFile(t, p, mk(1<<20+1))
		found, diags, err := discover(t, pi.NewAt(root))
		if err != nil || len(found) != 0 {
			t.Fatalf("found=%v err=%v", found, err)
		}
		if !hasDiag(diags, filepath.Base(p), "header line longer than") || len(diags) != 1 {
			t.Errorf("diags = %q", diagStrings(diags))
		}
	})
	t.Run("over 1 MiB with no newline is a diagnostic not silence", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "x_"+idPrefix+"000000000001.jsonl"), strings.TrimSuffix(mk(1<<20+5), "\n"))
		_, diags, _ := discover(t, pi.NewAt(root))
		if len(diags) != 1 {
			t.Errorf("diags = %q", diagStrings(diags))
		}
	})
}

func TestDiscoverSilentShapes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "--x--")
	writeFile(t, filepath.Join(dir, "a_zero.jsonl"), "")
	writeFile(t, filepath.Join(dir, "b_inflight.jsonl"), `{"type":"session","ver`)
	// Unterminated but complete header is a real header.
	writeFile(t, filepath.Join(dir, "c_"+idPrefix+"000000000007.jsonl"),
		`{"type":"session","version":3,"id":"`+idPrefix+`000000000007","timestamp":"2026-05-01T10:00:00.000Z"}`)
	found, diags, err := discover(t, pi.NewAt(root))
	if err != nil || len(diags) != 0 || len(found) != 1 || found[0].NativeID != idPrefix+"000000000007" {
		t.Fatalf("found=%v diags=%q err=%v", found, diagStrings(diags), err)
	}
}

func TestDiscoverSymlinks(t *testing.T) {
	outside := t.TempDir()
	realDir := writeSession(t, filepath.Join(outside, "dir"), idPrefix+"000000000001")
	realFile := writeSession(t, filepath.Join(outside, "files"), idPrefix+"000000000002")
	deep := writeSession(t, filepath.Join(outside, "deep", "nested"), idPrefix+"000000000003")

	root := t.TempDir()
	// Directory symlink at the per-cwd level: followed.
	must(t, os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root, "--linked--")))
	// File symlink inside a real subdirectory and directly in the root: followed.
	must(t, os.MkdirAll(filepath.Join(root, "--real--"), 0o755))
	linkInSub := filepath.Join(root, "--real--", "2026-05-01T10-00-00-000Z_"+idPrefix+"000000000002.jsonl")
	must(t, os.Symlink(realFile, linkInSub))
	// A symlinked directory below the per-cwd level: not walked.
	must(t, os.Symlink(filepath.Join(outside, "deep"), filepath.Join(root, "--real--", "deeper")))
	// Dangling .jsonl symlink: item diagnostic.
	dangling := filepath.Join(root, "--real--", "dangling.jsonl")
	must(t, os.Symlink(filepath.Join(outside, "missing.jsonl"), dangling))
	// A directory named *.jsonl is ignored without comment.
	must(t, os.MkdirAll(filepath.Join(root, "--real--", "dir.jsonl"), 0o755))
	// Dangling non-.jsonl symlink: silent.
	must(t, os.Symlink(filepath.Join(outside, "gone"), filepath.Join(root, "dangling-dir")))

	found, diags, err := discover(t, pi.NewAt(root))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(found), []string{idPrefix + "000000000001", idPrefix + "000000000002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	for _, d := range found {
		if d.NativeID == idPrefix+"000000000001" && d.Path != filepath.Join(root, "--linked--", filepath.Base(realDir)) {
			t.Errorf("symlinked dir path = %s", d.Path)
		}
		if d.NativeID == idPrefix+"000000000003" {
			t.Errorf("walked too deep: %s (%s)", d.Path, deep)
		}
	}
	if len(diags) != 1 || diags[0].Path != dangling {
		t.Errorf("diags = %q, want only the dangling symlink", diagStrings(diags))
	}
}

func TestDiscoverCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := pi.NewAt(filepath.Join(fixtureRoots(t), "percwd"))
	found, diags, err := s.Discover(ctx)
	if !errors.Is(err, context.Canceled) || found != nil || diags != nil {
		t.Fatalf("Discover = %v %v %v, want context.Canceled", found, diags, err)
	}
	if _, _, err := s.Correlate(ctx, found0()); !errors.Is(err, context.Canceled) {
		t.Errorf("Correlate err = %v, want context.Canceled", err)
	}
}

func TestDiscoverMakesNoWrites(t *testing.T) {
	base := fixtureRoots(t)
	before := snapshot(t, base)
	for _, r := range []string{"percwd", "flat", "rejected", "diagnosed", "duplicate", "walkshape"} {
		s := pi.NewAt(filepath.Join(base, r))
		found, _, err := s.Discover(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range found {
			if _, _, err := s.Correlate(t.Context(), d); err != nil {
				t.Fatal(err)
			}
		}
		_ = s.Present(t.Context())
	}
	if after := snapshot(t, base); !reflect.DeepEqual(before, after) {
		t.Error("discovery or correlation changed the fixture tree")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
