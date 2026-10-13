package pi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source/pi"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/verbs/capture"
)

// compact renders events one per line for exact assertions:
// kind|id|label|text, plus tool and result detail when present.
func compact(evs []source.Event) []string {
	var out []string
	for _, e := range evs {
		s := fmt.Sprintf("%s|%s|%s|%s", e.Kind, e.ID, e.Label, e.Text)
		if e.Images > 0 {
			s += fmt.Sprintf("|img=%d", e.Images)
		}
		if e.Redacted {
			s += "|redacted"
		}
		if t := e.Tool; t != nil {
			s += fmt.Sprintf("|tool=%s,%s,%s", t.ID, t.Name, t.Input)
			if r := t.Result; r != nil {
				s += fmt.Sprintf("|result=%q,err=%v,img=%d", r.Text, r.IsError, r.Images)
			} else {
				s += "|result=nil"
			}
		}
		out = append(out, s)
	}
	return out
}

func turnStrings(ts []source.Turn) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Role+": "+t.Text)
	}
	return out
}

func fixtureReader(t *testing.T, glob string) *os.File {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(fixtureRoots(t), glob))
	if err != nil || len(m) != 1 {
		t.Fatalf("glob %s = %v %v", glob, m, err)
	}
	f, err := os.Open(m[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

const (
	metaModel = "meta|01001001|model_change|fixture-provider/fixture-model"
	metaThink = "meta|02002002|thinking_level_change|high"
	ok        = "FIXTURE-OK"
)

func TestReadAndRenderFixtures(t *testing.T) {
	demo := "percwd/--home-user-Code-demo--/*_" + idPrefix
	two := []string{"user: " + ok, "assistant: " + ok}
	four := []string{"user: " + ok, "assistant: " + ok, "user: " + ok, "assistant: " + ok}
	tests := []struct {
		name   string
		glob   string
		events []string
		turns  []string
	}{
		{
			name: "basic: calls paired with results, thinking kept",
			glob: demo + "000000000001.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok,
				"thinking|04004004||" + ok,
				`tool_call|04004004||` + "|tool=call_FIXT01|fc_FIXT01,bash,{\"command\":\"echo FIXTURE-OK\"}|result=\"FIXTURE-OK\",err=false,img=0",
				"thinking|06006006||" + ok,
				`tool_call|06006006||` + "|tool=call_FIXT02|fc_FIXT02,bash,{\"command\":\"echo FIXTURE-OK\"}|result=\"FIXTURE-OK\",err=false,img=0",
				"assistant|08008008||" + ok,
			},
			turns: two,
		},
		{
			name: "branch: leaf path only, abandoned u2/a2 omitted",
			glob: demo + "000000000006.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok, "assistant|04004004||" + ok,
				"prompt|07007007||" + ok, "assistant|08008008||" + ok,
			},
			turns: four,
		},
		{
			name: "torn tail: partial line contributes nothing",
			glob: demo + "000000000007.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok, "assistant|04004004||" + ok,
			},
			turns: two,
		},
		{
			name: "bad lines skipped",
			glob: demo + "000000000009.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok, "assistant|04004004||" + ok,
				"prompt|05005005||" + ok, "assistant|06006006||" + ok,
			},
			turns: four,
		},
		{
			name: "broken chain falls back to whole-file order",
			glob: demo + "000000000010.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok, "prompt|05005005||" + ok, "assistant|06006006||" + ok,
			},
			turns: []string{"user: " + ok, "user: " + ok, "assistant: " + ok},
		},
		{
			name: "error-only: failed reply surfaces as meta, no turn",
			glob: demo + "000000000012.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok,
				"meta|04004004|assistant:error|FIXTURE-ERROR",
			},
			turns: []string{"user: " + ok},
		},
		{
			name: "fork-child: copied history then the new turn",
			glob: demo + "000000000004.jsonl",
			events: []string{
				metaModel, metaThink,
				"prompt|03003003||" + ok, "assistant|04004004||" + ok,
				"prompt|05005005||" + ok, "assistant|06006006||" + ok,
				"prompt|07007007||" + ok, "assistant|08008008||" + ok,
			},
			turns: []string{"user: " + ok, "assistant: " + ok, "user: " + ok, "assistant: " + ok, "user: " + ok, "assistant: " + ok},
		},
		{
			name:   "header-only",
			glob:   demo + "000000000011.jsonl",
			events: nil,
			turns:  nil,
		},
	}
	s := pi.New()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evs, err := s.ReadTranscript(fixtureReader(t, tc.glob))
			if err != nil {
				t.Fatal(err)
			}
			if got := compact(evs); !reflect.DeepEqual(got, tc.events) {
				t.Errorf("events:\n got %q\nwant %q", got, tc.events)
			}
			turns, err := s.RenderTranscript(fixtureReader(t, tc.glob), 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := turnStrings(turns); !reflect.DeepEqual(got, tc.turns) {
				t.Errorf("turns:\n got %q\nwant %q", got, tc.turns)
			}
		})
	}
}

