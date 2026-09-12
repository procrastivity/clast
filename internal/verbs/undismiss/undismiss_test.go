package undismiss

import (
	"errors"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
)

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

// TestRun_FromDismissed_ReturnsToCaptured drives undismiss over a plainly
// dismissed session: curation.json is removed, and the session reads
// captured afterward.
func TestRun_FromDismissed_ReturnsToCaptured(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	root := journaltest.New(t).
		Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"laptop", "not useful").
		Root()

	result, err := Run(root, "claude-8f3a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Key != key {
		t.Errorf("Key = %+v, want %+v", result.Key, key)
	}

	if _, ok, err := journal.ReadCuration(root, "2026-09-11", key); err != nil || ok {
		t.Errorf("ReadCuration after undismiss: ok=%v err=%v, want ok=false (captured)", ok, err)
	}
}

// TestRun_AcceptsAutoNoOpDismissal asserts undismiss accepts a dismissal
// carrying capture's own reserved auto:no-op reason exactly as it accepts
// any other reason — V15's only precondition is state dismissed, and the
// capture.go comment reserving the reason describes capture's own
// writers, not this verb (matter finding).
//
// Test note: undismissing a still-non-substantive session like this one
// is undone by the next capture sweep (applyAutoDismissal re-applies
// auto:no-op to any non-substantive session with no curation state) —
// that is M3 working as designed, not a defect for this verb to guard
// against.
func TestRun_AcceptsAutoNoOpDismissal(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "noop1"}
	root := journaltest.New(t).
		Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
			"framework", "auto:no-op").
		Root()

	if _, err := Run(root, "claude-noop1"); err != nil {
		t.Fatalf("Run: %v, want undismiss to accept an auto:no-op dismissal", err)
	}
	if _, ok, err := journal.ReadCuration(root, "2026-09-11", key); err != nil || ok {
		t.Errorf("ReadCuration after undismiss: ok=%v err=%v, want ok=false (captured)", ok, err)
	}
}

// TestRun_RefusesNonDismissed asserts a session that isn't dismissed
// refuses with validation.not-dismissed, for both captured and curated
// sessions, and leaves them untouched.
func TestRun_RefusesNonDismissed(t *testing.T) {
	t.Run("captured", func(t *testing.T) {
		key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
		root := journaltest.New(t).
			Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
				time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
			Root()

		_, err := Run(root, "claude-8f3a")
		assertCode(t, err, "validation.not-dismissed")
	})

	t.Run("curated", func(t *testing.T) {
		key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
		transcript := journal.TranscriptFingerprint{Lines: 5, SHA256: "x"}
		root := journaltest.New(t).
			Curated("2026-09-11", key, transcript,
				time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
				time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
				"laptop", "a title").
			Root()

		_, err := Run(root, "claude-8f3a")
		assertCode(t, err, "validation.not-dismissed")

		curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
		if err != nil || !ok || curation.State != journal.StateCurated {
			t.Errorf("curation after refused undismiss: state=%q ok=%v err=%v, want unchanged curated", curation.State, ok, err)
		}
	})
}

// TestRun_UnknownLocator_NotFoundSession asserts V4's not-found.session
// code surfaces through undismiss for a locator matching no session.
func TestRun_UnknownLocator_NotFoundSession(t *testing.T) {
	root := journaltest.New(t).
		Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
			journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
			time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)).
		Root()

	_, err := Run(root, "claude-nope")
	assertCode(t, err, "not-found.session")
}
