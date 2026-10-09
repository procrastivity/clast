package amp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// stateFile is the checkpoint's expected location under the pinned
// cacheDir seam — the scope-keyed name resolved the same way the
// source resolves it.
func stateFile(t *testing.T, s *Source) string {
	t.Helper()
	path := s.scanStatePath()
	if path == "" {
		t.Fatal("scanStatePath resolved empty under a pinned cacheDir")
	}
	return path
}

// stateFiles globs every scope-keyed checkpoint under the pinned cache
// dir — scope-isolation tests want the whole set, not one scope's file.
func stateFiles(t *testing.T, s *Source) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(s.cacheDir, "scan-state-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(matches)
	return matches
}

// readScanState parses the checkpoint file — a missing or malformed one
// is a test failure here (the production path degrades; the test wants
// to know).
func readScanState(t *testing.T, s *Source) scanState {
	t.Helper()
	data, err := os.ReadFile(stateFile(t, s))
	if err != nil {
		t.Fatalf("scan-state read: %v", err)
	}
	var st scanState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("scan-state parse: %v", err)
	}
	return st
}

// searchQueries returns the DSL string of every `threads search` call
// the fake saw, in order.
func searchQueries(f *fakeCLI) []string {
	var out []string
	for _, c := range f.calls {
		if len(c) >= 3 && c[0] == "threads" && c[1] == "search" {
			out = append(out, c[2])
		}
	}
	return out
}

// TestDiscoverWritesCheckpoint pins the first-run write: a clean full
// scan lands the floor at the scan's own start instant and seeds the
// pending set with every emitted id — emitted-but-unconfirmed.
func TestDiscoverWritesCheckpoint(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
			{id: "T-b", updatedAt: "2026-09-30T08:00:00.000Z"},
		},
	}
	s := fixtureSource(t, f)
	found, diags, err := s.Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover: err=%v diags=%v", err, diags)
	}
	if len(found) != 2 {
		t.Fatalf("found = %v, want 2 threads", found)
	}
	st := readScanState(t, s)
	if !st.CleanThrough.Equal(fixtureNow) {
		t.Errorf("clean_through = %v, want the scan start %v", st.CleanThrough, fixtureNow)
	}
	if len(st.PendingIDs) != 2 {
		t.Fatalf("pending_ids = %v, want both emitted ids pending", st.PendingIDs)
	}
}

// TestDiscoverCatchupNarrowsScan pins the catch-up shape: the second
// scan covers only [day(floor)-1d, tomorrow) — one window — then the
// pending lane's by-id lookups for every still-unconfirmed id, then
// corroboration. Rows at or below the floor's instant drop from the
// windows; the id: lane emits them anyway.
func TestDiscoverCatchupNarrowsScan(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-old", updatedAt: "2026-09-30T08:00:00.000Z"},  // below floor-1d windows — id: lane only
			{id: "T-edge", updatedAt: "2026-10-09T15:00:00.000Z"}, // inside the window but below MinTime
		},
	}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	f.calls = nil

	found, diags, err := s.Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover 2: err=%v diags=%v", err, diags)
	}
	queries := searchQueries(f)
	want := []string{
		"after:2026-10-09 before:2026-10-11", // day(2026-10-10)-1d … tomorrow
		"id:T-edge",
		"id:T-old",
	}
	if len(queries) != len(want) {
		t.Fatalf("search queries = %v, want %v", queries, want)
	}
	for i := range want {
		if queries[i] != want[i] {
			t.Fatalf("search queries = %v, want %v", queries, want)
		}
	}
	if len(found) != 2 {
		t.Fatalf("catch-up found = %v, want both pending ids re-emitted", found)
	}
	// Neither settled — the id: lane answered each — so pending stays.
	if st := readScanState(t, s); len(st.PendingIDs) != 2 {
		t.Fatalf("pending_ids = %v, want both still pending", st.PendingIDs)
	}
}

// TestDiscoverPendingSkipsRedundantLookup: a pending id a window already
// re-emitted costs no id: call — the sink check is the lane's dedup.
func TestDiscoverPendingSkipsRedundantLookup(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-near", updatedAt: "2026-10-10T15:00:00.000Z"}, // above MinTime → window covers it
		},
	}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	f.calls = nil

	found, _, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	for _, q := range searchQueries(f) {
		if q == "id:T-near" {
			t.Error("pending id the window re-emitted should not cost an id: call")
		}
	}
	if len(found) != 1 || found[0].NativeID != "T-near" {
		t.Fatalf("found = %v, want T-near via the window", found)
	}
}

