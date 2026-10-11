package devin

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/procrastivity/clast/internal/source"
	"github.com/tidwall/gjson"
)

// The offline readers (SURFACE V18/M9): TranscriptRenderer and
// TranscriptReader parse the committed devin-export-v1 artifact — never
// the live store, never the Devin executable. The active conversation
// is derived exactly the way capture derived the counts: walk
// session.main_chain_id back through parent_node_id to the NULL root
// and reverse. Rows stay native JSON text in the artifact; these files
// are the only place message content is materialized.

// exportDoc is the artifact's typed view. Sections the readers never
// consult (prompt_history, the non-head session fields) stay raw in the
// document — reading less than capture wrote is the point of the
// format's own gate.
type exportDoc struct {
	Format    string `json:"format"`
	SessionID string `json:"session_id"`
	Session   struct {
		MainChainID *int64 `json:"main_chain_id"`
	} `json:"session"`
	MessageNodes []struct {
		NodeID    int64  `json:"node_id"`
		Parent    *int64 `json:"parent_node_id"`
		Chat      string `json:"chat_message"`
		CreatedAt int64  `json:"created_at"`
	} `json:"message_nodes"`
	ToolCallState []struct {
		ToolCallID         string  `json:"tool_call_id"`
		ToolCallJSON       *string `json:"tool_call_json"`
		ToolCallUpdateJSON *string `json:"tool_call_update_json"`
	} `json:"tool_call_state"`
}

// parseExport decodes the artifact and refuses anything that is not the
// format this source stamps — a renamed or foreign document must fail
// loudly, not read empty.
func parseExport(r io.Reader) (*exportDoc, error) {
	var doc exportDoc
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("devin: parsing export: %w", err)
	}
	if doc.Format != exportFormat {
		return nil, fmt.Errorf("devin: artifact format %q, want %q", doc.Format, exportFormat)
	}
	return &doc, nil
}

// activeChain returns the session's chain nodes in conversation order:
// from main_chain_id through parent links to the root, reversed. A
// missing parent or a cycle truncates the walk at the anomaly — the
// walked prefix is the readable conversation, matching capture's
// Incomplete posture (the flag already lives in session.json). A NULL
// main_chain_id is the empty session: no chain, no error.
func (d *exportDoc) activeChain() []int {
	parent := make(map[int64]int64, len(d.MessageNodes))
	index := make(map[int64]int, len(d.MessageNodes))
	for i, n := range d.MessageNodes {
		index[n.NodeID] = i
		if n.Parent != nil {
			parent[n.NodeID] = *n.Parent
		}
	}
	if d.Session.MainChainID == nil {
		return nil
	}
	var rev []int
	seen := make(map[int64]bool, len(d.MessageNodes))
	for id := *d.Session.MainChainID; ; {
		if seen[id] {
			break
		}
		seen[id] = true
		i, ok := index[id]
		if !ok {
			break
		}
		rev = append(rev, i)
		p, ok := parent[id]
		if !ok {
			break
		}
		id = p
	}
	chain := make([]int, len(rev))
	for i, v := range rev {
		chain[len(rev)-1-i] = v
	}
	return chain
}

// nodeTime is a node's timestamp for events: chat_message's own
// metadata.created_at where it parses, else the row's unix-seconds
// created_at.
func nodeTime(chat string, createdAt int64) time.Time {
	if t, err := time.Parse(time.RFC3339Nano,
		gjson.Get(chat, "metadata.created_at").String()); err == nil {
		return t
	}
	return time.Unix(createdAt, 0).UTC()
}

// toolState is one tool_call_state row's reader-side verdict: the
// authoritative status plus the name and synthesized result text used
// when the chain carries no tool node for the call.
type toolState struct {
	name     string
	rawInput json.RawMessage
	status   string
	text     string
	isError  bool
}

// toolStates indexes tool_call_state by tool_call_id — session-scoped
// by construction (every row in the section belongs to this session).
// The update row is the verdict authority; the call row fills
// rawInput and the inference name.
func (d *exportDoc) toolStates() map[string]toolState {
	out := make(map[string]toolState, len(d.ToolCallState))
	for _, row := range d.ToolCallState {
		st := toolState{}
		if row.ToolCallJSON != nil {
			call := *row.ToolCallJSON
			st.name = gjson.Get(call, `_meta.cognition\.ai/inferenceToolName`).String()
			st.rawInput = json.RawMessage(gjson.Get(call, "rawInput").Raw)
		}
		if row.ToolCallUpdateJSON != nil {
			upd := *row.ToolCallUpdateJSON
			st.status = gjson.Get(upd, "status").String()
			if st.name == "" {
				st.name = gjson.Get(upd, `_meta.cognition\.ai/inferenceToolName`).String()
			}
			meta := gjson.Get(upd, "_meta")
			st.isError = st.status != "" && st.status != "completed" ||
				meta.Get(`cognition\.ai/rejected`).Bool() ||
				meta.Get(`cognition\.ai/canceled`).Bool()
			// The update's content[] carries ACP content blocks —
			// [{"type":"content","content":{"type":"text","text":…}}].
			var texts []string
			for _, blk := range gjson.Get(upd, "content").Array() {
				if t := blk.Get("content.text").String(); t != "" {
					texts = append(texts, t)
				}
			}
			st.text = joinLines(texts)
		}
		out[row.ToolCallID] = st
	}
	return out
}

func joinLines(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += "\n"
		}
		s += p
	}
	return s
}

