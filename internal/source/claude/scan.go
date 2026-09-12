package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// maxLineSize bounds the JSONL scanner's line buffer, ported from duo's
// claude runtime: Claude Code writes large single-line entries
// (skill_listing and similar attachments run past bufio.Scanner's 64KB
// default), and 16MiB covers the largest entries the prior art's notes
// describe.
const maxLineSize = 16 << 20

// entry covers the envelope fields fact extraction needs from any JSONL
// line — the claudeEntry shape ported from duo's claude runtime, minus
// the message-content fields (facts never materialize text, M10) and
// plus the capture facts that adapter didn't need: cwd (correlation
// evidence, M12) and gitBranch.
type entry struct {
	UUID        string `json:"uuid"`
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	CWD         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message json.RawMessage `json:"message"`
}

// message is the half of an entry's message field counting needs: the
// content's JSON shape (string = human prompt; array = blocks) is the
// discriminator, exactly as in the ported adapter.
type message struct {
	Content json.RawMessage `json:"content"`
}

// facts is one transcript's scan result: every M10 fact this source can
// extract, plus the count of lines that would not parse (tolerant
// posture — counted, skipped, never fatal).
type facts struct {
	CWD            string
	Branch         string
	StartedAt      time.Time
	LastActiveAt   time.Time
	UserCount      int
	AssistantCount int
	BadLines       int
}

// scanTranscript extracts facts from one session transcript in a single
// tolerant pass. Rules, ported-and-diverged from duo's parseLine:
//
//   - Sidechain entries never occur in a main transcript (subagents are
//     separate files), but are dropped if met — defense in depth.
//   - CWD is the first one seen (the directory the session started in);
//     Branch is the last one seen (the snapshot at capture, M10).
//   - StartedAt is the first parseable timestamp, LastActiveAt the
//     latest — bookkeeping entries (queue-operation and friends)
//     count for liveness even though they are not conversation.
//   - UserCount counts human prompts only: type "user", content a plain
//     string, not isMeta, origin absent or kind "human". Peer-injected
//     turns, task notifications, and tool_result carriers (content an
//     array) are not the user speaking.
//   - AssistantCount counts type "assistant" entries, deduped by uuid
//     (M14: identity is the entry id — a fork's copied history keeps
//     its parent's uuids, and counting keys on them, never on lines).
//     User entries dedupe the same way.
//   - Any other type contributes nothing; an unknown entry type is safe
//     by construction, not by allowlist.
//
// A line that is not JSON at all increments BadLines and nothing else —
// a live transcript can have a half-written trailing line. Only opening
// the file is a hard error; a scanner failure mid-file (a line past
// maxLineSize) surfaces as a diagnostic with the facts gathered so far.
func scanTranscript(path string) (facts, []source.Diagnostic, error) {
	f, err := os.Open(path)
	if err != nil {
		return facts{}, nil, fmt.Errorf("claude: opening %s: %w", path, err)
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()

	var out facts
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		scanLine(scanner.Bytes(), &out, seen)
	}

	var diags []source.Diagnostic
	if err := scanner.Err(); err != nil {
		diags = append(diags, source.Diagnostic{Path: path, Err: err})
	}
	return out, diags, nil
}

func scanLine(raw []byte, out *facts, seen map[string]struct{}) {
	var e entry
	if json.Unmarshal(raw, &e) != nil {
		out.BadLines++
		return
	}
	if e.IsSidechain {
		return
	}

	if at, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		if out.StartedAt.IsZero() {
			out.StartedAt = at
		}
		if at.After(out.LastActiveAt) {
			out.LastActiveAt = at
		}
	}
	if out.CWD == "" {
		out.CWD = e.CWD
	}
	if e.GitBranch != "" {
		out.Branch = e.GitBranch
	}

	switch e.Type {
	case "user":
		if !isHumanPrompt(e) {
			return
		}
	case "assistant":
	default:
		return
	}
	if e.UUID != "" {
		if _, dup := seen[e.UUID]; dup {
			return
		}
		seen[e.UUID] = struct{}{}
	}
	if e.Type == "user" {
		out.UserCount++
	} else {
		out.AssistantCount++
	}
}

// isHumanPrompt reports whether a type-"user" entry is the user actually
// speaking — see scanTranscript's counting rule.
func isHumanPrompt(e entry) bool {
	if e.IsMeta {
		return false
	}
	var m message
	if json.Unmarshal(e.Message, &m) != nil {
		return false
	}
	var text string
	if json.Unmarshal(m.Content, &text) != nil {
		return false // block-array content: a tool_result carrier, not a prompt.
	}
	return e.Origin == nil || e.Origin.Kind == "human"
}
