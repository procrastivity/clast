package llm_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

// eventLog collects full events an Observer sees.
type eventLog struct {
	mu sync.Mutex
	ev []llm.Event
}

func (l *eventLog) observe(e llm.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ev = append(l.ev, e)
}

func (l *eventLog) all() []llm.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]llm.Event(nil), l.ev...)
}

func (l *eventLog) phases() []llm.Phase {
	var ps []llm.Phase
	for _, e := range l.all() {
		ps = append(ps, e.Phase)
	}
	return ps
}

func (l *eventLog) texts(p llm.Phase) []string {
	var out []string
	for _, e := range l.all() {
		if e.Phase == p {
			out = append(out, e.Text)
		}
	}
	return out
}

func streamClient(t *testing.T, baseURL string) *llm.Client {
	t.Helper()
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	c, err := llm.NewClient(cfgWith(baseURL, "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func completeLogged(c *llm.Client) (string, *eventLog, error) {
	var log eventLog
	got, err := c.Complete(llm.WithObserver(context.Background(), log.observe), "sys", "usr")
	return got, &log, err
}

func sseData(js string) []string { return []string{"data: " + js, ""} }

func TestComplete_StreamDeltas(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream([]string{"Hel", "lo"})
	got, log, err := completeLogged(streamClient(t, stub.URL()))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Hello" {
		t.Errorf("content = %q, want Hello", got)
	}
	if want := []string{"Hel", "lo"}; !reflect.DeepEqual(log.texts(llm.PhaseDelta), want) {
		t.Errorf("delta texts = %v, want %v", log.texts(llm.PhaseDelta), want)
	}
	ps := log.phases()
	first, d1, d2, done := -1, -1, -1, -1
	for i, p := range ps {
		switch {
		case p == llm.PhaseFirstByte:
			first = i
		case p == llm.PhaseDelta && d1 < 0:
			d1 = i
		case p == llm.PhaseDelta:
			d2 = i
		case p == llm.PhaseDone:
			done = i
		}
	}
	if first < 0 || first >= d1 || d1 >= d2 || d2 >= done || done != len(ps)-1 {
		t.Errorf("phase order = %v", ps)
	}
	if !stub.Requests()[0].Stream {
		t.Error("request Stream = false, want true")
	}
}

func TestComplete_StreamFallsBackToJSON(t *testing.T) {
	stub := llmtest.New(t, "canned text")
	got, log, err := completeLogged(streamClient(t, stub.URL()))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "canned text" {
		t.Errorf("content = %q", got)
	}
	if !stub.Requests()[0].Stream {
		t.Error("request Stream = false, want true (default)")
	}
	if n := len(log.texts(llm.PhaseDelta)); n != 0 {
		t.Errorf("JSON reply produced %d delta events", n)
	}
}

func TestComplete_StreamDisabledByConfig(t *testing.T) {
	stub := llmtest.New(t, "plain")
	stub.SetStream([]string{"sse"})
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	cfg := config.Config{"llm": map[string]any{
		"base_url": stub.URL(), "model": "m", "stream": false,
	}}
	c, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "sse" {
		// The stub joins chunks into JSON for a non-stream request.
		t.Errorf("content = %q, want sse", got)
	}
	if stub.Requests()[0].Stream {
		t.Error("request Stream = true, want false")
	}
}

func TestNewClient_StreamConfig(t *testing.T) {
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	mk := func(v any, set bool) config.Config {
		sec := map[string]any{"base_url": "http://x", "model": "m"}
		if set {
			sec["stream"] = v
		}
		return config.Config{"llm": sec}
	}
	for name, cfg := range map[string]config.Config{
		"absent": mk(nil, false), "null": mk(nil, true), "true": mk(true, true), "false": mk(false, true),
	} {
		if _, err := llm.NewClient(cfg); err != nil {
			t.Errorf("%s: NewClient: %v", name, err)
		}
	}
	_, err := llm.NewClient(mk("yes", true))
	if err == nil || !strings.Contains(err.Error(), "llm.stream") {
		t.Fatalf("non-bool stream err = %v, want one naming llm.stream", err)
	}
}

func TestComplete_StreamRaw(t *testing.T) {
	const chunk = `{"choices":[{"delta":{"content":"%s"}}]}`
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	tests := []struct {
		name      string
		lines     []string
		want      string
		wantErr   string
		thinking  []string
		wantDelta []string
	}{
		{
			name: "keepalive comments skipped",
			lines: join([]string{": keepalive", ""}, sseData(fmt.Sprintf(chunk, "a")),
				[]string{": ping", ""}, sseData(fmt.Sprintf(chunk, "b")), []string{"data: [DONE]", ""}),
			want: "ab", wantDelta: []string{"a", "b"},
		},
		{
			name: "reasoning stays out of result",
			lines: join(
				sseData(`{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`),
				sseData(`{"choices":[{"delta":{"reasoning":"ok"}}]}`),
				sseData(fmt.Sprintf(chunk, "ans")), []string{"data: [DONE]", ""}),
			want: "ans", thinking: []string{"hmm", "ok"}, wantDelta: []string{"ans"},
		},
		{
			name: "contentless events ignored",
			lines: join(
				sseData(`{"choices":[{"delta":{"role":"assistant","content":""}}]}`),
				sseData(`{"choices":[]}`),
				sseData(`{"choices":[{"delta":{"content":null}}]}`),
				sseData(fmt.Sprintf(chunk, "x")),
				sseData(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`),
				sseData(`{"choices":[],"usage":{"total_tokens":3}}`),
				[]string{"data: [DONE]", ""}),
			want: "x", wantDelta: []string{"x"},
		},
		{
			name:    "error event",
			lines:   join(sseData(fmt.Sprintf(chunk, "a")), sseData(`{"error":{"message":"rate limited"}}`)),
			wantErr: "stream error: rate limited",
		},
		{
			name:    "malformed event",
			lines:   sseData(`{not json`),
			wantErr: "returned malformed stream event:",
		},
		{
			name:  "missing DONE is success",
			lines: join(sseData(fmt.Sprintf(chunk, "tail"))),
			want:  "tail", wantDelta: []string{"tail"},
		},
		{
			name:  "unterminated final event still counts",
			lines: []string{"data: " + fmt.Sprintf(chunk, "last")},
			want:  "last", wantDelta: []string{"last"},
		},
		{
			name:  "no space after data colon and CRLF-free event field",
			lines: []string{"event: message", "data:" + fmt.Sprintf(chunk, "n"), "", "data: [DONE]", ""},
			want:  "n", wantDelta: []string{"n"},
		},
		{
			name: "event split over two data lines",
			lines: []string{
				`data: {"choices":[{"delta":`, `data: {"content":"split"}}]}`, "",
				"data: [DONE]", "",
			},
			want: "split", wantDelta: []string{"split"},
		},
		{
			name: "stops at DONE",
			lines: join([]string{"data: [DONE]", ""},
				sseData(fmt.Sprintf(chunk, "after"))),
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := llmtest.New(t, "unused")
			stub.SetStreamRaw(tc.lines)
			got, log, err := completeLogged(streamClient(t, stub.URL()))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if contains(log.phases(), llm.PhaseDone) {
					t.Error("PhaseDone emitted on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(log.texts(llm.PhaseThinking), tc.thinking) {
				t.Errorf("thinking = %v, want %v", log.texts(llm.PhaseThinking), tc.thinking)
			}
			if !reflect.DeepEqual(log.texts(llm.PhaseDelta), tc.wantDelta) {
				t.Errorf("deltas = %v, want %v", log.texts(llm.PhaseDelta), tc.wantDelta)
			}
			if ps := log.phases(); ps[len(ps)-1] != llm.PhaseDone {
				t.Errorf("last phase = %v, want done", ps[len(ps)-1])
			}
		})
	}
}

func TestComplete_StreamNoObserver(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStreamRaw(append(sseData(`{"choices":[{"delta":{"reasoning_content":"r"}}]}`),
		append(sseData(`{"choices":[{"delta":{"content":"c"}}]}`), "data: [DONE]", "")...))
	got, err := streamClient(t, stub.URL()).Complete(context.Background(), "s", "u")
	if err != nil || got != "c" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestComplete_StreamLargeDelta(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	stub := llmtest.New(t, "unused")
	stub.SetStream([]string{big, "!"})
	got, log, err := completeLogged(streamClient(t, stub.URL()))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != big+"!" {
		t.Errorf("content length = %d, want %d", len(got), len(big)+1)
	}
	if d := log.texts(llm.PhaseDelta); len(d) != 2 || len(d[0]) != len(big) {
		t.Errorf("delta lengths wrong: %d deltas", len(d))
	}
}

func TestComplete_StreamLineTooLong(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream([]string{strings.Repeat("x", 2<<20)})
	_, _, err := completeLogged(streamClient(t, stub.URL()))
	if err == nil || !strings.Contains(err.Error(), "reading stream") {
		t.Fatalf("err = %v, want reading stream error", err)
	}
}

// sseServer answers every request with status, Content-Type ct and body.
func sseServer(t *testing.T, status int, ct, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestComplete_StreamContentTypeCharset(t *testing.T) {
	srv := sseServer(t, 200, "text/event-stream; charset=utf-8",
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	got, err := streamClient(t, srv.URL).Complete(context.Background(), "s", "u")
	if err != nil || got != "hi" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestComplete_StreamNon2xxKeepsErrorPath(t *testing.T) {
	srv := sseServer(t, 500, "text/event-stream",
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
	got, log, err := completeLogged(streamClient(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "returned") {
		t.Fatalf("err = %v, want non-2xx error", err)
	}
	if got != "" || len(log.texts(llm.PhaseDelta)) != 0 {
		t.Errorf("non-2xx SSE was parsed: %q", got)
	}
}

func TestComplete_StreamReturnsAtDoneWithoutWaiting(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStreamRaw([]string{`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n", "data: [DONE]\n", ": held open"})
	stub.SetDelay(0, 300*time.Millisecond)
	start := time.Now()
	got, err := streamClient(t, stub.URL()).Complete(context.Background(), "s", "u")
	if err != nil || got != "x" {
		t.Fatalf("got %q, %v", got, err)
	}
	if d := time.Since(start); d >= 500*time.Millisecond {
		t.Errorf("Complete took %v, want it to return at [DONE]", d)
	}
}

func TestComplete_StreamRequestBodyKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  any
		want bool
	}{{"default", nil, true}, {"false", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer srv.Close()
			t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
			sec := map[string]any{"base_url": srv.URL, "model": "m"}
			if tc.cfg != nil {
				sec["stream"] = tc.cfg
			}
			c, err := llm.NewClient(config.Config{"llm": sec})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if tc.want && !strings.Contains(body, `"stream":true`) {
				t.Errorf("body %s lacks \"stream\":true", body)
			}
			if !tc.want && strings.Contains(body, `"stream"`) {
				t.Errorf("body %s carries a stream key", body)
			}
		})
	}
}

func TestComplete_StreamReasoningPrecedence(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStreamRaw(append(sseData(`{"choices":[{"delta":{"reasoning_content":"RC","reasoning":"R"}}]}`), "data: [DONE]", ""))
	_, log, err := completeLogged(streamClient(t, stub.URL()))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, want := log.texts(llm.PhaseThinking), []string{"RC"}; !reflect.DeepEqual(got, want) {
		t.Errorf("thinking = %v, want %v", got, want)
	}
}
