// This file holds the transcript view: the model the transcript template
// renders (TranscriptPage), the URL scheme the server routes and the
// overview's links share, RenderTranscript, and GatherTranscript, which
// reads one captured transcript (a session's own, or one subagent's)
// through the registry's structured reader.
//
// Like Page, the model carries raw facts and the derived figures — the
// grouped items, tool digests, capped result text — are methods, so an
// override template (analyze/transcript.html) can reach every fact the
// shipped one uses. Nothing but markdownHTML's output is marked safe.

package analyzeverb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/procrastivity/clast/internal/asset"
	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
)

// transcriptAsset is the transcript template's path under the asset tree.
const transcriptAsset = "analyze/transcript.html"

// A rendered tool result (or input) is capped at whichever comes first.
// 200 lines is about four screens of output, enough to see what a command
// did; 20 KB catches the one-line megabyte (minified JSON, a base64 blob)
// that the line cap would let through. Together they held the three
// largest real transcripts well under 3x their input size.
const (
	maxResultLines = 200
	maxResultBytes = 20 * 1024
)

// maxDigestRunes caps a tool call's one-line summary.
const maxDigestRunes = 120

// TranscriptURL is a session transcript's page: /t/<session-id>. The
// paths are absolute, so they read the same from any page the server
// serves; the server's routes and the overview's links both build them
// here.
func TranscriptURL(sessionID string) string {
	return "/t/" + url.PathEscape(sessionID)
}

// AgentURL is a subagent transcript's page:
// /t/<session-id>/agents/<agent-id>.
func AgentURL(sessionID, agentID string) string {
	return TranscriptURL(sessionID) + "/agents/" + url.PathEscape(agentID)
}

// OverviewURL is the overview's anchor for a session: /#s-<session-id>.
func OverviewURL(sessionID string) string {
	return "/#s-" + url.PathEscape(sessionID)
}

// CallAnchor is the element id of a tool call, for deep links.
func CallAnchor(toolID string) string { return "call-" + toolID }

// TranscriptPage is one transcript view: a session's own conversation, or
// a subagent's when Agent is set.
type TranscriptPage struct {
	// Session is the owning session's overview facts (id, day, project,
	// title, start).
	Session Session
	// Agent is the subagent this page shows; nil for the session's own
	// transcript.
	Agent *source.Subagent
	// Events is the transcript in stream order.
	Events []source.Event
	// Subagents is the session's subagent transcripts; empty on a
	// subagent page.
	Subagents []source.Subagent
	// Warning, when set, is shown as a banner: the read stopped early and
	// Events holds only what was read.
	Warning string
}

// TranscriptItem is one displayed unit of a transcript: an event, or a
// run of events the page folds together.
type TranscriptItem struct {
	// Kind is "prompt", "assistant", "thinking", "redacted" (a run of
	// redacted thinking), "tool" (a call, or an orphan result) or "meta"
	// (a run of meta events).
	Kind  string
	Event source.Event
	// Metas is a "meta" item's run; Count is a "redacted" item's length.
	Metas []MetaLine
	Count int
	// Sub is the subagent an Agent call spawned, when one matches.
	Sub *source.Subagent
	// SubURL is Sub's transcript page.
	SubURL string
	// Orphan marks a tool item that is a result with no call.
	Orphan bool
}

// MetaLine is one meta event as the page shows it.
type MetaLine struct {
	Time  string
	Label string
	Text  string
}

func metaLine(e source.Event) MetaLine {
	l := MetaLine{Time: clock(e.Time), Label: e.Label, Text: e.Text}
	if l.Label == "" {
		l.Label = "meta"
	}
	return l
}

// Items groups the events for display: consecutive meta events fold into
// one run, consecutive redacted thinking into one line, and each Agent
// call is matched to the subagent transcript it spawned.
func (p TranscriptPage) Items() []TranscriptItem {
	subs := make(map[string]*source.Subagent, len(p.Subagents))
	for i := range p.Subagents {
		if id := p.Subagents[i].ToolUseID; id != "" {
			subs[id] = &p.Subagents[i]
		}
	}
	var out []TranscriptItem
	for _, e := range p.Events {
		last := len(out) - 1
		switch e.Kind {
		case source.KindMeta:
			if last >= 0 && out[last].Kind == "meta" {
				out[last].Metas = append(out[last].Metas, metaLine(e))
			} else {
				out = append(out, TranscriptItem{Kind: "meta", Metas: []MetaLine{metaLine(e)}})
			}
		case source.KindThinking:
			switch {
			case !e.Redacted:
				out = append(out, TranscriptItem{Kind: "thinking", Event: e})
			case last >= 0 && out[last].Kind == "redacted":
				out[last].Count++
			default:
				out = append(out, TranscriptItem{Kind: "redacted", Event: e, Count: 1})
			}
		case source.KindToolCall, source.KindToolResult:
			it := TranscriptItem{Kind: "tool", Event: e, Orphan: e.Kind == source.KindToolResult}
			if e.Tool != nil {
				if sub := subs[e.Tool.ID]; sub != nil && !it.Orphan {
					it.Sub = sub
					it.SubURL = AgentURL(p.Session.ID, sub.ID)
				}
			}
			out = append(out, it)
		default:
			out = append(out, TranscriptItem{Kind: string(e.Kind), Event: e})
		}
	}
	return out
}

