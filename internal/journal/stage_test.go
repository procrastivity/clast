package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStageCommit(t *testing.T) {
	root := t.TempDir()
	stage, err := StageSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Write("transcript.jsonl", strings.NewReader("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Write("subagents/agent-1.jsonl", strings.NewReader("{\"a\":1}\n")); err != nil {
		t.Fatal(err)
	}

	key := SessionKey{Harness: "claude", NativeID: "abc"}
	if err := stage.Commit("2026-09-01", key); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(TranscriptPath(root, "2026-09-01", key))
	if err != nil || string(data) != "{}\n" {
		t.Fatalf("transcript: %q, %v", data, err)
	}
	sidecar := filepath.Join(SessionDir(root, "2026-09-01", key), "subagents", "agent-1.jsonl")
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar: %v", err)
	}

	// The staging directory is gone; nothing dot-prefixed remains at root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".capture-") {
			t.Errorf("staging directory %s survived Commit", e.Name())
		}
	}
}

func TestSessionStageRefusesEscapingPaths(t *testing.T) {
	stage, err := StageSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stage.Discard() }()
	for _, bad := range []string{"../outside.jsonl", "/abs.jsonl", "a/../../b"} {
		if err := stage.Write(bad, strings.NewReader("x")); err == nil {
			t.Errorf("Write(%q) accepted an escaping path", bad)
		}
	}
}

func TestSessionStageDiscard(t *testing.T) {
	root := t.TempDir()
	stage, err := StageSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Write("transcript.jsonl", strings.NewReader("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Discard(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".capture-") {
			t.Errorf("staging directory %s survived Discard", e.Name())
		}
	}
}
