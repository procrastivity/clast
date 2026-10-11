package source

import (
	"context"
	"testing"
)

func TestReportPhaseRoundTrip(t *testing.T) {
	var got []string
	ctx := WithPhaseFunc(context.Background(), func(phase string) {
		got = append(got, phase)
	})
	ReportPhase(ctx, "search after:2025-01-01 before:2025-01-08")
	ReportPhase(ctx, "export T-1")
	if len(got) != 2 || got[0] != "search after:2025-01-01 before:2025-01-08" || got[1] != "export T-1" {
		t.Fatalf("phases did not round-trip: %v", got)
	}
}

func TestReportPhaseWithoutHookIsQuiet(t *testing.T) {
	// No hook installed, and an explicitly nil one — both must no-op.
	ReportPhase(context.Background(), "anything")
	ctx := WithPhaseFunc(context.Background(), nil)
	if ctx.Value(phaseKey{}) != nil {
		t.Error("WithPhaseFunc(nil) stored a hook anyway")
	}
	ReportPhase(ctx, "anything")
}
