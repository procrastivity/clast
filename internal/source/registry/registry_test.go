package registry

import (
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/source"
)

func TestTableCarriesImplementedSources(t *testing.T) {
	if len(Names) != 2 || Names[0] != "claude" || Names[1] != "amp" {
		t.Fatalf("Names = %v, want [claude amp]", Names)
	}
	s, ok := Lookup("claude")
	if !ok || s.Name() != "claude" {
		t.Fatalf("Lookup(claude) = %v, %v", s, ok)
	}
	a, ok := Lookup("amp")
	if !ok || a.Name() != "amp" {
		t.Fatalf("Lookup(amp) = %v, %v", a, ok)
	}
	if a.Model() != source.Network {
		t.Errorf("amp Model() = %q, want network — the sweep gate rides it", a.Model())
	}
	if _, ok := Lookup("devin"); ok {
		t.Fatal("Lookup(devin) should report false until the source lands")
	}
}

func TestLookupTranscriptRenderer_KnownFormat(t *testing.T) {
	r, ok := LookupTranscriptRenderer("claude-jsonl")
	if !ok || r == nil {
		t.Fatalf("LookupTranscriptRenderer(claude-jsonl) = %v, %v, want a renderer", r, ok)
	}
}

func TestLookupTranscriptRenderer_UnknownFormat(t *testing.T) {
	_, ok := LookupTranscriptRenderer("nonexistent-format")
	if ok {
		t.Fatal("LookupTranscriptRenderer(nonexistent-format) = true, want false")
	}
}

func TestValidateTranscriptFormat_KnownFormatPasses(t *testing.T) {
	r, err := ValidateTranscriptFormat("claude-jsonl")
	if err != nil || r == nil {
		t.Fatalf("ValidateTranscriptFormat(claude-jsonl) = %v, %v, want a renderer and nil error", r, err)
	}
}

func TestValidateTranscriptFormat_UnknownFormatRefused(t *testing.T) {
	_, err := ValidateTranscriptFormat("nonexistent-format")
	if err == nil {
		t.Fatal("ValidateTranscriptFormat: want an error for an unrendered format")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "validation.unknown-transcript-format" {
		t.Errorf("error code = %q, want validation.unknown-transcript-format", ce.Code)
	}
}

func TestLookupTranscriptReader(t *testing.T) {
	if _, ok := LookupTranscriptReader("claude-jsonl"); !ok {
		t.Error("claude-jsonl reader not found")
	}
	if _, ok := LookupTranscriptReader("nope"); ok {
		t.Error("unknown format found a reader")
	}
}

// TestAllRowsImplementPresence pins the shared capture contract's
// expectation: every registered source implements the OPTIONAL
// source.Presence probe, so capture's explicit --harness path can tell
// absent storage from present-but-empty. All is a static table — a
// missing probe is a test-visible gap, not a runtime leak.
func TestAllRowsImplementPresence(t *testing.T) {
	for _, s := range All {
		if _, ok := s.(source.Presence); !ok {
			t.Errorf("source %q does not implement source.Presence", s.Name())
		}
	}
}
