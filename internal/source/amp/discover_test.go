package amp

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// fixtureSource wires a test source to a fake CLI with the pinned
// "now" — windows cover 2025-01-01 … 2026-10-11.
func fixtureSource(t *testing.T, f *fakeCLI) *Source {
	t.Helper()
	s := newTestSource(t, fixtureDataDir(t))
	s.run = f.run
	return s
}

func discoveredIDs(found []source.Discovered) map[string]source.Discovered {
	out := make(map[string]source.Discovered, len(found))
	for _, d := range found {
		out[d.NativeID] = d
	}
	return out
}

// catalogFromPage loads a search-page fixture into catalog rows.
func catalogFromPage(t *testing.T, rel string) []fakeThread {
	t.Helper()
	var cat []fakeThread
	for _, r := range decodeSearchPage(t, mustFixture(t, rel)) {
		cat = append(cat, fakeThread{id: r.ID, updatedAt: r.UpdatedAt})
	}
	return cat
}

// TestDiscoverHappyPath enumerates the two pinned day windows plus
// synthesized boundary rows through the catalog-backed fake.
func TestDiscoverHappyPath(t *testing.T) {
	cat := append(catalogFromPage(t, "search/window-2026-09-28.json"),
		catalogFromPage(t, "search/window-2026-09-29.json")...)
	// Boundary rows on the real 7-day grid (stepped from 2025-01-01):
	// exactly a window start (after: is inclusive), exactly a window
	// end bound (before: is exclusive — it must surface via the NEXT
	// window), and the last millisecond of the window before. All must
	// enumerate exactly once.
	cat = append(cat,
		fakeThread{id: "T-boundary-start", updatedAt: "2026-09-29T00:00:00.000Z"},
		fakeThread{id: "T-boundary-end", updatedAt: "2026-10-06T00:00:00.000Z"},
		fakeThread{id: "T-boundary-tail", updatedAt: "2026-09-28T23:59:59.999Z"},
	)
	f := &fakeCLI{
		t:       t,
		catalog: cat,
		list:    mustFixture(t, "list/tail-empty.json"),
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover: err=%v diags=%v", err, diags)
	}
	got := discoveredIDs(found)
	if len(got) != len(cat) {
		t.Fatalf("found %d threads, want %d: %v", len(got), len(cat), got)
	}
	for _, th := range cat {
		d, ok := got[th.id]
		if !ok {
			t.Errorf("thread %s not discovered", th.id)
			continue
		}
		want, _ := time.Parse(time.RFC3339Nano, th.updatedAt)
		if !d.ModTime.Equal(want) {
			t.Errorf("%s ModTime = %v, want %v", th.id, d.ModTime, want)
		}
		if d.Path != threadURL(th.id) {
			t.Errorf("%s Path = %q, want %q", th.id, d.Path, threadURL(th.id))
		}
	}
	for i := 1; i < len(found); i++ {
		if found[i].ModTime.Before(found[i-1].ModTime) {
			t.Fatalf("found not ascending: %v then %v", found[i-1].ModTime, found[i].ModTime)
		}
	}
}

// TestDiscoverEmptyUniverse: every window returns [] — quiet success,
// not an error and not a diagnostic.
func TestDiscoverEmptyUniverse(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{}}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil || len(diags) != 0 || len(found) != 0 {
		t.Fatalf("Discover on empty universe: found=%v diags=%v err=%v", found, diags, err)
	}
}

