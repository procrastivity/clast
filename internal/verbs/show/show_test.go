package show_test

import (
	"os"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/verbs/show"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

func TestRun_CapturedSessionHasNoEntry(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Entry != nil {
		t.Errorf("Entry = %+v, want nil for a captured session", res.Entry)
	}
	if res.Item.State() != journal.StateCaptured {
		t.Errorf("State = %q, want captured", res.Item.State())
	}
}

func TestRun_CuratedSessionLoadsEntry(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	fx.Curated("2026-09-11", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
		mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		"framework", "a title",
	)

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Entry == nil {
		t.Fatal("Entry = nil, want the curated entry")
	}
	if res.Entry.Title != "a title" {
		t.Errorf("Entry.Title = %q, want %q", res.Entry.Title, "a title")
	}
}

func TestRun_PrefixLocatorResolves(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "unique-abc123"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)

	res, err := show.Run(fx.Root(), "claude-unique")
	if err != nil {
		t.Fatalf("Run(prefix): %v", err)
	}
	if res.Item.Key != key {
		t.Errorf("resolved key = %v, want %v", res.Item.Key, key)
	}
}

func TestRun_UnknownLocatorNotFound(t *testing.T) {
	fx := journaltest.New(t)
	_, err := show.Run(fx.Root(), "claude-nonexistent")
	if err == nil {
		t.Fatal("Run: want an error for an unknown locator")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "not-found.session" {
		t.Errorf("error code = %q, want not-found.session", ce.Code)
	}
}

func TestRun_AmbiguousLocatorRefused(t *testing.T) {
	fx := journaltest.New(t)
	a := journal.SessionKey{Harness: "claude", NativeID: "abc1"}
	b := journal.SessionKey{Harness: "claude", NativeID: "abc2"}
	fx.Captured("2026-09-10", a, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, mustParseTime(t, "2026-09-10T09:00:00-05:00"))
	fx.Captured("2026-09-10", b, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "b"}, mustParseTime(t, "2026-09-10T09:00:00-05:00"))

	_, err := show.Run(fx.Root(), "claude-abc")
	if err == nil {
		t.Fatal("Run: want an error for an ambiguous locator")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "validation.ambiguous-locator" {
		t.Errorf("error code = %q, want validation.ambiguous-locator", ce.Code)
	}
}

func TestTranscript_RendersThroughFormatRenderer(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithTranscript("2026-09-10", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello"}}`+"\n"))

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	turns, err := show.Transcript(fx.Root(), res.Item, 0)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "hello" {
		t.Errorf("turns = %+v, want one turn with text %q", turns, "hello")
	}
}

func TestTranscript_UnknownFormatRefused(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "codex", NativeID: "curated-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "nonexistent-format", Lines: 1, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithTranscript("2026-09-10", key, []byte("whatever"))

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, err = show.Transcript(fx.Root(), res.Item, 0)
	if err == nil {
		t.Fatal("Transcript: want an error for a format with no renderer")
	}
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != "validation.unknown-transcript-format" {
		t.Errorf("error code = %q, want validation.unknown-transcript-format", ce.Code)
	}
}

func TestTranscript_MaxCharsCapsEachTurn(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	).WithTranscript("2026-09-10", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello there world"}}`+"\n"))

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	turns, err := show.Transcript(fx.Root(), res.Item, 5)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "hello" {
		t.Errorf("turns = %+v, want capped to 5 runes", turns)
	}
}

func TestTranscript_MissingTranscriptFileIsAnError(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "no-transcript-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)
	// No WithTranscript call: the copy was never written.
	if _, err := os.Stat(journal.TranscriptPath(fx.Root(), "2026-09-10", key)); err == nil {
		t.Fatal("fixture unexpectedly has a transcript file")
	}

	res, err := show.Run(fx.Root(), key.DirName())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, err = show.Transcript(fx.Root(), res.Item, 0)
	if err == nil {
		t.Fatal("Transcript: want an error for a missing transcript file")
	}
}
