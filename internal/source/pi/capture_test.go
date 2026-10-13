package pi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source/pi"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// memWrite is an in-memory WriteArtifact.
type memWrite struct{ got map[string][]byte }

func (m *memWrite) write(rel string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if m.got == nil {
		m.got = map[string][]byte{}
	}
	m.got[rel] = b
	return nil
}

type manifestRow struct {
	glob       string // relative to testdata/roots
	started    string
	last       string
	lines      int
	user, asst int
	subst      bool
	diags      []string // substrings, in order
}

const (
	d01 = "percwd/--home-user-Code-demo--/"
)

func row(glob, started, last string, lines, u, a int, subst bool, diags ...string) manifestRow {
	return manifestRow{glob, started, last, lines, u, a, subst, diags}
}

func manifest() []manifestRow {
	return []manifestRow{
		row(d01+"*_"+idPrefix+"000000000001.jsonl", "2026-01-10T10:00:00.000Z", "2026-01-10T10:00:06.000Z", 9, 1, 3, true),
		row(d01+"*_"+idPrefix+"000000000002.jsonl", "2026-01-10T11:00:00.000Z", "2026-01-12T09:30:02.000Z", 9, 2, 3, true),
		row(d01+"*_"+idPrefix+"000000000003.jsonl", "2026-01-11T08:00:00.000Z", "2026-01-11T08:00:04.000Z", 7, 2, 2, true),
		row(d01+"*_"+idPrefix+"000000000004.jsonl", "2026-01-11T12:00:00.000Z", "2026-01-11T12:00:02.000Z", 9, 3, 3, true),
		row(d01+"*_"+idPrefix+"000000000005.jsonl", "2026-01-11T13:00:00.000Z", "2026-01-11T13:00:00.000Z", 7, 2, 2, true),
		row(d01+"*_"+idPrefix+"000000000006.jsonl", "2026-01-13T09:00:00.000Z", "2026-01-13T09:00:06.000Z", 9, 2, 2, true),
		row(d01+"*_"+idPrefix+"000000000007.jsonl", "2026-01-14T09:00:00.000Z", "2026-01-14T09:00:02.000Z", 6, 1, 1, true),
		row(d01+"*_"+idPrefix+"000000000008.jsonl", "2026-01-14T10:00:00.000Z", "2026-01-14T10:00:02.000Z", 5, 1, 1, true),
		row(d01+"*_"+idPrefix+"000000000009.jsonl", "2026-01-14T11:00:00.000Z", "2026-01-14T11:00:07.000Z", 10, 2, 2, true,
			"3 unparseable line(s) skipped (first at line 6)"),
		row(d01+"*_"+idPrefix+"000000000010.jsonl", "2026-01-14T12:00:00.000Z", "2026-01-14T12:00:04.000Z", 7, 2, 1, true,
			"1 unparseable line(s) skipped (first at line 5)", "leaf path broken at"),
		row(d01+"*_"+idPrefix+"000000000011.jsonl", "2026-01-15T09:00:00.000Z", "2026-01-15T09:00:00.000Z", 1, 0, 0, false),
		row(d01+"*_"+idPrefix+"000000000012.jsonl", "2026-01-15T10:00:00.000Z", "2026-01-15T10:00:02.000Z", 5, 1, 1, false),
		row(d01+"*_"+idPrefix+"000000000013.jsonl", "2026-01-15T11:00:00.000Z", "2026-01-15T11:00:02.000Z", 5, 1, 1, true),
		row("percwd/--no-cwd--/*.jsonl", "2026-01-16T09:00:00.000Z", "2026-01-16T09:00:02.000Z", 5, 1, 1, true),
		row("percwd/--relative-cwd--/*.jsonl", "2026-01-16T10:00:00.000Z", "2026-01-16T10:00:02.000Z", 5, 1, 1, true),
		row("percwd/--home-user-Code-other--/*.jsonl", "2026-01-16T11:00:00.000Z", "2026-01-16T11:00:02.000Z", 5, 1, 1, true),
		row("flat/*_"+idPrefix+"000000000020.jsonl", "2026-02-01T09:00:00.000Z", "2026-02-01T09:00:02.000Z", 5, 1, 1, true),
		row("flat/*_"+idPrefix+"000000000021.jsonl", "2026-02-01T10:00:00.000Z", "2026-02-01T10:00:02.000Z", 5, 1, 1, true),
		row("diagnosed/*/*_"+idPrefix+"999999999999.jsonl", "2026-03-02T09:00:00.000Z", "2026-03-02T09:00:02.000Z", 5, 1, 1, true),
		row("diagnosed/*/no-underscore-name.jsonl", "2026-03-02T10:00:00.000Z", "2026-03-02T10:00:02.000Z", 5, 1, 1, true),
		row("duplicate/--home-user-Code-demo--/*.jsonl", "2026-03-03T09:00:00.000Z", "2026-03-03T09:00:02.000Z", 5, 1, 1, true),
		row("duplicate/--home-user-Code-copy--/*.jsonl", "2026-03-03T09:00:00.000Z", "2026-03-03T09:00:04.000Z", 7, 2, 2, true),
		row("walkshape/--home-user-Code-demo--/*_"+idPrefix+"000000000060.jsonl", "2026-03-04T09:00:00.000Z", "2026-03-04T09:00:02.000Z", 5, 1, 1, true),
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// headerID reads the header id of a source file via the shared reader.
func headerID(t *testing.T, path string) string {
	t.Helper()
	h, err := pi.ReadHeaderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return h.ID
}

func TestCaptureFixtureManifest(t *testing.T) {
	base := fixtureRoots(t)
	for _, tc := range manifest() {
		t.Run(tc.glob, func(t *testing.T) {
			m, err := filepath.Glob(filepath.Join(base, tc.glob))
			if err != nil || len(m) != 1 {
				t.Fatalf("glob %s = %v, %v", tc.glob, m, err)
			}
			path := m[0]
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			d := source.Discovered{NativeID: headerID(t, path), Path: path}
			var mw memWrite
			facts, diags, err := pi.NewAt(base).Capture(t.Context(), d, mw.write)
			if err != nil {
				t.Fatal(err)
			}
			if len(mw.got) != 1 || !bytes.Equal(mw.got["transcript.jsonl"], src) {
				t.Fatalf("artifacts = %d, transcript.jsonl equal = %v", len(mw.got), bytes.Equal(mw.got["transcript.jsonl"], src))
			}
			lines, sum, err := source.FingerprintFile(path)
			if err != nil {
				t.Fatal(err)
			}
			wantFP := journal.TranscriptFingerprint{Format: "pi-jsonl-v3", Lines: lines, SHA256: sum}
			if facts.Transcript != wantFP {
				t.Errorf("fingerprint = %+v, want %+v", facts.Transcript, wantFP)
			}
			if facts.Transcript.Lines != tc.lines {
				t.Errorf("Lines = %d, want %d", facts.Transcript.Lines, tc.lines)
			}
			if !facts.StartedAt.Equal(mustTime(t, tc.started)) || !facts.LastActiveAt.Equal(mustTime(t, tc.last)) {
				t.Errorf("times = %v / %v, want %s / %s", facts.StartedAt, facts.LastActiveAt, tc.started, tc.last)
			}
			if facts.Counts.User != tc.user || facts.Counts.Assistant != tc.asst || facts.Substantive != tc.subst {
				t.Errorf("counts/substantive = %+v/%v, want %d/%d/%v", facts.Counts, facts.Substantive, tc.user, tc.asst, tc.subst)
			}
			if facts.Branch != "" || facts.Incomplete {
				t.Errorf("Branch=%q Incomplete=%v, want empty/false", facts.Branch, facts.Incomplete)
			}
			if len(diags) != len(tc.diags) {
				t.Fatalf("diags = %q, want %d", diagStrings(diags), len(tc.diags))
			}
			for i, sub := range tc.diags {
				if !strings.Contains(diags[i].Err.Error(), sub) || diags[i].Path != path {
					t.Errorf("diag %d = %v, want %q", i, diags[i].Err, sub)
				}
			}
		})
	}
}

// capturing runs Capture over content written to a temp file.
func capturing(t *testing.T, id, content string) (source.Facts, []source.Diagnostic, map[string][]byte, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.jsonl")
	writeFile(t, p, content)
	var mw memWrite
	f, d, err := pi.NewAt("x").Capture(t.Context(), source.Discovered{NativeID: id, Path: p}, mw.write)
	return f, d, mw.got, err
}

const hdr = `{"type":"session","version":3,"id":"s1","timestamp":"2026-05-01T10:00:00.000Z","cwd":"/x"}` + "\n"

func msg(id, parent, ts, role, extra string) string {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":%q%s}}`+"\n", id, p, ts, role, extra)
}

const content1 = `,"content":[{"type":"text","text":"FIXTURE-OK"}],"stopReason":"stop"`

func TestCaptureRules(t *testing.T) {
	ts := func(s int) string { return fmt.Sprintf("2026-05-01T10:00:%02d.000Z", s) }

	t.Run("blank lines are lines but neither entries nor bad", func(t *testing.T) {
		in := hdr + "\n   \n" + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", content1)
		f, d, _, err := capturing(t, "s1", in)
		if err != nil || len(d) != 0 || f.Transcript.Lines != 5 || f.Counts.User != 1 || f.Counts.Assistant != 1 {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("later session line is ignored and never the leaf", func(t *testing.T) {
		in := hdr + msg("a", "", ts(1), "user", "") + hdr + msg("b", "a", ts(2), "assistant", content1)
		f, d, _, err := capturing(t, "s1", in)
		if err != nil || len(d) != 0 || f.Counts.User != 1 || f.Counts.Assistant != 1 {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
		in = hdr + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", content1) + hdr
		f, d, _, _ = capturing(t, "s1", in)
		if len(d) != 0 || f.Counts.Assistant != 1 {
			t.Errorf("trailing header changed the leaf: %+v %q", f, diagStrings(d))
		}
	})
	t.Run("cycle falls back to whole-file", func(t *testing.T) {
		in := hdr + msg("a", "b", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", content1)
		f, d, _, err := capturing(t, "s1", in)
		if err != nil || len(d) != 1 || !strings.Contains(d[0].Err.Error(), "leaf path broken at") ||
			f.Counts.User != 1 || f.Counts.Assistant != 1 || !f.Substantive {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("duplicate entry id: last wins", func(t *testing.T) {
		in := hdr + msg("a", "", ts(1), "user", "") + msg("a", "", ts(2), "user", "") + msg("b", "a", ts(3), "assistant", content1)
		f, d, _, err := capturing(t, "s1", in)
		if err != nil || len(d) != 0 || f.Counts.User != 1 || f.Counts.Assistant != 1 {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("error and empty replies are not substantive; aborted with content is", func(t *testing.T) {
		in := hdr + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", `,"content":[],"stopReason":"error"`)
		f, _, _, _ := capturing(t, "s1", in)
		if f.Substantive || f.Counts.Assistant != 1 {
			t.Errorf("error-only: %+v", f)
		}
		in = hdr + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", `,"content":[{"type":"text"}],"stopReason":"error"`)
		if f, _, _, _ = capturing(t, "s1", in); f.Substantive {
			t.Error("stopReason error with content must not be substantive")
		}
		in = hdr + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", `,"content":[{"type":"text"}],"stopReason":"aborted"`)
		if f, _, _, _ = capturing(t, "s1", in); !f.Substantive {
			t.Error("aborted with content must be substantive")
		}
	})
	t.Run("abandoned branch is activity but not counted", func(t *testing.T) {
		in := hdr + msg("a", "", ts(1), "user", "") + msg("b", "a", ts(2), "assistant", content1) +
			msg("c", "a", ts(9), "assistant", content1) + msg("d", "c", ts(3), "user", "")
		f, _, _, _ := capturing(t, "s1", in)
		if f.Counts.User != 2 || f.Counts.Assistant != 1 || !f.LastActiveAt.Equal(mustTime(t, ts(9))) {
			t.Errorf("f=%+v", f)
		}
	})
	t.Run("entry older than header never moves LastActiveAt before StartedAt", func(t *testing.T) {
		in := hdr + msg("a", "", "2026-01-01T00:00:00.000Z", "user", "")
		f, _, _, _ := capturing(t, "s1", in)
		if !f.LastActiveAt.Equal(f.StartedAt) {
			t.Errorf("last=%v started=%v", f.LastActiveAt, f.StartedAt)
		}
	})
	t.Run("unterminated valid final line counts, torn one does not, bytes verbatim", func(t *testing.T) {
		body := hdr + msg("a", "", ts(1), "user", "")
		valid := body + strings.TrimSuffix(msg("b", "a", ts(2), "assistant", content1), "\n")
		f, d, got, err := capturing(t, "s1", valid)
		if err != nil || len(d) != 0 || f.Counts.Assistant != 1 || f.Transcript.Lines != 3 || string(got["transcript.jsonl"]) != valid {
			t.Errorf("valid tail: f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
		torn := body + `{"type":"message","id":"b","pa`
		f, d, got, err = capturing(t, "s1", torn)
		if err != nil || len(d) != 0 || f.Counts.Assistant != 0 || f.Transcript.Lines != 3 || string(got["transcript.jsonl"]) != torn {
			t.Errorf("torn tail: f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("valid JSON without id on the final unterminated line is a bad line", func(t *testing.T) {
		in := hdr + msg("a", "", ts(1), "user", "") + `{"type":"message"}`
		f, d, _, err := capturing(t, "s1", in)
		if err != nil || len(d) != 1 || !strings.Contains(d[0].Err.Error(), "1 unparseable line(s) skipped (first at line 3)") || f.Counts.User != 1 {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("header only", func(t *testing.T) {
		f, d, _, err := capturing(t, "s1", hdr)
		if err != nil || len(d) != 0 || f.Substantive || f.Transcript.Lines != 1 || !f.StartedAt.Equal(f.LastActiveAt) {
			t.Errorf("f=%+v d=%q err=%v", f, diagStrings(d), err)
		}
	})
	t.Run("unparseable header timestamp leaves StartedAt zero", func(t *testing.T) {
		in := `{"type":"session","version":3,"id":"s1","timestamp":"nope"}` + "\n" + msg("a", "", ts(1), "user", "")
		f, _, _, err := capturing(t, "s1", in)
		if err != nil || !f.StartedAt.IsZero() || !f.LastActiveAt.Equal(mustTime(t, ts(1))) {
			t.Errorf("f=%+v err=%v", f, err)
		}
	})
	t.Run("line over 16 MiB is a bad line; copy stays whole", func(t *testing.T) {
		big := `{"type":"custom","id":"big","parentId":"a","pad":"` + strings.Repeat("x", pi.MaxLineSize) + `"}` + "\n"
		in := hdr + msg("a", "", ts(1), "user", "") + big + msg("b", "big", ts(2), "assistant", content1)
		f, d, got, err := capturing(t, "s1", in)
		if err != nil || string(got["transcript.jsonl"]) != in {
			t.Fatalf("err=%v copy equal=%v", err, string(got["transcript.jsonl"]) == in)
		}
		if len(d) != 2 || !strings.Contains(d[0].Err.Error(), "1 unparseable line(s) skipped (first at line 3)") ||
			!strings.Contains(d[1].Err.Error(), "leaf path broken at big") || f.Counts.User != 1 || f.Counts.Assistant != 1 {
			t.Errorf("f=%+v d=%q", f, diagStrings(d))
		}
	})
}

func TestCaptureHeaderFailuresAreItemErrors(t *testing.T) {
	base := fixtureRoots(t)
	tests := []struct {
		name, file, id, want string
	}{
		{"version 2", "rejected/--home-user-Code-demo--/2026-03-01T09-00-00-000Z_01900000-0000-7000-8000-000000000030.jsonl", idPrefix + "000000000030", "unsupported session version 2"},
		{"version 4", "rejected/--home-user-Code-demo--/2026-03-01T10-00-00-000Z_01900000-0000-7000-8000-000000000031.jsonl", idPrefix + "000000000031", "unsupported session version 4"},
		{"hostile id", "rejected/--home-user-Code-demo--/2026-03-01T12-00-00-000Z_hostile-dotdot.jsonl", "../../escape", "not a valid id"},
		{"no header", "rejected/--home-user-Code-demo--/2026-03-01T16-00-00-000Z_no-header.jsonl", "x", "not a session header"},
		{"junk first", "rejected/--home-user-Code-demo--/2026-03-01T17-00-00-000Z_junk-first-line.jsonl", "x", "not a session header"},
		{"zero byte", "rejected/--home-user-Code-demo--/2026-03-01T14-00-00-000Z_zero-byte.jsonl", "x", "no complete session header"},
		{"in flight", "rejected/--home-user-Code-demo--/2026-03-01T15-00-00-000Z_header-inflight.jsonl", "x", "no complete session header"},
		{"id changed", "percwd/--home-user-Code-demo--/2026-01-10T10-00-00-000Z_01900000-0000-7000-8000-000000000001.jsonl", idPrefix + "000000000002", "differs from discovered id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mw memWrite
			_, _, err := pi.NewAt(base).Capture(t.Context(), source.Discovered{NativeID: tc.id, Path: filepath.Join(base, tc.file)}, mw.write)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("header over 1 MiB", func(t *testing.T) {
		in := `{"type":"session","version":3,"id":"s1","cwd":"/` + strings.Repeat("a", 1<<20) + `"}` + "\n"
		if _, _, _, err := capturing(t, "s1", in); err == nil || !strings.Contains(err.Error(), "header line longer") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, _, err := pi.NewAt(base).Capture(t.Context(), source.Discovered{NativeID: "a", Path: filepath.Join(t.TempDir(), "gone")}, (&memWrite{}).write)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCaptureWriteAndCancellation(t *testing.T) {
	base := fixtureRoots(t)
	path := filepath.Join(base, "percwd/--home-user-Code-demo--/2026-01-10T10-00-00-000Z_"+idPrefix+"000000000001.jsonl")
	d := source.Discovered{NativeID: idPrefix + "000000000001", Path: path}

	t.Run("write error propagates", func(t *testing.T) {
		boom := errors.New("boom")
		_, _, err := pi.NewAt(base).Capture(t.Context(), d, func(string, io.Reader) error { return boom })
		if !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		_, _, err := pi.NewAt(base).Capture(ctx, d, func(string, io.Reader) error { called = true; return nil })
		if !errors.Is(err, context.Canceled) || called {
			t.Errorf("err=%v called=%v", err, called)
		}
	})
	t.Run("canceled mid-copy is a bare cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		_, _, err := pi.NewAt(base).Capture(ctx, d, func(_ string, r io.Reader) error {
			buf := make([]byte, 16)
			if _, err := r.Read(buf); err != nil {
				return err
			}
			cancel()
			_, err := io.Copy(io.Discard, r)
			return err
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCaptureDoesNotWriteSource(t *testing.T) {
	base := fixtureRoots(t)
	before := snapshot(t, base)
	for _, tc := range manifest() {
		m, _ := filepath.Glob(filepath.Join(base, tc.glob))
		var mw memWrite
		if _, _, err := pi.NewAt(base).Capture(t.Context(), source.Discovered{NativeID: headerID(t, m[0]), Path: m[0]}, mw.write); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshot(t, base); !reflect.DeepEqual(before, after) {
		t.Error("Capture changed the fixture tree")
	}
}
