package llm

import (
	"context"
	"net/http/httptrace"
	"sync"
)

// Phase names one point in the life of a Complete call.
type Phase int

// The Phase values, in the order a successful call reports them.
const (
	PhaseStart       Phase = iota // Complete entered, before dialing
	PhaseConnected                // httptrace GotConn
	PhaseRequestSent              // httptrace WroteRequest with a nil error
	PhaseFirstByte                // httptrace GotFirstResponseByte
	PhaseThinking                 // one streamed reasoning chunk (SSE only)
	PhaseDelta                    // one streamed content chunk (SSE only)
	PhaseDone                     // reply fully read and parsed
)

// String returns the phase's short lowercase name.
func (p Phase) String() string {
	switch p {
	case PhaseStart:
		return "start"
	case PhaseConnected:
		return "connected"
	case PhaseRequestSent:
		return "request-sent"
	case PhaseFirstByte:
		return "first-byte"
	case PhaseThinking:
		return "thinking"
	case PhaseDelta:
		return "delta"
	case PhaseDone:
		return "done"
	default:
		return "unknown"
	}
}

// Event is one call-lifecycle notification handed to an Observer.
type Event struct {
	Phase Phase
	Text  string // the chunk, for PhaseThinking and PhaseDelta; "" otherwise
}

// Observer receives the lifecycle events of a Complete call. Contract:
// PhaseStart comes first; PhaseDone comes last and only on success (an
// error ends the call with no PhaseDone); PhaseFirstByte comes before any
// PhaseThinking or PhaseDelta; PhaseConnected may repeat. Calls to one
// Observer for one Complete never overlap, but an Observer shared across
// concurrent Complete calls must do its own locking.
type Observer func(Event)

type observerKey struct{}

// WithObserver returns a context carrying o, for Complete to report its
// phases to. A nil o returns ctx unchanged.
func WithObserver(ctx context.Context, o Observer) context.Context {
	if o == nil {
		return ctx
	}
	return context.WithValue(ctx, observerKey{}, o)
}

func observerFrom(ctx context.Context) Observer {
	o, _ := ctx.Value(observerKey{}).(Observer)
	return o
}

// serialize wraps o so its calls never overlap: httptrace callbacks can
// run on transport goroutines.
func serialize(o Observer) Observer {
	var mu sync.Mutex
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		o(e)
	}
}

// traceFor returns an httptrace.ClientTrace that reports the connection
// phases to emit.
func traceFor(emit Observer) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { emit(Event{Phase: PhaseConnected}) },
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				emit(Event{Phase: PhaseRequestSent})
			}
		},
		GotFirstResponseByte: func() { emit(Event{Phase: PhaseFirstByte}) },
	}
}
