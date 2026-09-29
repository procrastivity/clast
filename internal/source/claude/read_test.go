package claude

import (
	"os"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

func readFixture(t *testing.T) []source.Event {
	t.Helper()
	f, err := os.Open("testdata/readsession/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	events, err := New().ReadTranscript(f)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	return events
}

func TestReadTranscript_Fixture(t *testing.T) {
	ev := readFixture(t)
	type row struct {
		kind  source.EventKind
		label string
		text  string
	}
	want := []row{
		{source.KindMeta, "mode", "normal"},
		{source.KindPrompt, "", "please list the files"},
		{source.KindThinking, "", ""},
		{source.KindThinking, "", "need ls"},
		{source.KindAssistant, "", "Listing now."},
		{source.KindToolCall, "", ""}, // Bash, paired
		{source.KindToolCall, "", ""}, // Agent, paired, error
		{source.KindToolCall, "", ""}, // Read, unpaired
		{source.KindToolResult, "", ""},
		{source.KindPrompt, "", "see this"},
		{source.KindMeta, "user:meta", "<local-command-caveat>ignore</local-command-caveat>"},
		{source.KindMeta, "user:task-notification", "task done"},
		{source.KindMeta, "system:turn_duration", "1.5s"},
		{source.KindMeta, "system:local_command", "<command-name>/mode</command-name>"},
		{source.KindMeta, "attachment:queued_command", "typed while busy"},
		{source.KindMeta, "attachment:date", ""},
		{source.KindMeta, "brand-new-record", ""},
		{source.KindAssistant, "", "plain string reply"},
	}
	if len(ev) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(ev), len(want), ev)
	}
	for i, w := range want {
		if ev[i].Kind != w.kind || ev[i].Label != w.label || ev[i].Text != w.text {
			t.Errorf("event %d = {%s %q %q}, want {%s %q %q}", i, ev[i].Kind, ev[i].Label, ev[i].Text, w.kind, w.label, w.text)
		}
	}

	if !ev[2].Redacted || ev[3].Redacted {
		t.Errorf("Redacted = %v/%v, want true/false", ev[2].Redacted, ev[3].Redacted)
	}
	if ev[1].Time.IsZero() || !ev[0].Time.IsZero() {
		t.Errorf("times: prompt %v, mode %v", ev[1].Time, ev[0].Time)
	}

	bash := ev[5].Tool
	if bash.Name != "Bash" || string(bash.Input) != `{"command":"ls"}` || bash.Result == nil ||
		bash.Result.Text != "a.txt\nb.txt" || bash.Result.IsError {
		t.Errorf("bash call = %+v result %+v", bash, bash.Result)
	}
	agent := ev[6].Tool
	if agent.Result == nil || !agent.Result.IsError || agent.Result.Text != "first\nsecond" || agent.Result.Images != 1 {
		t.Errorf("agent result = %+v", agent.Result)
	}
	if ev[7].Tool.Result != nil {
		t.Errorf("unpaired call has result %+v", ev[7].Tool.Result)
	}
	if o := ev[8].Tool; o.ID != "toolu_gone" || o.Name != "" || o.Result == nil || o.Result.Text != "stray" {
		t.Errorf("orphan = %+v", o)
	}
	if ev[9].Images != 1 {
		t.Errorf("prompt images = %d, want 1", ev[9].Images)
	}
}

func TestReadTranscript_ToolReferenceResult(t *testing.T) {
	data := `{"type":"assistant","uuid":"a","message":{"content":[{"type":"tool_use","id":"t","name":"ToolSearch","input":{}}]}}` + "\n" +
		`{"type":"user","uuid":"u","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"tool_reference","tool_name":"Foo"}]}]}}` + "\n"
	ev, err := New().ReadTranscript(strings.NewReader(data))
	if err != nil || len(ev) != 1 || ev[0].Tool.Result.Text != "[tool reference: Foo]" {
		t.Fatalf("ev = %+v, err %v", ev, err)
	}
}

func TestReadTranscript_Empty(t *testing.T) {
	ev, err := New().ReadTranscript(strings.NewReader(""))
	if err != nil || len(ev) != 0 {
		t.Fatalf("ev = %+v, err %v", ev, err)
	}
}

func TestListSubagents(t *testing.T) {
	subs, err := New().ListSubagents("testdata/readsession")
	if err != nil {
		t.Fatal(err)
	}
	want := []source.Subagent{
		{ID: "aaa", Path: "subagents/agent-aaa.jsonl"},
		{ID: "bbb", Path: "subagents/agent-bbb.jsonl", AgentType: "Explore", Description: "look around", ToolUseID: "toolu_agent"},
	}
	if len(subs) != len(want) {
		t.Fatalf("subs = %+v", subs)
	}
	for i := range want {
		if subs[i] != want[i] {
			t.Errorf("subs[%d] = %+v, want %+v", i, subs[i], want[i])
		}
	}
}

func TestListSubagents_NoDir(t *testing.T) {
	subs, err := New().ListSubagents(t.TempDir())
	if err != nil || subs != nil {
		t.Fatalf("subs = %+v, err %v", subs, err)
	}
}