// TestDiscoverDrainsGoneAndSettled pins the drain witnesses: a pending
// id whose id: page comes back clean-empty leaves the set (the thread
// left the universe), and Unchanged drains an id the journal already
// holds at this revision.
func TestDiscoverDrainsGoneAndSettled(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-stays", updatedAt: "2026-09-30T08:00:00.000Z"},
			{id: "T-gone", updatedAt: "2026-09-29T08:00:00.000Z"},
			{id: "T-held", updatedAt: "2026-09-28T08:00:00.000Z"},
		},
	}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}

	// T-gone leaves the catalog → its id: page is clean-empty → drained.
	f.catalog = append(f.catalog[:1], f.catalog[2:]...)
	// T-held is confirmed committed in the journal → Unchanged drains it.
	prior := journal.Session{LastActiveAt: mustParseRFC(t, "2026-09-28T08:00:00.000Z")}
	unchanged, err := s.Unchanged(context.Background(),
		source.Discovered{NativeID: "T-held", Path: threadURL("T-held"), ModTime: mustParseRFC(t, "2026-09-28T08:00:00.000Z")},
		prior)
	if err != nil || !unchanged {
		t.Fatalf("Unchanged = %v, %v — same revision must be unchanged", unchanged, err)
	}

	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	st := readScanState(t, s)
	if len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-stays" {
		t.Fatalf("pending_ids = %v, want only T-stays", st.PendingIDs)
	}
}

// TestUnchangedIsFetchFree: the seam must answer without a single CLI
// call — a fetch would defeat the point. And it must never claim a
// newer revision is captured.
func TestUnchangedIsFetchFree(t *testing.T) {
	s := newTestSource(t, fixtureDataDir(t))
	s.run = func(context.Context, []string, callBudget) (cliResult, error) {
		t.Fatal("Unchanged made a CLI call")
		return cliResult{}, nil
	}
	prior := journal.Session{LastActiveAt: mustParseRFC(t, "2026-10-01T12:00:00.000Z")}

	// Same revision → unchanged.
	ok, err := s.Unchanged(context.Background(),
		source.Discovered{NativeID: "T-x", Path: threadURL("T-x"), ModTime: prior.LastActiveAt}, prior)
	if err != nil || !ok {
		t.Errorf("equal updatedAt/last_active_at: Unchanged = %v, %v, want true", ok, err)
	}
	// Older enumeration than the recorded revision → unchanged.
	ok, err = s.Unchanged(context.Background(),
		source.Discovered{NativeID: "T-x", Path: threadURL("T-x"), ModTime: prior.LastActiveAt.Add(-time.Hour)}, prior)
	if err != nil || !ok {
		t.Errorf("older updatedAt: Unchanged = %v, %v, want true", ok, err)
	}
	// Newer enumeration → changed.
	ok, err = s.Unchanged(context.Background(),
		source.Discovered{NativeID: "T-x", Path: threadURL("T-x"), ModTime: prior.LastActiveAt.Add(time.Second)}, prior)
	if err != nil || ok {
		t.Errorf("newer updatedAt: Unchanged = %v, %v, want false", ok, err)
	}
}

// TestCapturePendingDiscipline pins the retry-lane writes on the capture
// path: a retryable export failure keeps the id pending (a later sweep
// retries), while a terminal does-not-exist verdict drains it (the
// thread is gone for good — nothing to retry).
func TestCapturePendingDiscipline(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-flaky", updatedAt: "2026-10-01T08:00:00.000Z"},
			{id: "T-gone", updatedAt: "2026-10-01T08:00:00.000Z"},
		},
		results: map[string]cliResult{
			"threads export T-flaky": {code: 1, stderr: []byte("boom")},
			// T-gone has no export — the fake's own does-not-exist path.
		},
	}
	s := fixtureSource(t, f)
	found, _, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %v", found)
	}

	for _, d := range found {
		if _, _, err := s.Capture(context.Background(), d, dirWriter(t, t.TempDir())); err == nil {
			t.Fatalf("Capture(%s): want an error", d.NativeID)
		}
	}
	st := readScanState(t, s)
	if len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-flaky" {
		t.Fatalf("pending_ids = %v, want only the retryable failure kept", st.PendingIDs)
	}
}

