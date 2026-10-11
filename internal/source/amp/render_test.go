package amp

import (
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/procrastivity/clast/internal/source"
)

func TestTranscriptFormats_DeclaresAmpExport(t *testing.T) {
	s := New()
	formats := s.TranscriptFormats()
	if len(formats) != 1 || formats[0] != TranscriptFormat {
		t.Fatalf("TranscriptFormats() = %v, want [%s]", formats, TranscriptFormat)
	}
}

// renderFixture runs RenderTranscript over a pinned export doc.
func renderFixture(t *testing.T, rel string, maxChars int) []source.Turn {
	t.Helper()
	turns, err := New().RenderTranscript(strings.NewReader(string(mustFixture(t, rel))), maxChars)
	if err != nil {
		t.Fatalf("RenderTranscript(%s): %v", rel, err)
	}
	return turns
}

// TestRenderTranscript_NewEpoch: the new-epoch fixture renders one turn
// per message with visible text — prompt and reply alternating.
func TestRenderTranscript_NewEpoch(t *testing.T) {
	turns := renderFixture(t, "exports/idle-local-client.json", 0)
	want := []source.Turn{
		{Role: "user", Text: "Reply with exactly one word: OK"},
		{Role: "assistant", Text: "OK"},
		{Role: "user", Text: "Reply with exactly one word: YES"},
		{Role: "assistant", Text: "YES"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}
}

// TestRenderTranscript_OldEpoch: the pmid-less old serialization renders
// the same turn shape — epoch differences never reach the render.
func TestRenderTranscript_OldEpoch(t *testing.T) {
	turns := renderFixture(t, "exports/old-epoch.json", 0)
	want := []source.Turn{
		{Role: "user", Text: "fixture: what does this project do?"},
		{Role: "assistant", Text: "fixture: it indexes old-epoch exports."},
		{Role: "user", Text: "fixture: thanks"},
		{Role: "assistant", Text: "fixture: welcome"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}
}

// TestRenderTranscript_ToolMessagesRenderNoTurn: a tool_result carrier
// (a user message with no text) and an assistant message of pure
// thinking+tool_use contribute no turns — prose only, claude parity.
func TestRenderTranscript_ToolMessagesRenderNoTurn(t *testing.T) {
	turns := renderFixture(t, "exports/idle-task-toolresult.json", 0)
	want := []source.Turn{
		{Role: "user", Text: "Use the Task tool to spawn one subagent that replies with exactly SUBOK, then report its reply."},
		{Role: "assistant", Text: "SUBOK"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}
}

// TestRenderTranscript_MidTurnPrefix: a mid-turn doc renders the
// complete prefix it carries — no error, no partial-message invention.
func TestRenderTranscript_MidTurnPrefix(t *testing.T) {
	turns := renderFixture(t, "exports/midturn-streaming.json", 0)
	if len(turns) != 3 {
		t.Fatalf("turns = %+v, want the 3-message clean prefix", turns)
	}
}

// TestRenderTranscript_EmptyDoc: messages:[] renders nothing and is not
// an error.
func TestRenderTranscript_EmptyDoc(t *testing.T) {
	turns := renderFixture(t, "exports/edge-empty.json", 0)
	if len(turns) != 0 {
		t.Errorf("turns = %+v, want none", turns)
	}
}

// TestRenderTranscript_HiddenAndUnknownShapes: hidden text (injected
// context, not prose), info/compaction messages, and unknown roles
// produce no turns.
func TestRenderTranscript_HiddenAndUnknownShapes(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"user","messageId":1,"content":[
			{"type":"text","text":"attached file contents","hidden":true},
			{"type":"text","text":"the actual prompt"}]},
		{"role":"info","messageId":2,"content":[{"type":"summary","summary":"compacted so far"}]},
		{"role":"system","messageId":3,"content":[{"type":"text","text":"bookkeeping"}]},
		{"role":"assistant","messageId":4,"content":[{"type":"text","text":"an answer"}]}]}`
	turns, err := New().RenderTranscript(strings.NewReader(doc), 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	want := []source.Turn{
		{Role: "user", Text: "the actual prompt"},
		{Role: "assistant", Text: "an answer"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}
}

// TestRenderTranscript_JoinsTextBlocks: adjacent visible text blocks in
// one message join into a single turn, newline-separated.
func TestRenderTranscript_JoinsTextBlocks(t *testing.T) {
	doc := `{"id":"T-x","messages":[
		{"role":"assistant","messageId":1,"content":[
			{"type":"text","text":"part one"},
			{"type":"tool_use","id":"TU-1","name":"finder","input":{}},
			{"type":"text","text":"part two"}]}]}`
	turns, err := New().RenderTranscript(strings.NewReader(doc), 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "part one\npart two" {
		t.Errorf("turns = %+v, want the joined text blocks", turns)
	}
}

// TestRenderTranscript_MaxCharsCapsAtRunesNotBytes: the cap is runes —
// a 4-rune cap on "café" must not split the é's two bytes.
func TestRenderTranscript_MaxCharsCapsAtRunesNotBytes(t *testing.T) {
	doc := `{"id":"T-x","messages":[{"role":"user","messageId":1,"content":[{"type":"text","text":"café society"}]}]}`
	turns, err := New().RenderTranscript(strings.NewReader(doc), 4)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "café" {
		t.Errorf("turns = %+v, want capped at 4 runes", turns)
	}
}

// TestRenderTranscript_MalformedDocIsAnError: the transcript is one
// JSON document — bytes that are not one fail the read outright (there
// is no per-line skip posture to fall back on).
func TestRenderTranscript_MalformedDocIsAnError(t *testing.T) {
	for _, raw := range []string{"not json", `{"messages":[`, "g"} {
		if _, err := New().RenderTranscript(strings.NewReader(raw), 0); err == nil {
			t.Errorf("RenderTranscript(%q) returned nil error", raw)
		}
	}
}

// TestRenderTranscript_ReaderError: an unreadable stream surfaces the
// read failure.
func TestRenderTranscript_ReaderError(t *testing.T) {
	if _, err := New().RenderTranscript(iotest.ErrReader(errors.New("boom")), 0); err == nil {
		t.Fatal("want the read error surfaced")
	}
}
