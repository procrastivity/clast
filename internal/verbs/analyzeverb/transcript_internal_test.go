package analyzeverb

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

func tcall(id, name, input string, res *source.ToolResult) source.Event {
	return source.Event{
		Kind: source.KindToolCall, Time: at(14, 5),
		Tool: &source.ToolCall{ID: id, Name: name, Input: json.RawMessage(input), Result: res},
	}
}

func fixtureTranscript() TranscriptPage {
	return TranscriptPage{
		Session: Session{ID: "aaaaaaaa-1111", Day: "2026-09-21", Project: "clast", Title: "Ship <the> thing", StartedAt: at(14, 3)},
		Events: []source.Event{
			{Kind: source.KindMeta, Label: "mode", Time: at(14, 3)},
			{Kind: source.KindMeta, Label: "system:turn_duration", Text: "12s"},
			{Kind: source.KindPrompt, Time: at(14, 4), Text: "please <script>alert(1)</script> **run** it"},
			{Kind: source.KindThinking, Redacted: true},
			{Kind: source.KindThinking, Redacted: true},
			{Kind: source.KindThinking, Text: "hmm <b>x</b>"},
			{Kind: source.KindAssistant, Time: at(14, 5), Text: "Sure, `ls`."},
			tcall("toolu_1", "Bash", `{"command":"ls <dir>","description":"list"}`,
				&source.ToolResult{Text: "out <script>bad()</script>", Images: 2}),
			tcall("toolu_2", "Bash", `{"command":"false"}`, &source.ToolResult{Text: "boom", IsError: true}),
			tcall("toolu_3", "Read", `{"file_path":"/a/b.go"}`, nil),
			tcall("toolu_4", "Agent", `{"description":"dig"}`, &source.ToolResult{Text: "done"}),
			{Kind: source.KindToolResult, Tool: &source.ToolCall{ID: "toolu_9", Result: &source.ToolResult{Text: "stray"}}},
		},
		Subagents: []source.Subagent{
			{ID: "agent-1", AgentType: "Explore", Description: "dig", ToolUseID: "toolu_4"},
			{ID: "agent-2", Description: "lost <one>"},
		},
	}
}

func renderTranscriptFixture(t *testing.T, p TranscriptPage) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var buf bytes.Buffer
	if err := RenderTranscript(&buf, p); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestURLs(t *testing.T) {
	if got := TranscriptURL("abc"); got != "/t/abc" {
		t.Errorf("TranscriptURL = %q", got)
	}
	if got := AgentURL("abc", "agent-1"); got != "/t/abc/agents/agent-1" {
		t.Errorf("AgentURL = %q", got)
	}
	if got := OverviewURL("abc"); got != "/#s-abc" {
		t.Errorf("OverviewURL = %q", got)
	}
}

func TestDigest(t *testing.T) {
	tests := []struct{ tool, input, want string }{
		{"Bash", `{"command":"ls   -la\nfoo","description":"d"}`, "ls -la foo"},
		{"Bash", `{"description":"only desc"}`, "only desc"},
		{"Read", `{"file_path":"/x/y"}`, "/x/y"},
		{"Edit", `{"file_path":"/x/y","old_string":"a"}`, "/x/y"},
		{"Write", `{"file_path":"/w"}`, "/w"},
		{"Agent", `{"description":"look around","prompt":"long"}`, "look around"},
		{"WebFetch", `{"url":"http://e.x"}`, "http://e.x"},
		{"Grep", `{"pattern":"foo.*"}`, "foo.*"},
		{"Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"Mystery", `{"a": 1, "b": "x"}`, `{"a":1,"b":"x"}`},
		{"Read", `{"other":1}`, `{"other":1}`},
		{"Bash", `not json`, "not json"},
	}
	for _, tt := range tests {
		if got := digest(tt.tool, json.RawMessage(tt.input)); got != tt.want {
			t.Errorf("digest(%s, %s) = %q, want %q", tt.tool, tt.input, got, tt.want)
		}
	}
	long := digest("Bash", json.RawMessage(`{"command":"`+strings.Repeat("é", 500)+`"}`))
	if n := len([]rune(long)); n != maxDigestRunes || !strings.HasSuffix(long, "…") {
		t.Errorf("long digest = %d runes %q, want %d ending in an ellipsis", n, long[len(long)-4:], maxDigestRunes)
	}
}

func TestCapText(t *testing.T) {
	if c := capText("a\nb\n"); c.Truncated() || c.Text != "a\nb\n" {
		t.Errorf("small text changed: %+v", c)
	}
	byLines := capText(strings.Repeat("x\n", maxResultLines+50))
	if !byLines.Truncated() || byLines.MoreLines != 50 || strings.Count(byLines.Text, "\n") != maxResultLines-1 {
		t.Errorf("line cap: %d more lines, text has %d newlines", byLines.MoreLines, strings.Count(byLines.Text, "\n"))
	}
	oneLong := capText(strings.Repeat("é", maxResultBytes))
	if !oneLong.Truncated() || len(oneLong.Text) > maxResultBytes || !utf8Valid(oneLong.Text) {
		t.Errorf("byte cap: kept %d bytes, valid=%v", len(oneLong.Text), utf8Valid(oneLong.Text))
	}
	if oneLong.MoreBytes != 2*maxResultBytes-len(oneLong.Text) {
		t.Errorf("MoreBytes = %d", oneLong.MoreBytes)
	}
	if !strings.Contains(byLines.Note(), "50 more lines") {
		t.Errorf("Note = %q", byLines.Note())
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "") == s }

func TestItems_GroupsAndMatchesSubagents(t *testing.T) {
	items := fixtureTranscript().Items()
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it.Kind)
	}
	want := "meta prompt redacted thinking assistant tool tool tool tool tool"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("kinds = %q, want %q", got, want)
	}
	if len(items[0].Metas) != 2 || items[2].Count != 2 {
		t.Errorf("meta run %d, redacted count %d; want 2 and 2", len(items[0].Metas), items[2].Count)
	}
	agent := items[8]
	if agent.Sub == nil || agent.Sub.ID != "agent-1" || agent.SubURL != "/t/aaaaaaaa-1111/agents/agent-1" {
		t.Errorf("Agent call not matched: %+v", agent.Sub)
	}
	if items[5].Sub != nil {
		t.Error("Bash call matched a subagent")
	}
	if !items[9].Orphan {
		t.Error("orphan result not marked")
	}
}

