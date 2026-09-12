package claude

import (
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

func TestTranscriptFormats_DeclaresClaudeJSONL(t *testing.T) {
	s := New()
	formats := s.TranscriptFormats()
	if len(formats) != 1 || formats[0] != TranscriptFormat {
		t.Fatalf("TranscriptFormats() = %v, want [%s]", formats, TranscriptFormat)
	}
}

func TestRenderTranscript_UserAndAssistantStringContent(t *testing.T) {
	s := New()
	data := strings.NewReader(
		`{"type":"user","uuid":"u1","timestamp":"2026-09-11T09:00:00Z","message":{"content":"hello there"}}` + "\n" +
			`{"type":"assistant","uuid":"a1","timestamp":"2026-09-11T09:00:01Z","message":{"content":"hi yourself"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	want := []source.Turn{
		{Role: "user", Text: "hello there"},
		{Role: "assistant", Text: "hi yourself"},
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

func TestRenderTranscript_AssistantBlockArrayJoinsTextBlocks(t *testing.T) {
	s := New()
	data := strings.NewReader(
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-11T09:00:00Z","message":{"content":[` +
			`{"type":"text","text":"first part"},` +
			`{"type":"tool_use","id":"t1","name":"bash"},` +
			`{"type":"text","text":"second part"}` +
			`]}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %+v, want 1", turns)
	}
	if turns[0].Text != "first part\nsecond part" {
		t.Errorf("turns[0].Text = %q, want the two text blocks joined, tool_use skipped", turns[0].Text)
	}
}

func TestRenderTranscript_ToolResultOnlyBlockArrayRendersNoTurn(t *testing.T) {
	s := New()
	data := strings.NewReader(
		`{"type":"user","uuid":"u1","timestamp":"2026-09-11T09:00:00Z","message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"t1","content":"ok"}` +
			`]}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("turns = %+v, want none (no text block to render)", turns)
	}
}

func TestRenderTranscript_SidechainAndMetaSkipped(t *testing.T) {
	s := New()
	data := strings.NewReader(
		`{"type":"user","uuid":"u1","isSidechain":true,"message":{"content":"subagent chatter"}}` + "\n" +
			`{"type":"user","uuid":"u2","isMeta":true,"message":{"content":"bookkeeping"}}` + "\n" +
			`{"type":"user","uuid":"u3","message":{"content":"the real prompt"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "the real prompt" {
		t.Errorf("turns = %+v, want exactly the one non-sidechain, non-meta turn", turns)
	}
}

func TestRenderTranscript_OtherEntryTypesSkipped(t *testing.T) {
	s := New()
	data := strings.NewReader(
		`{"type":"summary","message":{"content":"a summary line"}}` + "\n" +
			`{"type":"user","uuid":"u1","message":{"content":"hello"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || turns[0].Role != "user" {
		t.Errorf("turns = %+v, want only the user turn", turns)
	}
}

func TestRenderTranscript_MalformedLineSkippedNotFatal(t *testing.T) {
	s := New()
	data := strings.NewReader(
		"not even json\n" +
			`{"type":"user","uuid":"u1","message":{"content":"hello"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 {
		t.Errorf("turns = %+v, want 1 (the garbage line skipped, not fatal)", turns)
	}
}

func TestRenderTranscript_MaxCharsCapsAtRunesNotBytes(t *testing.T) {
	s := New()
	// "café" is 4 runes, 5 bytes (é is 2 bytes in UTF-8) — a byte-based cap
	// of 4 would split the é mid-codepoint.
	data := strings.NewReader(
		`{"type":"user","uuid":"u1","message":{"content":"café society"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 4)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "café" {
		t.Errorf("turns[0].Text = %q, want %q (capped at 4 runes)", turns[0].Text, "café")
	}
}

func TestRenderTranscript_MaxCharsZeroMeansNoCap(t *testing.T) {
	s := New()
	long := strings.Repeat("x", 1000)
	data := strings.NewReader(
		`{"type":"user","uuid":"u1","message":{"content":"` + long + `"}}` + "\n",
	)
	turns, err := s.RenderTranscript(data, 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 1 || len(turns[0].Text) != 1000 {
		t.Errorf("turns[0].Text length = %d, want 1000 (uncapped)", len(turns[0].Text))
	}
}

func TestRenderTranscript_EmptyTranscriptRendersNoTurns(t *testing.T) {
	s := New()
	turns, err := s.RenderTranscript(strings.NewReader(""), 0)
	if err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("turns = %+v, want none", turns)
	}
}
