package amp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// readFixture runs ReadTranscript over a pinned export doc.
func readFixture(t *testing.T, rel string) []source.Event {
	t.Helper()
	events, err := New().ReadTranscript(strings.NewReader(string(mustFixture(t, rel))))
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", rel, err)
	}
	return events
}

// kinds flattens an event stream to "kind" or "kind:label" for compact
// sequence assertions.
func kinds(events []source.Event) []string {
	var out []string
	for _, e := range events {
		k := string(e.Kind)
		if e.Label != "" {
			k += ":" + e.Label
		}
		out = append(out, k)
	}
	return out
}

func joinKinds(events []source.Event) string { return strings.Join(kinds(events), " ") }

// TestReadTranscript_NewEpoch: the new-epoch fixture end to end — doc
// metas lead, lkas tails, prompts carry sentAt time and pmid ids.
func TestReadTranscript_NewEpoch(t *testing.T) {
	events := readFixture(t, "exports/idle-local-client.json")
	want := "meta:agentMode prompt meta:model assistant prompt assistant meta:lastKnownAgentState"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].Text != "medium" {
		t.Errorf("agentMode = %q, want medium", events[0].Text)
	}
	if events[2].Text != "gpt-5.6-sol" {
		t.Errorf("model meta = %q, want gpt-5.6-sol", events[2].Text)
	}
	if got, want := events[6].Text, "idle"; got != want {
		t.Errorf("lkas meta = %q, want %q", got, want)
	}
	// Identity is the protocolMessageID; user time is meta.sentAt
	// (epoch ms — this pin predates per-message createdAt).
	p := events[1]
	if p.ID != "M-034FvxhfHJ53Et5UiYqg7a" || p.Text != "Reply with exactly one word: OK" {
		t.Errorf("prompt = %+v", p)
	}
	if want := time.UnixMilli(1788064267876).UTC(); !p.Time.Equal(want) {
		t.Errorf("prompt time = %v, want sentAt %v", p.Time, want)
	}
	if a := events[3]; a.ID != "M-034FvxirYkoX7dfe8OGf3r" || a.Text != "OK" || !a.Time.IsZero() {
		t.Errorf("assistant = %+v (want zero time — assistant msgs carry none at this pin)", a)
	}
}

// TestReadTranscript_OldEpoch: the old serialization — no
// protocolMessageID (ids fall back to mid:<messageId>), no createdAt
// (user times come from meta.sentAt; assistant msgs have none), no
// lkas (no trailing marker).
func TestReadTranscript_OldEpoch(t *testing.T) {
	events := readFixture(t, "exports/old-epoch.json")
	want := "prompt meta:model assistant prompt assistant"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].ID != "mid:0" || events[2].ID != "mid:1" {
		t.Errorf("ids = %q %q, want mid:0 mid:1", events[0].ID, events[2].ID)
	}
	if events[1].Text != "claude-haiku-4-5-20251001" {
		t.Errorf("model meta = %q", events[1].Text)
	}
	if want := time.UnixMilli(1772463205123).UTC(); !events[0].Time.Equal(want) {
		t.Errorf("prompt time = %v, want sentAt %v", events[0].Time, want)
	}
	if events[3].Text != "fixture: thanks" || events[4].Text != "fixture: welcome" {
		t.Errorf("tail events = %+v %+v", events[3], events[4])
	}
}

// TestReadTranscript_ToolLinking: the Task subagent thread — the call
// pairs with its user-carried result via toolUseID=id, the carrier
// emits no event of its own, and the encrypted-reasoning thinking block
// is Redacted (its text is empty).
func TestReadTranscript_ToolLinking(t *testing.T) {
	events := readFixture(t, "exports/idle-task-toolresult.json")
	want := "meta:agentMode prompt meta:model thinking tool_call assistant meta:lastKnownAgentState"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if !events[3].Redacted || events[3].Text != "" {
		t.Errorf("thinking = %+v, want redacted (encrypted reasoning carries no text)", events[3])
	}
	call := events[4].Tool
	if call == nil || call.ID != "TU-034FwCUCWrYayCUhtFy2C5" || call.Name != "Task" {
		t.Fatalf("call = %+v", call)
	}
	var input map[string]string
	if err := json.Unmarshal(call.Input, &input); err != nil {
		t.Fatalf("call input: %v", err)
	}
	if input["description"] != "Return required token" {
		t.Errorf("input = %v", input)
	}
	if call.Result == nil || call.Result.Text != "SUBOK" || call.Result.IsError {
		t.Errorf("paired result = %+v, want text SUBOK no error", call.Result)
	}
}