// TestDiscoverCorruptStateRescans pins the contract's safe-rescan rule:
// a corrupt checkpoint file is the initial full scan, not an error.
func TestDiscoverCorruptStateRescans(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"}}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	f.calls = nil
	if err := os.WriteFile(stateFile(t, s), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	queries := searchQueries(f)
	if len(queries) == 0 || queries[0] != "after:2025-01-01 before:2025-01-08" {
		t.Fatalf("corrupt state did not rescan from the floor: %v", queries)
	}
}

// TestDiscoverDirtyWindowHoldsTheFloor pins the clean-window floor rule:
// a window that produced a diagnostic is never covered by the floor —
// the floor lands at the earliest dirty window's start so the next
// scan re-covers the whole span.
func TestDiscoverDirtyWindowHoldsTheFloor(t *testing.T) {
	f := &fakeCLI{
		t:       t,
		catalog: []fakeThread{{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"}},
		searches: map[string][]byte{
			// A row with no id — a per-item diagnostic in the first
			// window, so 2025-01-01 is the earliest dirty bound.
			"after:2025-01-01 before:2025-01-08": []byte(`[{"id":"","title":"x","updatedAt":"2025-01-02T00:00:00.000Z"}]`),
		},
	}
	s := fixtureSource(t, f)
	_, diags, err := s.Discover(context.Background())
	if err != nil || len(diags) == 0 {
		t.Fatalf("Discover 1: err=%v diags=%v, want a row diagnostic", err, diags)
	}
	if st := readScanState(t, s); !st.CleanThrough.Equal(historyFloor) {
		t.Fatalf("clean_through = %v, want the dirty window's start %v", st.CleanThrough, historyFloor)
	}
	f.calls = nil
	delete(f.searches, "after:2025-01-01 before:2025-01-08")

	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	queries := searchQueries(f)
	if len(queries) == 0 || queries[0] != "after:2024-12-31 before:2025-01-07" {
		t.Fatalf("dirty floor did not re-cover: first query %v", queries)
	}
}

// TestDiscoverAbsentBinaryWritesNoState pins the checkpoint's transport
// gate: an absent binary is quiet (nil, nil, nil) and writes nothing —
// a run that scanned nothing must not move the floor.
func TestDiscoverAbsentBinaryWritesNoState(t *testing.T) {
	s := newTestSource(t, fixtureDataDir(t))
	s.bin = filepath.Join(t.TempDir(), "no-such-amp") // unresolvable — realRun path
	found, diags, err := s.Discover(context.Background())
	if err != nil || len(found) != 0 || len(diags) != 0 {
		t.Fatalf("absent binary: found=%v diags=%v err=%v, want quiet", found, diags, err)
	}
	if _, err := os.Stat(stateFile(t, s)); !os.IsNotExist(err) {
		t.Fatalf("absent binary wrote scan-state: %v", err)
	}
}

// TestDiscoverPendingOverflowRescans pins the bound on the retry lane:
// an emitted set larger than the cap resets the checkpoint wholesale —
// the contract's overflow-forces-full-rescan posture.
func TestDiscoverPendingOverflowRescans(t *testing.T) {
	old := pendingCap
	pendingCap = 2
	defer func() { pendingCap = old }()

	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-1", updatedAt: "2026-10-01T08:00:00.000Z"},
			{id: "T-2", updatedAt: "2026-10-01T09:00:00.000Z"},
			{id: "T-3", updatedAt: "2026-10-01T10:00:00.000Z"},
		},
	}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	st := readScanState(t, s)
	if !st.CleanThrough.IsZero() || len(st.PendingIDs) != 0 {
		t.Fatalf("overflow should reset the checkpoint, got %+v", st)
	}
	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("post-overflow Discover: %v", err)
	}
	if q := searchQueries(f); len(q) == 0 || q[0] != "after:2025-01-01 before:2025-01-08" {
		t.Fatalf("overflow did not force a full rescan: %v", q)
	}
}

