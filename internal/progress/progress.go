// Package progress draws one status line on stderr while a verb waits on the
// LLM. The line shows a label, the call phase, and the elapsed time. It is
// off unless stderr is a terminal, so piped output and e2e runs never see it.
package progress

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/llm"
)

const (
	tickInterval = 100 * time.Millisecond
	eraseLine    = "\r\x1b[K"
	defaultWidth = 80
	sep          = " · "
)

var frames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type contextKey struct{}

// Reporter draws and clears one status line. The zero value is not usable;
// build one with New. A nil *Reporter is valid and every method on it does
// nothing, so callers never need to check for "progress off".
type Reporter struct {
	w    io.Writer
	tick time.Duration
	now  func() time.Time

	mu      sync.Mutex
	label   string
	phase   string
	chars   int       // runes of Delta text since the last Status
	start   time.Time // first Status after the last Clear
	active  bool      // a label is set and the line is being drawn
	drawn   bool      // a line is on screen
	frame   int
	stopped bool
	started bool // the draw goroutine has been launched

	quit chan struct{}
	done chan struct{} // closed when the draw goroutine returns
}

// New returns a Reporter that draws on streams.Err, or nil when progress
// must stay off: --json is set, TERM is dumb, or stderr is not a terminal.
func New(streams *iostreams.Streams, flags cliflags.Flags) *Reporter {
	if flags.JSON || os.Getenv("TERM") == "dumb" || streams == nil {
		return nil
	}
	f, ok := streams.Err.(*os.File)
	if !ok {
		return nil
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return newReporter(f, tickInterval, time.Now)
}

// newReporter builds an enabled Reporter that draws on w, for tests.
func newReporter(w io.Writer, tick time.Duration, now func() time.Time) *Reporter {
	return &Reporter{
		w:    w,
		tick: tick,
		now:  now,
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// WithReporter returns a context carrying r, for verbs to read via
// FromContext.
func WithReporter(ctx context.Context, r *Reporter) context.Context {
	return context.WithValue(ctx, contextKey{}, r)
}

// FromContext returns the Reporter stored by WithReporter, or nil if none
// was stored.
func FromContext(ctx context.Context) *Reporter {
	r, _ := ctx.Value(contextKey{}).(*Reporter)
	return r
}

// Status sets the label and starts drawing if the reporter is idle. A new
// call keeps the elapsed time and resets the phase and the character count.
func (r *Reporter) Status(label string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.label = label
	r.phase = ""
	r.chars = 0
	if !r.active {
		r.active = true
		r.start = r.now()
	}
	if !r.started {
		r.started = true
		go r.loop()
	}
	r.draw()
}

// Observer returns an llm.Observer that updates the phase text. It is safe
// to call from several goroutines.
func (r *Reporter) Observer() llm.Observer {
	if r == nil {
		return func(llm.Event) {}
	}
	return func(e llm.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch e.Phase {
		case llm.PhaseStart:
			r.phase = "connecting"
		case llm.PhaseConnected:
			r.phase = "connected"
		case llm.PhaseRequestSent:
			r.phase = "waiting for the model"
		case llm.PhaseFirstByte:
			r.phase = "receiving"
		case llm.PhaseThinking:
			r.phase = "thinking"
		case llm.PhaseDelta:
			r.chars += utf8.RuneCountInString(e.Text)
			r.phase = "receiving · " + strconv.Itoa(r.chars) + " chars"
		case llm.PhaseDone:
			r.phase = "done"
		}
	}
}

// Clear erases the line, stops drawing, and resets the elapsed time. The
// next Status starts a fresh count.
func (r *Reporter) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clear()
}

// Stop clears the line and ends the draw goroutine. It is idempotent, and a
// Status after Stop does nothing.
func (r *Reporter) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.clear()
	wasStopped := r.stopped
	r.stopped = true
	started := r.started
	r.mu.Unlock()
	if !wasStopped {
		close(r.quit)
	}
	if started {
		<-r.done
	}
}

// clear erases the line when one is on screen. The caller holds r.mu.
func (r *Reporter) clear() {
	if r.drawn {
		_, _ = io.WriteString(r.w, eraseLine)
		r.drawn = false
	}
	r.active = false
	r.label = ""
	r.phase = ""
	r.chars = 0
}

// loop redraws the line on every tick until Stop.
func (r *Reporter) loop() {
	defer close(r.done)
	t := time.NewTicker(r.tick)
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-t.C:
			r.mu.Lock()
			r.frame++
			r.draw()
			r.mu.Unlock()
		}
	}
}

// draw writes the current line when a label is active. The caller holds r.mu.
func (r *Reporter) draw() {
	if !r.active || r.stopped {
		return
	}
	line := string(frames[r.frame%len(frames)]) + " " + r.label
	if r.phase != "" {
		line += sep + r.phase
	}
	line += fmt.Sprintf("%s%ds", sep, int(r.now().Sub(r.start).Seconds()))
	_, _ = io.WriteString(r.w, eraseLine+truncate(line, width()-1))
	r.drawn = true
}

// width returns the terminal width from COLUMNS, or 80 when it is unset or
// invalid.
func width() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 1 {
		return n
	}
	return defaultWidth
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