// TestDiscoverSubdividesCappedDay: a 100-row day window subdivides on
// archived: — the pinned boundary-100 fixture and its 70/30 halves.
func TestDiscoverSubdividesCappedDay(t *testing.T) {
	f := &fakeCLI{
		t:       t,
		catalog: catalogBoundary100(t),
		list:    mustFixture(t, "list/tail-empty.json"),
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 100 {
		t.Fatalf("found %d threads, want the full subdivided 100", len(found))
	}
	for _, d := range found {
		if !strings.HasPrefix(d.NativeID, "T-01f00000") {
			t.Errorf("unexpected id %s", d.NativeID)
		}
	}
	for _, dg := range diags {
		t.Errorf("unexpected diagnostic: %v", dg.Err)
	}
}

// TestDiscoverSubdividesPinned: when an archived: leaf still hits the
// cap, the ladder descends to pinned:. Ids only reachable through the
// pinned leaves prove the full ladder ran.
func TestDiscoverSubdividesPinned(t *testing.T) {
	cat := append(synthCatalog(70, 0, "2026-10-08", true, false),
		synthCatalog(50, 1000, "2026-10-08", true, true)...)
	f := &fakeCLI{t: t, catalog: cat}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 120 {
		t.Fatalf("found %d threads, want 120 (70 unpinned + 50 pinned)", len(found))
	}
	for _, dg := range diags {
		t.Errorf("unexpected diagnostic: %v", dg.Err)
	}
}

// TestDiscoverCappedLeaf: a leaf still at the cap after archived/pinned
// subdivision is the named residual — its 100 rows are still emitted
// (they are real threads) and the gap lands as a diagnostic.
func TestDiscoverCappedLeaf(t *testing.T) {
	f := &fakeCLI{
		t:       t,
		catalog: synthCatalog(150, 0, "2026-10-08", false, false),
		list:    mustFixture(t, "list/tail-empty.json"),
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 100 {
		t.Fatalf("found %d threads, want the capped 100", len(found))
	}
	var capped int
	for _, dg := range diags {
		if strings.Contains(dg.Err.Error(), "cap") {
			capped++
		}
	}
	if capped != 1 {
		t.Fatalf("want exactly one capped-window diagnostic, got %v", diags)
	}
}

// TestDiscoverDedupWindowOverlap: the same id reported by two windows —
// the mid-scan forward jump — dedupes to the freshest updatedAt. The
// catalog models it as two same-id rows, each landing in its own window.
func TestDiscoverDedupWindowOverlap(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-jump", updatedAt: "2026-09-30T10:00:00.000Z"},
			{id: "T-jump", updatedAt: "2026-10-07T08:00:00.000Z"},
		},
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover: err=%v diags=%v", err, diags)
	}
	if len(found) != 1 {
		t.Fatalf("found %d threads, want 1 deduped", len(found))
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-10-07T08:00:00.000Z")
	if !found[0].ModTime.Equal(want) {
		t.Errorf("deduped ModTime = %v, want freshest %v", found[0].ModTime, want)
	}
}

// TestDiscoverMinTimePostFilter: the ms-precision checkpoint filter —
// rows at or below MinTime drop, newer ones survive, and the retry-id
// lane bypasses it.
func TestDiscoverMinTimePostFilter(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-old", updatedAt: "2026-10-08T10:00:00.000Z"},
			{id: "T-edge", updatedAt: "2026-10-08T12:00:00.000Z"},
			{id: "T-new", updatedAt: "2026-10-08T14:00:00.000Z"},
			{id: "T-retry", updatedAt: "2026-01-05T00:00:00.000Z"}, // below MinTime — the retry lane still emits it
		},
	}
	s := fixtureSource(t, f)
	checkpoint, _ := time.Parse(time.RFC3339Nano, "2026-10-08T12:00:00.000Z")
	found, diags, _, err := s.enumerate(context.Background(), scanParams{
		From:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Through: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		MinTime: checkpoint,
		IDs:     []string{"T-retry"},
	})
	if err != nil || len(diags) != 0 {
		t.Fatalf("enumerate: err=%v diags=%v", err, diags)
	}
	got := discoveredIDs(found)
	if _, ok := got["T-old"]; ok {
		t.Error("T-old (below MinTime) should be filtered")
	}
	if _, ok := got["T-edge"]; ok {
		t.Error("T-edge (at MinTime) should be filtered — strictly-after")
	}
	if _, ok := got["T-new"]; !ok {
		t.Error("T-new (above MinTime) should be emitted")
	}
	if _, ok := got["T-retry"]; !ok {
		t.Error("retry-id lane must bypass the MinTime filter")
	}
}

// TestDiscoverIDLookup: a retry-id lookup emits its thread even when the
// scanned windows no longer cover its updatedAt — and a deleted id
// emits nothing.
func TestDiscoverIDLookup(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-retry-ok", updatedAt: "2025-02-02T00:00:00.000Z"},
		},
	}
	s := fixtureSource(t, f)
	found, diags, _, err := s.enumerate(context.Background(), scanParams{
		From:    time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		Through: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		IDs:     []string{"T-retry-ok", "T-retry-gone"},
	})
	if err != nil || len(diags) != 0 {
		t.Fatalf("enumerate: err=%v diags=%v", err, diags)
	}
	if len(found) != 1 || found[0].NativeID != "T-retry-ok" {
		t.Fatalf("found = %v — the retry lane should surface T-retry-ok only", found)
	}
}

