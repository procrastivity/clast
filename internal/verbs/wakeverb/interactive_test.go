package wakeverb_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	"github.com/procrastivity/clast/internal/verbs/wakeverb"
)

// oneSessionFixture authors a single captured session with a transcript
// and returns its root and key.
func oneSessionFixture(t *testing.T, nativeID string, started time.Time) (root string, key journal.SessionKey) {
	t.Helper()
	fx := journaltest.New(t)
	key = journal.SessionKey{Harness: "claude", NativeID: nativeID}
	fx.Captured(started.Format("2006-01-02"), key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started).
		WithTranscript(started.Format("2006-01-02"), key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))
	return fx.Root(), key
}

const interactiveDraft = "# Session: fixed the wake ordering bug\n\n" +
	"## Goal\nSort the working set by recency.\n\n" +
	"Suggested tags: bugfix\n"

func TestRunInteractive_Accept_NoPromotions_WritesEntry(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-accept", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	streams := &iostreams.Streams{In: strings.NewReader("1\n4\n"), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Accepted != 1 || summary.Skipped != 0 || summary.Dismissed != 0 {
		t.Fatalf("summary = %+v, want 1 accepted only", summary)
	}

	item := findItem(t, root, key)
	if item.State() != journal.StateCurated {
		t.Errorf("state = %q, want curated", item.State())
	}
}

func TestRunInteractive_Skip_WritesNothing(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-skip", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	streams := &iostreams.Streams{In: strings.NewReader("4\n"), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Skipped != 1 || summary.Accepted != 0 {
		t.Fatalf("summary = %+v, want 1 skipped only", summary)
	}

	item := findItem(t, root, key)
	if item.State() != journal.StateCaptured {
		t.Errorf("state = %q, want untouched captured", item.State())
	}
}

func TestRunInteractive_Dismiss_WritesDismissal(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-dismiss", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	streams := &iostreams.Streams{In: strings.NewReader("3\n"), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Dismissed != 1 {
		t.Fatalf("summary = %+v, want 1 dismissed", summary)
	}

	item := findItem(t, root, key)
	if item.State() != journal.StateDismissed {
		t.Errorf("state = %q, want dismissed", item.State())
	}
}

// TestRunInteractive_EditUnsetEditor_SkipsWithMessage confirms the
// step-brief-decided $EDITOR fallback: unset (or blank) $EDITOR treats
// the session as skipped, with a message printed rather than a silent
// drop or a hard failure.
func TestRunInteractive_EditUnsetEditor_SkipsWithMessage(t *testing.T) {
	t.Setenv("EDITOR", "")
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-edit-unset", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	errOut := &bytes.Buffer{}
	streams := &iostreams.Streams{In: strings.NewReader("2\n"), Out: &bytes.Buffer{}, Err: errOut}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Skipped != 1 {
		t.Fatalf("summary = %+v, want 1 skipped", summary)
	}
	// The message lands on Err, not Out — RunInteractive's own doc
	// comment (C2.1: stdout carries exactly one thing, the eventual
	// summary; interactive chatter is stderr).
	if !strings.Contains(errOut.String(), "$EDITOR is not set") {
		t.Errorf("stderr output = %q, want it to mention $EDITOR is not set", errOut.String())
	}
	item := findItem(t, root, key)
	if item.State() != journal.StateCaptured {
		t.Errorf("state = %q, want untouched captured", item.State())
	}
}

// TestRunInteractive_EOF_StopsRunEarly confirms running out of stdin
// mid-review ends the run without error, leaving whatever wasn't reached
// untouched — the interactive counterpart to Auto mode's own "nothing
// left to decide" case.
func TestRunInteractive_EOF_StopsRunEarly(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-eof", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	streams := &iostreams.Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Drafted != 1 {
		t.Errorf("Drafted = %d, want 1 (the draft happens before the menu read)", summary.Drafted)
	}
	if summary.Accepted != 0 || summary.Skipped != 0 || summary.Dismissed != 0 {
		t.Errorf("summary = %+v, want no disposition recorded (stopped at EOF)", summary)
	}

	item := findItem(t, root, key)
	if item.State() != journal.StateCaptured {
		t.Errorf("state = %q, want untouched captured", item.State())
	}
}

// TestRunInteractive_TwoSessions_AcceptThenSkip drives the same run
// across two sessions: accept (with no promotions) the first, skip the
// second — the e2e counterpart to this asserts the same shape end to
// end; this unit-level test pins the exact stdin line count the menu
// consumes per path (2 lines for Accept: the main choice plus the
// promotion "continue"; 1 line for Skip), so a change to either menu's
// shape is caught here first.
func TestRunInteractive_TwoSessions_AcceptThenSkip(t *testing.T) {
	fx := journaltest.New(t)
	k1 := journal.SessionKey{Harness: "claude", NativeID: "multi-01"}
	k2 := journal.SessionKey{Harness: "claude", NativeID: "multi-02"}
	t1 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	fx.Captured("2026-09-11", k1, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, t1).
		WithTranscript("2026-09-11", k1, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))
	fx.Captured("2026-09-11", k2, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, t2).
		WithTranscript("2026-09-11", k2, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, fx.Root())
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want exactly 2", rows)
	}

	streams := &iostreams.Streams{In: strings.NewReader("1\n4\n4\n"), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, fx.Root(), mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Accepted != 1 || summary.Skipped != 1 {
		t.Fatalf("summary = %+v, want 1 accepted, 1 skipped", summary)
	}
}

// TestRunInteractive_Promotion_FoldsSectionIntoEntry drives Accept with
// one promoted Decision, confirming both the summary count and the
// written entry body carry it (flows/wake.md §5).
func TestRunInteractive_Promotion_FoldsSectionIntoEntry(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-promote", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	stdin := "1\n1\nuse postgres\nchose postgres over sqlite\n4\n"
	streams := &iostreams.Streams{In: strings.NewReader(stdin), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Accepted != 1 || summary.PromotedDecisions != 1 {
		t.Fatalf("summary = %+v, want 1 accepted, 1 promoted decision", summary)
	}

	item := findItem(t, root, key)
	e, ok, err := readEntry(t, root, item)
	if err != nil || !ok {
		t.Fatalf("reading entry.md: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(e.Body, "## Decision") || !strings.Contains(e.Body, "use postgres") {
		t.Errorf("entry body = %q, want the promoted Decision section", e.Body)
	}
	_ = key
}
