package amp

// Step-08 fault-recovery coverage: the Supersede recapture veto (a
// partial or stale read must never replace a more complete committed
// capture merely because metadata advanced), the Incomplete flag's
// facts plumbing, and the pending-set audit of the terminal-vs-
// retryable classification — who drains the lane and who stays owed.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// TestSupersedesVerdictTable pins the recapture veto's rule surface:
// committed truth is replaced only by a read provably not worse —
// strictly ahead in the doc's own revision stamp AND not shorter — or,
// for a commit that was itself flagged incomplete, a same-revision
// re-read that is fuller or finally reads clean.
func TestSupersedesVerdictTable(t *testing.T) {
	s := newTestSource(t, fixtureDataDir(t))
	d := source.Discovered{NativeID: "T-x", Path: threadURL("T-x")}
	rev := func(ts string) time.Time { return mustParseRFC(t, ts) }
	facts := func(stamp string, msgs int, incomplete bool) source.Facts {
		f := source.Facts{
			Incomplete: incomplete,
			Transcript: journal.TranscriptFingerprint{Lines: msgs},
		}
		if stamp != "" {
			f.LastActiveAt = rev(stamp)
		}
		return f
	}
	cleanPrior := journal.Session{
		LastActiveAt: rev("2026-10-01T10:00:00.000Z"),
		Transcript:   journal.TranscriptFingerprint{Lines: 4},
	}
	flaggedPrior := cleanPrior
	flaggedPrior.Incomplete = true

	for _, tc := range []struct {
		name  string
		prior journal.Session
		facts source.Facts
		want  bool
	}{
		// The normal recapture: ahead in revision and not shorter.
		{"ahead-fuller", cleanPrior, facts("2026-10-01T11:00:00.000Z", 6, false), true},
		{"ahead-same-count", cleanPrior, facts("2026-10-01T11:00:00.000Z", 4, false), true},
		{"ahead-flagged-doc", cleanPrior, facts("2026-10-01T11:00:00.000Z", 5, true), true},
		// The gap step-08 closes: ahead in time but shorter — a raced
		// or partial read must never overwrite committed content.
		{"ahead-shorter", cleanPrior, facts("2026-10-01T11:00:00.000Z", 2, false), false},
		{"ahead-empty", cleanPrior, facts("2026-10-01T11:00:00.000Z", 0, true), false},
		// A read stamped behind the committed revision proves nothing.
		{"stale-fuller", cleanPrior, facts("2026-10-01T09:00:00.000Z", 6, false), false},
		{"unstamped-doc", cleanPrior, facts("", 6, false), false},
		// Same revision: a clean commit's re-read is refused (equal or
		// fuller alike — contradictory evidence does not churn truth);
		// a flagged commit may be repaired by a fuller re-read or by
		// the same content finally reading clean.
		{"same-rev-fuller-clean-prior", cleanPrior, facts("2026-10-01T10:00:00.000Z", 6, false), false},
		{"same-rev-equal-clean-prior", cleanPrior, facts("2026-10-01T10:00:00.000Z", 4, false), false},
		{"same-rev-fuller-flagged-prior", flaggedPrior, facts("2026-10-01T10:00:00.000Z", 6, false), true},
		{"same-rev-fuller-still-flagged", flaggedPrior, facts("2026-10-01T10:00:00.000Z", 6, true), true},
		{"same-rev-equal-clean-now", flaggedPrior, facts("2026-10-01T10:00:00.000Z", 4, false), true},
		// An identical flagged re-read is churn, not a repair: refuse
		// rather than rewrite the same bytes each sweep.
		{"same-rev-equal-still-flagged", flaggedPrior, facts("2026-10-01T10:00:00.000Z", 4, true), false},
		{"same-rev-shorter-flagged", flaggedPrior, facts("2026-10-01T10:00:00.000Z", 2, false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := s.Supersedes(context.Background(), d, tc.prior, tc.facts)
			if got != tc.want {
				t.Fatalf("Supersedes = %v (why %q), want %v", got, why, tc.want)
			}
			if !got && why == "" {
				t.Error("a refusal must name its evidence for the diagnostic")
			}
			if got && why != "" {
				t.Errorf("an allowed verdict should carry no refusal text, got %q", why)
			}
		})
	}
}