// TestDiscoverSearchFailure: a transport failure mid-scan is a real
// source-level error — never downgraded, never a quiet empty.
func TestDiscoverSearchFailure(t *testing.T) {
	f := &fakeCLI{
		t: t,
		results: map[string]cliResult{
			// The first window (historyFloor + 7d) fails.
			"threads search after:2025-01-01 before:2025-01-08 -n 100 --json": {
				code:   1,
				stderr: mustFixture(t, "errors/offline.txt"),
			},
		},
	}
	found, _, err := fixtureSource(t, f).Discover(context.Background())
	if err == nil {
		t.Fatal("offline search returned nil error")
	}
	if !strings.Contains(err.Error(), "Cannot reach Amp servers") {
		t.Errorf("error %q should carry the CLI's stderr", err)
	}
	if len(found) != 0 {
		t.Errorf("found %d threads on a failed enumeration", len(found))
	}
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errOffline {
		t.Errorf("want errOffline, got %v", err)
	}
}

// TestDiscoverMalformedPage: a non-JSON search page is a real error.
func TestDiscoverMalformedPage(t *testing.T) {
	f := &fakeCLI{
		t: t,
		results: map[string]cliResult{
			"threads search after:2025-01-01 before:2025-01-08 -n 100 --json": {
				stdout: []byte("<html>not json</html>"),
			},
		},
	}
	_, _, err := fixtureSource(t, f).Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("want malformed-page error, got %v", err)
	}
}

// TestDiscoverAuthFailure: a credential rejection fails closed — a real
// error naming the auth failure, never a prompt, never quiet.
func TestDiscoverAuthFailure(t *testing.T) {
	f := &fakeCLI{
		t: t,
		results: map[string]cliResult{
			"threads search after:2025-01-01 before:2025-01-08 -n 100 --json": {
				code:   1,
				stderr: []byte("Error: not authenticated — run `amp login` to continue"),
			},
		},
	}
	_, _, err := fixtureSource(t, f).Discover(context.Background())
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errAuth {
		t.Fatalf("want errAuth, got %v", err)
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("error %q should carry the auth stderr", err)
	}
}

