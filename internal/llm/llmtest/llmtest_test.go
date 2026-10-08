package llmtest_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/llm/llmtest"
)

func post(t *testing.T, c *http.Client, url string, stream bool) (*http.Response, string, error) {
	t.Helper()
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	if stream {
		body = `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	}
	resp, err := c.Post(url+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

func TestSetStreamChunksAndDone(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream([]string{"a", `b"c`})
	resp, body, err := post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	eq(t, "text/event-stream", resp.Header.Get("Content-Type"))
	eq(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"b\\\"c\"}}]}\n\n"+
			"data: [DONE]\n\n", body)
}

func TestSetStreamNonStreamRequestGetsJoinedJSON(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream([]string{"a", "b"})
	resp, body, err := post(t, http.DefaultClient, s.URL(), false)
	noErr(t, err)
	eq(t, "application/json", resp.Header.Get("Content-Type"))
	if !strings.Contains(body, `"content":"ab"`) {
		t.Fatalf("%q lacks %q", body, `"content":"ab"`)
	}
}

func TestSetStreamNilSendsOnlyDone(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream(nil)
	_, body, err := post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	eq(t, "data: [DONE]\n\n", body)
}

func TestStreamRequestedButNotConfiguredGetsJSON(t *testing.T) {
	s := llmtest.New(t, "canned")
	resp, body, err := post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	eq(t, "application/json", resp.Header.Get("Content-Type"))
	if !strings.Contains(body, `"content":"canned"`) {
		t.Fatalf("%q lacks %q", body, `"content":"canned"`)
	}
	if !(s.Requests()[0].Stream) {
		t.Fatal("want true: s.Requests()[0].Stream")
	}
}

func TestSetStreamRawVerbatim(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStreamRaw([]string{": keepalive", "", "data: {\"x\":1}"})
	resp, body, err := post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	eq(t, "text/event-stream", resp.Header.Get("Content-Type"))
	eq(t, ": keepalive\n\ndata: {\"x\":1}\n", body)
}

func TestRequestsRecordsStream(t *testing.T) {
	s := llmtest.New(t, "canned")
	_, _, err := post(t, http.DefaultClient, s.URL(), false)
	noErr(t, err)
	_, _, err = post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	reqs := s.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests", len(reqs))
	}
	if reqs[0].Stream {
		t.Fatal("want false: reqs[0].Stream")
	}
	if !(reqs[1].Stream) {
		t.Fatal("want true: reqs[1].Stream")
	}
}

func TestOverridesClearStreamMode(t *testing.T) {
	for name, clear := range map[string]func(*llmtest.Server){
		"SetResponse": func(s *llmtest.Server) { s.SetResponse("plain") },
		"Fail":        func(s *llmtest.Server) { s.Fail(500, "boom") },
		"RespondRaw":  func(s *llmtest.Server) { s.RespondRaw(200, "raw") },
	} {
		t.Run(name, func(t *testing.T) {
			s := llmtest.New(t, "canned")
			s.SetStream([]string{"a"})
			clear(s)
			resp, _, err := post(t, http.DefaultClient, s.URL(), true)
			noErr(t, err)
			if resp.Header.Get("Content-Type") == "text/event-stream" {
				t.Fatal("stream mode still active")
			}

			s.SetStreamRaw([]string{"x"})
			clear(s)
			resp, _, err = post(t, http.DefaultClient, s.URL(), true)
			noErr(t, err)
			if resp.Header.Get("Content-Type") == "text/event-stream" {
				t.Fatal("stream mode still active")
			}
		})
	}
}

func TestDelayBeforeFirstByteHonorsClientTimeout(t *testing.T) {
	t0 := time.Now()
	// Registered before New so it runs after the server closes (LIFO):
	// a handler that ignores the request context would stall Close.
	t.Cleanup(func() {
		if d := time.Since(t0); d >= time.Second {
			t.Errorf("test took %v, want < 1s", d)
		}
	})
	s := llmtest.New(t, "canned")
	s.SetDelay(time.Second, 0)
	start := time.Now()
	_, _, err := post(t, &http.Client{Timeout: 50 * time.Millisecond}, s.URL(), true)
	if err == nil {
		t.Fatal("expected error")
	}
	if d := time.Since(start); d >= 500*time.Millisecond {
		t.Fatalf("took %v, want < %v", d, 500*time.Millisecond)
	}
}

func TestDelayBetweenChunks(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream([]string{"a", "b", "c"})
	s.SetDelay(0, 40*time.Millisecond)
	start := time.Now()
	_, body, err := post(t, http.DefaultClient, s.URL(), true)
	noErr(t, err)
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Fatalf("took %v, want >= %v", d, 80*time.Millisecond)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("%q lacks %q", body, "[DONE]")
	}
}

func TestStreamFlushesEachWrite(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream([]string{"a", "b"})
	s.SetDelay(0, 300*time.Millisecond)
	start := time.Now()
	resp, err := http.Post(s.URL()+"/chat/completions", "application/json",
		strings.NewReader(`{"stream":true}`))
	noErr(t, err)
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 8)
	_, err = resp.Body.Read(buf)
	noErr(t, err)
	if d := time.Since(start); d >= 250*time.Millisecond {
		t.Fatalf("took %v, want < %v", d, 250*time.Millisecond)
	}
}

func TestConcurrentStreamsDoNotMixChunks(t *testing.T) {
	s := llmtest.New(t, "canned")
	s.SetStream([]string{"a", "b", "c"})
	s.SetDelay(0, 5*time.Millisecond)
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"c\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var wg sync.WaitGroup
	bodies := make([]string, 2)
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, b, _ := post(t, http.DefaultClient, s.URL(), true)
			bodies[i] = b
		}()
	}
	wg.Wait()
	eq(t, want, bodies[0])
	eq(t, want, bodies[1])
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eq(t *testing.T, want, got string) {
	t.Helper()
	if want != got {
		t.Fatalf("want %q, got %q", want, got)
	}
}
