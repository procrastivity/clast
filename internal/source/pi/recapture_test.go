package pi

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/verbs/capture"
)

// rig drives the public capture.Run over a temp Pi root and a temp
// journal. Nothing here reads private state: outcomes, stored bytes,
// session.json fields and mtimes only.
type rig struct {
	t    *testing.T
	root string
	jr   string
	deps capture.Deps
	dir  string
	n    int
}

func newRig(t *testing.T, autoDismiss bool) *rig {
	t.Helper()
	r := &rig{t: t, root: t.TempDir(), jr: t.TempDir()}
	r.dir = filepath.Join(r.root, "--home-user-Code-demo--")
	r.deps = capture.Deps{
		Root:            r.jr,
		Sources:         []source.Source{NewAt(r.root)},
		AutoDismissNoop: autoDismiss,
		Now:             func() time.Time { r.n++; return time.Unix(1792000000+int64(r.n), 0).UTC() },
	}
	return r
}

func (r *rig) sweep() []capture.Captured {
	r.t.Helper()
	c, diags, failed, err := capture.Run(r.t.Context(), r.deps)
	if err != nil || len(failed) != 0 {
		r.t.Fatalf("Run: err=%v failed=%v", err, failed)
	}
	if len(diags) != 0 {
		r.t.Fatalf("unexpected diagnostics: %q", diagStrings(diags))
	}
	return c
}

func (r *rig) path(id string) string {
	return filepath.Join(r.dir, "2026-05-01T10-00-00-000Z_"+id+".jsonl")
}

func (r *rig) write(id, content string) string {
	r.t.Helper()
	writeFile(r.t, r.path(id), content)
	return r.path(id)
}

func (r *rig) append(id, content string) {
	r.t.Helper()
	f, err := os.OpenFile(r.path(id), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		r.t.Fatal(werr)
	}
}

// find locates the one captured session with this native id.
func (r *rig) find(id string) (journal.Session, string) {
	r.t.Helper()
	items, _, err := journal.Walk(r.jr)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, it := range items {
		if it.Key.Harness == "pi" && it.Key.NativeID == id {
			return it.Session, filepath.Dir(journal.SessionJSONPath(r.jr, it.Shard, it.Key))
		}
	}
	r.t.Fatalf("session %s not in journal", id)
	return journal.Session{}, ""
}

