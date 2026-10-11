package source

import "context"

// PhaseFunc receives one short phrase describing the work a source is
// doing right now — a search window, a pending-lane lookup, an export.
// Whoever renders it (the capture verb's progress line) stays unknown
// to the source, mirroring how llm.Observer lives in the producing
// package while internal/progress only supplies the sink.
type PhaseFunc func(phase string)

// phaseKey is the context key WithPhaseFunc installs under — unexported
// so the only way in or out is the pair below.
type phaseKey struct{}

// WithPhaseFunc returns a context carrying report — the seam a caller
// installs so a source can disclose in-flight work while a duty runs.
// A nil report returns ctx unchanged.
func WithPhaseFunc(ctx context.Context, report PhaseFunc) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, phaseKey{}, report)
}

// ReportPhase announces what the source is doing, at the granularity of
// one transport call. It exists for sources whose duties can mean
// minutes of subprocess work (amp's windowed enumeration and per-thread
// exports): a caller that renders the phase gives a stalled run a
// visible distinction from a slow one. Sources whose work is local file
// reads measured in milliseconds have nothing worth reporting and never
// call it.
func ReportPhase(ctx context.Context, phase string) {
	if f, ok := ctx.Value(phaseKey{}).(PhaseFunc); ok {
		f(phase)
	}
}
