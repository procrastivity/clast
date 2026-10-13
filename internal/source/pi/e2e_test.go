package pi_test

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source/pi"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/verbs/capture"
)

type fileState struct {
	sum   [32]byte
	mtime time.Time
}

// treeState records every file's bytes and mtime under root.
func treeState(t *testing.T, root string) map[string]fileState {
	t.Helper()
	m := map[string]fileState{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		m[p] = fileState{sha256.Sum256(b), info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestCaptureEndToEnd drives the public capture.Run with an injected pi
// source over a copy of the percwd fixture and a temporary journal.
func TestCaptureEndToEnd(t *testing.T) {
	srcRoot := filepath.Join(t.TempDir(), "percwd")
	copyTree(t, filepath.Join(fixtureRoots(t), "percwd"), srcRoot)
	journalRoot := t.TempDir()
	deps := capture.Deps{
		Root:    journalRoot,
		Sources: []source.Source{pi.NewAt(srcRoot)},
		Now:     func() time.Time { return time.Unix(1792000000, 0).UTC() },
	}
	srcBefore := snapshot(t, srcRoot)

	captured, diags, failed, err := capture.Run(t.Context(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("Run: err=%v failed=%v", err, failed)
	}
	if len(captured) != 16 {
		t.Fatalf("captured %d, want 16", len(captured))
	}
	// Exactly the two fixtures with bad lines are disclosed: bad-lines
	// (1) and broken-chain (2).
	if len(diags) != 3 {
		t.Fatalf("diags = %d: %q", len(diags), diagStrings(diags))
	}

	for _, c := range captured {
		if c.Recaptured || c.AutoDismissed {
			t.Errorf("%v: recaptured=%v dismissed=%v", c.Key, c.Recaptured, c.AutoDismissed)
		}
		if c.Key.Harness != "pi" {
			t.Errorf("harness = %q", c.Key.Harness)
		}
		sess, ok, err := journal.ReadSession(journalRoot, c.Shard, c.Key)
		if err != nil || !ok {
			t.Fatalf("ReadSession %v: %v %v", c.Key, ok, err)
		}
		srcFiles, _ := filepath.Glob(filepath.Join(srcRoot, "*", "*"+c.Key.NativeID+".jsonl"))
		if len(srcFiles) != 1 {
			t.Fatalf("source for %s = %v", c.Key.NativeID, srcFiles)
		}
		srcBytes, _ := os.ReadFile(srcFiles[0])
		stored, err := os.ReadFile(filepath.Join(filepath.Dir(journal.SessionJSONPath(journalRoot, c.Shard, c.Key)), "transcript.jsonl"))
		if err != nil || !bytes.Equal(stored, srcBytes) {
			t.Errorf("%s: stored transcript differs from source (err=%v)", c.Key.NativeID, err)
		}
		lines, sum, _ := source.FingerprintFile(srcFiles[0])
		if sess.Harness != "pi" || sess.SessionID != c.Key.NativeID || sess.SourcePath != srcFiles[0] ||
			sess.Transcript.Format != "pi-jsonl-v3" || sess.Transcript.Lines != lines || sess.Transcript.SHA256 != sum ||
			sess.Transcript.Artifact != "" || sess.Branch != "" || sess.Incomplete || sess.Project != nil {
			t.Errorf("%s: session.json = %+v", c.Key.NativeID, sess)
		}
		if !sess.CapturedAt.Equal(deps.Now()) {
			t.Errorf("captured_at = %v", sess.CapturedAt)
		}
	}

	// Spot-check facts through the journal for the shapes that differ.
	check := func(globSuffix string, u, a int, subst bool, last string) {
		t.Helper()
		for _, c := range captured {
			if !strings.HasSuffix(c.Key.NativeID, globSuffix) {
				continue
			}
			s, _, _ := journal.ReadSession(journalRoot, c.Shard, c.Key)
			if s.Counts.User != u || s.Counts.Assistant != a || s.Substantive != subst || !s.LastActiveAt.Equal(mustTime(t, last)) {
				t.Errorf("%s: %+v subst=%v last=%v", globSuffix, s.Counts, s.Substantive, s.LastActiveAt)
			}
			return
		}
		t.Errorf("%s not captured", globSuffix)
	}
	check("000000000006", 2, 2, true, "2026-01-13T09:00:06.000Z") // branch: leaf path
	check("000000000010", 2, 1, true, "2026-01-14T12:00:04.000Z") // broken chain: whole-file
	check("000000000012", 1, 1, false, "2026-01-15T10:00:02.000Z")
	check("000000000005", 2, 2, true, "2026-01-11T13:00:00.000Z")

	if after := snapshot(t, srcRoot); !equalSnap(srcBefore, after) {
		t.Error("capture changed the source tree")
	}

	// Second run: a clean no-op, nothing rewritten (bytes and mtimes).
	journalBefore := treeState(t, journalRoot)
	captured, diags, failed, err = capture.Run(t.Context(), deps)
	if err != nil || len(failed) != 0 || len(captured) != 0 || len(diags) != 0 {
		t.Fatalf("rerun: captured=%d diags=%q failed=%v err=%v", len(captured), diagStrings(diags), failed, err)
	}
	journalAfter := treeState(t, journalRoot)
	if len(journalBefore) != len(journalAfter) {
		t.Errorf("journal file count %d -> %d", len(journalBefore), len(journalAfter))
	}
	for p, b := range journalBefore {
		if a, ok := journalAfter[p]; !ok || a != b {
			t.Errorf("%s rewritten or removed on a no-op rerun", p)
		}
	}

	// A grown session is the only one recaptured.
	grown := filepath.Join(srcRoot, "--home-user-Code-demo--", "2026-01-10T10-00-00-000Z_"+idPrefix+"000000000001.jsonl")
	f, err := os.OpenFile(grown, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.WriteString(msg("zz", "", "2026-01-10T10:05:00.000Z", "user", ""))
	_ = f.Close()
	if werr != nil {
		t.Fatal(werr)
	}
	captured, _, _, err = capture.Run(t.Context(), deps)
	if err != nil || len(captured) != 1 || !captured[0].Recaptured || captured[0].Key.NativeID != idPrefix+"000000000001" {
		t.Fatalf("regrow: captured=%v err=%v", captured, err)
	}
}

func equalSnap(a, b map[string]snapEntry) bool {
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