func (r *rig) artifact(id string) []byte {
	r.t.Helper()
	_, dir := r.find(id)
	b, err := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

// sessionState is the bytes+mtime of everything in one session's
// journal directory.
func (r *rig) sessionState(id string) map[string]fileState {
	_, dir := r.find(id)
	return treeState(r.t, dir)
}

func sameState(a, b map[string]fileState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func ptsHdr(id, ts, extra string) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":%q,"cwd":"/home/user/Code/demo"%s}`+"\n", id, ts, extra)
}

func sec(s int) string { return fmt.Sprintf("2026-05-01T10:%02d:%02d.000Z", s/60, s%60) }

// turn is a user message plus an assistant reply chained to parent;
// it returns the lines and the last id.
func turn(n int, parent string, at int) (string, string) {
	u, a := fmt.Sprintf("u%d", n), fmt.Sprintf("a%d", n)
	return msg(u, parent, sec(at), "user", `,"content":[{"type":"text","text":"FIXTURE-OK"}]`) +
		msg(a, u, sec(at+1), "assistant", content1), a
}

func twoTurns(id string) string {
	t1, last := turn(1, "", 1)
	t2, _ := turn(2, last, 3)
	return ptsHdr(id, sec(0), "") + t1 + t2
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestRecaptureUnchangedNoChurn(t *testing.T) {
	r := newRig(t, true)
	copyTree(t, filepath.Join(fixtureRoots(t), "percwd", "--home-user-Code-demo--"), r.dir)
	// bad-lines and broken-chain carry capture-time diagnostics; drop
	// them so sweep() can insist on silence.
	for _, n := range []string{"009", "010"} {
		m, _ := filepath.Glob(filepath.Join(r.dir, "*00000000"+n+".jsonl"))
		for _, p := range m {
			_ = os.Remove(p)
		}
	}
	if c := r.sweep(); len(c) == 0 {
		t.Fatal("first sweep captured nothing")
	}
	before := treeState(t, r.jr)

	if c := r.sweep(); len(c) != 0 {
		t.Fatalf("rerun captured %d", len(c))
	}
	if !sameState(before, treeState(t, r.jr)) {
		t.Error("journal churned on an unchanged rerun")
	}

	// touch: only the source mtime moves. An mtime-keyed check would
	// recapture every session here.
	future := time.Now().Add(48 * time.Hour)
	m, _ := filepath.Glob(filepath.Join(r.dir, "*.jsonl"))
	for _, p := range m {
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
	}
	if c := r.sweep(); len(c) != 0 {
		t.Fatalf("touch-only sweep captured %d: %v", len(c), c)
	}
	if !sameState(before, treeState(t, r.jr)) {
		t.Error("journal churned after a source touch")
	}
}

func TestRecaptureResumeAppend(t *testing.T) {
	r := newRig(t, false)
	idA, idB := idPrefix+"0000000a01", idPrefix+"0000000a02"
	r.write(idA, twoTurns(idA))
	r.write(idB, twoTurns(idB))
	r.sweep()
	first, _ := r.find(idA)
	bBefore := r.sessionState(idB)
	eq(t, "initial counts", first.Counts, journal.SessionCounts{User: 2, Assistant: 2})

	t3, _ := turn(3, "a2", 5) // resume: new user turn plus reply
	r.append(idA, t3)
	c := r.sweep()
	if len(c) != 1 || !c[0].Recaptured || c[0].Key.NativeID != idA {
		t.Fatalf("captured = %+v, want only %s recaptured", c, idA)
	}
	src, _ := os.ReadFile(r.path(idA))
	if !bytes.Equal(r.artifact(idA), src) {
		t.Error("artifact != new source bytes")
	}
	got, _ := r.find(idA)
	eq(t, "counts", got.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	if !got.LastActiveAt.After(first.LastActiveAt) {
		t.Errorf("LastActiveAt did not move: %v -> %v", first.LastActiveAt, got.LastActiveAt)
	}
	// Wrong shortcut: re-deriving identity from the new content (shard
	// or key) would split the session.
	if !got.StartedAt.Equal(first.StartedAt) {
		t.Errorf("StartedAt moved: %v -> %v", first.StartedAt, got.StartedAt)
	}
	if got.Transcript.Lines != first.Transcript.Lines+2 || got.Transcript.SHA256 == first.Transcript.SHA256 {
		t.Errorf("fingerprint = %+v from %+v", got.Transcript, first.Transcript)
	}
	items, _, _ := journal.Walk(r.jr)
	if len(items) != 2 {
		t.Errorf("journal has %d sessions, want 2", len(items))
	}
	if !sameState(bBefore, r.sessionState(idB)) {
		t.Error("untouched sibling session was rewritten")
	}
}

func TestRecaptureNewlineRepair(t *testing.T) {
	r := newRig(t, false)
	id := idPrefix + "0000000b01"
	body := strings.TrimSuffix(twoTurns(id), "\n") // unterminated-valid
	r.write(id, body)
	r.sweep()
	first, _ := r.find(id)
	eq(t, "initial counts", first.Counts, journal.SessionCounts{User: 2, Assistant: 2})

	r.append(id, "\n") // Pi's repair on open
	c := r.sweep()
	// Wrong shortcut: line count alone is the same (the unterminated
	// last line already counted once), so only the hash sees this.
	if len(c) != 1 || !c[0].Recaptured {
		t.Fatalf("newline repair not recaptured: %+v", c)
	}
	got, _ := r.find(id)
	eq(t, "lines", got.Transcript.Lines, first.Transcript.Lines)
	if got.Transcript.SHA256 == first.Transcript.SHA256 {
		t.Error("sha did not change")
	}
	eq(t, "counts", got.Counts, first.Counts)
	eq(t, "last active", got.LastActiveAt, first.LastActiveAt)
	src, _ := os.ReadFile(r.path(id))
	if !bytes.Equal(r.artifact(id), src) {
		t.Error("artifact != repaired source")
	}
	if c := r.sweep(); len(c) != 0 {
		t.Errorf("repaired file not a fixpoint: %+v", c)
	}
}

func TestRecaptureTruncateAndRewrite(t *testing.T) {
	r := newRig(t, false)
	id := idPrefix + "0000000c01"
	orig := twoTurns(id)
	r.write(id, orig)
	r.sweep()
	first, _ := r.find(id)

	expectConverged := func(label, want string, wantUser, wantAsst int) journal.Session {
		t.Helper()
		c := r.sweep()
		if len(c) != 1 || !c[0].Recaptured {
			t.Fatalf("%s: not recaptured: %+v", label, c)
		}
		if !bytes.Equal(r.artifact(id), []byte(want)) {
			t.Errorf("%s: artifact != new bytes", label)
		}
		s, _ := r.find(id)
		eq(t, label+" counts", s.Counts, journal.SessionCounts{User: wantUser, Assistant: wantAsst})
		return s
	}

	// Shrink: a v3 file should not, but a truncate must still converge.
	shrunk := strings.Join(strings.SplitAfter(orig, "\n")[:3], "")
	r.write(id, shrunk)
	s1 := expectConverged("shrink", shrunk, 1, 1)
	if s1.Transcript.Lines >= first.Transcript.Lines {
		t.Errorf("lines %d not below %d", s1.Transcript.Lines, first.Transcript.Lines)
	}

	// Same line count, different content. Wrong shortcut: comparing
	// line count (or size) alone calls this unchanged.
	rewritten := strings.ReplaceAll(shrunk, "FIXTURE-OK", "REWRITTEN-LONGER")
	if len(rewritten) == len(shrunk) {
		t.Fatal("test bug: want a size change")
	}
	r.write(id, rewritten)
	s2 := expectConverged("rewrite", rewritten, 1, 1)
	eq(t, "rewrite lines", s2.Transcript.Lines, s1.Transcript.Lines)
	if s2.Transcript.SHA256 == s1.Transcript.SHA256 {
		t.Error("rewrite kept the old sha")
	}

	// Same lines AND same byte size: only the hash differs.
	samesize := strings.ReplaceAll(rewritten, "REWRITTEN-LONGER", "REWRITTEN-LONGEX")
	r.write(id, samesize)
	expectConverged("same-size rewrite", samesize, 1, 1)

	// Truncate to the header, then regrow to the original line count
	// with different content.
	r.write(id, strings.SplitAfter(orig, "\n")[0])
	if c := r.sweep(); len(c) != 1 {
		t.Fatalf("truncate to header: %+v", c)
	}
	regrown := strings.ReplaceAll(orig, "FIXTURE-OK", "REGROWN-OK")
	r.write(id, regrown)
	s3 := expectConverged("regrow", regrown, 2, 2)
	eq(t, "regrow lines", s3.Transcript.Lines, first.Transcript.Lines)
	if s3.Transcript.SHA256 == first.Transcript.SHA256 {
		t.Error("regrown content matched the original sha")
	}
}

func TestRecaptureTornTailCompletes(t *testing.T) {
	r := newRig(t, false)
	id := idPrefix + "0000000d01"
	t1, last := turn(1, "", 1)
	u2 := msg("u2", last, sec(3), "user", `,"content":[{"type":"text","text":"FIXTURE-OK"}]`)
	a2 := msg("a2", "u2", sec(4), "assistant", content1)
	cut := len(a2) / 2
	r.write(id, ptsHdr(id, sec(0), "")+t1+u2+a2[:cut]) // torn: no newline, invalid JSON
	r.sweep()
	torn, _ := r.find(id)
	eq(t, "torn counts", torn.Counts, journal.SessionCounts{User: 2, Assistant: 1})
	if !strings.HasSuffix(string(r.artifact(id)), a2[:cut]) {
		t.Error("torn tail not kept verbatim")
	}
	if c := r.sweep(); len(c) != 0 {
		t.Fatalf("stalled torn tail churned: %+v", c)
	}

	// Pi finishes the line and goes on.
	t3, _ := turn(3, "a2", 6)
	r.append(id, a2[cut:]+t3)
	if c := r.sweep(); len(c) != 1 || !c[0].Recaptured {
		t.Fatalf("completed tail not recaptured: %+v", c)
	}
	got, _ := r.find(id)
	// Wrong shortcut: keeping the torn-era facts would leave a2 out.
	eq(t, "counts", got.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	eq(t, "last active", got.LastActiveAt, mustTime(t, sec(7)))
	src, _ := os.ReadFile(r.path(id))
	if !bytes.Equal(r.artifact(id), src) {
		t.Error("artifact != source")
	}
}

func forkOf(parentBytes, childID, childTS, parentPath string) string {
	lines := strings.SplitAfter(parentBytes, "\n")
	return ptsHdr(childID, childTS, fmt.Sprintf(`,"parentSession":%q`, parentPath)) + strings.Join(lines[1:], "")
}

func TestRecaptureForks(t *testing.T) {
	r := newRig(t, false)
	pid, cid, lid := idPrefix+"0000000e01", idPrefix+"0000000e02", idPrefix+"0000000e03"
	parent := twoTurns(pid)
	r.write(pid, parent)
	r.sweep()
	pFirst, _ := r.find(pid)
	pState := r.sessionState(pid)

	// Fork added in a later sweep: copied entries under the same ids.
	r.write(cid, forkOf(parent, cid, sec(10), r.path(pid)))
	c := r.sweep()
	if len(c) != 1 || c[0].Key.NativeID != cid || c[0].Recaptured {
		t.Fatalf("fork sweep = %+v, want one new session %s", c, cid)
	}
	if !sameState(pState, r.sessionState(pid)) {
		t.Error("parent journal touched by the fork's arrival")
	}
	child, _ := r.find(cid)
	// Wrong shortcut: de-duplicating the child's entries by id against
	// the parent would merge or zero the fork's own counts.
	eq(t, "child counts", child.Counts, journal.SessionCounts{User: 2, Assistant: 2})
	if !child.StartedAt.Equal(mustTime(t, sec(10))) || child.StartedAt.Equal(pFirst.StartedAt) {
		t.Errorf("child StartedAt = %v", child.StartedAt)
	}
	_, pDir := r.find(pid)
	_, cDir := r.find(cid)
	if pDir == cDir {
		t.Error("fork shares the parent's journal directory")
	}
	srcChild, _ := os.ReadFile(r.path(cid))
	if !bytes.Equal(r.artifact(cid), srcChild) || !bytes.Contains(r.artifact(cid), []byte(`"parentSession"`)) {
		t.Error("child artifact is not its own full verbatim copy")
	}

	// Child grows: parent untouched.
	t3, _ := turn(3, "a2", 12)
	r.append(cid, t3)
	if c := r.sweep(); len(c) != 1 || c[0].Key.NativeID != cid || !c[0].Recaptured {
		t.Fatalf("child growth = %+v", c)
	}
	grown, _ := r.find(cid)
	eq(t, "child counts", grown.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	if !sameState(pState, r.sessionState(pid)) {
		t.Error("parent touched when the child grew")
	}

	// Parent grows (own new entry ids): child untouched.
	cState := r.sessionState(cid)
	p3, _ := turn(30, "a2", 14)
	r.append(pid, p3)
	if c := r.sweep(); len(c) != 1 || c[0].Key.NativeID != pid || !c[0].Recaptured {
		t.Fatalf("parent growth = %+v", c)
	}
	pGrown, _ := r.find(pid)
	eq(t, "parent counts", pGrown.Counts, journal.SessionCounts{User: 3, Assistant: 3})
	if !sameState(cState, r.sessionState(cid)) {
		t.Error("child touched when the parent grew")
	}

	// A second, fresh fork (no new entries) of the grown parent, in
	// another sweep: both earlier sessions stay put.
	pState, cState = r.sessionState(pid), r.sessionState(cid)
	pb, _ := os.ReadFile(r.path(pid))
	r.write(lid, forkOf(string(pb), lid, sec(20), r.path(pid)))
	if c := r.sweep(); len(c) != 1 || c[0].Key.NativeID != lid {
		t.Fatalf("second fork = %+v", c)
	}
	if !sameState(pState, r.sessionState(pid)) || !sameState(cState, r.sessionState(cid)) {
		t.Error("existing family members touched by a new fork")
	}
	items, _, _ := journal.Walk(r.jr)
	eq(t, "sessions", len(items), 3)
	lf, _ := r.find(lid)
	if lf.LastActiveAt.Before(lf.StartedAt) {
		t.Errorf("fresh fork LastActiveAt %v before StartedAt %v", lf.LastActiveAt, lf.StartedAt)
	}
}

func TestRecaptureCurationGrowthRetractsAutoDismissal(t *testing.T) {
	r := newRig(t, true)
	autoID, userID := idPrefix+"0000000f01", idPrefix+"0000000f02"
	r.write(autoID, ptsHdr(autoID, sec(0), ""))
	r.write(userID, ptsHdr(userID, sec(0), ""))
	c := r.sweep()
	if len(c) != 2 || !c[0].AutoDismissed || !c[1].AutoDismissed {
		t.Fatalf("header-only sessions not auto-dismissed: %+v", c)
	}
	// The user takes over one of them with their own dismissal.
	var userKey journal.SessionKey
	var userShard string
	for _, x := range c {
		if x.Key.NativeID == userID {
			userKey, userShard = x.Key, x.Shard
		}
	}
	reason := "mine"
	if err := journal.WriteCuration(r.jr, userShard, userKey, journal.Curation{
		State: journal.StateDismissed, At: time.Unix(1, 0).UTC(), Machine: "m", Reason: &reason,
	}); err != nil {
		t.Fatal(err)
	}

	t1, _ := turn(1, "", 1)
	r.append(autoID, t1)
	r.append(userID, t1)
	c = r.sweep()
	if len(c) != 2 {
		t.Fatalf("growth sweep = %+v", c)
	}
	for _, x := range c {
		if !x.Recaptured || x.AutoDismissed {
			t.Errorf("%s: recaptured=%v dismissed=%v", x.Key.NativeID, x.Recaptured, x.AutoDismissed)
		}
		s, _ := r.find(x.Key.NativeID)
		if !s.Substantive || s.Counts.Assistant != 1 {
			t.Errorf("%s: facts after growth %+v subst=%v", x.Key.NativeID, s.Counts, s.Substantive)
		}
		cur, present, err := journal.ReadCuration(r.jr, x.Shard, x.Key)
		if err != nil {
			t.Fatal(err)
		}
		switch x.Key.NativeID {
		case autoID:
			if present {
				t.Errorf("auto:no-op dismissal not retracted: %+v", cur)
			}
		case userID:
			// Wrong shortcut: retracting any dismissal on growth would
			// override the user's own call.
			if !present || cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != "mine" {
				t.Errorf("user dismissal lost: %+v present=%v", cur, present)
			}
		}
	}
}
