package pi_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source/pi"

	"github.com/procrastivity/clast/internal/source"
)

func found0() source.Discovered { return source.Discovered{Path: "/nonexistent.jsonl"} }

func TestCorrelateFixtures(t *testing.T) {
	base := fixtureRoots(t)
	const demo, other = "/home/user/Code/demo", "/home/user/Code/other"
	want := map[string]string{} // id suffix (last 3 digits) -> cwd
	for i := 1; i <= 13; i++ {
		want[pad3(i)] = demo
	}
	want["014"] = "" // no cwd
	want["015"] = "" // relative cwd
	want["016"] = other

	s := pi.NewAt(filepath.Join(base, "percwd"))
	found, _, err := s.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 16 {
		t.Fatalf("found %d, want 16", len(found))
	}
	for _, d := range found {
		got, diags, err := s.Correlate(t.Context(), d)
		if err != nil || len(diags) != 0 {
			t.Errorf("%s: err=%v diags=%q", d.NativeID, err, diagStrings(diags))
		}
		if w := want[d.NativeID[len(d.NativeID)-3:]]; got != w {
			t.Errorf("%s: cwd = %q, want %q", d.NativeID, got, w)
		}
	}

	flat := pi.NewAt(filepath.Join(base, "flat"))
	ff, _, _ := flat.Discover(t.Context())
	cwds := map[string]string{}
	for _, d := range ff {
		c, _, err := flat.Correlate(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		cwds[d.NativeID] = c
	}
	if cwds[idPrefix+"000000000020"] != demo || cwds[idPrefix+"000000000021"] != other {
		t.Errorf("flat cwds = %v", cwds)
	}
}

func pad3(i int) string {
	s := "00" + string(rune('0'+i/10)) + string(rune('0'+i%10))
	return s[len(s)-3:]
}

func TestCorrelateFailures(t *testing.T) {
	root := t.TempDir()
	s := pi.NewAt(root)

	t.Run("missing file is an item error", func(t *testing.T) {
		_, _, err := s.Correlate(t.Context(), source.Discovered{Path: filepath.Join(root, "gone.jsonl")})
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("file no longer a header gives a diagnostic", func(t *testing.T) {
		p := filepath.Join(root, "x.jsonl")
		writeFile(t, p, `{"type":"message"}`+"\n")
		cwd, diags, err := s.Correlate(t.Context(), source.Discovered{Path: p})
		if err != nil || cwd != "" || len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "not a session header") {
			t.Errorf("cwd=%q diags=%q err=%v", cwd, diagStrings(diags), err)
		}
	})
	t.Run("file emptied is quiet", func(t *testing.T) {
		p := filepath.Join(root, "empty.jsonl")
		writeFile(t, p, "")
		cwd, diags, err := s.Correlate(t.Context(), source.Discovered{Path: p})
		if err != nil || cwd != "" || len(diags) != 0 {
			t.Errorf("cwd=%q diags=%q err=%v", cwd, diagStrings(diags), err)
		}
	})
	t.Run("cwd is lexical", func(t *testing.T) {
		p := filepath.Join(root, "lex.jsonl")
		writeFile(t, p, `{"type":"session","version":3,"id":"a","timestamp":"2026-05-01T10:00:00.000Z","cwd":"/a/../b/"}`+"\n")
		cwd, _, err := s.Correlate(t.Context(), source.Discovered{Path: p})
		if err != nil || cwd != "/a/../b/" {
			t.Errorf("cwd=%q err=%v", cwd, err)
		}
	})
	t.Run("cwd of wrong type is empty", func(t *testing.T) {
		p := filepath.Join(root, "num.jsonl")
		writeFile(t, p, `{"type":"session","version":3,"id":"a","cwd":5}`+"\n")
		cwd, diags, err := s.Correlate(t.Context(), source.Discovered{Path: p})
		if err != nil || cwd != "" || len(diags) != 0 {
			t.Errorf("cwd=%q diags=%q err=%v", cwd, diagStrings(diags), err)
		}
	})
}

func TestReadHeader(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantID  string
		wantErr string // substring; "silent" for ErrSilentSkip
	}{
		{"valid", `{"type":"session","version":3,"id":"abc-1.2_x"}` + "\n", "abc-1.2_x", ""},
		{"version float", `{"type":"session","version":3.0,"id":"a"}` + "\n", "", "unsupported session version 3.0"},
		{"version string", `{"type":"session","version":"3","id":"a"}` + "\n", "", `unsupported session version "3"`},
		{"version null", `{"type":"session","version":null,"id":"a"}` + "\n", "", "version missing"},
		{"version 5", `{"type":"session","version":5,"id":"a"}` + "\n", "", "version 5 (supported: 3)"},
		{"id number", `{"type":"session","version":3,"id":7}` + "\n", "", "not a session header"},
		{"id missing", `{"type":"session","version":3}` + "\n", "", "not a session header"},
		{"id leading dot", `{"type":"session","version":3,"id":".a"}` + "\n", "", "not a valid id"},
		{"id slash", `{"type":"session","version":3,"id":"a/b"}` + "\n", "", "not a valid id"},
		{"id newline", `{"type":"session","version":3,"id":"a\n"}` + "\n", "", "not a valid id"},
		{"id empty", `{"type":"session","version":3,"id":""}` + "\n", "", "not a valid id"},
		{"array", "[]\n", "", "not a session header"},
		{"blank first line", "\n" + `{"type":"session","version":3,"id":"a"}` + "\n", "", "not a session header"},
		{"empty", "", "", "silent"},
		{"torn", `{"type":"session","ve`, "", "silent"},
		{"terminated junk", "junk\n", "", "not a session header"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := pi.ReadHeader(strings.NewReader(tc.in))
			switch {
			case tc.wantErr == "silent":
				if !errors.Is(err, pi.ErrSilentSkip) {
					t.Errorf("err = %v, want silent skip", err)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want %q", err, tc.wantErr)
				}
			default:
				if err != nil || h.ID != tc.wantID {
					t.Errorf("h=%+v err=%v", h, err)
				}
			}
		})
	}
	h, err := pi.ReadHeader(strings.NewReader(`{"type":"session","version":3,"id":"a","timestamp":"2026-01-10T10:00:00.000Z","cwd":"/x"}` + "\n"))
	if err != nil || h.Timestamp.Format("2006-01-02T15:04:05.000Z07:00") != "2026-01-10T10:00:00.000Z" || h.CWD != "/x" {
		t.Errorf("h=%+v err=%v", h, err)
	}
}