func TestReaderTimesAndFormats(t *testing.T) {
	s := pi.New()
	if !reflect.DeepEqual(s.TranscriptFormats(), []string{"pi-jsonl-v3"}) {
		t.Errorf("formats = %v", s.TranscriptFormats())
	}
	evs, _ := s.ReadTranscript(fixtureReader(t, "percwd/--home-user-Code-demo--/*_"+idPrefix+"000000000001.jsonl"))
	if want := time.Date(2026, 1, 10, 10, 0, 1, 0, time.UTC); !evs[2].Time.Equal(want) {
		t.Errorf("prompt time = %v, want %v", evs[2].Time, want)
	}
	subs, err := s.ListSubagents(t.TempDir())
	if subs != nil || err != nil {
		t.Errorf("ListSubagents = %v %v", subs, err)
	}
}

func TestRenderCapsRunes(t *testing.T) {
	in := hdr + `{"type":"message","id":"a","parentId":null,"timestamp":"2026-05-01T10:00:01.000Z","message":{"role":"user","content":"héllo wörld"}}` + "\n"
	turns, err := pi.New().RenderTranscript(strings.NewReader(in), 4)
	if err != nil || len(turns) != 1 || turns[0].Text != "héll" {
		t.Errorf("turns=%v err=%v", turns, err)
	}
}

func TestReadSyntheticToolPairing(t *testing.T) {
	line := func(id, parent, body string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-05-01T10:00:00.000Z","message":%s}`+"\n", id, p, body)
	}
	in := hdr +
		line("u", "", `{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","data":"x"}]}`) +
		line("a1", "u", `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"p":1}},{"type":"toolCall","id":"c2","name":"bash","arguments":{}},{"type":"thinking","thinking":""}],"stopReason":"toolUse"}`) +
		line("r1", "a1", `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}],"isError":true}`) +
		line("r0", "r1", `{"role":"toolResult","toolCallId":"ghost","toolName":"edit","content":[{"type":"text","text":"orphan"},{"type":"image"}],"isError":false}`) +
		line("r1b", "r0", `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"dup"}]}`) +
		line("b", "r1b", `{"role":"bashExecution","command":"ls"}`) +
		line("x", "b", `{"role":"mystery","content":"m"}`)
	evs, err := pi.New().ReadTranscript(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"prompt|u||hi|img=1",
		`tool_call|a1||` + `|tool=c1,read,{"p":1}|result="line1\nline2",err=true,img=0`,
		`tool_call|a1||` + `|tool=c2,bash,{}|result=nil`,
		"thinking|a1|||redacted",
		`tool_result|r0||` + `|tool=ghost,edit,|result="orphan",err=false,img=1`,
		`tool_result|r1b||` + `|tool=c1,read,|result="dup",err=false,img=0`,
		"meta|b|bashExecution|ls",
		"meta|x|message:mystery|m",
	}
	if got := compact(evs); !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
	// The renderer shows prose only: the user text, nothing else.
	turns, _ := pi.New().RenderTranscript(strings.NewReader(in), 0)
	if got := turnStrings(turns); !reflect.DeepEqual(got, []string{"user: hi"}) {
		t.Errorf("turns = %q", got)
	}
}

func TestReadMetaEntryTypes(t *testing.T) {
	e := func(id, parent, rest string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return fmt.Sprintf(`{"id":%q,"parentId":%s,"timestamp":"2026-05-01T10:00:00.000Z",%s}`+"\n", id, p, rest)
	}
	in := hdr +
		e("1", "", `"type":"compaction","summary":"sum"`) +
		e("2", "1", `"type":"session_info","name":"nm"`) +
		e("3", "2", `"type":"branch_summary","summary":"bs"`) +
		e("4", "3", `"type":"custom","customType":"ext","data":{}`) +
		e("5", "4", `"type":"custom_message","customType":"ext","content":"cm","display":true`) +
		e("6", "5", `"type":"label","targetId":"1","label":"lb"`) +
		e("7", "6", `"type":"future_thing"`)
	evs, err := pi.New().ReadTranscript(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"meta|1|compaction|sum", "meta|2|session_info|nm", "meta|3|branch_summary|bs",
		"meta|4|custom:ext|", "meta|5|custom_message:ext|cm", "meta|6|label|lb", "meta|7|future_thing|",
	}
	if got := compact(evs); !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
}