// TranscriptFormats implements both transcript seams: this source reads
// and renders exactly the one format its own Capture stamps.
func (s *Source) TranscriptFormats() []string {
	return []string{exportFormat}
}

// ReadTranscript implements source.TranscriptReader over devin-export-v1:
// the active chain's messages mapped onto the shared event vocabulary.
//
//   - user + is_user_input → KindPrompt; other role:user payloads
//     (auto-continue, compaction) → KindMeta "user".
//   - assistant thinking → KindThinking (empty object → Redacted);
//     non-empty content → KindAssistant; each tool_calls[] entry →
//     KindToolCall, arguments verbatim as Input.
//   - role:tool nodes pair onto their call by tool_call_id; a result
//     whose call is off-chain (rebuilt prefix, truncated walk) becomes
//     its own KindToolResult event. IsError comes from
//     chisel/tool_result_meta.success, then the tool_call_state verdict.
//   - tool_call_state rows with no chain node (hook-blocked, update-only)
//     synthesize a KindToolResult — or fill the known call's Result —
//     from the update's content; that is the only place a rejected or
//     canceled verdict's text exists.
//   - system and unknown roles → KindMeta labeled "role:<role>"; the
//     subagent_completion_notification node lands there by role, its
//     payload readable as Text.
//   - malformed chat_message JSON contributes no events — the artifact
//     still carries it verbatim; a reader skips what it cannot read.
func (s *Source) ReadTranscript(r io.Reader) ([]source.Event, error) {
	doc, err := parseExport(r)
	if err != nil {
		return nil, err
	}
	states := doc.toolStates()
	var events []source.Event
	calls := map[string]*source.ToolCall{}
	answered := map[string]bool{} // state rows already consumed by a tool node

	for _, idx := range doc.activeChain() {
		n := doc.MessageNodes[idx]
		at := nodeTime(n.Chat, n.CreatedAt)
		if !gjson.Valid(n.Chat) {
			continue
		}
		role := gjson.Get(n.Chat, "role").String()
		id := gjson.Get(n.Chat, "message_id").String()
		content := gjson.Get(n.Chat, "content").String()
		switch role {
		case "user":
			if gjson.Get(n.Chat, "metadata.is_user_input").Bool() {
				events = append(events, source.Event{Kind: source.KindPrompt, Time: at, ID: id, Text: content})
			} else {
				events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: id, Label: "user", Text: content})
			}
		case "assistant":
			if t := gjson.Get(n.Chat, "thinking"); t.Exists() {
				text := t.Get("thinking").String()
				events = append(events, source.Event{Kind: source.KindThinking, Time: at, ID: id, Text: text, Redacted: text == ""})
			}
			if content != "" {
				events = append(events, source.Event{Kind: source.KindAssistant, Time: at, ID: id, Text: content})
			}
			for _, tc := range gjson.Get(n.Chat, "tool_calls").Array() {
				call := &source.ToolCall{
					ID:    tc.Get("id").String(),
					Name:  tc.Get("name").String(),
					Input: json.RawMessage(tc.Get("arguments").Raw),
				}
				if call.ID != "" {
					calls[call.ID] = call
				}
				events = append(events, source.Event{Kind: source.KindToolCall, Time: at, ID: id, Tool: call})
			}
		case "tool":
			tcid := gjson.Get(n.Chat, "tool_call_id").String()
			res := &source.ToolResult{Time: at, Text: content}
			res.IsError = gjson.Get(n.Chat, `metadata.extensions.chisel/tool_result_meta.success`).Exists() &&
				!gjson.Get(n.Chat, `metadata.extensions.chisel/tool_result_meta.success`).Bool()
			if st, ok := states[tcid]; ok {
				answered[tcid] = true
				if st.isError {
					res.IsError = true
				}
			}
			if call, ok := calls[tcid]; ok && call.Result == nil {
				call.Result = res
				continue
			}
			events = append(events, source.Event{
				Kind: source.KindToolResult, Time: at, ID: id,
				Tool: &source.ToolCall{ID: tcid, Result: res},
			})
		default:
			events = append(events, source.Event{Kind: source.KindMeta, Time: at, ID: id, Label: "role:" + role, Text: content})
		}
	}

	// Update-only verdicts: a state row no on-chain tool node consumed.
	// A known call gets its Result filled; an unknown call becomes an
	// orphan result event — the interrupted/hook-blocked evidence lives
	// nowhere else (ATIF omits tool results entirely).
	for _, row := range doc.ToolCallState {
		if row.ToolCallUpdateJSON == nil || answered[row.ToolCallID] {
			continue
		}
		st := states[row.ToolCallID]
		res := &source.ToolResult{Text: st.text, IsError: st.isError}
		if call, ok := calls[row.ToolCallID]; ok && call.Result == nil {
			call.Result = res
			continue
		}
		events = append(events, source.Event{
			Kind: source.KindToolResult,
			ID:   "tool-result:" + row.ToolCallID,
			Tool: &source.ToolCall{ID: row.ToolCallID, Name: st.name, Result: res},
		})
	}
	return events, nil
}

// ListSubagents implements source.TranscriptReader: devin's subagent
// conversations are chains inside the same forest, not per-subagent
// artifacts — there is no file to list. They stay evidence in the
// export (the subagent_heads section and their own forest roots); a
// structured per-subagent read is a documented gap, not a hidden one.
func (s *Source) ListSubagents(_ string) ([]source.Subagent, error) {
	return nil, nil
}