// IsAgent reports whether the page shows a subagent's transcript.
func (p TranscriptPage) IsAgent() bool { return p.Agent != nil }

// Title is the page's heading: the session's label, or the subagent's
// description.
func (p TranscriptPage) Title() string {
	if p.Agent == nil {
		return p.Session.Label()
	}
	if p.Agent.Description != "" {
		return p.Agent.Description
	}
	return "agent " + p.Agent.ID
}

// OverviewURL is the back-link to the session on the overview.
func (p TranscriptPage) OverviewURL() string { return OverviewURL(p.Session.ID) }

// ParentURL is a subagent page's back-link: the parent transcript, at
// the spawning call when it is known.
func (p TranscriptPage) ParentURL() string {
	u := TranscriptURL(p.Session.ID)
	if p.Agent != nil && p.Agent.ToolUseID != "" {
		u += "#" + CallAnchor(p.Agent.ToolUseID)
	}
	return u
}

// AgentURL is the transcript page of one of the session's subagents.
func (p TranscriptPage) AgentURL(a source.Subagent) string { return AgentURL(p.Session.ID, a.ID) }

// Time is the event's clock time in the local zone; "" when it has none.
func (it TranscriptItem) Time() string { return clock(it.Event.Time) }

func clock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04:05")
}

// HTML is the prose (prompt, assistant, thinking) rendered from markdown.
func (it TranscriptItem) HTML() template.HTML { return markdownHTML(it.Event.Text) }

// Anchor is the element id of a tool item.
func (it TranscriptItem) Anchor() string {
	if it.Event.Tool == nil || it.Event.Tool.ID == "" {
		return ""
	}
	return CallAnchor(it.Event.Tool.ID)
}

// ToolName is the tool's name, else "tool".
func (it TranscriptItem) ToolName() string {
	if it.Event.Tool == nil || it.Event.Tool.Name == "" {
		return "tool"
	}
	return it.Event.Tool.Name
}

// Digest is the one-line summary of a call: the input field that says
// most for the tool, else the input as compact JSON.
func (it TranscriptItem) Digest() string {
	if it.Event.Tool == nil {
		return ""
	}
	return digest(it.Event.Tool.Name, it.Event.Tool.Input)
}

// Input is the call's input, pretty-printed and capped.
func (it TranscriptItem) Input() Capped {
	if it.Event.Tool == nil || len(it.Event.Tool.Input) == 0 {
		return Capped{}
	}
	var buf bytes.Buffer
	text := string(it.Event.Tool.Input)
	if err := json.Indent(&buf, it.Event.Tool.Input, "", "  "); err == nil {
		text = buf.String()
	}
	return capText(text)
}

// HasResult reports whether the tool item has a result recorded.
func (it TranscriptItem) HasResult() bool {
	return it.Event.Tool != nil && it.Event.Tool.Result != nil
}

// Result is the result's text, capped.
func (it TranscriptItem) Result() Capped {
	if !it.HasResult() {
		return Capped{}
	}
	return capText(it.Event.Tool.Result.Text)
}

// ResultTime is the clock time the result was written.
func (it TranscriptItem) ResultTime() string {
	if !it.HasResult() {
		return ""
	}
	return clock(it.Event.Tool.Result.Time)
}

// ResultImages is the number of images the result carried.
func (it TranscriptItem) ResultImages() int {
	if !it.HasResult() {
		return 0
	}
	return it.Event.Tool.Result.Images
}

// IsError reports whether the result is an error.
func (it TranscriptItem) IsError() bool { return it.HasResult() && it.Event.Tool.Result.IsError }

// Capped is text cut to the render caps, with what the cut dropped.
type Capped struct {
	Text string
	// MoreLines and MoreBytes count what was cut; both 0 when nothing was.
	MoreLines int
	MoreBytes int
}

// Truncated reports whether anything was cut.
func (c Capped) Truncated() bool { return c.MoreBytes > 0 }

// Note is the marker shown where the cut was made.
func (c Capped) Note() string {
	return fmt.Sprintf("… %d more lines (%d bytes) truncated", c.MoreLines, c.MoreBytes)
}

