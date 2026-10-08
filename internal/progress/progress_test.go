package progress

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/llm"
)

// syncBuf is a bytes.Buffer safe for the draw goroutine and the test.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuf) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTest returns a reporter whose goroutine never ticks on its own, so the
// test drives every redraw through redraw.
func newTest(t *testing.T) (*Reporter, *syncBuf, *fakeClock) {
	t.Helper()
	buf := &syncBuf{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	r := newReporter(buf, time.Hour, clk.Now)
	t.Cleanup(r.Stop)
	return r, buf, clk
}

// redraw forces one tick and returns what it wrote.
func redraw(r *Reporter, buf *syncBuf) string {
	buf.Reset()
	r.mu.Lock()
	r.draw()
	r.mu.Unlock()
	return buf.String()
}

func TestNewDisabled(t *testing.T) {
	t.Setenv("TERM", "xterm")
	var stderr bytes.Buffer
	if r := New(&iostreams.Streams{Err: &stderr}, cliflags.Flags{}); r != nil {
		t.Error("bytes.Buffer stderr: want nil")
	}

	tty, err := os.Open("/dev/null") // a char device, so only the flag can disable it
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer func() { _ = tty.Close() }()
	if r := New(&iostreams.Streams{Err: tty}, cliflags.Flags{JSON: true}); r != nil {
		t.Error("JSON: want nil")
	}
	t.Setenv("TERM", "dumb")
	if r := New(&iostreams.Streams{Err: tty}, cliflags.Flags{}); r != nil {
		t.Error("TERM=dumb: want nil")
	}
}

func TestNewEnabledOnCharDevice(t *testing.T) {
	t.Setenv("TERM", "xterm")
	tty, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer func() { _ = tty.Close() }()
	r := New(&iostreams.Streams{Err: tty}, cliflags.Flags{})
	if r == nil {
		t.Fatal("char device stderr: want a reporter")
	}
	r.Stop()
}

func TestNilReceiver(t *testing.T) {
	var r *Reporter
	r.Status("x")
	r.Observer()(llm.Event{Phase: llm.PhaseStart})
	r.Clear()
	r.Stop()
	if FromContext(context.Background()) != nil {
		t.Error("FromContext on empty ctx: want nil")
	}
	if FromContext(WithReporter(context.Background(), nil)) != nil {
		t.Error("FromContext with nil reporter: want nil")
	}
}

func TestContextRoundTrip(t *testing.T) {
	r, _, _ := newTest(t)
	if got := FromContext(WithReporter(context.Background(), r)); got != r {
		t.Error("FromContext did not return the stored reporter")
	}
}

func TestDrawPhasesAndElapsed(t *testing.T) {
	r, buf, clk := newTest(t)
	r.Status("summarizing 1/3")
	obs := r.Observer()

	cases := []struct {
		e    llm.Event
		want string
	}{
		{llm.Event{Phase: llm.PhaseStart}, "connecting"},
		{llm.Event{Phase: llm.PhaseConnected}, "connected"},
		{llm.Event{Phase: llm.PhaseRequestSent}, "waiting for the model"},
		{llm.Event{Phase: llm.PhaseFirstByte}, "receiving"},
		{llm.Event{Phase: llm.PhaseThinking, Text: "hm"}, "thinking"},
		{llm.Event{Phase: llm.PhaseDelta, Text: "abc"}, "receiving · 3 chars"},
		{llm.Event{Phase: llm.PhaseDone}, "done"},
	}
	for i, c := range cases {
		clk.Advance(2 * time.Second)
		obs(c.e)
		out := redraw(r, buf)
		if !strings.HasPrefix(out, "\r\x1b[K") {
			t.Errorf("%v: redraw %q lacks the erase prefix", c.e.Phase, out)
		}
		wantTail := " summarizing 1/3 · " + c.want + " · " + strconv.Itoa(2*(i+1)) + "s"
		if !strings.HasSuffix(out, wantTail) {
			t.Errorf("%v: got %q, want suffix %q", c.e.Phase, out, wantTail)
		}
	}
}

func TestFrameAdvances(t *testing.T) {
	r, buf, _ := newTest(t)
	r.Status("x")
	a := redraw(r, buf)
	r.mu.Lock()
	r.frame++
	r.mu.Unlock()
	b := redraw(r, buf)
	if a == b || !strings.Contains(a, "⠋") || !strings.Contains(b, "⠙") {
		t.Errorf("frames did not advance: %q then %q", a, b)
	}
}

func TestClearResetsElapsed(t *testing.T) {
	r, buf, clk := newTest(t)
	r.Status("a")
	clk.Advance(5 * time.Second)
	if out := redraw(r, buf); !strings.HasSuffix(out, "5s") {
		t.Fatalf("before Clear: %q", out)
	}
	buf.Reset()
	r.Clear()
	if got := buf.String(); got != "\r\x1b[K" {
		t.Errorf("Clear wrote %q, want the erase sequence", got)
	}
	if out := redraw(r, buf); out != "" {
		t.Errorf("draw after Clear wrote %q, want nothing", out)
	}
	clk.Advance(7 * time.Second)
	buf.Reset()
	r.Status("b")
	if out := buf.String(); !strings.HasSuffix(out, "b · 0s") {
		t.Errorf("Status after Clear: %q, want elapsed 0s", out)
	}
}

func TestClearIdleWritesNothing(t *testing.T) {
	r, buf, _ := newTest(t)
	r.Clear()
	if buf.String() != "" {
		t.Errorf("Clear on idle reporter wrote %q", buf.String())
	}
}

func TestStatusKeepsElapsedResetsPhase(t *testing.T) {
	r, buf, clk := newTest(t)
	r.Status("one")
	r.Observer()(llm.Event{Phase: llm.PhaseDelta, Text: "xyz"})
	clk.Advance(4 * time.Second)
	r.Status("two")
	out := redraw(r, buf)
	if strings.Contains(out, "receiving") || !strings.HasSuffix(out, " two · 4s") {
		t.Errorf("got %q, want phase reset and elapsed 4s", out)
	}
	r.Observer()(llm.Event{Phase: llm.PhaseDelta, Text: "q"})
	if out := redraw(r, buf); !strings.Contains(out, "receiving · 1 chars") {
		t.Errorf("char count not reset by Status: %q", out)
	}
}

func TestDeltaCountsRunes(t *testing.T) {
	r, buf, _ := newTest(t)
	r.Status("x")
	obs := r.Observer()
	obs(llm.Event{Phase: llm.PhaseDelta, Text: "ab"})
	obs(llm.Event{Phase: llm.PhaseDelta, Text: "cdé"})
	if out := redraw(r, buf); !strings.Contains(out, "receiving · 5 chars") {
		t.Errorf("got %q, want 5 chars", out)
	}
}

func TestTruncate(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	r, buf, _ := newTest(t)
	r.Status(strings.Repeat("é", 200))
	out := redraw(r, buf)
	line := strings.TrimPrefix(out, "\r\x1b[K")
	if n := utf8.RuneCountInString(line); n != 39 {
		t.Errorf("line is %d runes, want 39", n)
	}
	if strings.Count(out, "\r") != 1 {
		t.Errorf("want exactly one redraw in %q", out)
	}
}

func TestWidthFallback(t *testing.T) {
	for _, v := range []string{"", "abc", "0", "-5"} {
		t.Setenv("COLUMNS", v)
		if got := width(); got != 80 {
			t.Errorf("COLUMNS=%q: width %d, want 80", v, got)
		}
	}
}

func TestLoopDrawsOnTick(t *testing.T) {
	buf := &syncBuf{}
	clk := &fakeClock{t: time.Unix(0, 0)}
	r := newReporter(buf, time.Millisecond, clk.Now)
	defer r.Stop()
	r.Status("tick")
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(buf.String(), "\r\x1b[K") < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine never redrew: %q", buf.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopEndsGoroutine(t *testing.T) {
	buf := &syncBuf{}
	r := newReporter(buf, time.Millisecond, time.Now)
	r.Status("x")
	select {
	case <-r.done:
		t.Fatal("goroutine ended before Stop")
	default:
	}
	r.Stop()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop left the goroutine running")
	}
	if !strings.HasSuffix(buf.String(), "\r\x1b[K") {
		t.Errorf("Stop did not clear the line: %q", buf.String())
	}
}

func TestStopLeavesNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		r := newReporter(&syncBuf{}, time.Millisecond, time.Now)
		r.Status("x")
		r.Stop()
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before, %d after", before, runtime.NumGoroutine())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStopTwiceAndStatusAfterStop(t *testing.T) {
	buf := &syncBuf{}
	r := newReporter(buf, time.Millisecond, time.Now)
	r.Status("x")
	r.Stop()
	r.Stop() // must not panic on a double close

	n := runtime.NumGoroutine()
	buf.Reset()
	r.Status("again")
	r.Observer()(llm.Event{Phase: llm.PhaseStart})
	r.Clear()
	time.Sleep(20 * time.Millisecond)
	if buf.String() != "" {
		t.Errorf("drew after Stop: %q", buf.String())
	}
	if got := runtime.NumGoroutine(); got > n {
		t.Errorf("Status after Stop started a goroutine: %d -> %d", n, got)
	}
}

func TestStopWithoutStatus(_ *testing.T) {
	r := newReporter(&syncBuf{}, time.Millisecond, time.Now)
	r.Stop() // goroutine never started; must not block
	r.Stop()
}

func TestObserverConcurrent(t *testing.T) {
	r := newReporter(&syncBuf{}, time.Millisecond, time.Now)
	defer r.Stop()
	r.Status("x")
	obs := r.Observer()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				obs(llm.Event{Phase: llm.PhaseDelta, Text: "ab"})
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chars != 1600 {
		t.Errorf("chars = %d, want 1600", r.chars)
	}
}
