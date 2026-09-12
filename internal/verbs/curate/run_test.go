package curate

import (
	"os"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
)

var fixedNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func wellFormedEntry(title string) []byte {
	return []byte("---\ntitle: " + title + "\ntags: [a]\n---\n\nbody\n")
}

// TestRun_FromCaptured_Curates drives curate over a plain captured session:
// entry.md lands, curation.json is written curated with the current
// transcript fingerprint stamped, and PriorState reports captured.
func TestRun_FromCaptured_Curates(t *testing.T) {
	root := journaltest.New(t).
		Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
			journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "abc"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	data := wellFormedEntry("a fresh entry")
	result, err := Run(root, "claude-8f3a", data, fixedNow)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PriorState != journal.StateCaptured {
		t.Errorf("PriorState = %q, want %q", result.PriorState, journal.StateCaptured)
	}

	got, err := os.ReadFile(journal.EntryPath(root, "2026-09-11", result.Key))
	if err != nil {
		t.Fatalf("reading entry.md: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("entry.md = %q, want %q", got, data)
	}

	curation, ok, err := journal.ReadCuration(root, "2026-09-11", result.Key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.State != journal.StateCurated {
		t.Errorf("curation.State = %q, want %q", curation.State, journal.StateCurated)
	}
	if curation.Reason != nil {
		t.Errorf("curation.Reason = %v, want nil", curation.Reason)
	}
	if curation.TranscriptAtCuration == nil || curation.TranscriptAtCuration.Lines != 10 || curation.TranscriptAtCuration.SHA256 != "abc" {
		t.Errorf("curation.TranscriptAtCuration = %+v, want {Lines:10 SHA256:abc}", curation.TranscriptAtCuration)
	}
	if !curation.At.Equal(fixedNow) {
		t.Errorf("curation.At = %v, want %v", curation.At, fixedNow)
	}
}

// TestRun_FromCurated_ReCuratesAndClearsStaleness drives curate over an
// already-curated, stale session: the new curation.json's fingerprint is
// refreshed to the CURRENT transcript, so the session reads not-stale
// immediately afterward (M7).
func TestRun_FromCurated_ReCuratesAndClearsStaleness(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	current := journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "new"}
	stale := journal.TranscriptStamp{Lines: 10, SHA256: "old"}
	fx := journaltest.New(t).CuratedStale("2026-09-11", key, current, stale,
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"laptop", "old title")
	root := fx.Root()

	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatalf("Walk (precondition): %v", err)
	}
	before, err := journal.Resolve(items, "claude-8f3a")
	if err != nil {
		t.Fatalf("Resolve (precondition): %v", err)
	}
	if !before.Stale() {
		t.Fatal("precondition: fixture session must be stale")
	}

	result, err := Run(root, "claude-8f3a", wellFormedEntry("refreshed"), fixedNow)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PriorState != journal.StateCurated {
		t.Errorf("PriorState = %q, want %q", result.PriorState, journal.StateCurated)
	}

	items, _, err = journal.Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	after, err := journal.Resolve(items, "claude-8f3a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if after.Stale() {
		t.Error("after re-curate: Stale() = true, want false (fingerprint refreshed to current)")
	}
}

// TestRun_FromDismissed_ReplacesDismissal_NoUndismissCeremony drives curate
// over a dismissed session: the dismissal is replaced outright by a curated
// curation.json in one call — V14's edge, no undismiss step required.
func TestRun_FromDismissed_ReplacesDismissal_NoUndismissCeremony(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	transcript := journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "x"}
	root := journaltest.New(t).
		Dismissed("2026-09-11", key, transcript,
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"laptop", "not useful").
		Root()

	result, err := Run(root, "claude-8f3a", wellFormedEntry("revived"), fixedNow)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PriorState != journal.StateDismissed {
		t.Errorf("PriorState = %q, want %q", result.PriorState, journal.StateDismissed)
	}

	curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.State != journal.StateCurated {
		t.Errorf("curation.State = %q, want %q (dismissal must be fully replaced)", curation.State, journal.StateCurated)
	}
	if curation.Reason != nil {
		t.Errorf("curation.Reason = %v, want nil — a curated document never carries the old dismissal reason", curation.Reason)
	}
}

// TestRun_UnknownLocator_NotFoundSession asserts V4's not-found.session code
// surfaces through curate for a locator matching no session.
func TestRun_UnknownLocator_NotFoundSession(t *testing.T) {
	root := journaltest.New(t).
		Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
			journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	_, err := Run(root, "claude-nope", wellFormedEntry("x"), fixedNow)
	assertCode(t, err, "not-found.session")
}

// TestRun_AmbiguousLocator asserts V4's ambiguous-locator refusal surfaces
// through curate when the locator prefix-matches more than one session.
func TestRun_AmbiguousLocator(t *testing.T) {
	fx := journaltest.New(t)
	fx.Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3aaaa"},
		journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))
	fx.Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3bbbb"},
		journal.TranscriptFingerprint{Lines: 1, SHA256: "y"},
		time.Date(2026, 9, 11, 9, 5, 0, 0, time.UTC))
	root := fx.Root()

	_, err := Run(root, "claude-8f3", wellFormedEntry("x"), fixedNow)
	assertCode(t, err, "validation.ambiguous-locator")
}

// TestRun_InvalidDocument_NeverWritesAnything asserts a document that fails
// V14's validation is refused before any write — the target session's
// prior state (and, when it was curated, its existing entry.md) is left
// completely untouched.
func TestRun_InvalidDocument_NeverWritesAnything(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	transcript := journal.TranscriptFingerprint{Lines: 5, SHA256: "x"}
	root := journaltest.New(t).
		Curated("2026-09-11", key, transcript,
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"laptop", "original title").
		Root()

	before, err := os.ReadFile(journal.EntryPath(root, "2026-09-11", key))
	if err != nil {
		t.Fatalf("reading fixture entry.md: %v", err)
	}

	_, err = Run(root, "claude-8f3a", []byte("not an entry at all"), fixedNow)
	assertCode(t, err, "validation.entry-frontmatter")

	after, err := os.ReadFile(journal.EntryPath(root, "2026-09-11", key))
	if err != nil {
		t.Fatalf("reading entry.md after refused Run: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("entry.md changed after a refused curate: got %q, want unchanged %q", after, before)
	}
}
