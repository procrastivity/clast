package pi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/verbs/capture"
)

// hookSource wraps the real source so a test can act between Discover
// and Capture (the only window the file can change under the verb) or
// interpose on the stage's write seam. Everything else is the real
// source.
type hookSource struct {
	*Source
	before func(d source.Discovered)
	wrap   func(source.WriteArtifact) source.WriteArtifact
}

func (h hookSource) Capture(ctx context.Context, d source.Discovered, w source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	if h.before != nil {
		h.before(d)
	}
	if h.wrap != nil {
		w = h.wrap(w)
	}
	return h.Source.Capture(ctx, d, w)
}

func (r *rig) run(ctx context.Context) ([]capture.Captured, []source.Diagnostic, []capture.SourceFailure, error) {
	r.t.Helper()
	return capture.Run(ctx, r.deps)
}

func (r *rig) hook(h hookSource) {
	h.Source = NewAt(r.root)
	r.deps.Sources = []source.Source{h}
}

func (r *rig) plain() { r.deps.Sources = []source.Source{NewAt(r.root)} }

func (r *rig) turnsOf(id string) int {
	r.t.Helper()
	turns, err := NewAt(r.root).RenderTranscript(bytes.NewReader(r.artifact(id)), 1<<20)
	if err != nil {
		r.t.Fatalf("committed artifact not readable: %v", err)
	}
	return len(turns)
}

