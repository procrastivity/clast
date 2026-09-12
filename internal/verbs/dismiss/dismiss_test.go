package dismiss

import (
	"errors"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
)

var fixedNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error = %v (%T), want a *clasterr.Error", err, err)
	}
	if cerr.Code != code {
		t.Errorf("code = %q, want %q", cerr.Code, code)
	}
}

// TestRun_FromCaptured_Dismisses drives dismiss over a plain captured
// session: curation.json lands dismissed with the given reason.
func TestRun_FromCaptured_Dismisses(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	root := journaltest.New(t).
		Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	result, err := Run(root, "claude-8f3a", "not useful", fixedNow, "framework")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Redismiss {
		t.Error("Redismiss = true, want false (first dismissal)")
	}

	curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.State != journal.StateDismissed {
		t.Errorf("State = %q, want %q", curation.State, journal.StateDismissed)
	}
	if curation.Reason == nil || *curation.Reason != "not useful" {
		t.Errorf("Reason = %v, want \"not useful\"", curation.Reason)
	}
}

// TestRun_Redismiss_ReplacesReason drives dismiss twice over the same
// session with different reasons: the second call succeeds and the stored
// reason is the new one, not the old (settled by planning finding).
func TestRun_Redismiss_ReplacesReason(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	root := journaltest.New(t).
		Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"laptop", "first reason").
		Root()

	result, err := Run(root, "claude-8f3a", "second reason", fixedNow, "framework")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Redismiss {
		t.Error("Redismiss = false, want true")
	}

	curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.Reason == nil || *curation.Reason != "second reason" {
		t.Errorf("Reason = %v, want \"second reason\"", curation.Reason)
	}
}

// TestRun_RefusesCurated asserts a curated session cannot be dismissed:
// validation.curated, and the curation document is left untouched.
func TestRun_RefusesCurated(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	transcript := journal.TranscriptFingerprint{Lines: 5, SHA256: "x"}
	root := journaltest.New(t).
		Curated("2026-09-11", key, transcript,
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"laptop", "a curated title").
		Root()

	_, err := Run(root, "claude-8f3a", "not useful", fixedNow, "framework")
	assertCode(t, err, "validation.curated")

	curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.State != journal.StateCurated {
		t.Errorf("State = %q after a refused dismiss, want unchanged %q", curation.State, journal.StateCurated)
	}
}

// TestRun_RefusesReservedReason asserts a caller cannot dismiss with the
// reserved auto:no-op reason, regardless of the session's current state.
func TestRun_RefusesReservedReason(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	root := journaltest.New(t).
		Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	_, err := Run(root, "claude-8f3a", "auto:no-op", fixedNow, "framework")
	assertCode(t, err, "validation.reserved-reason")

	if _, ok, err := journal.ReadCuration(root, "2026-09-11", key); err != nil || ok {
		t.Errorf("ReadCuration after a refused dismiss: ok=%v err=%v, want ok=false (still captured)", ok, err)
	}
}

// TestRun_UnknownLocator_NotFoundSession asserts V4's not-found.session
// code surfaces through dismiss for a locator matching no session.
func TestRun_UnknownLocator_NotFoundSession(t *testing.T) {
	root := journaltest.New(t).
		Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
			journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	_, err := Run(root, "claude-nope", "manual", fixedNow, "framework")
	assertCode(t, err, "not-found.session")
}
