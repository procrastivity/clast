package wakeverb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	"github.com/procrastivity/clast/internal/verbs/wakeverb"
)

// sequencedStub is a minimal OpenAI-compatible /chat/completions stub that
// answers its Nth request with responses[N] (its last response for any
// request beyond len(responses)), and records each request's own user
// message content — llmtest.Server's single canned response can't change
// mid-run, and RunInteractive's Edit path (F2, llm-verbs seal sweep) needs
// exactly that: a first draft, then a distinguishable regenerated one.
type sequencedStub struct {
	responses []string

	srv  *httptest.Server
	mu   sync.Mutex
	seen []string
}

func newSequencedStub(t *testing.T, responses ...string) *sequencedStub {
	t.Helper()
	s := &sequencedStub{responses: responses}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sequencedStub) URL() string { return s.srv.URL }

// requests returns every request's own rendered user-message content
// seen so far, in arrival order.
func (s *sequencedStub) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.seen))
	copy(out, s.seen)
	return out
}

func (s *sequencedStub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	var user string
	for _, m := range req.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}

	s.mu.Lock()
	idx := len(s.seen)
	s.seen = append(s.seen, user)
	resp := s.responses[len(s.responses)-1]
	if idx < len(s.responses) {
		resp = s.responses[idx]
	}
	s.mu.Unlock()

	var out struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	out.Choices = make([]struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}, 1)
	out.Choices[0].Message.Role = "assistant"
	out.Choices[0].Message.Content = resp

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

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

// editedWakeDraft is what the stub's SECOND response carries — distinct
// from interactiveDraft so a test can tell whether the accepted entry
// carries the ORIGINAL draft or the regenerated one.
const editedWakeDraft = "# Session: revised per feedback\n\n" +
	"## Goal\nProve the Edit path's regenerated draft is what actually gets curated.\n\n" +
	"Suggested tags: revised\n"

// TestRunInteractive_Edit_RegeneratesWithFeedbackThenAccepts pins F2
// (llm-verbs seal sweep): flows/wake.md §4's Edit path is "take the
// requested changes as feedback, regenerate the draft (§3) incorporating
// them, and return to this decision" — not the old $EDITOR mechanism.
// Choosing Edit ("2"), then a feedback line, must call the LLM a SECOND
// time (the stub captures 2 requests total) with the old porcelain's own
// "Revisions requested by user: …" framing appended to the user prompt,
// show the regenerated draft, and return to the same menu — Accept ("1")
// then curates the REGENERATED draft's title, not the original.
func TestRunInteractive_Edit_RegeneratesWithFeedbackThenAccepts(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-edit-feedback", started)

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	// The stub answers the initial draft request with interactiveDraft
	// and the regeneration (after Edit + feedback) with editedWakeDraft —
	// a two-call sequenced stub, since llmtest.Server's own canned
	// response can't change mid-run (RunInteractive is one synchronous
	// call, with no hook to swap it between requests).
	wrapped := newSequencedStub(t, interactiveDraft, editedWakeDraft)
	client := mustClient(t, wrapped.URL())

	// "2" (Edit) -> feedback line -> regenerated draft shown -> "1"
	// (Accept) -> "4" (no more promotions).
	stdin := "2\nplease shorten the goal section\n1\n4\n"
	streams := &iostreams.Streams{In: strings.NewReader(stdin), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Accepted != 1 || summary.Skipped != 0 {
		t.Fatalf("summary = %+v, want 1 accepted, 0 skipped", summary)
	}

	reqs := wrapped.requests()
	if len(reqs) != 2 {
		t.Fatalf("stub captured %d requests, want exactly 2 (initial draft + one regeneration)", len(reqs))
	}
	if !strings.Contains(reqs[1], "Revisions requested by user: please shorten the goal section") {
		t.Errorf("second request's user content = %q, want the old porcelain's own feedback framing", reqs[1])
	}

	item := findItem(t, root, key)
	e, ok, err := readEntry(t, root, item)
	if err != nil || !ok {
		t.Fatalf("reading entry.md: ok=%v err=%v", ok, err)
	}
	if e.Title != "revised per feedback" {
		t.Errorf("entry title = %q, want the REGENERATED draft's title, not the original", e.Title)
	}
}

// TestRunInteractive_Edit_EOFDuringFeedbackPrompt_SkipsSession pins F2's
// own EOF posture: running out of stdin at the "What should change?"
// prompt skips this one session — consistent with the menu's own EOF
// posture (choice "4") — without calling the LLM again and without
// stopping the whole run (that escalation is reserved for EOF at the
// per-session Choice prompt itself, F4).
func TestRunInteractive_Edit_EOFDuringFeedbackPrompt_SkipsSession(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-edit-eof", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	// "2\n" (Edit) leaves nothing for the feedback-line read: EOF.
	streams := &iostreams.Streams{In: strings.NewReader("2\n"), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Skipped != 1 || summary.Accepted != 0 || summary.Dismissed != 0 {
		t.Fatalf("summary = %+v, want 1 skipped only", summary)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1 (EOF at the feedback prompt never regenerates)", len(reqs))
	}
	item := findItem(t, root, key)
	if item.State() != journal.StateCaptured {
		t.Errorf("state = %q, want untouched captured", item.State())
	}
}

// TestRunInteractive_Dismiss_StaleCuratedSession_RefusalIsPerSessionNotFatal
// pins F1 (the seal blocker, llm-verbs seal sweep): a stale row already
// carries an entry.md on disk, so dismiss.Run refuses it
// (validation.curated) — flows/wake.md §6 walks a stale session through
// §2-§5 exactly like a fresh one, and §4 offers Dismiss unconditionally,
// so the choice must stay offered and the refusal must be a per-session
// diagnostic, never a run-aborting error. This drives the stale session
// first (Dismiss, refused) and a second, fresh session after it (Accept):
// before the fix, dismiss.Run's error propagated straight out of
// RunInteractive, and the fresh session below was never reached at all.
func TestRunInteractive_Dismiss_StaleCuratedSession_RefusalIsPerSessionNotFatal(t *testing.T) {
	fx := journaltest.New(t)
	staleKey := journal.SessionKey{Harness: "claude", NativeID: "int-stale-dismiss"}
	freshKey := journal.SessionKey{Harness: "claude", NativeID: "int-fresh-after-stale"}
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	curatedAt := started.Add(2 * time.Hour)
	fx.CuratedStale("2026-09-11", staleKey,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 1, SHA256: "original"},
		started, curatedAt, "framework", "old title before the session grew",
	).WithTranscript("2026-09-11", staleKey, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))
	fx.Captured("2026-09-11", freshKey, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started.Add(time.Hour)).
		WithTranscript("2026-09-11", freshKey, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, fx.Root())
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2 (the stale session offered alongside the fresh one)", rows)
	}

	errOut := &bytes.Buffer{}
	// Row order is chronological by StartedAt: staleKey (09:00) then
	// freshKey (10:00). "3" (Dismiss, refused -> skip) for the stale row,
	// then "1"/"4" (Accept, no promotions) for the fresh one.
	streams := &iostreams.Streams{In: strings.NewReader("3\n1\n4\n"), Out: &bytes.Buffer{}, Err: errOut}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, fx.Root(), mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v, want nil — a stale-dismiss refusal must not abort the run", err)
	}
	if summary.Dismissed != 0 {
		t.Errorf("Dismissed = %d, want 0 (the refused dismiss must not count as dismissed)", summary.Dismissed)
	}
	if summary.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (the stale session, counted skipped after the refusal)", summary.Skipped)
	}
	if summary.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 — the run must reach and accept the fresh session after the refusal", summary.Accepted)
	}
	if !strings.Contains(errOut.String(), "cannot dismiss") {
		t.Errorf("stderr = %q, want a per-session diagnostic naming the refusal", errOut.String())
	}

	stalePost := findItem(t, fx.Root(), staleKey)
	if stalePost.State() != journal.StateCurated {
		t.Errorf("stale session state = %q, want still curated (dismiss refused, unchanged)", stalePost.State())
	}
	freshPost := findItem(t, fx.Root(), freshKey)
	if freshPost.State() != journal.StateCurated {
		t.Errorf("fresh session state = %q, want curated (accepted)", freshPost.State())
	}
}