// TestReadTranscript_ShellResult: the {output, exitCode} shell shape —
// the output text is the payload; exitCode 0 is not an error. Also pins
// the plugin custom-agent doc meta ("agent", not "agentMode").
func TestReadTranscript_ShellResult(t *testing.T) {
	events := readFixture(t, "exports/idle-mixed-provenance.json")
	if got, want := events[0].Label, "agent"; got != want {
		t.Fatalf("leading meta label = %q, want %q", got, want)
	}
	if events[0].Text != "grok-4-6 (xai/grok-4.6)" {
		t.Errorf("agent meta = %q", events[0].Text)
	}
	var call *source.ToolCall
	for _, e := range events {
		if e.Kind == source.KindToolCall {
			call = e.Tool
		}
	}
	if call == nil || call.ID != "TU-034Fw3wgyvrQKamTNQuywt" || call.Name != "shell_command" {
		t.Fatalf("shell call = %+v", call)
	}
	if call.Result == nil || call.Result.Text != "done\n" || call.Result.IsError {
		t.Errorf("shell result = %+v, want output text, no error", call.Result)
	}
}

// TestReadTranscript_ResultShapes pins every run.result decoding rule
// on synthesized docs: the misread trap (status "done"/"cancelled" is
// never an error signal by itself), the shell exitCode, and honest
// fallbacks.
func TestReadTranscript_ResultShapes(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"assistant","messageId":1,"protocolMessageID":"M-1","content":[
			{"type":"tool_use","id":"TU-1","name":"shell_command","input":{"command":"false"}},
			{"type":"tool_use","id":"TU-2","name":"shell_command","input":{"command":"rm"}},
			{"type":"tool_use","id":"TU-3","name":"Task","input":{}},
			{"type":"tool_use","id":"TU-4","name":"read_web_page","input":{"url":"https://x"}},
			{"type":"tool_use","id":"TU-5","name":"finder","input":{}}]},
		{"role":"user","messageId":2,"protocolMessageID":"M-2","content":[
			{"type":"tool_result","toolUseID":"TU-1","run":{"result":{"output":"boom\n","exitCode":1},"status":"done"}},
			{"type":"tool_result","toolUseID":"TU-2","run":{"result":"Tool rejected by plugin: nope","status":"done"}},
			{"type":"tool_result","toolUseID":"TU-3","run":{"result":"interrupted","status":"cancelled"}},
			{"type":"tool_result","toolUseID":"TU-4","run":{"result":{"title":"a page","body":"words"},"status":"done"}},
			{"type":"tool_result","toolUseID":"TU-5","run":{"status":"done"}}]}]}`

	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	res := map[string]*source.ToolResult{}
	for _, e := range events {
		if e.Kind == source.KindToolCall {
			if e.Tool.Result == nil {
				t.Fatalf("call %s unpaired", e.Tool.ID)
			}
			res[e.Tool.ID] = e.Tool.Result
		}
	}
	if r := res["TU-1"]; !r.IsError || r.Text != "boom\n" {
		t.Errorf("exit-code result = %+v, want output text + IsError", r)
	}
	if r := res["TU-2"]; r.IsError || r.Text != "Tool rejected by plugin: nope" {
		t.Errorf("deny result = %+v, want the text verbatim and NOT error (misread trap)", r)
	}
	if r := res["TU-3"]; r.IsError || r.Text != "interrupted" {
		t.Errorf("cancelled-status result = %+v, want text verbatim, no error sniffing", r)
	}
	if r := res["TU-4"]; r.IsError || r.Text != `{"title":"a page","body":"words"}` {
		t.Errorf("object result = %+v, want compact JSON", r)
	}
	if r := res["TU-5"]; r.IsError || r.Text != "" {
		t.Errorf("result-less run = %+v, want empty", r)
	}
}

// TestReadTranscript_OrphanToolResult: a result whose call never landed
// (a truncated/forked prefix) is its own KindToolResult event.
func TestReadTranscript_OrphanToolResult(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":[
			{"type":"tool_result","toolUseID":"TU-gone","run":{"result":"stray","status":"done"}}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Kind != source.KindToolResult {
		t.Fatalf("events = %+v, want one orphan result", events)
	}
	if o := events[0].Tool; o.ID != "TU-gone" || o.Name != "" || o.Result == nil || o.Result.Text != "stray" {
		t.Errorf("orphan = %+v", o)
	}
}

// TestReadTranscript_UnpairedCall: the mid-turn tool_use snapshot —
// the pending call keeps a nil Result and the trailing lkas meta
// records the doc stopped at tool_use.
func TestReadTranscript_UnpairedCall(t *testing.T) {
	events := readFixture(t, "exports/midturn-tooluse.json")
	var call *source.ToolCall
	for _, e := range events {
		if e.Kind == source.KindToolCall {
			call = e.Tool
		}
	}
	if call == nil || call.ID != "toolu_01Fixture0000000000000001" || call.Name != "Read" || call.Result != nil {
		t.Errorf("unpaired call = %+v, want Read with nil Result", call)
	}
	tail := events[len(events)-1]
	if tail.Kind != source.KindMeta || tail.Label != "lastKnownAgentState" || tail.Text != "tool_use" {
		t.Errorf("tail = %+v, want the lkas marker", tail)
	}
}

// TestReadTranscript_InfoSummary: an info-role message is in-stream
// compaction — one meta event per block, the summary text as payload.
func TestReadTranscript_InfoSummary(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":[{"type":"text","text":"before"}]},
		{"role":"info","messageId":2,"content":[{"type":"summary","summary":"compacted history here"}]},
		{"role":"info","messageId":3,"content":[]},
		{"role":"assistant","messageId":4,"content":[{"type":"text","text":"after"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "prompt meta:info:summary meta:info assistant"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[1].Text != "compacted history here" {
		t.Errorf("summary meta = %q", events[1].Text)
	}
}

// TestReadTranscript_HiddenText: a hidden text block is injected
// context, not the user's prose — a labelled meta in stream order, the
// visible text still a prompt.
func TestReadTranscript_HiddenText(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":[
			{"type":"text","text":"real question"},
			{"type":"text","text":"file contents","hidden":true},
			{"type":"text","text":"and a followup"}]},
		{"role":"assistant","messageId":2,"content":[
			{"type":"text","text":"spoken","hidden":true},
			{"type":"text","text":"shown"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "prompt meta:user:hidden prompt meta:assistant:hidden assistant"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].Text != "real question" || events[2].Text != "and a followup" {
		t.Errorf("prompts = %q / %q", events[0].Text, events[2].Text)
	}
}

// TestReadTranscript_UnknownShapes: unknown block types and unknown
// roles degrade to labelled meta events — visible, never fatal; a
// message whose content is not decodable at all is a bare role meta.
func TestReadTranscript_UnknownShapes(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"assistant","messageId":1,"content":[
			{"type":"canvas","data":"whatever"},
			{"type":"text","text":"still answered"}]},
		{"role":"system","messageId":2,"content":[{"type":"text","text":"a notice"}]},
		{"role":"user","messageId":3,"content":{"not":"an array"}},
		{"role":"user","content":[{"type":"mystery"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "meta:assistant:canvas assistant meta:system meta:user meta:user:mystery"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[2].Text != "a notice" {
		t.Errorf("unknown-role meta text = %q", events[2].Text)
	}
	// No ids at all on the last message → the idx: fallback.
	if events[4].ID != "idx:3" {
		t.Errorf("id-less message event ID = %q, want idx:3", events[4].ID)
	}
}

// TestReadTranscript_Images: image blocks count onto the pending prompt
// (pixels are URL refs, never inline); an assistant-side image is an
// unknown-in-context block — a meta, not an invention.
func TestReadTranscript_Images(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":[
			{"type":"text","text":"look at this"},
			{"type":"image","source":"https://ampcode.com/f/1"},
			{"type":"image","source":"https://ampcode.com/f/2"}]},
		{"role":"assistant","messageId":2,"content":[
			{"type":"image","sourcePath":"/tmp/x.png"},
			{"type":"text","text":"nice"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "prompt meta:assistant:image assistant"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].Images != 2 {
		t.Errorf("prompt images = %d, want 2", events[0].Images)
	}
}

// TestReadTranscript_MixedUserMessage: text and a tool_result interleaved
// in one user message keep stream order — prompt, pair, prompt.
func TestReadTranscript_MixedUserMessage(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"assistant","messageId":1,"content":[
			{"type":"tool_use","id":"TU-1","name":"finder","input":{}}]},
		{"role":"user","messageId":2,"content":[
			{"type":"text","text":"first"},
			{"type":"tool_result","toolUseID":"TU-1","run":{"result":"found","status":"done"}},
			{"type":"text","text":"second"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "tool_call prompt prompt"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].Tool.Result == nil || events[0].Tool.Result.Text != "found" {
		t.Errorf("paired result = %+v", events[0].Tool.Result)
	}
	if events[1].Text != "first" || events[2].Text != "second" {
		t.Errorf("prompt texts = %q / %q", events[1].Text, events[2].Text)
	}
}

// TestReadTranscript_StringContent: a bare-string content field (seen
// in neither epoch — tolerance, not evidence) still surfaces as prose.
func TestReadTranscript_StringContent(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":"plain prompt"},
		{"role":"assistant","messageId":2,"content":"plain reply"}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want := "prompt assistant"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if events[0].Text != "plain prompt" || events[1].Text != "plain reply" {
		t.Errorf("texts = %q / %q", events[0].Text, events[1].Text)
	}
}

// TestReadTranscript_EmptyDoc: messages:[] still reports the doc-level
// mode and nothing else — no error, no invented events.
func TestReadTranscript_EmptyDoc(t *testing.T) {
	events := readFixture(t, "exports/edge-empty.json")
	if got := joinKinds(events); got != "meta:agentMode" {
		t.Fatalf("kinds = %q, want the lone agentMode meta", got)
	}
	if events[0].Text != "medium" {
		t.Errorf("agentMode = %q", events[0].Text)
	}
}

// TestReadTranscript_MidturnStreaming: the clean-prefix doc reads
// exactly its messages; the lkas marker says where the doc stopped.
func TestReadTranscript_MidturnStreaming(t *testing.T) {
	events := readFixture(t, "exports/midturn-streaming.json")
	want := "meta:agentMode prompt meta:model assistant prompt meta:lastKnownAgentState"
	if got := joinKinds(events); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if tail := events[len(events)-1]; tail.Text != "streaming" {
		t.Errorf("lkas tail = %q, want streaming", tail.Text)
	}
}

// TestReadTranscript_MalformedDoc: bytes that are not one JSON object
// fail the read — the events-so-far contract is vacuous for a
// single-document format (nothing was gathered).
func TestReadTranscript_MalformedDoc(t *testing.T) {
	for _, raw := range []string{"not json", `{"messages":[`, `[1,2]`} {
		if _, err := New().ReadTranscript(strings.NewReader(raw)); err == nil {
			t.Errorf("ReadTranscript(%q) returned nil error", raw)
		}
	}
	if _, err := New().ReadTranscript(iotest.ErrReader(errors.New("boom"))); err == nil {
		t.Error("reader error must surface")
	}
	// Valid JSON that is not an export shape is a doc with no messages —
	// tolerant, not an error.
	events, err := New().ReadTranscript(strings.NewReader(`{"unrelated":true}`))
	if err != nil || len(events) != 0 {
		t.Errorf("non-export doc: events=%v err=%v", events, err)
	}
}

// TestReadTranscript_CreatedAtPreferred: the new epoch's createdAt wins
// over meta.sentAt when both stamp a message.
func TestReadTranscript_CreatedAtPreferred(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"createdAt":"2026-10-02T10:13:40.123Z","meta":{"sentAt":1780668820123},"content":[{"type":"text","text":"hi"}]}]}`
	events, err := New().ReadTranscript(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-10-02T10:13:40.123Z")
	if !events[0].Time.Equal(want) {
		t.Errorf("time = %v, want createdAt %v (not sentAt)", events[0].Time, want)
	}
}

// TestListSubagents_AlwaysNil: amp exports carry no subagent
// transcripts — the answer is nil even if a stray subagents/ directory
// exists beside the artifact (capture never writes one, and none is
// invented).
func TestListSubagents_AlwaysNil(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	subs, err := New().ListSubagents(dir)
	if err != nil || subs != nil {
		t.Errorf("subs = %+v err = %v, want nil,nil", subs, err)
	}
}
