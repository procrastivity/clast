package amp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// readDoc is the ReadTranscript-level decode of one export document:
// the message list (ExportMessage — capture's own shape) plus the
// document-level meta the structured read surfaces as leading/trailing
// meta events. Nothing here is required to be present — both
// serialization epochs parse through the same tolerant decode.
type readDoc struct {
	// AgentMode is the creation-pinned model+prompt+tools tier: the
	// built-in modes (low|medium|high|ultra|free) sit at meta.agentMode
	// (top-level agentMode mirrors it); a plugin custom-agent thread
	// carries meta.agent{model,name}/meta.agentSelection instead.
	AgentMode string `json:"agentMode"`
	Meta      struct {
		AgentMode string `json:"agentMode"`
		Agent     *struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"agent"`
		AgentSelection *struct {
			PluginAgentModeKey string `json:"pluginAgentModeKey"`
		} `json:"agentSelection"`
		LastKnownAgentState *struct {
			State string `json:"state"`
		} `json:"lastKnownAgentState"`
	} `json:"meta"`
	Messages []ExportMessage `json:"messages"`
}

// readBlock is one element of a message's content array — every block
// kind both epochs carry (step-02 §2): text (±hidden), thinking
// (±signature/openAIReasoning — encrypted reasoning leaves the text
// empty), tool_use, tool_result (run{result,status}), summary
// (compaction, on info-role messages), image.
type readBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Hidden   bool   `json:"hidden"`
	Thinking string `json:"thinking"`
	Summary  string `json:"summary"`

	ID    string          `json:"id"`    // tool_use
	Name  string          `json:"name"`  // tool_use
	Input json.RawMessage `json:"input"` // tool_use

	ToolUseID string   `json:"toolUseID"` // tool_result ↔ tool_use.id
	Run       *toolRun `json:"run"`       // tool_result
}

// toolRun is a tool_result block's payload: a status word plus the
// tool-defined result value.
type toolRun struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

// contentBlocks decodes a message's content field: the documented block
// array, or — tolerated, seen in neither epoch — a bare string lifted
// into one text block. Absent or null content decodes to no blocks.
// Anything else reports ok=false and the caller drops the message.
func contentBlocks(raw json.RawMessage) ([]readBlock, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []readBlock{{Type: "text", Text: text}}, true
	}
	var blocks []readBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil, false
	}
	return blocks, true
}

// readDocFrom reads the whole export document — one JSON value, so a
// read/parse failure is a whole-read failure; there are no lines to
// skip (claude's per-line tolerance has no analog here).
func readDocFrom(r io.Reader) (readDoc, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return readDoc{}, fmt.Errorf("amp: reading transcript: %w", err)
	}
	var doc readDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return readDoc{}, fmt.Errorf("amp: parsing transcript: %w", err)
	}
	return doc, nil
}

// ReadTranscript implements source.TranscriptReader over the
// amp-export document. Rules:
//
//   - Document level: meta.agentMode (else top-level agentMode, else
//     the plugin agent's name/mode key) lands as a leading meta event;
//     meta.lastKnownAgentState.state lands as a trailing one — on a
//     mid-turn capture it is the visible "the doc stopped here" marker
//     ("streaming"/"tool_use"), on a converged one "idle".
//   - user messages carry both human prompts (text blocks) and tool
//     results (tool_result blocks — amp's user-role double duty,
//     step-02 §2). Adjacent text+image blocks fold into one prompt,
//     flushed around non-text blocks. Hidden text is injected context,
//     not the user's prose: it keeps stream order as a "user:hidden"
//     meta event rather than posing as a prompt.
//   - tool_result pairs with its call by toolUseID = the call's id,
//     exactly as the doc pairs them; a result whose call is absent (a
//     truncated or forked prefix) surfaces as an orphan KindToolResult.
//   - assistant blocks map one to one: text, thinking (an empty
//     thinking field — signature-only or encrypted reasoning — is
//     Redacted, not dropped), tool_use. An assistant usage.model that
//     changes between messages emits a "model" meta event — the only
//     model-transition signal the format carries.
//   - info messages are in-stream compaction records: each block lands
//     as a meta event labelled "info:<type>" ("info:summary" carrying
//     the summary text).
//   - Unknown roles and unknown block types degrade to labelled meta
//     events — visible, never fatal, never invented content. Messages
//     are events in array order; ids are entryID's (protocolMessageID,
//     else "mid:<messageId>" in the pmid-less old epoch).
//   - Timestamps normalize per field: createdAt (RFC3339, new epoch)
//     else meta.sentAt (epoch ms, user messages) else zero.
func (s *Source) ReadTranscript(r io.Reader) ([]source.Event, error) {
	doc, err := readDocFrom(r)
	if err != nil {
		return nil, err
	}
	var events []source.Event
	events = append(events, docMetaEvents(doc)...)

	calls := map[string]*source.ToolCall{}
	lastModel := ""
	for i, m := range doc.Messages {
		id := entryID(m, i)
		if m.Role == "assistant" && m.Usage != nil && m.Usage.Model != "" && m.Usage.Model != lastModel {
			events = append(events, source.Event{
				Kind: source.KindMeta, Time: messageTime(m), ID: id, Label: "model", Text: m.Usage.Model,
			})
		}
		if m.Usage != nil && m.Usage.Model != "" {
			lastModel = m.Usage.Model
		}
		events = append(events, messageEvents(m, id, calls)...)
	}
	if st := doc.Meta.LastKnownAgentState; st != nil && st.State != "" {
		events = append(events, source.Event{Kind: source.KindMeta, Label: "lastKnownAgentState", Text: st.State})
	}
	return events, nil
}

// docMetaEvents renders the document-level records as leading meta
// events: the creation-pinned agent mode ("agentMode:medium"), or the
// plugin custom agent's name/model on threads that carry one.
func docMetaEvents(doc readDoc) []source.Event {
	var out []source.Event
	mode := doc.Meta.AgentMode
	if mode == "" {
		mode = doc.AgentMode
	}
	if mode != "" {
		out = append(out, source.Event{Kind: source.KindMeta, Label: "agentMode", Text: mode})
	}
	var name, model string
	if a := doc.Meta.Agent; a != nil {
		name, model = a.Name, a.Model
	}
	if name == "" && doc.Meta.AgentSelection != nil {
		name = doc.Meta.AgentSelection.PluginAgentModeKey
	}
	if name != "" {
		text := name
		if model != "" && model != name {
			text += " (" + model + ")"
		}
		out = append(out, source.Event{Kind: source.KindMeta, Label: "agent", Text: text})
	}
	return out
}

// messageEvents maps one export message onto its events, dispatching on
// role. A message whose content is neither a block array nor a bare
// string degrades to a labelled meta event — the message existed, its
// payload was unreadable; that is the honest report.
func messageEvents(m ExportMessage, id string, calls map[string]*source.ToolCall) []source.Event {
	at := messageTime(m)
	blocks, ok := contentBlocks(m.Content)
	if !ok {
		return []source.Event{{Kind: source.KindMeta, Time: at, ID: id, Label: m.Role}}
	}
	switch m.Role {
	case "user":
		return userEvents(id, at, blocks, calls)
	case "assistant":
		return assistantEvents(id, at, blocks, calls)
	case "info":
		return infoEvents(id, at, blocks)
	default:
		// An unknown role: one meta event, carrying whatever text the
		// blocks happened to hold.
		return []source.Event{{
			Kind: source.KindMeta, Time: at, ID: id,
			Label: roleLabel(m.Role), Text: joinBlockText(blocks),
		}}
	}
}

// userEvents walks one user message's blocks: visible text and images
// fold into prompt events; tool_result blocks pair into their calls or
// surface as orphans; everything else (hidden text, summaries, unknown
// blocks) is a labelled meta event in stream order.
func userEvents(id string, at time.Time, blocks []readBlock, calls map[string]*source.ToolCall) []source.Event {
	var events []source.Event
	var texts []string
	var images int
	flush := func() {
		if len(texts) > 0 || images > 0 {
			events = append(events, source.Event{
				Kind: source.KindPrompt, Time: at, ID: id,
				Text: strings.Join(texts, "\n"), Images: images,
			})
		}
		texts, images = nil, 0
	}
	meta := func(label, text string) {
		events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: id, Label: label, Text: text})
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Hidden {
				flush()
				meta("user:hidden", b.Text)
				continue
			}
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			images++
		case "tool_result":
			flush()
			res := resultFromRun(b.Run)
			res.Time = at
			if call, ok := calls[b.ToolUseID]; ok && call.Result == nil {
				call.Result = res
				continue
			}
			events = append(events, source.Event{
				Kind: source.KindToolResult, Time: at, ID: id,
				Tool: &source.ToolCall{ID: b.ToolUseID, Result: res},
			})
		default:
			// summary, and whatever the hourly build train adds next.
			flush()
			meta("user:"+blockType(b), blockText(b))
		}
	}
	flush()
	return events
}

// assistantEvents maps one assistant message's blocks one to one:
// text, thinking (Redacted when the block carries no readable text),
// tool_use (registered for pairing); hidden text, summaries, and
// unknown blocks are labelled meta events.
func assistantEvents(id string, at time.Time, blocks []readBlock, calls map[string]*source.ToolCall) []source.Event {
	var events []source.Event
	meta := func(label, text string) {
		events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: id, Label: label, Text: text})
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Hidden {
				meta("assistant:hidden", b.Text)
			} else if b.Text != "" {
				events = append(events, source.Event{Kind: source.KindAssistant, Time: at, ID: id, Text: b.Text})
			}
		case "thinking":
			events = append(events, source.Event{
				Kind: source.KindThinking, Time: at, ID: id,
				Text: b.Thinking, Redacted: b.Thinking == "",
			})
		case "tool_use":
			call := &source.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input}
			if b.ID != "" {
				calls[b.ID] = call
			}
			events = append(events, source.Event{Kind: source.KindToolCall, Time: at, ID: id, Tool: call})
		default:
			meta("assistant:"+blockType(b), blockText(b))
		}
	}
	return events
}

// infoEvents maps an in-stream compaction record: one meta event per
// block ("info:summary" for the summary payload the pinned docs carry),
// a bare "info" event when the message holds no blocks at all.
func infoEvents(id string, at time.Time, blocks []readBlock) []source.Event {
	if len(blocks) == 0 {
		return []source.Event{{Kind: source.KindMeta, Time: at, ID: id, Label: "info"}}
	}
	var events []source.Event
	for _, b := range blocks {
		events = append(events, source.Event{
			Kind: source.KindMeta, Time: at, ID: id,
			Label: "info:" + blockType(b), Text: blockText(b),
		})
	}
	return events
}

// resultFromRun flattens a tool_result's run payload into the shared
// ToolResult. IsError derives ONLY from the {output, exitCode} shell
// shape with a non-zero exit — run.status is unreliable evidence:
// "done" even when a human rejects the call (the deny-misread trap,
// notes/62 §4), and the text is never sniffed for it.
func resultFromRun(run *toolRun) *source.ToolResult {
	res := &source.ToolResult{}
	if run == nil {
		return res
	}
	res.IsError = run.isError()
	res.Text, res.Images = runResultContent(run.Result)
	return res
}

// isError reports whether the result is the shell {output, exitCode}
// shape with a non-zero exit — the only trustworthy in-band error
// signal the format carries.
func (r *toolRun) isError() bool {
	var shell struct {
		ExitCode *int `json:"exitCode"`
	}
	if json.Unmarshal(r.Result, &shell) == nil && shell.ExitCode != nil {
		return *shell.ExitCode != 0
	}
	return false
}

// runResultContent renders run.result for display: a string verbatim,
// the shell shape's own output text, a block-array result's joined text
// (images counted), and anything else as its compact JSON — the honest
// payload, never an invention.
func runResultContent(raw json.RawMessage) (string, int) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", 0
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, 0
	}
	var shell struct {
		Output   string `json:"output"`
		ExitCode *int   `json:"exitCode"`
	}
	if json.Unmarshal(raw, &shell) == nil && shell.ExitCode != nil {
		return shell.Output, 0
	}
	var blocks []readBlock
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 {
		var parts []string
		images := 0
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "image":
				images++
			}
		}
		if len(parts) > 0 || images > 0 {
			return strings.Join(parts, "\n"), images
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		return buf.String(), 0
	}
	return string(raw), 0
}

// messageTime is a message's own timestamp: the new epoch's createdAt
// (RFC3339 ms), else the user-message meta.sentAt (epoch ms — the only
// per-message stamp the old epoch carries), else zero.
func messageTime(m ExportMessage) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
		return t
	}
	if m.Meta.SentAt != 0 {
		return time.UnixMilli(m.Meta.SentAt).UTC()
	}
	return time.Time{}
}

// blockType labels a block for a meta event; a typeless block is
// "unknown" rather than a dangling colon.
func blockType(b readBlock) string {
	if b.Type == "" {
		return "unknown"
	}
	return b.Type
}

// blockText is a meta event's payload for a non-text block: the
// summary's text on a summary block, else the block's text field.
func blockText(b readBlock) string {
	if b.Type == "summary" {
		return b.Summary
	}
	return b.Text
}

// joinBlockText concatenates a message's text blocks — the payload an
// unknown-role message's meta event carries.
func joinBlockText(blocks []readBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// roleLabel names a message role for a meta label, "message" when the
// role itself is absent.
func roleLabel(role string) string {
	if role == "" {
		return "message"
	}
	return role
}

// ListSubagents implements source.TranscriptReader: amp exports carry
// no subagent transcripts — a Task subagent's whole observable trace is
// the parent's opaque tool_result (notes/62 §7's structural gap), and
// Capture writes transcript.json alone. The contract's answer is nil,
// nil — never an invented sidecar.
func (s *Source) ListSubagents(_ string) ([]source.Subagent, error) {
	return nil, nil
}