// TestDiscoverNoCacheDirDegrades: a source with no resolvable cache
// location still scans — the checkpoint is a cache, never a requirement.
func TestDiscoverNoCacheDirDegrades(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "") // os.UserHomeDir fails → nowhere to park state
	f := &fakeCLI{t: t, catalog: []fakeThread{{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"}}}
	s := fixtureSource(t, f)
	s.cacheDir = ""
	if s.scanStatePath() != "" {
		t.Fatal("scanStatePath should be empty with no resolvable cache home")
	}
	found, _, err := s.Discover(context.Background())
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover without a cache dir: found=%v err=%v", found, err)
	}
}

// dataDirWithAccount writes a state dir holding the pinned install id
// and an accounts.json whose active user is uid — the account axis of
// the scope key varied alone.
func dataDirWithAccount(t *testing.T, uid string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device-id.json"), mustFixture(t, "identity/device-id.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	accts := `{"version":1,"accounts":[{"serverURL":"https://ampcode.com/","userID":"` + uid + `"}],"active":{"https://ampcode.com/":"` + uid + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(accts), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rescope swings the source at a different data dir — the account or
// install changed under it, so the cached scope and install evidence
// recompute.
func rescope(s *Source, dataDir string) {
	s.dataDir = dataDir
	s.scopeOK, s.installOK = false, false
}

// readScanStateFile parses one checkpoint file by path — for the scope
// isolation assertions that read a scope's file after the source has
// moved on to another.
func readScanStateFile(t *testing.T, path string) scanState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scan-state read %s: %v", path, err)
	}
	var st scanState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("scan-state parse %s: %v", path, err)
	}
	return st
}

// TestScanStateScopeAccountIsolation pins the account axis of the scope
// key: a different ambient account gets a different checkpoint file —
// its own fresh rescan — and this account's pending set is never
// probed, spent, or destroyed. Switching back resumes the file exactly
// as it was left.
func TestScanStateScopeAccountIsolation(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)                    // user_fixture1 — account A
	dirA := s.dataDir                           // the fixture's own account-A dir
	dirB := dataDirWithAccount(t, "user_other") // same install, other account
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover under account A: %v", err)
	}
	fileA := stateFile(t, s)
	if got := readScanState(t, s); len(got.PendingIDs) != 1 || got.PendingIDs[0] != "T-a" {
		t.Fatalf("account A pending = %v, want [T-a]", got.PendingIDs)
	}

	// Account B: the checkpoint rescopes — a fresh file and a rescan
	// from the floor, with A's file untouched.
	rescope(s, dirB)
	f.catalog = []fakeThread{{id: "T-b", updatedAt: "2026-10-02T08:00:00.000Z"}}
	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover under account B: %v", err)
	}
	fileB := stateFile(t, s)
	if fileB == fileA {
		t.Fatal("account switch did not rescope the checkpoint")
	}
	if queries := searchQueries(f); len(queries) == 0 || queries[0] != "after:2025-01-01 before:2025-01-08" {
		t.Fatalf("account switch did not rescan from the floor: %v", queries)
	}
	for _, q := range searchQueries(f) {
		if q == "id:T-a" {
			t.Error("account B probed account A's pending id")
		}
	}
	if got := readScanStateFile(t, fileA); len(got.PendingIDs) != 1 || got.PendingIDs[0] != "T-a" {
		t.Fatalf("account A's file was disturbed: %v", got.PendingIDs)
	}
	if got := readScanStateFile(t, fileB); len(got.PendingIDs) != 1 || got.PendingIDs[0] != "T-b" {
		t.Fatalf("account B pending = %v, want [T-b]", got.PendingIDs)
	}

	// Switching back resumes A exactly — its pending id is still owed
	// and re-probed by A's own scope.
	rescope(s, dirA)
	f.catalog = []fakeThread{{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"}}
	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover back under account A: %v", err)
	}
	if stateFile(t, s) != fileA {
		t.Fatal("switching back did not resume account A's checkpoint")
	}
	found := false
	for _, q := range searchQueries(f) {
		if q == "id:T-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("A's pending id was not re-probed on switch-back: %v", searchQueries(f))
	}
}

