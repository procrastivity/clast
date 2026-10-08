package llm_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

func timeoutClient(t *testing.T, stub *llmtest.Server, firstByte, idle, total time.Duration) *llm.Client {
	t.Helper()
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	c, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	llm.SetTimeouts(c, firstByte, idle, total)
	return c
}

func chunks(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "x"
	}
	return out
}

func TestTimeout_LongStreamNotCut(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream(chunks(5))
	stub.SetDelay(0, 40*time.Millisecond)
	c := timeoutClient(t, stub, 100*time.Millisecond, 100*time.Millisecond, 5*time.Second)

	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "xxxxx" {
		t.Fatalf("content = %q, want %q", got, "xxxxx")
	}
}

func TestTimeout_FirstByteStream(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream(chunks(3))
	stub.SetDelay(500*time.Millisecond, 0)
	c := timeoutClient(t, stub, 100*time.Millisecond, time.Second, 5*time.Second)

	start := time.Now()
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "calling") || !strings.Contains(err.Error(), "timeout awaiting response headers") {
		t.Fatalf("err = %v, want first-byte transport error", err)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("took %v, want under 400ms", d)
	}
}

func TestTimeout_FirstByteJSON(t *testing.T) {
	stub := llmtest.New(t, "ok")
	stub.SetDelay(500*time.Millisecond, 0)
	c := timeoutClient(t, stub, 100*time.Millisecond, time.Second, 5*time.Second)

	start := time.Now()
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "timeout awaiting response headers") {
		t.Fatalf("err = %v, want first-byte transport error", err)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("took %v, want under 400ms", d)
	}
}

func TestTimeout_IdleStall(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream(chunks(3))
	stub.SetDelay(0, 500*time.Millisecond)
	c := timeoutClient(t, stub, time.Second, 100*time.Millisecond, 5*time.Second)

	var rec phaseRecorder
	start := time.Now()
	_, err := c.Complete(rec.ctx(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "stalled: no data for 100ms") {
		t.Fatalf("err = %v, want stalled error", err)
	}
	rec.requireNoDone(t)
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("took %v, want under 400ms", d)
	}
}

func TestTimeout_TotalCap(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream(chunks(40))
	stub.SetDelay(0, 20*time.Millisecond)
	c := timeoutClient(t, stub, time.Second, 500*time.Millisecond, 150*time.Millisecond)

	var rec phaseRecorder
	_, err := c.Complete(rec.ctx(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "exceeded 150ms") {
		t.Fatalf("err = %v, want exceeded error", err)
	}
	rec.requireNoDone(t)
	if strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err = %v, must not read as stalled", err)
	}
}

func TestTimeout_ParentCancelPassesThrough(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.SetStream(chunks(40))
	stub.SetDelay(0, 20*time.Millisecond)
	c := timeoutClient(t, stub, time.Second, time.Second, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(80*time.Millisecond, cancel)
	_, err := c.Complete(ctx, "s", "u")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "stalled") || strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, parent cancel must not read as a limit", err)
	}
}

func TestTimeout_SuccessLeavesNoTimerOrGoroutine(t *testing.T) {
	stub := llmtest.New(t, "ok")
	stub.SetStream(chunks(3))
	c := timeoutClient(t, stub, time.Second, 50*time.Millisecond, 5*time.Second)

	if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Outlive the idle limit: a leaked timer would fire here, harmlessly,
	// but the goroutine count must settle back.
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines %d -> %d, leak suspected", before, after)
	}
}

// phaseRecorder collects observed phases for a Complete call.
type phaseRecorder struct {
	mu sync.Mutex
	ps []llm.Phase
}

func (r *phaseRecorder) ctx() context.Context {
	return llm.WithObserver(context.Background(), func(e llm.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ps = append(r.ps, e.Phase)
	})
}

func (r *phaseRecorder) requireNoDone(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if contains(r.ps, llm.PhaseDone) {
		t.Fatalf("phases %v include PhaseDone on a failed call", r.ps)
	}
}

// jsonClient serves handler on a local httptest server and returns a
// Client pointed at it with the given limits.
func jsonClient(t *testing.T, handler http.HandlerFunc, firstByte, idle, total time.Duration) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	c, err := llm.NewClient(cfgWith(srv.URL, "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	llm.SetTimeouts(c, firstByte, idle, total)
	return c
}

func TestTimeout_JSONIdleStall(t *testing.T) {
	c := jsonClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, time.Second, 100*time.Millisecond, 5*time.Second)

	var rec phaseRecorder
	_, err := c.Complete(rec.ctx(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "stalled: no data for 100ms") {
		t.Fatalf("err = %v, want stalled error", err)
	}
	rec.requireNoDone(t)
}

func TestTimeout_JSONTotalCap(t *testing.T) {
	c := jsonClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"choices":[{"message":{"role":"assistant","content":"` + strings.Repeat("a", 200) + `"}}]}`
		for i := 0; i < len(body); i++ {
			if _, err := w.Write([]byte{body[i]}); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}, time.Second, 500*time.Millisecond, 150*time.Millisecond)

	var rec phaseRecorder
	_, err := c.Complete(rec.ctx(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "exceeded 150ms") {
		t.Fatalf("err = %v, want exceeded error", err)
	}
	rec.requireNoDone(t)
}

func TestTimeout_JSONTrickleNotCut(t *testing.T) {
	const body = `{"choices":[{"message":{"role":"assistant","content":"trickled"}}]}`
	c := jsonClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for i := 0; i < len(body); i++ {
			if _, err := w.Write([]byte{body[i]}); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}, time.Second, 100*time.Millisecond, 5*time.Second)

	// The body outlasts the idle limit several times over; only a read
	// that goes through the idle reader keeps resetting the timer.
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "trickled" {
		t.Fatalf("content = %q, want %q", got, "trickled")
	}
}

func TestTimeout_MalformedAfterIdleNotMislabeled(t *testing.T) {
	c := jsonClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// One write: the delta and the malformed event are both buffered
		// before the observer below stalls the parser.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {not json\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, time.Second, 50*time.Millisecond, 5*time.Second)

	// Observer time counts toward IdleTimeout: no body Read happens while
	// the observer runs, so the idle timer fires here. The malformed
	// event that follows must still surface as itself, not as "stalled".
	ctx := llm.WithObserver(context.Background(), func(e llm.Event) {
		if e.Phase == llm.PhaseDelta {
			time.Sleep(200 * time.Millisecond)
		}
	})
	_, err := c.Complete(ctx, "s", "u")
	if err == nil || !strings.Contains(err.Error(), "returned malformed stream event") {
		t.Fatalf("err = %v, want malformed stream event error", err)
	}
	if strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err = %v, must not be mislabeled stalled", err)
	}
}