// TestCaptureIncompleteFlag pins the flag's plumbing into Facts: a
// mid-turn (CompleteFlag) doc and a retry-exhausted (CompleteRetry)
// doc both commit with Incomplete set; a clean doc does not.
func TestCaptureIncompleteFlag(t *testing.T) {
	// Mid-turn — flagged on the spot.
	raw := mustFixture(t, "exports/midturn-streaming.json")
	s, _, d := captureSource(t, raw)
	facts, diags, err := s.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if err != nil || len(diags) == 0 {
		t.Fatalf("mid-turn capture: err=%v diags=%v", err, diags)
	}
	if !facts.Incomplete {
		t.Error("a mid-turn commit must record Incomplete")
	}

	// Clean — no flag.
	s, _, d = captureSource(t, mustFixture(t, "exports/idle-local-client.json"))
	facts, diags, err = s.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if err != nil || len(diags) != 0 {
		t.Fatalf("clean capture: err=%v diags=%v", err, diags)
	}
	if facts.Incomplete {
		t.Error("a clean commit must not record Incomplete")
	}

	// Retry-exhausted — the last faithful read past the bound.
	empty := []byte(strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", "T-empty", 1))
	f := &fakeCLI{t: t, exports: map[string][]byte{"T-empty": empty}}
	s = exportSource(t, f)
	facts, _, err = s.Capture(context.Background(), discoveredForID(t, empty), dirWriter(t, t.TempDir()))
	if err != nil {
		t.Fatalf("exhausted-retry capture: %v", err)
	}
	if !facts.Incomplete {
		t.Error("a retry-exhausted commit must record Incomplete")
	}
}

// TestCaptureClassificationAudit audits the whole terminal-vs-retryable
// matrix on the pending lane: the provably-gone verdicts
// (does-not-exist, invalid id) and the provably-permanent one (byte cap
// — an export only grows) settle; every other failure — auth, spawn,
// offline, timeout, malformed stdout, a generic exit, caller
// cancellation — keeps the id owed for a later sweep. Call counts pin
// the retry-bound discipline alongside (terminal verdicts fail fast at
// one call; retryable kinds spend the full bound).
func TestCaptureClassificationAudit(t *testing.T) {
	timeoutHook := func(ctx context.Context, _ []string) (cliResult, error) {
		<-ctx.Done() // the per-call budget's deadline
		return cliResult{}, ctx.Err()
	}
	cases := []struct {
		name        string
		setup       func(t *testing.T, s *Source, f *fakeCLI)
		wantPending bool
		wantCalls   int
		wantCancel  bool
	}{
		{
			name: "does-not-exist settles", wantPending: false, wantCalls: 1,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 1, stderr: mustFixture(t, "errors/export-nonexistent.txt")}}
			},
		},
		{
			name: "invalid-id settles", wantPending: false, wantCalls: 1,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 1, stderr: mustFixture(t, "errors/export-malformed-id.txt")}}
			},
		},
		{
			name: "auth stays pending (one call)", wantPending: true, wantCalls: 1,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 1, stderr: []byte("Error: not authenticated — run `amp login`")}}
			},
		},
		{
			name: "spawn stays pending (one call)", wantPending: true, wantCalls: 1,
			setup: func(_ *testing.T, s *Source, f *fakeCLI) {
				s.run = func(_ context.Context, args []string, _ callBudget) (cliResult, error) {
					f.calls = append(f.calls, args)
					return cliResult{}, &cliError{kind: errSpawn, op: "amp " + strings.Join(args, " "), err: errors.New("executable vanished")}
				}
			},
		},
		{
			name: "offline retries the bound", wantPending: true, wantCalls: 3,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 1, stderr: mustFixture(t, "errors/offline.txt")}}
			},
		},
		{
			name: "generic exit retries the bound", wantPending: true, wantCalls: 3,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 1, stderr: []byte("boom")}}
			},
		},
		{
			name: "malformed stdout retries the bound", wantPending: true, wantCalls: 3,
			setup: func(_ *testing.T, _ *Source, f *fakeCLI) {
				f.results = map[string]cliResult{"threads export T-x": {code: 0, stdout: []byte(`{"id":"T-x","messages":`)}}
			},
		},
		{
			// A capped export only ever grows, so at this budget the
			// fetch is provably permanent: one call, the id quarantines
			// and settles — the errQuarantined lane, not a retry.
			name: "byte cap quarantines and settles", wantPending: false, wantCalls: 1,
			setup: func(_ *testing.T, s *Source, f *fakeCLI) {
				s.exportBudget.maxBytes = 16 // the seam-boundary cap check
				f.results = map[string]cliResult{"threads export T-x": {code: 0, stdout: []byte(`{"id":"T-x","messages":[],"v":9}`)}}
			},
		},
		{
			name: "per-call timeout retries the bound", wantPending: true, wantCalls: 3,
			setup: func(_ *testing.T, s *Source, f *fakeCLI) {
				s.exportBudget.timeout = 10 * time.Millisecond
				f.hook = timeoutHook
			},
		},
		{
			name: "caller cancellation stays pending", wantPending: true, wantCalls: 1, wantCancel: true,
			setup: func(_ *testing.T, _ *Source, _ *fakeCLI) {},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCLI{t: t, catalog: []fakeThread{
				{id: "T-x", updatedAt: "2026-10-01T08:00:00.000Z"},
			}}
			s := fixtureSource(t, f)
			found, _, err := s.Discover(context.Background())
			if err != nil || len(found) != 1 {
				t.Fatalf("Discover: found=%v err=%v", found, err)
			}
			tc.setup(t, s, f)

			ctx := context.Background()
			if tc.wantCancel {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			_, _, err = s.Capture(ctx, found[0], dirWriter(t, t.TempDir()))
			if err == nil {
				t.Fatal("Capture must fail in this fixture")
			}
			if tc.wantCancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("want the cancellation verbatim, got %v", err)
			}
			st := readScanState(t, s)
			if got := len(st.PendingIDs) != 0; got != tc.wantPending {
				t.Fatalf("pending_ids = %v, wantPending=%v", st.PendingIDs, tc.wantPending)
			}
			if n := f.exportCalls(); n != tc.wantCalls {
				t.Errorf("exportCalls = %d, want %d", n, tc.wantCalls)
			}
		})
	}
}

