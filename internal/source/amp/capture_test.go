package amp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// captureSource is a fixture-bound source with its export transport
// faked — the shape a capture run sees after Discover+Correlate.
func captureSource(t *testing.T, raw []byte) (*Source, *fakeCLI, source.Discovered) {
	t.Helper()
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	s := newTestSource(t, fixtureDataDir(t))
	s.run = f.run
	return s, f, discoveredForID(t, raw)
}

// TestCaptureHappyPath: the verbatim export lands as transcript.json and
// the Facts projection is computed from those same bytes — artifact and
// fingerprint can never diverge.
func TestCaptureHappyPath(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json")
	s, f, d := captureSource(t, raw)

	dir := t.TempDir()
	facts, diags, err := s.Capture(context.Background(), d, dirWriter(t, dir))
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("idle-local doc should capture clean, diags=%v", diags)
	}

	written, err := os.ReadFile(filepath.Join(dir, TranscriptFile))
	if err != nil {
		t.Fatalf("reading %s: %v", TranscriptFile, err)
	}
	if !bytes.Equal(written, raw) {
		t.Error("transcript.json must be the verbatim export bytes")
	}

	// Facts against the pinned document's known shape.
	doc := parseFixture(t, "exports/idle-local-client.json")
	wantSum := sha256.Sum256(projectMessages(doc))
	if facts.Transcript.Format != TranscriptFormat {
		t.Errorf("format = %q, want %q", facts.Transcript.Format, TranscriptFormat)
	}
	if facts.Transcript.Lines != 4 {
		t.Errorf("lines = %d, want 4", facts.Transcript.Lines)
	}
	if facts.Transcript.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("sha256 mismatch — fingerprint must cover the projection of the written bytes")
	}
	if facts.Counts.User != 2 || facts.Counts.Assistant != 2 {
		t.Errorf("counts = %+v, want user=2 assistant=2", facts.Counts)
	}
	if !facts.Substantive {
		t.Error("a doc with assistant messages is substantive")
	}
	if !facts.StartedAt.Equal(time.UnixMilli(1788064264680).UTC()) {
		t.Errorf("StartedAt = %v", facts.StartedAt)
	}
	if !facts.LastActiveAt.Equal(mustParse(t, doc.UpdatedAt)) {
		t.Errorf("LastActiveAt = %v, want %v", facts.LastActiveAt, doc.UpdatedAt)
	}
	if facts.Branch != "" {
		t.Errorf("Branch = %q — the export carries a commit sha, not a branch", facts.Branch)
	}
	if n := f.exportCalls(); n != 1 {
		t.Errorf("exportCalls = %d, want 1", n)
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestCaptureMidTurn: a streaming doc is captured now — flagged with the
// agent state, never delayed until idle.
func TestCaptureMidTurn(t *testing.T) {
	raw := mustFixture(t, "exports/midturn-streaming.json")
	s, _, d := captureSource(t, raw)

	dir := t.TempDir()
	facts, diags, err := s.Capture(context.Background(), d, dirWriter(t, dir))
	if err != nil {
		t.Fatalf("mid-turn capture must not error: %v", err)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "streaming") {
		t.Fatalf("want one mid-turn diagnostic naming the state, got %v", diags)
	}
	if diags[0].Path != d.Path {
		t.Errorf("diag Path = %q, want the thread URL", diags[0].Path)
	}
	if _, err := os.Stat(filepath.Join(dir, TranscriptFile)); err != nil {
		t.Errorf("mid-turn doc must still be written: %v", err)
	}
	if facts.Transcript.Lines != 3 {
		t.Errorf("lines = %d, want 3", facts.Transcript.Lines)
	}
}

// TestCaptureToolResultCounts: a user message that is a tool_result
// carrier is not a prompt — the count matches amp's own messageCount
// rule.
func TestCaptureToolResultCounts(t *testing.T) {
	raw := mustFixture(t, "exports/idle-task-toolresult.json")
	s, _, d := captureSource(t, raw)
	facts, _, err := s.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if facts.Counts.User != 1 || facts.Counts.Assistant != 2 {
		t.Errorf("counts = %+v, want user=1 (tool_result carrier excluded) assistant=2", facts.Counts)
	}
}

// TestCaptureOldEpoch: the 0-based messageId epoch captures clean —
// contiguity is relative to the epoch's own base.
func TestCaptureOldEpoch(t *testing.T) {
	raw := mustFixture(t, "exports/old-epoch.json")
	s, _, d := captureSource(t, raw)
	facts, diags, err := s.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if err != nil || len(diags) != 0 {
		t.Fatalf("old-epoch capture: diags=%v err=%v", diags, err)
	}
	if facts.Counts.User != 2 || facts.Transcript.Lines != 4 {
		t.Errorf("old-epoch facts = %+v", facts)
	}
}

// TestCaptureNotExist: a deleted thread is the terminal per-item
// diagnostic — an error, no retry, nothing written.
func TestCaptureNotExist(t *testing.T) {
	s, f, _ := captureSource(t, mustFixture(t, "exports/idle-local-client.json"))
	dir := t.TempDir()
	_, _, err := s.Capture(context.Background(),
		source.Discovered{NativeID: "T-gone", Path: threadURL("T-gone")}, dirWriter(t, dir))
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errNotExist {
		t.Fatalf("want errNotExist, got %v", err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Errorf("exportCalls = %d — does-not-exist must not retry", n)
	}
	if _, statErr := os.Stat(filepath.Join(dir, TranscriptFile)); !os.IsNotExist(statErr) {
		t.Error("nothing may be written for a failed capture")
	}
}

// TestCaptureEmptyWithReportedCount: a 0-messages export for a thread
// `threads list` reports as non-empty keeps its diagnostic even after
// the retry bound returns the doc.
func TestCaptureEmptyWithReportedCount(t *testing.T) {
	raw := []byte(strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", "T-empty", 1))
	s, f, d := captureSource(t, raw)
	s.listCounts = map[string]int{"T-empty": 4}

	dir := t.TempDir()
	_, diags, err := s.Capture(context.Background(), d, dirWriter(t, dir))
	if err != nil {
		t.Fatalf("an exhausted retry still captures the last doc: %v", err)
	}
	var sawEmpty, sawCount bool
	for _, dg := range diags {
		sawEmpty = sawEmpty || strings.Contains(dg.Err.Error(), "no messages")
		sawCount = sawCount || strings.Contains(dg.Err.Error(), "reports 4")
	}
	if !sawEmpty || !sawCount {
		t.Fatalf("want the retry note AND the list-count diag, got %v", diags)
	}
	if _, statErr := os.Stat(filepath.Join(dir, TranscriptFile)); statErr != nil {
		t.Error("the last faithful read must still be written")
	}
	if n := f.exportCalls(); n != 3 {
		t.Errorf("exportCalls = %d, want the 3-attempt bound", n)
	}
}

// TestCaptureWriteFailure: the artifact write is the caller's primitive;
// its failure propagates as the capture error.
func TestCaptureWriteFailure(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json")
	s, _, d := captureSource(t, raw)
	want := errors.New("disk full")
	_, _, err := s.Capture(context.Background(), d, func(string, io.Reader) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("want the writer's error, got %v", err)
	}
}