// TestRunInteractive_EOF_StopsRunEarly confirms running out of stdin
// mid-review ends the run without error, leaving whatever wasn't reached
// untouched — the interactive counterpart to Auto mode's own "nothing
// left to decide" case. F4 (llm-verbs seal sweep) amends the accounting:
// before the fix, the one row that hit EOF was Drafted but never
// dispositioned at all, so Considered (1) didn't reconcile against
// Accepted+Dismissed+Skipped (0+0+0) — §7's own summary invariant broke
// silently. Now that row counts as skipped, and a "stopped early" line
// says so on stderr.
func TestRunInteractive_EOF_StopsRunEarly(t *testing.T) {
	started := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	root, key := oneSessionFixture(t, "int-eof", started)

	stub := llmtest.New(t, interactiveDraft)
	client := mustClient(t, stub.URL())

	rows, err := wakeplumbingRun(t, root)
	if err != nil {
		t.Fatalf("wake.Run: %v", err)
	}

	errOut := &bytes.Buffer{}
	streams := &iostreams.Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: errOut}
	summary, err := wakeverb.RunInteractive(context.Background(), streams, root, mustCutoff(t), journal.Day("2026-09-10"), rows, client, "framework")
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if summary.Drafted != 1 {
		t.Errorf("Drafted = %d, want 1 (the draft happens before the menu read)", summary.Drafted)
	}
	if summary.Accepted != 0 || summary.Dismissed != 0 {
		t.Errorf("summary = %+v, want 0 accepted, 0 dismissed", summary)
	}
	if summary.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 — F4: the unreached row now reconciles into Skipped instead of vanishing from the tally", summary.Skipped)
	}
	if summary.Considered != summary.Accepted+summary.Dismissed+summary.Skipped {
		t.Errorf("Considered (%d) != Accepted+Dismissed+Skipped (%d) — §7's summary invariant broken",
			summary.Considered, summary.Accepted+summary.Dismissed+summary.Skipped)
	}
	if !strings.Contains(errOut.String(), "stopped early, 1 session(s) not reached") {
		t.Errorf("stderr = %q, want a stopped-early line naming the unreached count", errOut.String())
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