// TestDiscoverPendingOutlivesAnUncapturedSweep pins the seam between
// scanstate write and captures: a run that enumerated but died before
// any capture leaves the emitted ids pending, and the next sweep's id:
// lane re-probes them rather than losing the work.
func TestDiscoverPendingOutlivesAnUncapturedSweep(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-09-30T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	// The "run" ends here — no captures ran. The checkpoint already
	// holds the owed id; the next sweep must re-probe it.
	f.calls = nil
	found, _, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	var probed bool
	for _, q := range searchQueries(f) {
		if q == "id:T-a" {
			probed = true
		}
	}
	if !probed || len(found) != 1 {
		t.Fatalf("uncaptured pending id was not re-probed: queries=%v found=%v", searchQueries(f), found)
	}
	if st := readScanState(t, s); len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-a" {
		t.Fatalf("pending_ids = %v, want [T-a] still owed", st.PendingIDs)
	}
}

// TestAcquireExhaustedDocIsVetoedOnRecapture composes the two halves of
// the worse-read rule at the seam: a retry-exhausted (CompleteRetry)
// read is still captured when nothing is committed, but Supersedes
// refuses it against a fuller committed capture.
func TestAcquireExhaustedDocIsVetoedOnRecapture(t *testing.T) {
	empty := []byte(strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", "T-empty", 1))
	f := &fakeCLI{t: t, exports: map[string][]byte{"T-empty": empty}}
	s := exportSource(t, f)
	d := discoveredForID(t, empty)

	raw, doc, note, err := s.acquireExport(context.Background(), d)
	if err != nil || doc == nil || note == "" {
		t.Fatalf("exhausted retry must still surface the last doc: raw=%v doc=%v note=%q err=%v",
			raw != nil, doc != nil, note, err)
	}
	facts := factsFromDoc(doc)
	facts.Incomplete = note != ""
	prior := journal.Session{
		LastActiveAt: d.ModTime.Add(-time.Hour), // an older commit
		Transcript:   journal.TranscriptFingerprint{Lines: 3},
	}
	if ok, why := s.Supersedes(context.Background(), d, prior, facts); ok {
		t.Fatalf("an exhausted empty read superseded a 3-message commit: %q", why)
	}
	// …while the same exhausted doc supersedes a commit it is ahead of
	// and not shorter than — faithful new content is never vetoed.
	prior.Transcript.Lines = 0
	if ok, _ := s.Supersedes(context.Background(), d, prior, facts); !ok {
		t.Error("an ahead-of-nothing read must still supersede")
	}
}