// capText cuts s at maxResultLines lines or maxResultBytes bytes,
// whichever is reached first, never inside a UTF-8 sequence.
func capText(s string) Capped {
	end, lines := 0, 0
	for end < len(s) && end < maxResultBytes && lines < maxResultLines {
		i := strings.IndexByte(s[end:], '\n')
		if i < 0 {
			end = len(s)
			break
		}
		end += i + 1
		lines++
	}
	if end > maxResultBytes {
		// The last line crossed the byte cap; cut inside it.
		end = maxResultBytes
	}
	for end > 0 && end < len(s) && !utf8.RuneStart(s[end]) {
		end--
	}
	if end >= len(s) {
		return Capped{Text: s}
	}
	rest := s[end:]
	return Capped{
		Text:      strings.TrimRight(s[:end], "\n"),
		MoreLines: strings.Count(strings.TrimRight(rest, "\n"), "\n") + 1,
		MoreBytes: len(rest),
	}
}

// digestFields names, per tool, the input fields that best identify a
// call, in preference order.
var digestFields = map[string][]string{
	"Bash":         {"command", "description"},
	"Read":         {"file_path"},
	"Edit":         {"file_path"},
	"MultiEdit":    {"file_path"},
	"Write":        {"file_path"},
	"NotebookEdit": {"notebook_path"},
	"Agent":        {"description"},
	"Task":         {"description"},
	"WebFetch":     {"url"},
	"WebSearch":    {"query"},
	"Grep":         {"pattern"},
	"Glob":         {"pattern"},
}

func digest(tool string, input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) == nil {
		for _, name := range digestFields[tool] {
			if s, ok := fields[name].(string); ok && strings.TrimSpace(s) != "" {
				return oneLine(s)
			}
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, input) != nil {
		return oneLine(string(input))
	}
	return oneLine(buf.String())
}

// oneLine folds s's whitespace to single spaces and cuts it to
// maxDigestRunes.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxDigestRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxDigestRunes-1]) + "…"
}

// RenderTranscript writes p as a transcript page to w. As with Render,
// the page is executed into memory first.
func RenderTranscript(w io.Writer, p TranscriptPage) error {
	res, err := asset.Resolve(transcriptAsset)
	if err != nil {
		return clasterr.New("analyze.transcript-unavailable",
			fmt.Sprintf("analyze: resolving the transcript template: %v", err))
	}
	where := res.Path
	if where == "" {
		where = "embedded " + transcriptAsset
	}
	tmpl, err := template.New(transcriptAsset).Parse(string(res.Bytes()))
	if err != nil {
		return clasterr.New("analyze.transcript-unavailable",
			fmt.Sprintf("analyze: parsing the transcript template (%s): %v", where, err))
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return clasterr.New("analyze.transcript-unavailable",
			fmt.Sprintf("analyze: rendering the transcript template (%s): %v", where, err))
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// GatherTranscript reads the transcript held in sessionDir (a journal
// session directory) for sess, whose overview facts head the page. With
// agentID empty it reads the session's own transcript.jsonl; otherwise the
// subagent of that id (analyze.agent-not-found when the session has no such
// one). The reader comes from session.json's transcript.format through the
// registry (analyze.transcript-unavailable when there is none).
//
// A read that fails midway returns the page of events gathered so far
// together with the error, so a caller may still show what was read.
func GatherTranscript(sessionDir string, sess Session, agentID string) (TranscriptPage, error) {
	page := TranscriptPage{Session: sess}
	unavailable := func(format string, args ...any) error {
		return clasterr.New("analyze.transcript-unavailable", "analyze: "+fmt.Sprintf(format, args...))
	}

	raw, err := os.ReadFile(filepath.Join(sessionDir, "session.json"))
	if err != nil {
		return page, unavailable("reading session.json: %v", err)
	}
	var rec journal.Session
	if err := json.Unmarshal(raw, &rec); err != nil {
		return page, unavailable("parsing session.json: %v", err)
	}
	reader, ok := sourceregistry.LookupTranscriptReader(rec.Transcript.Format)
	if !ok {
		return page, unavailable("no structured reader for transcript format %q", rec.Transcript.Format)
	}
	subs, err := reader.ListSubagents(sessionDir)
	if err != nil {
		return page, unavailable("listing subagents: %v", err)
	}

	path, err := journal.TranscriptPathByDir(sessionDir, rec.Transcript.ArtifactName())
	if err != nil {
		return page, unavailable("%v", err)
	}
	if agentID == "" {
		page.Subagents = subs
	} else {
		for i := range subs {
			if subs[i].ID == agentID {
				page.Agent = &subs[i]
			}
		}
		if page.Agent == nil {
			return page, clasterr.New("analyze.agent-not-found",
				fmt.Sprintf("analyze: session %s has no subagent %q", sess.ID, agentID))
		}
		if !filepath.IsLocal(filepath.FromSlash(page.Agent.Path)) {
			return page, unavailable("subagent path %q leaves the session directory", page.Agent.Path)
		}
		path = filepath.Join(sessionDir, filepath.FromSlash(page.Agent.Path))
	}

	f, err := os.Open(path)
	if err != nil {
		return page, unavailable("opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	page.Events, err = reader.ReadTranscript(f)
	if err != nil {
		return page, unavailable("reading %s: %v", path, err)
	}
	return page, nil
}
