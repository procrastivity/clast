package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// readRecord is the envelope ReadTranscript decodes: entry's fields
// (scan.go) plus the ones only a structured read needs. It is separate
// from entry so fact extraction keeps decoding as little as it did.
type readRecord struct {
	entry
	Subtype    string          `json:"subtype"`
	Content    json.RawMessage `json:"content"`
	DurationMs int64           `json:"durationMs"`
	Attachment struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
	Mode       string `json:"mode"`
	AITitle    string `json:"aiTitle"`
	PRURL      string `json:"prUrl"`
	Operation  string `json:"operation"`
	AgentName  string `json:"agentName"`
	Permission string `json:"permissionMode"`
}

// readBlock is one element of a block-array message content, wide enough
// for every block kind ReadTranscript understands: text, thinking,
// tool_use, tool_result, image, and tool_reference.
type readBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolName  string          `json:"tool_name"`
}

// ReadTranscript implements source.TranscriptReader over the claude
// envelope. Rules:
//
//   - user + string content, not isMeta, origin absent or human: a
//     prompt. Block-array content is walked block by block: tool_result
//     blocks pair with their call, text blocks are prompts (meta when
//     the entry is isMeta or non-human), images are counted.
//   - user entries that are isMeta or carry another origin (task
//     notifications, peer messages) are KindMeta labelled "user:<origin>".
//   - assistant blocks map one to one: text, thinking (empty text or a
//     redacted_thinking block is Redacted, not dropped), tool_use.
//   - every other record type is KindMeta, label = type (system records:
//     "system:<subtype>", attachments: "attachment:<type>"). Unknown
//     types fall through the same way, so a new record type is safe by
//     construction.
//   - sidechain entries are kept: main transcripts hold none, but a
//     subagent transcript is all sidechain and is read by this same call.
//   - entries repeating an already-seen uuid (a fork's copied history)
//     are dropped, the same identity rule as scan.go's counting.
//
// A tool_use with no later result keeps a nil Result; a result whose call
// never appeared becomes its own KindToolResult event.
func (s *Source) ReadTranscript(r io.Reader) ([]source.Event, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var events []source.Event
	calls := map[string]*source.ToolCall{}
	seen := map[string]struct{}{}
	for scanner.Scan() {
		var rec readRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		if rec.UUID != "" && (rec.Type == "user" || rec.Type == "assistant") {
			if _, dup := seen[rec.UUID]; dup {
				continue
			}
			seen[rec.UUID] = struct{}{}
		}
		at, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
		switch rec.Type {
		case "user":
			events = readUser(events, calls, rec, at)
		case "assistant":
			events = readAssistant(events, calls, rec, at)
		default:
			events = append(events, metaEvent(rec, at))
		}
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("claude: reading transcript: %w", err)
	}
	return events, nil
}

// readUser appends the events of one user entry.
func readUser(events []source.Event, calls map[string]*source.ToolCall, rec readRecord, at time.Time) []source.Event {
	var m message
	if json.Unmarshal(rec.Message, &m) != nil {
		return events
	}
	// Injected turns are meta whatever their content shape.
	label := ""
	switch {
	case rec.Origin != nil && rec.Origin.Kind != "human":
		label = "user:" + rec.Origin.Kind
	case rec.IsMeta:
		label = "user:meta"
	}
	add := func(text string, images int) {
		ev := source.Event{Kind: source.KindPrompt, Time: at, ID: rec.UUID, Text: text, Images: images}
		if label != "" {
			ev.Kind, ev.Label = source.KindMeta, label
		}
		events = append(events, ev)
	}

	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		add(text, 0)
		return events
	}
	var blocks []readBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return events
	}
	// Adjacent text and image blocks form one prompt.
	var texts []string
	images := 0
	flush := func() {
		if len(texts) > 0 || images > 0 {
			add(strings.Join(texts, "\n"), images)
		}
		texts, images = nil, 0
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			images++
		case "tool_result":
			flush()
			res := &source.ToolResult{Time: at, IsError: b.IsError}
			res.Text, res.Images = resultContent(b.Content)
			if call, ok := calls[b.ToolUseID]; ok && call.Result == nil {
				call.Result = res
				continue
			}
			events = append(events, source.Event{
				Kind: source.KindToolResult, Time: at, ID: rec.UUID,
				Tool: &source.ToolCall{ID: b.ToolUseID, Result: res},
			})
		}
	}
	flush()
	return events
}

