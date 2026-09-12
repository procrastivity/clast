package wakeverb_test

import (
	"context"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	wakeplumbing "github.com/procrastivity/clast/internal/verbs/wake"
	"github.com/procrastivity/clast/internal/verbs/wakeverb"
)

func mustCutoff(t *testing.T) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	return c
}

func mustClient(t *testing.T, baseURL string) *llm.Client {
	t.Helper()
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(baseURL, "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}
	return client
}

// TestRunAuto_SkipBelowThreshold_JournalUntouched pins the Auto mode
// length guard's central promise: a draft below auto_min_chars is
// neither written nor dismissed — the session stays exactly `captured`,
// available for a later interactive pass.
func TestRunAuto_SkipBelowThreshold_JournalUntouched(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "short-01"}
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started).
		WithTranscript("2026-09-11", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))

	stub := llmtest.New(t, "# Session: too short\n\nSuggested tags: x\n")
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, fx.Root())
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1", rows)
	}

	summary, err := wakeverb.RunAuto(context.Background(), fx.Root(), mustCutoff(t), journal.Day("2026-09-10"), rows, client, 60)
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if summary.Accepted != 0 || summary.Skipped != 1 || summary.SkippedBelowThreshold != 1 {
		t.Fatalf("summary = %+v, want 0 accepted, 1 skipped (1 below threshold)", summary)
	}

	item := findItem(t, fx.Root(), key)
	if item.State() != journal.StateCaptured {
		t.Errorf("session state = %q, want %q (untouched)", item.State(), journal.StateCaptured)
	}
	if item.EntryExists {
		t.Error("entry.md exists, want none written for a below-threshold skip")
	}
}

// TestRunAuto_Accept_WritesEntryAndCuration confirms a qualifying draft
// is written through curate: entry.md carries the extracted title/tags/
// body, and curation.json lands the session curated with a fresh
// transcript fingerprint.
func TestRunAuto_Accept_WritesEntryAndCuration(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "long-01"}
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started).
		WithTranscript("2026-09-11", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))

	draft := "# Session: fixed the wake ordering bug\n\n" +
		"## Goal\nSort the working set by recency, matching the old skill's wanted order.\n\n" +
		"## What shipped\n- Reordered groupByProject to sort by most-recently-active project.\n\n" +
		"Suggested tags: bugfix, wake\n"
	stub := llmtest.New(t, draft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, fx.Root())
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	summary, err := wakeverb.RunAuto(context.Background(), fx.Root(), mustCutoff(t), journal.Day("2026-09-10"), rows, client, 60)
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if summary.Accepted != 1 || summary.Skipped != 0 {
		t.Fatalf("summary = %+v, want 1 accepted, 0 skipped", summary)
	}
	if summary.ProjectsTouched() != 1 {
		t.Errorf("ProjectsTouched() = %d, want 1", summary.ProjectsTouched())
	}

	item := findItem(t, fx.Root(), key)
	if item.State() != journal.StateCurated {
		t.Fatalf("session state = %q, want curated", item.State())
	}
	if item.Stale() {
		t.Error("freshly curated session reads stale")
	}
	e, ok, err := readEntry(t, fx.Root(), item)
	if err != nil || !ok {
		t.Fatalf("reading entry.md: ok=%v err=%v", ok, err)
	}
	if e.Title != "fixed the wake ordering bug" {
		t.Errorf("entry title = %q, want %q", e.Title, "fixed the wake ordering bug")
	}
	if len(e.Tags) != 2 || e.Tags[0] != "bugfix" || e.Tags[1] != "wake" {
		t.Errorf("entry tags = %v, want [bugfix wake]", e.Tags)
	}
}

// TestRunAuto_StaleSession_OfferedAsRecuration confirms a stale curated
// session walks through Auto mode exactly like a fresh captured one:
// accepting its draft re-curates in place (MODEL M7), never revoking the
// prior entry outright first.
func TestRunAuto_StaleSession_OfferedAsRecuration(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "stale-01"}
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	curatedAt := started.Add(2 * time.Hour)
	// Curated against a shorter transcript than what's on disk now (M7:
	// stale = curated && transcript != transcript_at_curation).
	fx.CuratedStale("2026-09-11", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 1, SHA256: "original"},
		started, curatedAt, "framework", "old title before the session grew",
	).WithTranscript("2026-09-11", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))

	pre := findItem(t, fx.Root(), key)
	if !pre.Stale() {
		t.Fatal("fixture session is not stale, want it to be — test setup bug")
	}

	draft := "# Session: session grew, updated summary\n\n" +
		"## Goal\nCapture the additional turns the transcript picked up after curation.\n\n" +
		"## What shipped\n- Documented the newer turns.\n\n" +
		"Suggested tags: followup\n"
	stub := llmtest.New(t, draft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, fx.Root())
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the stale session offered exactly once", rows)
	}

	summary, err := wakeverb.RunAuto(context.Background(), fx.Root(), mustCutoff(t), journal.Day("2026-09-10"), rows, client, 60)
	if err != nil {
		t.Fatalf("RunAuto: %v", err)
	}
	if summary.Accepted != 1 {
		t.Fatalf("summary = %+v, want 1 accepted", summary)
	}

	post := findItem(t, fx.Root(), key)
	if post.Stale() {
		t.Error("session still reads stale after re-curation")
	}
	if post.State() != journal.StateCurated {
		t.Errorf("state = %q, want curated", post.State())
	}
	e, ok, err := readEntry(t, fx.Root(), post)
	if err != nil || !ok {
		t.Fatalf("reading re-curated entry.md: ok=%v err=%v", ok, err)
	}
	if e.Title != "session grew, updated summary" {
		t.Errorf("re-curated title = %q, want the fresh draft's title", e.Title)
	}
}

// wakeplumbingRun is a thin wrapper over internal/verbs/wake's own Run
// (no --since restriction) — every test in this file needs the exact
// same working-set rows RunAuto/RunInteractive consume in production.
func wakeplumbingRun(t *testing.T, root string) ([]wakeplumbing.Row, error) {
	t.Helper()
	return wakeplumbing.Run(root, nil, mustCutoff(t))
}

// readEntry reads item's entry.md, given only its Key/Shard — the same
// two facts journal.EntryPath needs.
func readEntry(t *testing.T, root string, item journal.WalkItem) (entry.Entry, bool, error) {
	t.Helper()
	return entry.Read(journal.EntryPath(root, item.Shard, item.Key))
}