func TestReadTolerance(t *testing.T) {
	big := `{"type":"custom","id":"big","parentId":null,"pad":"` + strings.Repeat("x", pi.MaxLineSize) + `"}` + "\n"
	u := `{"type":"message","id":"a","parentId":null,"timestamp":"2026-05-01T10:00:00.000Z","message":{"role":"user","content":"q"}}` + "\n"
	// An oversize line does not stop the read; it is just not an entry.
	evs, err := pi.New().ReadTranscript(strings.NewReader(hdr + big + u))
	if err != nil || len(evs) != 1 || evs[0].Text != "q" {
		t.Errorf("oversize: %v %v", compact(evs), err)
	}
	// Torn tail, blank lines, junk, later header lines.
	evs, err = pi.New().ReadTranscript(strings.NewReader(hdr + "\n junk\n" + u + hdr + `{"type":"message","id":"b","pa`))
	if err != nil || len(evs) != 1 {
		t.Errorf("tolerance: %v %v", compact(evs), err)
	}
	// Valid unterminated final line is read.
	evs, _ = pi.New().ReadTranscript(strings.NewReader(hdr + strings.TrimSuffix(u, "\n")))
	if len(evs) != 1 {
		t.Errorf("unterminated valid tail: %v", compact(evs))
	}
	// Empty and header-only inputs.
	for _, in := range []string{"", hdr} {
		if evs, err := pi.New().ReadTranscript(strings.NewReader(in)); err != nil || len(evs) != 0 {
			t.Errorf("%q: %v %v", in, evs, err)
		}
	}
}

// TestOfflineEndToEnd captures into a journal, deletes the source, and
// reads the stored artifact back through the Pi renderer and reader.
// The public show/analyze verbs resolve only through registry.All
// (Stage 4 wiring), so the source's own methods are called on the
// journal artifact directly.
func TestOfflineEndToEnd(t *testing.T) {
	srcRoot := filepath.Join(t.TempDir(), "percwd")
	copyTree(t, filepath.Join(fixtureRoots(t), "percwd"), srcRoot)
	journalRoot := t.TempDir()
	s := pi.NewAt(srcRoot)
	captured, _, failed, err := capture.Run(t.Context(), capture.Deps{
		Root: journalRoot, Sources: []source.Source{s},
		Now: func() time.Time { return time.Unix(1792000000, 0).UTC() },
	})
	if err != nil || len(failed) != 0 || len(captured) != 16 {
		t.Fatalf("capture: n=%d failed=%v err=%v", len(captured), failed, err)
	}
	if err := os.RemoveAll(srcRoot); err != nil {
		t.Fatal(err)
	}

	for _, c := range captured {
		sess, ok, err := journal.ReadSession(journalRoot, c.Shard, c.Key)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if sess.Transcript.Format != "pi-jsonl-v3" {
			t.Fatalf("format = %q", sess.Transcript.Format)
		}
		path, err := journal.TranscriptPath(journalRoot, c.Shard, c.Key, sess.Transcript.ArtifactName())
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		turns, err := s.RenderTranscript(f, 0)
		_, _ = f.Seek(0, 0)
		evs, err2 := s.ReadTranscript(f)
		_ = f.Close()
		if err != nil || err2 != nil {
			t.Fatalf("%s: %v %v", c.Key.NativeID, err, err2)
		}
		// Consistent with facts: on a healthy leaf path the turn counts
		// equal the captured counts (the broken-chain fixture reads
		// whole-file, as its counts do).
		users := 0
		for _, tn := range turns {
			if tn.Role == "user" {
				users++
			}
		}
		prompts := 0
		for _, e := range evs {
			if e.Kind == source.KindPrompt {
				prompts++
			}
		}
		if users != sess.Counts.User || prompts != users {
			t.Errorf("%s: turns user=%d prompts=%d, counts=%+v", c.Key.NativeID, users, prompts, sess.Counts)
		}
	}

	// Deep check on the tool-bearing session through the stored copy.
	var basic captured0
	for _, c := range captured {
		if c.Key.NativeID == idPrefix+"000000000001" {
			basic = captured0{c.Shard, c.Key}
		}
	}
	p, _ := journal.TranscriptPath(journalRoot, basic.shard, basic.key, "transcript.jsonl")
	f, _ := os.Open(p)
	defer func() { _ = f.Close() }()
	evs, err := s.ReadTranscript(f)
	if err != nil || len(evs) != 8 || evs[4].Tool == nil || evs[4].Tool.Result == nil || evs[4].Tool.Result.Text != ok {
		t.Errorf("stored basic read: %v %v", compact(evs), err)
	}
}

type captured0 struct {
	shard string
	key   journal.SessionKey
}
