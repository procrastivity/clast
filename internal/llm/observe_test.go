package llm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

// recorder collects the phases an Observer sees.
type recorder struct {
	mu     sync.Mutex
	phases []llm.Phase
}

func (r *recorder) observe(e llm.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, e.Phase)
}

func (r *recorder) seen() []llm.Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]llm.Phase(nil), r.phases...)
}

func contains(ps []llm.Phase, want llm.Phase) bool {
	for _, p := range ps {
		if p == want {
			return true
		}
	}
	return false
}

func TestPhase_String(t *testing.T) {
	want := map[llm.Phase]string{
		llm.PhaseStart:       "start",
		llm.PhaseConnected:   "connected",
		llm.PhaseRequestSent: "request-sent",
		llm.PhaseFirstByte:   "first-byte",
		llm.PhaseThinking:    "thinking",
		llm.PhaseDelta:       "delta",
		llm.PhaseDone:        "done",
	}
	for p, s := range want {
		if got := p.String(); got != s {
			t.Errorf("Phase(%d).String() = %q, want %q", int(p), got, s)
		}
	}
}

func TestComplete_ObserverPhases(t *testing.T) {
	stub := llmtest.New(t, "ok")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var rec recorder
	if _, err := client.Complete(llm.WithObserver(context.Background(), rec.observe), "sys", "usr"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := rec.seen()
	if len(got) < 5 {
		t.Fatalf("phases = %v, want at least 5", got)
	}
	if got[0] != llm.PhaseStart {
		t.Errorf("first phase = %v, want start", got[0])
	}
	if got[len(got)-1] != llm.PhaseDone {
		t.Errorf("last phase = %v, want done", got[len(got)-1])
	}
	for _, p := range []llm.Phase{llm.PhaseConnected, llm.PhaseRequestSent, llm.PhaseFirstByte} {
		if !contains(got[1:len(got)-1], p) {
			t.Errorf("phases %v missing %v between start and done", got, p)
		}
	}
}

func TestComplete_ObserverNoDoneOnError(t *testing.T) {
	stub := llmtest.New(t, "ok")
	stub.Fail(500, "boom")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var rec recorder
	if _, err := client.Complete(llm.WithObserver(context.Background(), rec.observe), "sys", "usr"); err == nil {
		t.Fatal("Complete: want an error on a 500")
	}
	got := rec.seen()
	if len(got) == 0 || got[0] != llm.PhaseStart {
		t.Errorf("phases = %v, want start first", got)
	}
	if contains(got, llm.PhaseDone) {
		t.Errorf("phases = %v, want no done after an error", got)
	}
}

func TestComplete_ObserverCancelNoDone(t *testing.T) {
	stub := llmtest.New(t, "ok")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var rec recorder
	obs := func(e llm.Event) {
		rec.observe(e)
		if e.Phase == llm.PhaseConnected {
			cancel()
		}
	}
	if _, err := client.Complete(llm.WithObserver(ctx, obs), "sys", "usr"); err == nil {
		t.Fatal("Complete: want an error after cancel")
	}
	if contains(rec.seen(), llm.PhaseDone) {
		t.Errorf("phases = %v, want no done after cancel", rec.seen())
	}
}

func TestComplete_ObserverConcurrent(t *testing.T) {
	stub := llmtest.New(t, "ok")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const n = 8
	recs := make([]recorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := client.Complete(llm.WithObserver(ctx, recs[i].observe), "sys", "usr"); err != nil {
				t.Errorf("Complete %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	for i := range recs {
		got := recs[i].seen()
		if len(got) == 0 || got[0] != llm.PhaseStart || got[len(got)-1] != llm.PhaseDone {
			t.Errorf("call %d phases = %v, want start first and done last", i, got)
		}
	}
}
