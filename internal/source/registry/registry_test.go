package registry

import (
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
)

func TestTableCarriesClaude(t *testing.T) {
	if len(Names) != 1 || Names[0] != "claude" {
		t.Fatalf("Names = %v, want [claude]", Names)
	}
	s, ok := Lookup("claude")
	if !ok || s.Name() != "claude" {
		t.Fatalf("Lookup(claude) = %v, %v", s, ok)
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