// TestDiscoverTimeout: a hanging search dies at its per-call budget as
// an ordinary bounded error — not context.DeadlineExceeded.
func TestDiscoverTimeout(t *testing.T) {
	f := &fakeCLI{
		t: t,
		hook: func(ctx context.Context, _ []string) (cliResult, error) {
			<-ctx.Done()
			return cliResult{}, ctx.Err()
		},
	}
	s := fixtureSource(t, f)
	s.searchBudget.timeout = 20 * time.Millisecond
	_, _, err := s.Discover(context.Background())
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errTimeout {
		t.Fatalf("want errTimeout, got %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Error("per-call timeout must not satisfy errors.Is(context.DeadlineExceeded)")
	}
}

// TestDiscoverCancellation: caller cancellation propagates as the
// context error — fatal upstream.
func TestDiscoverCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeCLI{
		t: t,
		hook: func(ctx context.Context, _ []string) (cliResult, error) {
			cancel()
			return cliResult{}, ctx.Err()
		},
	}
	_, _, err := fixtureSource(t, f).Discover(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestDiscoverAbsentBinary: no amp on the transport = absent storage —
// the contract's quiet nil, nil, nil (Present is the explicit-path
// disambiguator).
func TestDiscoverAbsentBinary(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = filepath.Join(t.TempDir(), "no-such-amp")
	// run is nil — the real exec path hits LookPath.
	found, diags, err := s.Discover(context.Background())
	if found != nil || diags != nil || err != nil {
		t.Fatalf("want nil, nil, nil for absent binary; got %v, %v, %v", found, diags, err)
	}
}

// TestDiscoverCorroboration: a thread visible to `threads list` but
// missed by enumeration is a diagnostic — and a failed corroboration
// call degrades to one diag, never a Discover error.
func TestDiscoverCorroboration(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-01f00000-0000-7000-8000-000000000001", updatedAt: "2026-10-08T23:00:00.037Z"},
			{id: "T-01f00000-0000-7000-8000-000000000002", updatedAt: "2026-10-08T22:00:00.000Z"},
			{id: "T-01f00000-0000-7000-8000-000000000003", updatedAt: "2026-10-08T21:00:00.000Z"},
		},
		list: mustFixture(t, "list/page.json"), // lists exactly the catalog's 3 ids
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil || len(diags) != 0 || len(found) != 3 {
		t.Fatalf("Discover: found=%v diags=%v err=%v", found, diags, err)
	}

	// An id only list can see → enumeration-gap diagnostics.
	f.catalog = f.catalog[:1]
	found, diags, err = fixtureSource(t, f).Discover(context.Background())
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover: found=%v err=%v", found, err)
	}
	var misses int
	for _, dg := range diags {
		if strings.Contains(dg.Err.Error(), "not enumerated") {
			misses++
			if !strings.HasPrefix(dg.Path, "https://ampcode.com/threads/") {
				t.Errorf("diagnostic Path = %q, want a thread URL", dg.Path)
			}
		}
	}
	if misses != 2 {
		t.Fatalf("want 2 corroboration-miss diagnostics, got %v", diags)
	}

	// Corroboration itself failing → one diag, enumeration still wins.
	f2 := &fakeCLI{
		t:       t,
		catalog: f.catalog,
		results: map[string]cliResult{
			"threads list --include-archived --limit 500 --json": {
				code:   1,
				stderr: mustFixture(t, "errors/offline.txt"),
			},
		},
	}
	_, diags, err = fixtureSource(t, f2).Discover(context.Background())
	if err != nil {
		t.Fatalf("corroboration failure must not fail Discover: %v", err)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "corroboration unavailable") {
		t.Fatalf("want one corroboration-unavailable diagnostic, got %v", diags)
	}
}

// TestDiscoverCorroborationInSpanGate pins the step-12 span gate: once
// a floor exists, a listed-but-unemitted id queues only when evidence
// puts it inside the scanned span — its own list `updated` stamp above
// the floor, a committed revision above the floor, or no committed
// record at all. A listed id committed at-or-below the floor is out of
// scope, not missed: no diagnostic, no pending entry.
func TestDiscoverCorroborationInSpanGate(t *testing.T) {
	journalDir := t.TempDir()
	writeCommitted := func(id, rev string) {
		t.Helper()
		if err := journal.WriteSession(journalDir, "2026-10-01", journal.SessionKey{Harness: "amp", NativeID: id},
			journal.Session{LastActiveAt: mustParseRFC(t, rev)}); err != nil {
			t.Fatal(err)
		}
	}
	// The floor after a clean sweep is the scan's start instant —
	// 2026-10-10T12:00Z under the pinned clock.
	writeCommitted("T-committed-old", "2026-10-01T08:00:00.000Z")  // below the floor — out of span
	writeCommitted("T-committed-late", "2026-10-10T13:00:00.000Z") // above the floor — owed a re-emit

	f := &fakeCLI{
		t:       t,
		catalog: []fakeThread{{id: "T-seen", updatedAt: "2026-10-10T11:00:00.000Z"}},
	}
	s := fixtureSource(t, f)
	s.ScopeJournal(journalDir)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}

	// Catch-up: T-seen leaves the catalog entirely, and list still sees
	// the whole universe — committed ids on both sides of the floor,
	// a fresh never-committed id, and a stale-stamped one.
	f.catalog = nil
	f.list = []byte(`[
		{"id":"T-committed-old","updated":"2026-10-01T08:00:00.000Z","messageCount":2},
		{"id":"T-committed-late","updated":"2026-10-01T00:00:00.000Z","messageCount":2},
		{"id":"T-fresh","updated":"2026-10-10T20:00:00.000Z","messageCount":1},
		{"id":"T-backlog","updated":"2026-09-01T00:00:00.000Z","messageCount":1}
	]`)
	f.calls = nil

	found, diags, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v, want nothing emitted", found)
	}
	var misses []string
	for _, dg := range diags {
		if strings.Contains(dg.Err.Error(), "not enumerated") {
			misses = append(misses, dg.Path)
		}
	}
	// T-committed-late queues on journal evidence despite the stale
	// list stamp; T-fresh on the list stamp; T-backlog on the
	// never-committed arm. T-committed-old — committed below the
	// floor — produces nothing.
	if len(misses) != 3 {
		t.Fatalf("corroboration misses = %v, want the three in-span ids", misses)
	}
	for _, p := range misses {
		if strings.HasSuffix(p, "T-committed-old") {
			t.Errorf("committed-below-floor id diagnosed as a miss: %v", misses)
		}
	}
	wantPending := []string{"T-backlog", "T-committed-late", "T-fresh"}
	if st := readScanState(t, s); !slices.Equal(st.PendingIDs, wantPending) {
		t.Fatalf("pending_ids = %v, want %v (T-seen drained gone)", st.PendingIDs, wantPending)
	}
	// The queued ids are NOT probed this sweep — corroboration runs
	// after the id: lane; they cost their lookups next sweep.
	for _, q := range searchQueries(f) {
		if strings.HasPrefix(q, "id:") && q != "id:T-seen" {
			t.Errorf("unexpected id: probe %q — queued misses probe next sweep", q)
		}
	}
}

