package devin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

// render reads a captured artifact through the public renderer seam.
func render(t *testing.T, s *Source, id string, maxChars int) []source.Turn {
	t.Helper()
	_, a := mustCapture(t, s, id)
	turns, err := s.RenderTranscript(bytes.NewReader(a["transcript.json"]), maxChars)
	if err != nil {
		t.Fatalf("RenderTranscript %s: %v", id, err)
	}
	return turns
}

// readEvents reads a captured artifact through the public reader seam.
// Capture diagnostics (amber-fox's malformed node) are the capture
// layer's concern — the reader's job is what it does with the artifact.
func readEvents(t *testing.T, s *Source, id string) []source.Event {
	t.Helper()
	a := artifacts{}
	if _, _, err := s.Capture(context.Background(), source.Discovered{NativeID: id}, a.writer); err != nil {
		t.Fatalf("Capture %s: %v", id, err)
	}
	events, err := s.ReadTranscript(bytes.NewReader(a["transcript.json"]))
	if err != nil {
		t.Fatalf("ReadTranscript %s: %v", id, err)
	}
	return events
}

func eventsOf(events []source.Event, kind source.EventKind) []source.Event {
	var out []source.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestRenderBasicConversation(t *testing.T) {
	turns := render(t, NewAt(buildStore(t, "basic.sql")), "quiet-meadow", 0)
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2 (system/tool/meta skip)", len(turns))
	}
	if turns[0].Role != "user" || !strings.Contains(turns[0].Text, "FIXTURE-OK") {
		t.Errorf("turn[0] = %+v", turns[0])
	}
	if turns[1].Role != "assistant" || !strings.Contains(turns[1].Text, "FIXTURE-OK") {
		t.Errorf("turn[1] = %+v", turns[1])
	}
	// The abandoned rebuild branch contributes nothing — its repeated
	// message copy never renders twice.
}