// readAssistant appends the events of one assistant entry.
func readAssistant(events []source.Event, calls map[string]*source.ToolCall, rec readRecord, at time.Time) []source.Event {
	var m message
	if json.Unmarshal(rec.Message, &m) != nil {
		return events
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		if text == "" {
			return events
		}
		return append(events, source.Event{Kind: source.KindAssistant, Time: at, ID: rec.UUID, Text: text})
	}
	var blocks []readBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return events
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				events = append(events, source.Event{Kind: source.KindAssistant, Time: at, ID: rec.UUID, Text: b.Text})
			}
		case "thinking", "redacted_thinking":
			events = append(events, source.Event{
				Kind: source.KindThinking, Time: at, ID: rec.UUID, Text: b.Thinking,
				Redacted: b.Thinking == "",
			})
		case "tool_use":
			call := &source.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input}
			if b.ID != "" {
				calls[b.ID] = call
			}
			events = append(events, source.Event{Kind: source.KindToolCall, Time: at, ID: rec.UUID, Tool: call})
		}
	}
	return events
}

// resultContent flattens a tool_result's content — a plain string or a
// block array — into text plus an image count. Blocks other than text and
// image (tool_reference) render as a short bracketed note so a result is
// never silently empty.
func resultContent(raw json.RawMessage) (string, int) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, 0
	}
	var blocks []readBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return "", 0
	}
	var parts []string
	images := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			images++
		case "tool_reference":
			parts = append(parts, "[tool reference: "+b.ToolName+"]")
		}
	}
	return strings.Join(parts, "\n"), images
}

// metaEvent classifies a non-conversation record as KindMeta with a label
// and, where the record carries one, a short readable payload.
func metaEvent(rec readRecord, at time.Time) source.Event {
	ev := source.Event{Kind: source.KindMeta, Time: at, ID: rec.UUID, Label: rec.Type}
	switch rec.Type {
	case "system":
		ev.Label = "system:" + rec.Subtype
		var text string
		if json.Unmarshal(rec.Content, &text) == nil {
			ev.Text = text
		}
		if rec.Subtype == "turn_duration" {
			ev.Text = (time.Duration(rec.DurationMs) * time.Millisecond).String()
		}
	case "attachment":
		ev.Label = "attachment:" + rec.Attachment.Type
		ev.Text = rec.Attachment.Prompt // queued_command: what was typed while busy
	case "mode":
		ev.Text = rec.Mode
	case "permission-mode":
		ev.Text = rec.Permission
	case "ai-title":
		ev.Text = rec.AITitle
	case "agent-name":
		ev.Text = rec.AgentName
	case "pr-link":
		ev.Text = rec.PRURL
	case "queue-operation":
		ev.Text = rec.Operation
	}
	return ev
}

// ListSubagents implements source.TranscriptReader: the
// subagents/agent-<id>.jsonl files capture copied into sessionDir, each
// linked through its agent-<id>.meta.json sibling to the parent's
// spawning tool call. A missing or unreadable meta file leaves the
// descriptive fields empty rather than hiding the transcript.
func (s *Source) ListSubagents(sessionDir string) ([]source.Subagent, error) {
	dir := filepath.Join(sessionDir, "subagents")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claude: listing %s: %w", dir, err)
	}
	var out []source.Subagent
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		sa := source.Subagent{ID: id, Path: "subagents/" + name}
		var meta struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
			ToolUseID   string `json:"toolUseId"`
		}
		if b, err := os.ReadFile(filepath.Join(dir, "agent-"+id+".meta.json")); err == nil && json.Unmarshal(b, &meta) == nil {
			sa.AgentType, sa.Description, sa.ToolUseID = meta.AgentType, meta.Description, meta.ToolUseID
		}
		out = append(out, sa)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