func (r *rig) srcBytes(id string) string {
	b, err := os.ReadFile(r.path(id))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func hasSub(diags []source.Diagnostic, sub string) bool { return hasDiag(diags, "", sub) }

// noStrays asserts that the journal holds only normal records and the
// Pi root only Pi files: recovery has no checkpoint or staging state.
func (r *rig) noStrays() {
	r.t.Helper()
	err := filepath.WalkDir(r.jr, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(r.jr, p)
		if strings.Contains(rel, ".capture-") {
			r.t.Errorf("staging leftover: %s", rel)
		}
		if d.IsDir() || !strings.HasPrefix(rel, "sessions"+string(filepath.Separator)) {
			return nil
		}
		switch d.Name() {
		case "session.json", "transcript.jsonl", "curation.json":
		default:
			r.t.Errorf("unexpected journal file: %s", rel)
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	err = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".jsonl") {
			r.t.Errorf("non-session file in Pi root: %s", p)
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func threeTurns(id string) (string, []string) {
	t1, l1 := turn(1, "", 1)
	t2, l2 := turn(2, l1, 3)
	t3, _ := turn(3, l2, 5)
	return ptsHdr(id, sec(0), ""), []string{t1, t2, t3}
}

func TestResilienceMalformedAfterGoodCapture(t *testing.T) {
	r := newRig(t, false)
	id := idPrefix + "0000001a01"
	r.write(id, twoTurns(id))
	r.sweep()
	good, _ := r.find(id)
	eq(t, "good counts", good.Counts, journal.SessionCounts{User: 2, Assistant: 2})

	// A bad mid-file line, a third turn, and a torn tail.
	h, turns := threeTurns(id)
	torn := `{"type":"message","id":"zz","parentId":"a3","timest`
	body := h + turns[0] + "{this is not json}\n" + turns[1] + turns[2] + torn
	r.write(id, body)
	c, diags, failed, err := r.run(t.Context())
	if err != nil || len(failed) != 0 || len(c) != 1 || !c[0].Recaptured {
		t.Fatalf("run: c=%+v failed=%v err=%v", c, failed, err)
	}
	// One aggregated diagnostic; the torn tail is silent and uncounted.
	if len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "1 unparseable line(s) skipped (first at line 4)") {
		t.Fatalf("diags = %q", diagStrings(diags))
	}
	got, _ := r.find(id)
	eq(t, "counts", got.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	eq(t, "last active", got.LastActiveAt, mustTime(t, sec(6)))
	eq(t, "started", got.StartedAt, good.StartedAt)
	if !got.Substantive {
		t.Error("substantive lost")
	}
	if string(r.artifact(id)) != body {
		t.Error("artifact is not the verbatim new bytes")
	}
	eq(t, "turns", r.turnsOf(id), 6)
	if c := r.sweep2(); len(c) != 0 {
		t.Errorf("stable malformed file churned: %+v", c)
	}

	// A line that carried a chain link is corrupted: the leaf path
	// breaks, counts fall back to the whole file, never to zero.
	broken := h + turns[0] + strings.SplitAfter(turns[1], "\n")[0] + "{corrupt a2\n" + turns[2]
	r.write(id, broken)
	c, diags, _, err = r.run(t.Context())
	if err != nil || len(c) != 1 || len(diags) != 2 ||
		!hasSub(diags, "1 unparseable line(s) skipped (first at line 5)") || !hasSub(diags, "leaf path broken at a2") {
		t.Fatalf("broken chain: c=%+v diags=%q err=%v", c, diagStrings(diags), err)
	}
	got, _ = r.find(id)
	eq(t, "whole-file counts", got.Counts, journal.SessionCounts{User: 3, Assistant: 2})
	if !got.Substantive || string(r.artifact(id)) != broken {
		t.Error("damaged capture lost substance or verbatim bytes")
	}
	eq(t, "turns still readable", r.turnsOf(id) > 0, true)
	r.noStrays()
}

// sweep2 is sweep without the no-diagnostics rule: a stable malformed
// file must be silent because unchanged sessions are skipped before
// any parse.
func (r *rig) sweep2() []capture.Captured {
	r.t.Helper()
	c, diags, failed, err := r.run(r.t.Context())
	if err != nil || len(failed) != 0 || len(diags) != 0 {
		r.t.Fatalf("sweep2: diags=%q failed=%v err=%v", diagStrings(diags), failed, err)
	}
	return c
}

func TestResilienceHeaderCorruptionKeepsPriorCapture(t *testing.T) {
	good := func(r *rig, id string) (string, map[string]fileState) {
		body := twoTurns(id)
		r.write(id, body)
		r.sweep()
		return body, r.sessionState(id)
	}
	rest := func(body string) string {
		return strings.SplitAfter(body, "\n")[1] + strings.Join(strings.SplitAfter(body, "\n")[2:], "")
	}

	cases := []struct {
		name string
		line func(id string) string
		diag string
	}{
		{"unparseable", func(string) string { return "{not a header\n" }, "not a session header"},
		{"version 4", func(id string) string {
			return strings.Replace(ptsHdr(id, sec(0), ""), `"version":3`, `"version":4`, 1)
		}, "unsupported session version 4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, false)
			id := idPrefix + "0000001b01"
			body, state := good(r, id)
			other := idPrefix + "0000001b02"

			r.write(id, tc.line(id)+rest(body))
			r.write(other, twoTurns(other)) // a healthy neighbour still captures
			c, diags, failed, err := r.run(t.Context())
			if err != nil || len(failed) != 0 || len(c) != 1 || c[0].Key.NativeID != other {
				t.Fatalf("run: c=%+v failed=%v err=%v", c, failed, err)
			}
			if !hasSub(diags, tc.diag) {
				t.Errorf("diags = %q, want %q", diagStrings(diags), tc.diag)
			}
			if !sameState(state, r.sessionState(id)) {
				t.Error("prior capture changed")
			}
			eq(t, "prior readable", r.turnsOf(id), 4)

			// Restored with a new turn: converges to the new bytes.
			t3, _ := turn(3, "a2", 5)
			r.write(id, body+t3)
			c = r.sweep()
			if len(c) != 1 || !c[0].Recaptured || c[0].Key.NativeID != id {
				t.Fatalf("restore: %+v", c)
			}
			if string(r.artifact(id)) != body+t3 {
				t.Error("did not converge to restored bytes")
			}
			r.noStrays()
		})
	}

	t.Run("restored identical is a no-op", func(t *testing.T) {
		r := newRig(t, false)
		id := idPrefix + "0000001b03"
		body, state := good(r, id)
		r.write(id, "{junk\n"+rest(body))
		if _, diags, _, err := r.run(t.Context()); err != nil || !hasSub(diags, "not a session header") {
			t.Fatalf("diags=%q err=%v", diagStrings(diags), err)
		}
		r.write(id, body)
		if c := r.sweep(); len(c) != 0 {
			t.Errorf("restored bytes recaptured: %+v", c)
		}
		if !sameState(state, r.sessionState(id)) {
			t.Error("prior capture changed")
		}
	})

	t.Run("different header id before sweep", func(t *testing.T) {
		r := newRig(t, false)
		id, other := idPrefix+"0000001b04", idPrefix+"0000001b05"
		body, state := good(r, id)
		r.write(id, ptsHdr(other, sec(0), "")+rest(body))
		c, diags, _, err := r.run(t.Context())
		// The header is Pi's authority: this is a different session,
		// captured under its own key with the filename-id disclosure.
		if err != nil || len(c) != 1 || c[0].Key.NativeID != other || !hasSub(diags, "filename id") {
			t.Fatalf("c=%+v diags=%q err=%v", c, diagStrings(diags), err)
		}
		if !sameState(state, r.sessionState(id)) {
			t.Error("original session's record changed")
		}
	})

	t.Run("file replaced between Discover and Capture", func(t *testing.T) {
		r := newRig(t, false)
		id, other := idPrefix+"0000001b06", idPrefix+"0000001b07"
		body, state := good(r, id)
		t3, _ := turn(3, "a2", 5)
		r.write(id, body+t3) // grown, so the verb will capture
		r.hook(hookSource{before: func(d source.Discovered) {
			if d.NativeID == id {
				r.write(id, ptsHdr(other, sec(0), "")+rest(body))
			}
		}})
		c, diags, failed, err := r.run(t.Context())
		if err != nil || len(failed) != 0 || len(c) != 0 {
			t.Fatalf("c=%+v failed=%v err=%v", c, failed, err)
		}
		if len(diags) != 1 {
			t.Fatalf("diags = %q", diagStrings(diags))
		}
		if !sameState(state, r.sessionState(id)) {
			t.Error("prior capture changed")
		}
		r.noStrays()
		r.plain()
		r.write(id, body+t3)
		if c := r.sweep(); len(c) != 1 || !c[0].Recaptured {
			t.Fatalf("converge: %+v", c)
		}
	})
}

func TestResilienceReadFailures(t *testing.T) {
	setup := func(t *testing.T) (*rig, string, string, map[string]fileState) {
		r := newRig(t, false)
		a, b := idPrefix+"0000001c01", idPrefix+"0000001c02"
		r.write(a, twoTurns(a))
		r.write(b, twoTurns(b))
		r.sweep()
		return r, a, b, r.sessionState(a)
	}

	t.Run("unreadable source", func(t *testing.T) {
		skipIfRoot(t)
		r, a, b, stateA := setup(t)
		t3, _ := turn(3, "a2", 5)
		r.append(a, t3)
		r.append(b, t3)
		fresh := idPrefix + "0000001c03"
		r.write(fresh, twoTurns(fresh))
		if err := os.Chmod(r.path(a), 0); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, r.root)
		c, diags, failed, err := r.run(t.Context())
		if err != nil || len(failed) != 0 {
			t.Fatalf("failed=%v err=%v", failed, err)
		}
		got := map[string]bool{}
		for _, x := range c {
			got[x.Key.NativeID] = true
		}
		if len(c) != 2 || !got[b] || !got[fresh] {
			t.Errorf("captured = %+v, want b and fresh only", c)
		}
		if len(diags) == 0 {
			t.Error("no diagnostic for the unreadable file")
		}
		if !sameState(stateA, r.sessionState(a)) {
			t.Error("prior capture of the unreadable session changed")
		}
		if !equalSnap(before, snapshot(t, r.root)) {
			t.Error("source storage changed")
		}
		if err := os.Chmod(r.path(a), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := r.sweep(); len(c) != 1 || c[0].Key.NativeID != a || !c[0].Recaptured {
			t.Fatalf("after restore: %+v", c)
		}
		r.noStrays()
	})

	t.Run("vanished before sweep", func(t *testing.T) {
		r, a, _, stateA := setup(t)
		if err := os.Remove(r.path(a)); err != nil {
			t.Fatal(err)
		}
		c, _, failed, err := r.run(t.Context())
		if err != nil || len(failed) != 0 || len(c) != 0 {
			t.Fatalf("c=%+v failed=%v err=%v", c, failed, err)
		}
		// Source deletion never deletes journal history.
		if !sameState(stateA, r.sessionState(a)) {
			t.Error("prior capture changed")
		}
	})

	t.Run("vanished between Discover and Capture", func(t *testing.T) {
		r, a, _, stateA := setup(t)
		fresh, healthy := idPrefix+"0000001c04", idPrefix+"0000001c05"
		r.write(fresh, twoTurns(fresh))
		r.write(healthy, twoTurns(healthy))
		r.hook(hookSource{before: func(d source.Discovered) {
			if d.NativeID == fresh {
				_ = os.Remove(r.path(fresh))
			}
		}})
		c, diags, failed, err := r.run(t.Context())
		if err != nil || len(failed) != 0 || len(diags) == 0 {
			t.Fatalf("c=%+v diags=%q failed=%v err=%v", c, diagStrings(diags), failed, err)
		}
		if len(c) != 1 || c[0].Key.NativeID != healthy {
			t.Errorf("captured = %+v, want only the healthy session", c)
		}
		if !sameState(stateA, r.sessionState(a)) {
			t.Error("unrelated capture changed")
		}
		items, _, _ := journal.Walk(r.jr)
		for _, it := range items {
			if it.Key.NativeID == fresh {
				t.Error("failed first capture left a visible session")
			}
		}
		r.noStrays()
	})
}

func TestResilienceJournalFailures(t *testing.T) {
	t.Run("stage write refuses mid-copy", func(t *testing.T) {
		r := newRig(t, false)
		a, b := idPrefix+"0000001d01", idPrefix+"0000001d02"
		r.write(a, twoTurns(a))
		r.sweep()
		prior := r.sessionState(a)
		t3, _ := turn(3, "a2", 5)
		r.append(a, t3)
		r.write(b, twoTurns(b))
		sentinel := errors.New("stage refused the write")
		r.hook(hookSource{wrap: func(_ source.WriteArtifact) source.WriteArtifact {
			return func(_ string, rd io.Reader) error {
				_, _ = io.CopyN(io.Discard, rd, 64)
				return sentinel
			}
		}})
		c, diags, failed, err := r.run(t.Context())
		if err != nil || len(failed) != 0 || len(c) != 0 || len(diags) != 2 {
			t.Fatalf("c=%+v diags=%q failed=%v err=%v", c, diagStrings(diags), failed, err)
		}
		for _, d := range diags {
			if !errors.Is(d.Err, sentinel) {
				t.Errorf("diag %v lost the stage error", d.Err)
			}
		}
		if !sameState(prior, r.sessionState(a)) {
			t.Error("prior capture changed")
		}
		r.noStrays()
		r.plain()
		c = r.sweep()
		if len(c) != 2 {
			t.Fatalf("repair sweep captured %d, want 2", len(c))
		}
		if string(r.artifact(a)) != r.srcBytes(a) {
			t.Error("not repaired")
		}
	})

	t.Run("commit refused", func(t *testing.T) {
		skipIfRoot(t)
		r := newRig(t, false)
		a := idPrefix + "0000001d03"
		r.write(a, twoTurns(a))
		r.sweep()
		prior := r.sessionState(a)
		_, dir := r.find(a)
		t3, _ := turn(3, "a2", 5)
		r.append(a, t3)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		c, _, failed, err := r.run(t.Context())
		if err == nil || len(failed) != 0 || len(c) != 0 {
			t.Fatalf("c=%+v failed=%v err=%v, want a fatal store-write error", c, failed, err)
		}
		if !sameState(prior, r.sessionState(a)) {
			t.Error("prior capture changed by the failed commit")
		}
		eq(t, "prior readable", r.turnsOf(a), 4)
		stale, _ := filepath.Glob(filepath.Join(r.jr, ".capture-*"))
		t.Logf("staging dirs left after commit failure: %d", len(stale))
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// The journal does not clean the failed stage (see report); only
		// the next healthy sweep's outcome is asserted here.
		c = r.sweep()
		if len(c) != 1 || !c[0].Recaptured {
			t.Fatalf("repair: %+v", c)
		}
		if string(r.artifact(a)) != r.srcBytes(a) {
			t.Error("not repaired")
		}
		s, _ := r.find(a)
		eq(t, "counts", s.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	})
}

func TestResilienceCancellation(t *testing.T) {
	r := newRig(t, false)
	a, b := idPrefix+"0000001e01", idPrefix+"0000001e02"
	r.write(a, twoTurns(a))
	r.sweep()
	prior := r.sessionState(a)
	t3, _ := turn(3, "a2", 5)
	r.append(a, t3)
	r.write(b, twoTurns(b))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.hook(hookSource{wrap: func(w source.WriteArtifact) source.WriteArtifact {
		return func(name string, rd io.Reader) error {
			cancel() // mid-copy
			return w(name, rd)
		}
	}})
	c, diags, failed, err := r.run(ctx)
	if err == nil || !strings.Contains(err.Error(), "context canceled") || len(failed) != 0 || len(c) != 0 {
		t.Fatalf("c=%+v failed=%v err=%v, want fatal cancellation", c, failed, err)
	}
	// Cancellation is never folded into a diagnostic.
	for _, d := range diags {
		if errors.Is(d.Err, context.Canceled) {
			t.Errorf("cancellation reported as diagnostic: %v", d.Err)
		}
	}
	if !sameState(prior, r.sessionState(a)) {
		t.Error("prior capture changed")
	}
	items, _, _ := journal.Walk(r.jr)
	if len(items) != 1 {
		t.Errorf("journal has %d sessions after cancel, want 1 (no half-committed b)", len(items))
	}
	r.noStrays()

	r.plain()
	if c := r.sweep(); len(c) != 2 {
		t.Fatalf("converge: %+v", c)
	}
	for _, id := range []string{a, b} {
		if string(r.artifact(id)) != r.srcBytes(id) {
			t.Errorf("%s did not converge", id)
		}
	}
	if c := r.sweep(); len(c) != 0 {
		t.Errorf("not a fixpoint: %+v", c)
	}
	r.noStrays()
}