// TestDiscoverCorroborationNoJournalQueuesAll pins the conservative
// arm: with no journal bound there is no committed-revision evidence,
// so a catch-up miss queues exactly as before — list corroboration
// never loses work for want of journal data.
func TestDiscoverCorroborationNoJournalQueuesAll(t *testing.T) {
	f := &fakeCLI{
		t:       t,
		catalog: []fakeThread{{id: "T-seen", updatedAt: "2026-10-10T11:00:00.000Z"}},
	}
	s := fixtureSource(t, f) // no ScopeJournal — the unbound scope
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	f.catalog = nil
	f.list = []byte(`[
		{"id":"T-seen","updated":"2026-10-10T11:00:00.000Z","messageCount":1},
		{"id":"T-old-row","updated":"2026-09-01T00:00:00.000Z","messageCount":1}
	]`)
	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	// T-seen left the catalog (gone via id:) but still lists → queued;
	// T-old-row's stale stamp is unknowable without the journal →
	// queued. Both join pending.
	if st := readScanState(t, s); !slices.Equal(st.PendingIDs, []string{"T-old-row", "T-seen"}) {
		t.Fatalf("pending_ids = %v, want both listed rows owed without journal evidence", st.PendingIDs)
	}
}

// TestDiscoverListCountsFeedsCapture: the corroboration pass records
// messageCounts the 0-messages retry hint consults.
func TestDiscoverListCountsFeedsCapture(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-01f00000-0000-7000-8000-000000000001", updatedAt: "2026-10-08T23:00:00.037Z"},
		},
		list: mustFixture(t, "list/page.json"),
	}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.reportedCount("T-01f00000-0000-7000-8000-000000000001"); got != 4 {
		t.Errorf("reportedCount = %d, want 4 from the list page", got)
	}
}

// TestDiscoverBadRow: a row with an unparseable updatedAt or no id is a
// per-item diagnostic; the good rows still enumerate.
func TestDiscoverBadRow(t *testing.T) {
	// Serve the malformed page to whichever window covers 2026-10-08 —
	// the grid alignment is the source's own stepping, so the fake keys
	// on the parsed bounds rather than a hardcoded string.
	covering, _ := time.Parse(time.RFC3339Nano, "2026-10-08T12:00:00.000Z")
	f := &fakeCLI{
		t: t,
		searchFn: func(query string) ([]byte, bool) {
			q := parseSearchQuery(t, query)
			if q.id == "" && !covering.Before(q.after) && covering.Before(q.before) {
				return []byte(`[
					{"id":"T-good","title":"g","updatedAt":"2026-10-08T10:00:00.000Z"},
					{"id":"T-bad","title":"b","updatedAt":"not-a-date"},
					{"title":"no id","updatedAt":"2026-10-08T11:00:00.000Z"}
				]`), true
			}
			return nil, false
		},
		catalog: []fakeThread{},
	}
	found, diags, err := fixtureSource(t, f).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 1 || found[0].NativeID != "T-good" {
		t.Fatalf("found = %v", found)
	}
	if len(diags) != 2 {
		t.Fatalf("want 2 row diagnostics, got %v", diags)
	}
}
