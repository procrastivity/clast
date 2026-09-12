package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

// dirWriter binds source.WriteArtifact to a plain directory — the test
// stand-in for the journal primitive the capture verb will bind.
func dirWriter(t *testing.T, dir string) source.WriteArtifact {
	t.Helper()
	return func(relPath string, r io.Reader) error {
		path := filepath.Join(dir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			return err
		}
		return f.Close()
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestCaptureCopiesVerbatimWithSidecars(t *testing.T) {
	dest := t.TempDir()
	d := discoveredByID(t, simpleID)
	facts, diags, err := fixtureSource().Capture(context.Background(), d, dirWriter(t, dest))
	if err != nil || len(diags) != 0 {
		t.Fatalf("Capture: err=%v diags=%v", err, diags)
	}

	srcSum := fileSHA256(t, d.Path)
	if got := fileSHA256(t, filepath.Join(dest, "transcript.jsonl")); got != srcSum {
		t.Errorf("transcript copy is not verbatim: %s != %s", got, srcSum)
	}
	if facts.Transcript.SHA256 != srcSum {
		t.Errorf("fingerprint sha = %s, want %s", facts.Transcript.SHA256, srcSum)
	}
	if facts.Transcript.Format != TranscriptFormat {
		t.Errorf("format = %q, want %q", facts.Transcript.Format, TranscriptFormat)
	}
	if facts.Transcript.Lines != 11 {
		t.Errorf("lines = %d, want 11", facts.Transcript.Lines)
	}
	if facts.Counts.User != 2 || facts.Counts.Assistant != 2 || !facts.Substantive {
		t.Errorf("facts = %+v", facts)
	}
	if facts.Branch != "feature/hello" {
		t.Errorf("branch = %q", facts.Branch)
	}

	for _, sidecar := range []string{"agent-00000000deadbeef.jsonl", "agent-00000000deadbeef.meta.json"} {
		src := filepath.Join(strings.TrimSuffix(d.Path, ".jsonl"), "subagents", sidecar)
		copied := filepath.Join(dest, "subagents", sidecar)
		if fileSHA256(t, copied) != fileSHA256(t, src) {
			t.Errorf("sidecar %s is not a verbatim copy", sidecar)
		}
	}
}

func TestCaptureNoopSession(t *testing.T) {
	facts, diags, err := fixtureSource().Capture(context.Background(), discoveredByID(t, noopID), dirWriter(t, t.TempDir()))
	if err != nil || len(diags) != 0 {
		t.Fatalf("Capture: err=%v diags=%v", err, diags)
	}
	if facts.Substantive || facts.Counts.Assistant != 0 || facts.Counts.User != 1 {
		t.Errorf("facts = %+v", facts)
	}
}

// TestRecaptureGrownTranscript is the M13 file-tail promise: a grown
// source file re-copies whole and the fingerprint moves.
func TestRecaptureGrownTranscript(t *testing.T) {
	home := t.TempDir()
	slugDir := filepath.Join(home, "projects", "-home-user-Code-demo")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(discoveredByID(t, noopID).Path)
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(slugDir, noopID+".jsonl")
	if err := os.WriteFile(live, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewAt(home)
	dest := t.TempDir()
	discover := func() source.Discovered {
		found, _, err := src.Discover(context.Background())
		if err != nil || len(found) != 1 {
			t.Fatalf("Discover: %v %v", found, err)
		}
		return found[0]
	}

	first, _, err := src.Capture(context.Background(), discover(), dirWriter(t, dest))
	if err != nil {
		t.Fatal(err)
	}

	grown := append(fixture, []byte(`{"parentUuid":"20000000-bbbb-4bbb-8bbb-000000000001","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Understood."}],"stop_reason":"end_turn"},"uuid":"20000000-bbbb-4bbb-8bbb-000000000002","timestamp":"2026-09-02T08:00:05.000Z","userType":"external","cwd":"/home/user/Code/demo","sessionId":"bbbbbbbb-2222-4222-8222-222222222222","version":"2.1.226","gitBranch":"main"}`+"\n")...)
	if err := os.WriteFile(live, grown, 0o644); err != nil {
		t.Fatal(err)
	}

	second, _, err := src.Capture(context.Background(), discover(), dirWriter(t, dest))
	if err != nil {
		t.Fatal(err)
	}

	if second.Transcript.Lines != first.Transcript.Lines+1 {
		t.Errorf("lines = %d after growth, want %d", second.Transcript.Lines, first.Transcript.Lines+1)
	}
	if second.Transcript.SHA256 == first.Transcript.SHA256 {
		t.Error("fingerprint did not move on growth")
	}
	if got := fileSHA256(t, filepath.Join(dest, "transcript.jsonl")); got != fileSHA256(t, live) {
		t.Error("recapture did not re-copy the grown file")
	}
	if !second.Substantive || second.Counts.Assistant != 1 {
		t.Errorf("second capture facts = %+v", second)
	}
}