func TestRenderCapsRunesNotBytes(t *testing.T) {
	// unicode.jsonl shapes a one-turn artifact inline — the cap must
	// count runes so a multibyte glyph never splits mid-codepoint.
	doc := `{"format":"devin-export-v1","store_schema_version":17,"session_id":"uni",
		"session":{"main_chain_id":1},
		"message_nodes":[
			{"node_id":0,"parent_node_id":null,"chat_message":"{\"message_id\":\"m0\",\"role\":\"system\",\"content\":\"sys\"}","created_at":1,"metadata":null},
			{"node_id":1,"parent_node_id":0,"chat_message":"{\"message_id\":\"m1\",\"role\":\"user\",\"content\":\"héllo wörld ✨\",\"metadata\":{\"is_user_input\":true}}","created_at":2,"metadata":null}
		],"tool_call_state":[],"prompt_history":[],"subagent_heads":[]}`
	turns, err := New().RenderTranscript(strings.NewReader(doc), 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Text != "héllo " {
		t.Fatalf("turns = %+v, want one 6-rune turn", turns)
	}
	for _, r := range turns[0].Text {
		if r == '�' {
			t.Fatal("cap split a codepoint")
		}
	}
}

func TestRenderRefusesForeignArtifact(t *testing.T) {
	if _, err := New().RenderTranscript(strings.NewReader(`{"format":"claude-jsonl"}`), 0); err == nil {
		t.Fatal("a foreign format must not read as devin-export-v1")
	}
	if _, err := New().RenderTranscript(strings.NewReader(`not json`), 0); err == nil {
		t.Fatal("garbage must fail, not read empty")
	}
}

func TestReadBasicEvents(t *testing.T) {
	events := readEvents(t, NewAt(buildStore(t, "basic.sql")), "quiet-meadow")

	var seq []source.EventKind
	for _, e := range events {
		seq = append(seq, e.Kind)
	}
	// system meta, prompt, thinking, tool_call (result attached),
	// thinking, assistant — both assistant nodes carry thinking.
	if len(seq) != 6 {
		t.Fatalf("event kinds = %v", seq)
	}
	if seq[0] != source.KindMeta || events[0].Label != "role:system" {
		t.Errorf("first event = %+v", events[0])
	}
	if seq[1] != source.KindPrompt {
		t.Errorf("prompt = %+v", events[1])
	}
	if seq[2] != source.KindThinking {
		t.Errorf("thinking = %+v", events[2])
	}
	call := events[3]
	if call.Kind != source.KindToolCall || call.Tool == nil {
		t.Fatalf("tool event = %+v", call)
	}
	if call.Tool.Name != "exec" {
		t.Errorf("call name = %q", call.Tool.Name)
	}
	if call.Tool.Result == nil {
		t.Fatal("the tool node must pair onto the call")
	}
	if !strings.Contains(call.Tool.Result.Text, "FIXTURE-OK") || call.Tool.Result.IsError {
		t.Errorf("result = %+v", call.Tool.Result)
	}
	if call.Tool.Result.Time.IsZero() {
		t.Error("result carries no time")
	}
	if seq[4] != source.KindThinking || seq[5] != source.KindAssistant {
		t.Errorf("tail events = %+v", events[4:])
	}
	if len(eventsOf(events, source.KindToolResult)) != 0 {
		t.Error("paired results never appear as their own event")
	}
}

func TestReadInterruptedVerdicts(t *testing.T) {
	events := readEvents(t, NewAt(buildStore(t, "edge-cases.sql")), "amber-fox")

	byCallID := map[string]*source.ToolCall{}
	var orphans []source.Event
	for _, e := range events {
		switch e.Kind {
		case source.KindToolCall:
			byCallID[e.Tool.ID] = e.Tool
		case source.KindToolResult:
			orphans = append(orphans, e)
		}
	}
	// The canceled call: result paired, marked error, canceled text.
	if r := byCallID["exec_0"].Result; r == nil || !r.IsError || !strings.Contains(r.Text, "Canceled") {
		t.Errorf("exec_0 result = %+v", r)
	}
	// The user-rejected call.
	if r := byCallID["read_1"].Result; r == nil || !r.IsError || !strings.Contains(r.Text, "rejected") {
		t.Errorf("read_1 result = %+v", r)
	}
	// The hook-blocked update-only row: no call on chain, so one orphan
	// result carries the blocked text.
	if len(orphans) != 1 || orphans[0].ID != "tool-result:exec_1" {
		t.Fatalf("orphans = %+v, want tool-result:exec_1", orphans)
	}
	if !orphans[0].Tool.Result.IsError || !strings.Contains(orphans[0].Tool.Result.Text, "blocked by probe policy") {
		t.Errorf("orphan result = %+v", orphans[0].Tool.Result)
	}
	if orphans[0].Tool.Name != "exec" {
		t.Errorf("orphan name from inferenceToolName = %q", orphans[0].Tool.Name)
	}
}

func TestReadShortenedChainAndSubagentMeta(t *testing.T) {
	events := readEvents(t, NewAt(buildStore(t, "branched-shortened.sql")), "rapid-lark")
	// Active chain 6(system)→7(assistant)→10(system notification): the
	// abandoned 0-5 branch and the subagent sidechain 8-9 never render.
	var kinds []source.EventKind
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.Kind == source.KindAssistant && e.Text != "Rewound answer." {
			t.Errorf("assistant = %q", e.Text)
		}
	}
	if len(kinds) != 3 {
		t.Fatalf("kinds = %v, want meta/assistant/meta", kinds)
	}
	if !strings.Contains(events[2].Text, "subagent_completion_notification") {
		t.Errorf("notification text = %q", events[2].Text)
	}

	// The tool-tip session reads call→paired-result with nothing dangling.
	events = readEvents(t, NewAt(buildStore(t, "branched-shortened.sql")), "crashed-stork")
	calls := eventsOf(events, source.KindToolCall)
	if len(calls) != 1 || calls[0].Tool.Result == nil {
		t.Fatalf("crashed-stork calls = %+v", calls)
	}
	if !strings.Contains(calls[0].Tool.Result.Text, "running in background") {
		t.Errorf("result text = %q", calls[0].Tool.Result.Text)
	}
}

func TestReadEmptyAndForeign(t *testing.T) {
	events := readEvents(t, NewAt(buildStore(t, "edge-cases.sql")), "empty-rook")
	if len(events) != 0 {
		t.Errorf("empty session = %d events", len(events))
	}
	if _, err := New().ReadTranscript(strings.NewReader(`{"format":"other"}`)); err == nil {
		t.Error("foreign format must refuse")
	}
}

func TestListSubagentsIsEmpty(t *testing.T) {
	subs, err := New().ListSubagents(t.TempDir())
	if err != nil || subs != nil {
		t.Fatalf("subs = %v err = %v, want nil,nil — subagents are in-artifact chains", subs, err)
	}
}