func TestParentURL(t *testing.T) {
	p := fixtureTranscript()
	p.Agent = &p.Subagents[0]
	if got, want := p.ParentURL(), "/t/aaaaaaaa-1111#call-toolu_4"; got != want {
		t.Errorf("ParentURL = %q, want %q", got, want)
	}
	p.Agent = &p.Subagents[1]
	if got, want := p.ParentURL(), "/t/aaaaaaaa-1111"; got != want {
		t.Errorf("ParentURL without a call = %q, want %q", got, want)
	}
}

func TestRenderTranscript(t *testing.T) {
	got := renderTranscriptFixture(t, fixtureTranscript())
	for _, bad := range []string{"<script>", "<b>x</b>", "<dir>", "<one>", "<the>"} {
		if strings.Contains(got, bad) {
			t.Errorf("page carries unescaped %q", bad)
		}
	}
	for _, want := range []string{
		`&lt;script&gt;alert(1)&lt;/script&gt;`, // prompt, escaped
		`<strong>run</strong>`,
		`out &lt;script&gt;bad()&lt;/script&gt;`, // tool output, escaped
		`id="call-toolu_1"`, `id="call-toolu_3"`,
		`class="metarun"`, `<input type="checkbox" id="showmeta">`,
		`system:turn_duration: 12s`, `>mode</div>`,
		`thinking (redacted) ×2`,
		`[2 image(s)]`, `no result recorded`, `(call not in transcript)`,
		`href="/t/aaaaaaaa-1111/agents/agent-1"`, `href="/t/aaaaaaaa-1111/agents/agent-2"`,
		`class="error"`, `href="/#s-aaaaaaaa-1111"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(got, "truncated") {
		t.Error("small fixture shows a truncation note")
	}
}

func TestRenderTranscript_TruncationNote(t *testing.T) {
	p := fixtureTranscript()
	p.Events = []source.Event{tcall("t", "Bash", `{"command":"x"}`,
		&source.ToolResult{Text: strings.Repeat("line\n", maxResultLines+7)})}
	if got := renderTranscriptFixture(t, p); !strings.Contains(got, "7 more lines") {
		t.Error("truncation note missing")
	}
}

func TestRenderTranscript_AgentPage(t *testing.T) {
	p := fixtureTranscript()
	p.Agent, p.Subagents = &p.Subagents[0], nil
	got := renderTranscriptFixture(t, p)
	if !strings.Contains(got, `href="/t/aaaaaaaa-1111#call-toolu_4"`) {
		t.Error("agent page has no back-link to the spawning call")
	}
}

func TestRenderTranscript_UserOverrideAndBroken(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := filepath.Join(cfg, "clast", "analyze", "transcript.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{{range .Items}}{{.Kind}};{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderTranscript(&buf, fixtureTranscript()); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "meta;prompt;redacted;thinking;assistant;tool;tool;tool;tool;tool;"; got != want {
		t.Errorf("override render = %q, want %q", got, want)
	}
	if err := os.WriteFile(path, []byte(`{{.NoSuchField}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err := RenderTranscript(&buf, fixtureTranscript())
	if err == nil || !strings.Contains(err.Error(), "analyze: rendering the transcript template") || buf.Len() != 0 {
		t.Fatalf("broken override: err = %v, wrote %d bytes", err, buf.Len())
	}
}

func TestGatherTranscript(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("session.json", `{"transcript":{"format":"claude-jsonl"}}`)
	write("transcript.jsonl", `{"type":"user","uuid":"u1","timestamp":"2026-09-21T14:00:00Z","message":{"role":"user","content":"hello"}}`+"\n")
	write("subagents/agent-a1.jsonl", `{"type":"user","uuid":"u2","message":{"role":"user","content":"sub prompt"}}`+"\n")
	write("subagents/agent-a1.meta.json", `{"agentType":"Explore","description":"look","toolUseId":"toolu_x"}`)

	sess := Session{ID: "s1"}
	p, err := GatherTranscript(dir, sess, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 1 || p.Events[0].Text != "hello" || len(p.Subagents) != 1 || p.IsAgent() {
		t.Errorf("session page = %d events, %d subagents", len(p.Events), len(p.Subagents))
	}
	a, err := GatherTranscript(dir, sess, p.Subagents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsAgent() || len(a.Events) != 1 || a.Events[0].Text != "sub prompt" || a.Subagents != nil {
		t.Errorf("agent page = %+v", a)
	}
	if _, err := GatherTranscript(dir, sess, "nope"); err == nil || !strings.Contains(err.Error(), "no subagent") {
		t.Errorf("unknown agent: err = %v", err)
	}
	write("session.json", `{"transcript":{"format":"mystery"}}`)
	if _, err := GatherTranscript(dir, sess, ""); err == nil || !strings.Contains(err.Error(), "no structured reader") {
		t.Errorf("unknown format: err = %v", err)
	}
}
