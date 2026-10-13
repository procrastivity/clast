package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// The offline readers (SURFACE V18, M9): TranscriptRenderer and
// TranscriptReader parse the captured pi-jsonl-v3 artifact only — never
// the live Pi store, never a Pi binary. Like capture's counts they read
// the LEAF PATH (the active branch, walked by the same walkLeaf) and fall
// back to whole-file order when the path is broken. Message text is
// materialized here and nowhere else (M10).

// TranscriptFormats implements both transcript seams: this source reads
// and renders exactly the one format its own Capture stamps.
func (s *Source) TranscriptFormats() []string { return []string{Format} }

// entryRec is one parsed entry line with the fields the readers need.
type entryRec struct {
	Type      string          `json:"type"`
	ID        *string         `json:"id"`
	ParentID  json.RawMessage `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`

	// Non-message entry payloads (compact meta text only).
	Provider      string          `json:"provider"`
	ModelID       string          `json:"modelId"`
	ThinkingLevel string          `json:"thinkingLevel"`
	Summary       string          `json:"summary"`
	Name          string          `json:"name"`
	Label         string          `json:"label"`
	CustomType    string          `json:"customType"`
	Content       json.RawMessage `json:"content"`
}

// block is one content block, wide enough for every kind read.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// msgBody is a message entry's payload.
type msgBody struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`
	Command      string          `json:"command"`
	CustomType   string          `json:"customType"`
}

func (e entryRec) id() string { return *e.ID }

// forEachLine calls fn for every line of r with its 1-based number. A
// line over maxLineSize is delivered as nil (the capture rule: bad, not
// fatal). Blank-line and torn-tail tolerance is the caller's: a torn
// tail simply fails to parse.
func forEachLine(r io.Reader, fn func(n int, line []byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	over := false
	n := 1
	flush := func() {
		if over {
			fn(n, nil)
		} else {
			fn(n, buf)
		}
		buf, over = buf[:0], false
		n++
	}
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 && !over {
			if len(buf)+len(chunk) > maxLineSize {
				over, buf = true, buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err != nil {
			if err == io.EOF {
				if len(buf) > 0 || over {
					flush()
				}
				return nil
			}
			return err
		}
		if !isPrefix {
			flush()
		}
	}
}

// activeEntries parses r and returns the conversation entries in
// conversation order: the leaf path root-first, or every entry in file
// order when the path is broken. Line 1 (the header), later session
// lines, blank, unparseable and id-less lines are not entries.
func activeEntries(r io.Reader) ([]entryRec, error) {
	var all []entryRec
	byID := map[string]int{}
	err := forEachLine(r, func(n int, line []byte) {
		if n == 1 || len(bytes.TrimSpace(line)) == 0 {
			return
		}
		var e entryRec
		if json.Unmarshal(line, &e) != nil || e.Type == "session" || e.ID == nil {
			return
		}
		byID[e.id()] = len(all)
		all = append(all, e)
	})
	if err != nil {
		return nil, fmt.Errorf("pi: reading transcript: %w", err)
	}
	if len(all) == 0 {
		return nil, nil
	}
	path, broken := walkLeaf(all[len(all)-1].id(), func(id string) (string, bool, bool) {
		i, ok := byID[id]
		if !ok {
			return "", false, false
		}
		p, has := parentOf(all[i].ParentID)
		return p, has, true
	})
	if broken != "" {
		return all, nil
	}
	out := make([]entryRec, len(path))
	for i, id := range path {
		out[len(path)-1-i] = all[byID[id]]
	}
	return out, nil
}

// contentBlocks decodes message content: a plain string is one text
// block; an array is its blocks; anything else is none.
func contentBlocks(raw json.RawMessage) []block {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return nil
		}
		return []block{{Type: "text", Text: text}}
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// textOf joins the text blocks of a message, newline-separated.
func textOf(blocks []block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// RenderTranscript implements source.TranscriptRenderer: capped prose
// turns of the active branch. Following the claude and devin renderers,
// only user and assistant text renders; tool calls, tool results,
// thinking blocks and bookkeeping entries contribute no turn, and a turn
// with no text is skipped rather than rendered empty. maxChars, when >
// 0, caps each turn's text at that many runes.
func (s *Source) RenderTranscript(r io.Reader, maxChars int) ([]source.Turn, error) {
	entries, err := activeEntries(r)
	if err != nil {
		return nil, err
	}
	var turns []source.Turn
	for _, e := range entries {
		if e.Type != "message" {
			continue
		}
		var m msgBody
		if json.Unmarshal(e.Message, &m) != nil || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		text := textOf(contentBlocks(m.Content))
		if text == "" {
			continue
		}
		if maxChars > 0 {
			text = truncateRunes(text, maxChars)
		}
		turns = append(turns, source.Turn{Role: m.Role, Text: text})
	}
	return turns, nil
}

// truncateRunes caps s at n runes, never bytes (V7).
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ReadTranscript implements source.TranscriptReader over the active
// branch:
//
//   - user message: text (and image-block count) → KindPrompt.
//   - assistant message: text → KindAssistant, thinking → KindThinking
//     (empty text is Redacted), toolCall → KindToolCall (id, name,
//     arguments verbatim as Input). An error or aborted reply that
//     carries errorMessage also yields a KindMeta "assistant:<stopReason>"
//     whose Text is that message, so a failed reply is not invisible.
//   - toolResult message pairs onto its call by toolCallId == toolCall.id
//     (Result.Text from text blocks, Images from image blocks, IsError);
//     a result with no earlier unanswered call becomes its own
//     KindToolResult event carrying toolName. An unanswered call keeps a
//     nil Result.
//   - every other record is KindMeta, label = its type: model_change
//     ("provider/model"), thinking_level_change, compaction,
//     branch_summary (summary), session_info (name), label, custom and
//     custom_message ("<type>:<customType>"); message roles bashExecution
//     ("bashExecution", Text = command) and any other role
//     ("message:<role>"). Unknown types fall through the same way.
//   - malformed lines and a torn tail contribute nothing.
func (s *Source) ReadTranscript(r io.Reader) ([]source.Event, error) {
	entries, err := activeEntries(r)
	if err != nil {
		return nil, err
	}
	var events []source.Event
	calls := map[string]*source.ToolCall{}
	for _, e := range entries {
		at, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
		if e.Type != "message" {
			events = append(events, metaEvent(e, at))
			continue
		}
		var m msgBody
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		blocks := contentBlocks(m.Content)
		switch m.Role {
		case "user":
			images := 0
			for _, b := range blocks {
				if b.Type == "image" {
					images++
				}
			}
			if text := textOf(blocks); text != "" || images > 0 {
				events = append(events, source.Event{Kind: source.KindPrompt, Time: at, ID: e.id(), Text: text, Images: images})
			}
		case "assistant":
			events = readAssistant(events, calls, e, m, blocks, at)
		case "toolResult":
			res := &source.ToolResult{Time: at, Text: textOf(blocks), IsError: m.IsError}
			for _, b := range blocks {
				if b.Type == "image" {
					res.Images++
				}
			}
			if call, ok := calls[m.ToolCallID]; ok && call.Result == nil {
				call.Result = res
				continue
			}
			events = append(events, source.Event{
				Kind: source.KindToolResult, Time: at, ID: e.id(),
				Tool: &source.ToolCall{ID: m.ToolCallID, Name: m.ToolName, Result: res},
			})
		case "bashExecution":
			events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: e.id(), Label: "bashExecution", Text: m.Command})
		default:
			events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: e.id(), Label: "message:" + m.Role, Text: textOf(blocks)})
		}
	}
	return events, nil
}

func readAssistant(events []source.Event, calls map[string]*source.ToolCall, e entryRec, m msgBody, blocks []block, at time.Time) []source.Event {
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				events = append(events, source.Event{Kind: source.KindAssistant, Time: at, ID: e.id(), Text: b.Text})
			}
		case "thinking":
			events = append(events, source.Event{Kind: source.KindThinking, Time: at, ID: e.id(), Text: b.Thinking, Redacted: b.Thinking == ""})
		case "toolCall":
			call := &source.ToolCall{ID: b.ID, Name: b.Name, Input: b.Arguments}
			if b.ID != "" {
				calls[b.ID] = call
			}
			events = append(events, source.Event{Kind: source.KindToolCall, Time: at, ID: e.id(), Tool: call})
		}
	}
	if m.ErrorMessage != "" && (m.StopReason == "error" || m.StopReason == "aborted") {
		events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: e.id(), Label: "assistant:" + m.StopReason, Text: m.ErrorMessage})
	}
	return events
}

// metaEvent classifies a non-message entry as KindMeta, label = type,
// with a short readable payload where the entry carries one.
func metaEvent(e entryRec, at time.Time) source.Event {
	ev := source.Event{Kind: source.KindMeta, Time: at, ID: e.id(), Label: e.Type}
	switch e.Type {
	case "model_change":
		ev.Text = e.Provider + "/" + e.ModelID
	case "thinking_level_change":
		ev.Text = e.ThinkingLevel
	case "compaction", "branch_summary":
		ev.Text = e.Summary
	case "session_info":
		ev.Text = e.Name
	case "label":
		ev.Text = e.Label
	case "custom":
		ev.Label = "custom:" + e.CustomType
	case "custom_message":
		ev.Label = "custom_message:" + e.CustomType
		ev.Text = textOf(contentBlocks(e.Content))
	}
	return ev
}

// ListSubagents implements source.TranscriptReader: Pi writes no
// subagent sidecars (one file per session), so there is nothing to list.
func (s *Source) ListSubagents(_ string) ([]source.Subagent, error) { return nil, nil }

var (
	_ source.TranscriptRenderer = (*Source)(nil)
	_ source.TranscriptReader   = (*Source)(nil)
)
