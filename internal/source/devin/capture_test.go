package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// artifacts collects what a Capture hands source.WriteArtifact, keyed
// by relative path — the scratch-destination binding the seam exists
// for.
type artifacts map[string][]byte

func (a artifacts) writer(relPath string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	a[relPath] = b
	return nil
}

func mustCapture(t *testing.T, s *Source, id string) (source.Facts, artifacts) {
	t.Helper()
	a := artifacts{}
	facts, diags, err := s.Capture(context.Background(), source.Discovered{NativeID: id}, a.writer)
	if err != nil {
		t.Fatalf("Capture %s: %v", id, err)
	}
	for _, d := range diags {
		t.Fatalf("unexpected diagnostic: %s: %v", d.Path, d.Err)
	}
	if _, ok := a["transcript.json"]; !ok {
		t.Fatalf("no transcript.json artifact; got %v", keys(a))
	}
	return facts, a
}

func keys(a artifacts) []string {
	var out []string
	for k := range a {
		out = append(out, k)
	}
	return out
}

func parsedDoc(t *testing.T, a artifacts) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(a["transcript.json"], &doc); err != nil {
		t.Fatalf("artifact is not valid JSON: %v", err)
	}
	return doc
}

func TestCaptureBasic(t *testing.T) {
	facts, a := mustCapture(t, NewAt(buildStore(t, "basic.sql")), "quiet-meadow")
	doc := parsedDoc(t, a)

	if doc["format"] != "devin-export-v1" {
		t.Errorf("format = %v", doc["format"])
	}
	if doc["session_id"] != "quiet-meadow" {
		t.Errorf("session_id = %v", doc["session_id"])
	}
	if doc["store_schema_version"].(float64) != 17 {
		t.Errorf("store_schema_version = %v", doc["store_schema_version"])
	}
	sess := doc["session"].(map[string]any)
	if sess["working_directory"] != "/home/user/Code/demo" || sess["model"] != "swe-2-max" {
		t.Errorf("session row = %v", sess)
	}
	nodes := doc["message_nodes"].([]any)
	if len(nodes) != 7 {
		t.Fatalf("message_nodes = %d, want the whole 7-node forest", len(nodes))
	}
	// The abandoned rebuild branch is exported too — forest fidelity.
	if nodes[5].(map[string]any)["parent_node_id"] != nil {
		t.Errorf("node 5 (rebuild root) parent = %v, want null", nodes[5].(map[string]any)["parent_node_id"])
	}
	tool := doc["tool_call_state"].([]any)
	if len(tool) != 1 {
		t.Fatalf("tool_call_state = %d rows", len(tool))
	}
	if !strings.Contains(tool[0].(map[string]any)["tool_call_update_json"].(string), `"completed"`) {
		t.Errorf("tool_call_update_json not preserved verbatim: %v", tool[0])
	}
	prompts := doc["prompt_history"].([]any)
	if len(prompts) != 2 {
		t.Errorf("prompt_history = %d rows, want 2 (incl. is_shell)", len(prompts))
	}

	// Facts from the same snapshot: chain 0-4 = system,user,assistant,
	// tool,assistant — the abandoned branch never inflates counts.
	if facts.Counts.User != 1 || facts.Counts.Assistant != 2 {
		t.Errorf("counts = %+v, want user=1 assistant=2", facts.Counts)
	}
	if !facts.Substantive || facts.Incomplete {
		t.Errorf("substantive=%v incomplete=%v", facts.Substantive, facts.Incomplete)
	}
	if facts.Transcript.Lines != 7 || facts.Transcript.Format != "devin-export-v1" ||
		facts.Transcript.Artifact != "transcript.json" || facts.Transcript.SHA256 == "" {
		t.Errorf("fingerprint = %+v", facts.Transcript)
	}
	if facts.StartedAt.Unix() != 1791600000 || facts.LastActiveAt.Unix() != 1791600120 {
		t.Errorf("timestamps = %v..%v", facts.StartedAt, facts.LastActiveAt)
	}
}

func TestCaptureDeterministic(t *testing.T) {
	_, a1 := mustCapture(t, NewAt(buildStore(t, "basic.sql")), "quiet-meadow")
	_, a2 := mustCapture(t, NewAt(buildStore(t, "basic.sql")), "quiet-meadow")
	if !bytes.Equal(a1["transcript.json"], a2["transcript.json"]) {
		t.Fatal("identical store content must export byte-identical artifacts")
	}
}