// TestScanStateScopeJournalAndInstall pin the other two scope axes:
// the bound journal target and the local install id each rescope the
// checkpoint independently of the account.
func TestScanStateScopeJournalAndInstall(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	journalA, journalB := t.TempDir(), t.TempDir()

	s.ScopeJournal(journalA)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	fileJA := stateFile(t, s)

	// A different journal target gets a different checkpoint — a run
	// pointed elsewhere never inherits this journal's progress.
	s.ScopeJournal(journalB)
	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover on journal B: %v", err)
	}
	fileJB := stateFile(t, s)
	if fileJB == fileJA {
		t.Fatal("journal switch did not rescope the checkpoint")
	}
	if queries := searchQueries(f); len(queries) == 0 || queries[0] != "after:2025-01-01 before:2025-01-08" {
		t.Fatalf("journal switch did not rescan from the floor: %v", queries)
	}
	if got := readScanStateFile(t, fileJA); len(got.PendingIDs) != 1 {
		t.Fatalf("journal A's file was disturbed: %v", got.PendingIDs)
	}

	// Same journal, but the install id changed — a reinstall is a new
	// source scope and starts over.
	dirC := dataDirWithAccount(t, "user_fixture1")
	if err := os.WriteFile(filepath.Join(dirC, "device-id.json"),
		[]byte(`{"installationID":"99999999-0000-4000-8000-000000000000"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rescope(s, dirC)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover on new install: %v", err)
	}
	if fileJC := stateFile(t, s); fileJC == fileJA || fileJC == fileJB {
		t.Fatal("install change did not rescope the checkpoint")
	}
	if files := stateFiles(t, s); len(files) != 3 {
		t.Fatalf("want three scope files (journalA, journalB+install1, journalB+install2), got %v", files)
	}
}

// TestScanStateUnattributedNeverKeepsFloor pins the no-evidence rule:
// when no account discriminator exists at all (no accounts.json, no
// AMP_API_KEY, no credential files), the checkpoint still records
// pending bookkeeping but the clean-through floor never persists — a
// floor that cannot be told apart from another account's could hide a
// whole history. Every run rescans from historyFloor.
func TestScanStateUnattributedNeverKeepsFloor(t *testing.T) {
	dir := t.TempDir() // install id only — no account evidence anywhere
	if err := os.WriteFile(filepath.Join(dir, "device-id.json"), mustFixture(t, "identity/device-id.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := newTestSource(t, dir)
	s.run = f.run
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	st := readScanState(t, s)
	if !st.CleanThrough.IsZero() {
		t.Fatalf("unattributed scope persisted a floor: %v", st.CleanThrough)
	}
	if len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-a" {
		t.Fatalf("pending bookkeeping lost: %v", st.PendingIDs)
	}
	if !strings.Contains(st.Scope, "install:") || strings.Contains(st.Scope, "acct:") {
		t.Errorf("scope descriptor = %q, want install-only evidence", st.Scope)
	}

	f.calls = nil
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	if queries := searchQueries(f); len(queries) == 0 || queries[0] != "after:2025-01-01 before:2025-01-08" {
		t.Fatalf("unattributed scope did not rescan from the floor: %v", queries)
	}
}

// TestScanStatePhaseRecorded pins the phase provenance: the checkpoint
// written by the floor scan says "initial"; one written by a scan that
// continued from a committed floor says "catchup".
func TestScanStatePhaseRecorded(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	if st := readScanState(t, s); st.Phase != phaseInitial {
		t.Fatalf("phase after initial scan = %q, want %q", st.Phase, phaseInitial)
	}
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	if st := readScanState(t, s); st.Phase != phaseCatchup {
		t.Fatalf("phase after catch-up scan = %q, want %q", st.Phase, phaseCatchup)
	}
}

// TestDiscoverCorroborationMissQueuesPending pins the checkpoint-safety
// rule for enumeration gaps: an id list sees but the windows miss is
// diagnosed AND queued into pending — the floor may cross its window
// because its capture is recoverably queued, never silently skipped.
// The next sweep's id: lane re-probes it.
func TestDiscoverCorroborationMissQueuesPending(t *testing.T) {
	f := &fakeCLI{
		t: t,
		catalog: []fakeThread{
			{id: "T-seen", updatedAt: "2026-10-08T21:00:00.000Z"},
		},
		list: []byte(`[{"id":"T-seen","messageCount":1},{"id":"T-missed","messageCount":3}]`),
	}
	s := fixtureSource(t, f)
	found, diags, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 1 || found[0].NativeID != "T-seen" {
		t.Fatalf("found = %v — the missed id is queued, not emitted on stale metadata", found)
	}
	var miss int
	for _, dg := range diags {
		if strings.Contains(dg.Err.Error(), "not enumerated") {
			miss++
		}
	}
	if miss != 1 {
		t.Fatalf("want the enumeration-gap diagnostic, got %v", diags)
	}
	st := readScanState(t, s)
	if len(st.PendingIDs) != 2 {
		t.Fatalf("pending_ids = %v — the missed id must be queued", st.PendingIDs)
	}

	// The queue delivers: the next scan's id: lane probes it, and a
	// catalog row appearing for it enumerates+captures normally.
	f.catalog = append(f.catalog, fakeThread{id: "T-missed", updatedAt: "2026-10-09T08:00:00.000Z"})
	f.calls = nil
	found, _, err = s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	var probed bool
	for _, q := range searchQueries(f) {
		if q == "id:T-missed" {
			probed = true
		}
	}
	if !probed {
		t.Fatalf("queued miss was never re-probed: %v", searchQueries(f))
	}
	got := discoveredIDs(found)
	if _, ok := got["T-missed"]; !ok {
		t.Fatalf("queued miss did not enumerate once it became searchable: %v", got)
	}
}

// TestScanStateMergePreservesForeignPending pins the three-way merge in
// recordScan: pending added by another writer between this scan's hint
// read and its checkpoint write is kept, never clobbered by the stale
// base.
func TestScanStateMergePreservesForeignPending(t *testing.T) {
	dataDir := fixtureDataDir(t)
	journalDir := t.TempDir()
	f1 := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s1 := newTestSource(t, dataDir)
	s1.run = f1.run
	s1.ScopeJournal(journalDir)
	if _, _, err := s1.Discover(context.Background()); err != nil {
		t.Fatalf("s1 Discover: %v", err)
	}
	if got := readScanState(t, s1); len(got.PendingIDs) != 1 || got.PendingIDs[0] != "T-a" {
		t.Fatalf("s1 pending = %v, want [T-a]", got.PendingIDs)
	}

	// s2 is the "other process": same scope (data dir, journal target)
	// and the same checkpoint file, sharing only the cache dir. Its
	// catalog row sits inside the catch-up window AND above MinTime —
	// the floor's instant is fixtureNow, so 13:00 the same day is what
	// a fresh discovery looks like under it.
	f2 := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-b", updatedAt: "2026-10-10T13:00:00.000Z"},
	}}
	s2 := newTestSource(t, dataDir)
	s2.run = f2.run
	s2.cacheDir = s1.cacheDir
	s2.ScopeJournal(journalDir)
	if stateFile(t, s2) != stateFile(t, s1) {
		t.Fatal("same scope must resolve the same checkpoint file")
	}
	// s1 lands a foreign pending write between s2's hint read (its own
	// Discover load) and s2's locked checkpoint write.
	s2.beforeRMW = func() { s1.notePending("T-c") }
	if _, _, err := s2.Discover(context.Background()); err != nil {
		t.Fatalf("s2 Discover: %v", err)
	}
	st := readScanState(t, s2)
	want := []string{"T-b", "T-c"}
	if len(st.PendingIDs) != 2 || st.PendingIDs[0] != want[0] || st.PendingIDs[1] != want[1] {
		t.Fatalf("pending_ids = %v, want %v — s1's concurrent write must survive s2's checkpoint", st.PendingIDs, want)
	}
	// And T-a drained honestly: s2's own id: lane proved it absent from
	// f2's catalog — the merge keeps foreign work, it does not resurrect.
	for _, id := range st.PendingIDs {
		if id == "T-a" {
			t.Error("T-a should have drained — s2's id: lane proved it gone")
		}
	}
}

// TestScanStateLockMutualExclusion pins the lockfile's blocking half: a
// held <file>.lock makes a writer wait; once released the write lands.
func TestScanStateLockMutualExclusion(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	lock := stateFile(t, s) + ".lock"
	if err := os.WriteFile(lock, []byte("foreign holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = os.Remove(lock)
		close(released)
	}()
	start := time.Now()
	s.notePending("T-x")
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("notePending did not wait on the held lock (elapsed %v)", elapsed)
	}
	<-released
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock file left behind: %v", err)
	}
	if st := readScanState(t, s); !slices.Contains(st.PendingIDs, "T-x") {
		t.Fatalf("pending_ids = %v, want T-x added after the release", st.PendingIDs)
	}
}

// TestScanStateStaleLockStolen pins stale-lock takeover: a lock whose
// age no live RMW can explain is removed and the write proceeds — a
// crashed run can never wedge the checkpoint.
func TestScanStateStaleLockStolen(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	lock := stateFile(t, s) + ".lock"
	if err := os.WriteFile(lock, []byte("crashed run"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-scanLockStaleAge - time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.notePending("T-y")
	if elapsed := time.Since(start); elapsed > scanLockWait {
		t.Fatalf("stale lock was not stolen promptly (elapsed %v)", elapsed)
	}
	if st := readScanState(t, s); !slices.Contains(st.PendingIDs, "T-y") {
		t.Fatalf("pending_ids = %v, want T-y", st.PendingIDs)
	}
}

// TestScanStateLockUnwritableDegrades pins the cache posture on the
// lock itself: a lock that cannot be created degrades to an unlocked
// best-effort write — a sweep may never hang or fail on bookkeeping.
func TestScanStateLockUnwritableDegrades(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-a", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	// The cache dir is a FILE — neither the lock nor the state file can
	// be created inside it; every step degrades quietly.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.cacheDir = blocker
	done := make(chan struct{})
	go func() { s.notePending("T-z"); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notePending hung on an unwritable lock dir")
	}
}

// TestDiscoverUpdatedAtTieAtFloor pins the equal-updatedAt edge: a row
// at exactly the checkpoint instant is emitted by its window (it IS in
// coverage), stays pending, and next run — filtered out of the windows
// by MinTime — is still re-emitted by the id: lane. The tie is covered
// by pending-is-emitted, never by window luck.
func TestDiscoverUpdatedAtTieAtFloor(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-tie", updatedAt: "2026-10-10T12:00:00.000Z"}, // exactly fixtureNow, the scan start
	}}
	s := fixtureSource(t, f)
	found, _, err := s.Discover(context.Background())
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover 1: found=%v err=%v", found, err)
	}
	f.calls = nil

	found, _, err = s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	var probed bool
	for _, q := range searchQueries(f) {
		if q == "id:T-tie" {
			probed = true
		}
	}
	if !probed {
		t.Fatalf("tie-at-floor id was not re-probed: %v", searchQueries(f))
	}
	if len(found) != 1 || found[0].NativeID != "T-tie" {
		t.Fatalf("found = %v — the floor-tie thread must stay reachable", found)
	}
}

// TestDiscoverCatchupSpansLongOutage pins the >window outage: a sweep
// absent for far longer than the 7-day stepping window still re-covers
// the whole gap — the catch-up's first window starts at the floor's
// overlap day and the ascending windows span the outage, so a thread
// updated mid-gap is found rather than silently skipped.
func TestDiscoverCatchupSpansLongOutage(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-old", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	if _, _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("Discover 1: %v", err)
	}

	later := fixtureNow.AddDate(0, 0, 45) // a 45-day outage
	s.now = func() time.Time { return later }
	f.catalog = append(f.catalog, fakeThread{id: "T-gap", updatedAt: "2026-10-30T09:00:00.000Z"})
	f.calls = nil

	found, _, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	queries := searchQueries(f)
	if len(queries) == 0 || queries[0] != "after:2026-10-09 before:2026-10-16" {
		t.Fatalf("catch-up did not resume at the floor's overlap day: %v", queries)
	}
	got := discoveredIDs(found)
	if _, ok := got["T-gap"]; !ok {
		t.Fatalf("mid-gap update not enumerated — the outage span was not re-covered: %v", got)
	}
	if _, ok := got["T-old"]; !ok {
		t.Fatalf("pending id dropped across the outage: %v", got)
	}
	if st := readScanState(t, s); !st.CleanThrough.Equal(later) {
		t.Errorf("clean_through = %v, want the new scan start %v", st.CleanThrough, later)
	}
}

// TestDiscoverEqualUpdatedAtOrdering pins deterministic emission order
// for equal-ms updatedAt rows: the (ModTime, NativeID) sort keys them
// stably — the tie is an ordering curiosity, never a correctness lever.
func TestDiscoverEqualUpdatedAtOrdering(t *testing.T) {
	f := &fakeCLI{t: t, catalog: []fakeThread{
		{id: "T-bbb", updatedAt: "2026-10-01T08:00:00.000Z"},
		{id: "T-aaa", updatedAt: "2026-10-01T08:00:00.000Z"},
	}}
	s := fixtureSource(t, f)
	found, _, err := s.Discover(context.Background())
	if err != nil || len(found) != 2 {
		t.Fatalf("Discover: found=%v err=%v", found, err)
	}
	if found[0].NativeID != "T-aaa" || found[1].NativeID != "T-bbb" {
		t.Fatalf("equal-updatedAt order = %v, want id-sorted", found)
	}
}

func mustParseRFC(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}