func TestCaptureEdgeSessions(t *testing.T) {
	s := NewAt(buildStore(t, "edge-cases.sql"))

	// Interrupted session: chain tip at node 4, malformed node 6 is a
	// per-node diagnostic (never a failure), NULL tool_call_json and
	// rejected verdicts export verbatim.
	a := artifacts{}
	facts, diags, err := s.Capture(context.Background(), source.Discovered{NativeID: "amber-fox"}, a.writer)
	if err != nil {
		t.Fatalf("Capture amber-fox: %v", err)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Path, ":node 6") {
		t.Fatalf("diags = %v, want one malformed-node diagnostic", diags)
	}
	if facts.Counts.User != 1 || facts.Counts.Assistant != 2 {
		t.Errorf("amber-fox counts = %+v", facts.Counts)
	}
	doc := parsedDoc(t, a)
	tool := doc["tool_call_state"].([]any)
	if len(tool) != 3 {
		t.Fatalf("tool_call_state = %d rows, want 3 (incl. update-only)", len(tool))
	}
	var nullCall bool
	for _, row := range tool {
		if row.(map[string]any)["tool_call_id"] == "exec_1" {
			nullCall = row.(map[string]any)["tool_call_json"] == nil
		}
	}
	if !nullCall {
		t.Error("hook-blocked row must export tool_call_json as null")
	}
	if !strings.Contains(string(a["transcript.json"]), "cognition.ai/rejected") {
		t.Error("rejected verdict evidence not in export")
	}

	// ID-reuse session: node_id 0-2 and tool_call_id exec_0 collide
	// with amber-fox — session scoping must keep them distinct.
	facts, a = mustCapture(t, s, "brisk-owl")
	doc = parsedDoc(t, a)
	if len(doc["message_nodes"].([]any)) != 3 || len(doc["tool_call_state"].([]any)) != 1 {
		t.Error("cross-session rows leaked into brisk-owl's export")
	}
	if !strings.Contains(string(a["transcript.json"]), "custom_unknown_field") {
		t.Error("unknown native field not preserved in chat_message text")
	}

	// Hidden and empty sessions capture like any other.
	if _, a = mustCapture(t, s, "hidden-wren"); len(parsedDoc(t, a)["message_nodes"].([]any)) != 2 {
		t.Error("hidden session must capture")
	}
	facts, a = mustCapture(t, s, "empty-rook")
	if facts.Incomplete || facts.Counts.User != 0 || facts.Substantive {
		t.Errorf("empty-rook facts = %+v", facts)
	}
	if len(parsedDoc(t, a)["message_nodes"].([]any)) != 0 {
		t.Error("empty session must export zero nodes")
	}
}

func TestCaptureBranchedShortened(t *testing.T) {
	s := NewAt(buildStore(t, "branched-shortened.sql"))
	facts, a := mustCapture(t, s, "rapid-lark")

	// The active chain (6→7→10) is legitimately shorter than the
	// abandoned 0-5 branch: counts come from the chain only, and the
	// shrink is not an anomaly.
	if facts.Counts.User != 0 || facts.Counts.Assistant != 1 {
		t.Errorf("rapid-lark counts = %+v, want user=0 assistant=1", facts.Counts)
	}
	if facts.Incomplete {
		t.Error("a shortened-but-consistent chain is not incomplete")
	}
	doc := parsedDoc(t, a)
	if len(doc["message_nodes"].([]any)) != 11 {
		t.Errorf("whole forest = %d nodes, want 11", len(doc["message_nodes"].([]any)))
	}
	sub := doc["subagent_heads"].([]any)
	if len(sub) != 1 || sub[0].(map[string]any)["agent_id"] != "deadbeef" {
		t.Errorf("subagent_heads = %v", sub)
	}

	// A tip on a tool node (the SIGKILL shape) is a faithful chain.
	facts, _ = mustCapture(t, s, "crashed-stork")
	if facts.Counts.User != 1 || facts.Counts.Assistant != 1 || facts.Incomplete {
		t.Errorf("crashed-stork facts = %+v", facts)
	}
}

func TestCaptureSchema16Floor(t *testing.T) {
	facts, a := mustCapture(t, NewAt(buildStore(t, "schema16.sql")), "old-pine")
	doc := parsedDoc(t, a)
	if doc["store_schema_version"].(float64) != 16 {
		t.Errorf("schema version = %v", doc["store_schema_version"])
	}
	if len(doc["subagent_heads"].([]any)) != 0 {
		t.Error("schema 16 must export an empty subagent_heads section")
	}
	if facts.Counts.Assistant != 1 || facts.Counts.User != 1 {
		t.Errorf("old-pine counts = %+v", facts.Counts)
	}
}

func TestCaptureVanishedSession(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	_, _, err := s.Capture(context.Background(), source.Discovered{NativeID: "ghost"}, artifacts{}.writer)
	if err == nil || !strings.Contains(err.Error(), "no longer present") {
		t.Fatalf("err = %v, want per-item vanished-session failure", err)
	}
}

func TestCaptureLeavesStoreUntouched(t *testing.T) {
	path := buildStore(t, "basic.sql")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewAt(path)
	mustDiscover(t, s)
	mustCapture(t, s, "quiet-meadow")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		t.Error("mode=ro reads must not touch the store file")
	}
	if _, err := os.Stat(path + "-journal"); !os.IsNotExist(err) {
		t.Error("reader created a rollback journal — the store is being written")
	}
}

// TestCaptureCancelUnblocks proves ctx propagation: canceling mid-read
// surfaces through the pipe rather than deadlocking the stage write.
func TestCaptureCancelUnblocks(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := s.Capture(ctx, source.Discovered{NativeID: "quiet-meadow"}, artifacts{}.writer)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Log("capture won the race before cancel — acceptable")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled capture did not return")
	}
}
