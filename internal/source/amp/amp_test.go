package amp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// fixtureNow pins the enumeration upper bound: windows run from
// historyFloor through 2026-10-11, covering every fixture date.
var fixtureNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// fixtureInstallID is identity/device-id.json's uuid — the "local"
// install the synthesized fixtures share.
const fixtureInstallID = "11111111-2222-4333-8444-555555555555"

// mustFixture reads a file under testdata/.
func mustFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fixtureAccountsJSON is the accounts.json fixture sources carry — the
// install is logged into one fixture account on the production server,
// which makes the scope attributed (a floor may persist). Scope tests
// swap it or drop it for the other account-evidence tiers.
const fixtureAccountsJSON = `{"version":1,"accounts":[{"serverURL":"https://ampcode.com/","userID":"user_fixture1"}],"active":{"https://ampcode.com/":"user_fixture1"}}`

// fixtureDataDir builds a state dir holding the pinned device-id.json
// plus a fixture accounts.json — the non-secret account evidence the
// scan-state scope keys on. (No credential material — Present is tested
// separately.)
func fixtureDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device-id.json"), mustFixture(t, "identity/device-id.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(fixtureAccountsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newTestSource returns a Source with every runtime seam pinned: fixed
// clock, no-op retry sleep, fixed hostname, a per-test cache dir (the
// scan-state checkpoint stays hermetic), no ambient AMP_API_KEY (the
// scope's env evidence would otherwise depend on the dev environment),
// and a caller-supplied data dir. run is left for the test to set (fake
// or real-runner).
func newTestSource(t *testing.T, dataDir string) *Source {
	t.Helper()
	t.Setenv("AMP_API_KEY", "")
	s := NewAt("", dataDir)
	s.now = func() time.Time { return fixtureNow }
	s.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	s.hostname = func() (string, error) { return "fixture-host", nil }
	s.cacheDir = t.TempDir()
	return s
}

// discoveredForID builds the Discovered Discover would emit for a
// fixture export doc — NativeID + URL Path + its own updatedAt.
func discoveredForID(t *testing.T, raw []byte) source.Discovered {
	t.Helper()
	doc, err := ParseExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := time.Parse(time.RFC3339Nano, doc.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return source.Discovered{NativeID: doc.ID, Path: threadURL(doc.ID), ModTime: ts}
}

// dirWriter binds source.WriteArtifact to a plain directory — the test
// stand-in for the journal primitive the capture verb binds (same as
// claude's).
func dirWriter(t *testing.T, dir string) source.WriteArtifact {
	t.Helper()
	return func(relPath string, r io.Reader) error {
		path := filepath.Join(dir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			return err
		}
		return f.Close()
	}
}

// fakeThread is one catalog row with the boolean facets subdivision
// filters on.
type fakeThread struct {
	id        string
	updatedAt string
	archived  bool
	pinned    bool
}

// fakeCLI is the deterministic transport seam. Consulted in order:
//   - hook: short-circuits everything (timeout/cancellation fakes)
//   - results: canned cliResult keyed by space-joined argv (errors,
//     malformed payloads, exit codes)
//   - searchFn: a query-aware responder (receives the DSL string)
//   - searches: exact-query → page bytes
//   - catalog: a thread set filtered through real window/predicate
//     semantics, silently truncating pages at 100 like the server
//   - exports/exportSeq/list: per-verb canned payloads
//
// calls records argv for assertions that count fetches — never order.
type fakeCLI struct {
	t         *testing.T
	hook      func(ctx context.Context, args []string) (cliResult, error)
	results   map[string]cliResult
	searchFn  func(query string) (page []byte, ok bool)
	searches  map[string][]byte
	catalog   []fakeThread
	exports   map[string][]byte
	exportSeq map[string][]cliResult
	list      []byte
	calls     [][]string
}

func (f *fakeCLI) run(ctx context.Context, args []string, _ callBudget) (cliResult, error) {
	f.calls = append(f.calls, args)
	if f.hook != nil {
		return f.hook(ctx, args)
	}
	key := strings.Join(args, " ")
	if res, ok := f.results[key]; ok {
		return res, nil
	}
	if len(args) < 2 || args[0] != "threads" {
		f.t.Fatalf("fakeCLI: unexpected argv %v", args)
	}
	switch args[1] {
	case "search":
		if len(args) != 6 || args[3] != "-n" || args[4] != "100" || args[5] != "--json" {
			f.t.Fatalf("fakeCLI: unexpected search argv %v", args)
		}
		query := args[2]
		if f.searchFn != nil {
			if page, ok := f.searchFn(query); ok {
				return cliResult{stdout: page}, nil
			}
		}
		if page, ok := f.searches[query]; ok {
			return cliResult{stdout: page}, nil
		}
		if f.catalog != nil {
			return cliResult{stdout: f.catalogPage(query)}, nil
		}
		return cliResult{stdout: []byte("[]")}, nil
	case "export":
		if len(args) != 3 {
			f.t.Fatalf("fakeCLI: unexpected export argv %v", args)
		}
		id := args[2]
		if seq, ok := f.exportSeq[id]; ok && len(seq) > 0 {
			res := seq[0]
			f.exportSeq[id] = seq[1:]
			return res, nil
		}
		if b, ok := f.exports[id]; ok {
			return cliResult{stdout: b}, nil
		}
		return cliResult{code: 1, stderr: []byte("Error: Thread " + id + " does not exist.")}, nil
	case "list":
		if f.list != nil {
			return cliResult{stdout: f.list}, nil
		}
		return cliResult{stdout: []byte("[]")}, nil
	}
	f.t.Fatalf("fakeCLI: unexpected argv %v", args)
	return cliResult{}, nil
}

// exportCalls counts `threads export` invocations — the observable
// "fetched N times" signal for retry and memo tests.
func (f *fakeCLI) exportCalls() int {
	n := 0
	for _, c := range f.calls {
		if len(c) >= 2 && c[0] == "threads" && c[1] == "export" {
			n++
		}
	}
	return n
}

// searchQuery is the parsed form of a DSL string the source emits —
// shared by the catalog filter and tests that need window bounds.
type searchQuery struct {
	after, before    time.Time
	archived, pinned *bool
	id               string
}

// parseSearchQuery parses the clast-generated DSL. Any other token —
// including the garbage forms the server would silently ignore — is a
// test failure: the source must only ever emit validated syntax.
func parseSearchQuery(t *testing.T, query string) searchQuery {
	t.Helper()
	var q searchQuery
	for _, tok := range strings.Fields(query) {
		switch {
		case strings.HasPrefix(tok, "after:"):
			q.after = mustDay(t, strings.TrimPrefix(tok, "after:"))
		case strings.HasPrefix(tok, "before:"):
			q.before = mustDay(t, strings.TrimPrefix(tok, "before:"))
		case strings.HasPrefix(tok, "archived:"):
			b := mustBool(t, strings.TrimPrefix(tok, "archived:"))
			q.archived = &b
		case strings.HasPrefix(tok, "pinned:"):
			b := mustBool(t, strings.TrimPrefix(tok, "pinned:"))
			q.pinned = &b
		case strings.HasPrefix(tok, "id:"):
			q.id = strings.TrimPrefix(tok, "id:")
		default:
			t.Fatalf("unparseable query token %q in %q", tok, query)
		}
	}
	return q
}

// catalogPage answers a search query by filtering the catalog through
// the server's real semantics: after: inclusive, before: exclusive, UTC
// day bounds, archived:/pinned: facets, id: exact — and the silent
// 100-row cap.
func (f *fakeCLI) catalogPage(query string) []byte {
	f.t.Helper()
	q := parseSearchQuery(f.t, query)
	var rows []map[string]string
	for _, th := range f.catalog {
		if q.id != "" {
			if th.id != q.id {
				continue
			}
		} else {
			ts, err := time.Parse(time.RFC3339Nano, th.updatedAt)
			if err != nil {
				f.t.Fatalf("fakeCLI: catalog row %s bad updatedAt: %v", th.id, err)
			}
			if ts.Before(q.after) || !ts.Before(q.before) {
				continue
			}
		}
		if q.archived != nil && th.archived != *q.archived {
			continue
		}
		if q.pinned != nil && th.pinned != *q.pinned {
			continue
		}
		rows = append(rows, map[string]string{
			"id": th.id, "title": "t " + th.id, "updatedAt": th.updatedAt,
		})
	}
	if len(rows) > searchPageCap {
		rows = rows[:searchPageCap] // the silent cap, exactly as the server emits it
	}
	if rows == nil {
		rows = []map[string]string{}
	}
	page, err := json.Marshal(rows)
	if err != nil {
		f.t.Fatal(err)
	}
	return page
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad day %q: %v", s, err)
	}
	return d
}

func mustBool(t *testing.T, s string) bool {
	t.Helper()
	b, err := strconv.ParseBool(s)
	if err != nil {
		t.Fatalf("bad bool %q: %v", s, err)
	}
	return b
}

// catalogFromFixtures builds catalog rows from a search-page fixture,
// applying archived tags by membership in the two subdivide sets.
func catalogBoundary100(t *testing.T) []fakeThread {
	t.Helper()
	rows := decodeSearchPage(t, mustFixture(t, "search/boundary-100.json"))
	archivedIDs := map[string]bool{}
	for _, r := range decodeSearchPage(t, mustFixture(t, "search/subdivide-100-archived-true.json")) {
		archivedIDs[r.ID] = true
	}
	for _, r := range decodeSearchPage(t, mustFixture(t, "search/subdivide-100-archived-false.json")) {
		if _, ok := archivedIDs[r.ID]; ok {
			t.Fatalf("id %s in both archived subdivisions", r.ID)
		}
	}
	var cat []fakeThread
	for _, r := range rows {
		cat = append(cat, fakeThread{id: r.ID, updatedAt: r.UpdatedAt, archived: archivedIDs[r.ID]})
	}
	return cat
}

func decodeSearchPage(t *testing.T, raw []byte) []searchRow {
	t.Helper()
	var rows []searchRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// synthCatalog builds n catalog threads on one UTC day with the given
// facets, spaced a minute apart.
func synthCatalog(n, start int, day string, archived, pinned bool) []fakeThread {
	cat := make([]fakeThread, 0, n)
	for i := 0; i < n; i++ {
		cat = append(cat, fakeThread{
			id:        fmt.Sprintf("T-synth-%06d", start+i),
			updatedAt: fmt.Sprintf("%sT%02d:%02d:00.000Z", day, (start+i)/60, (start+i)%60),
			archived:  archived,
			pinned:    pinned,
		})
	}
	return cat
}

// TestLocalInstallID: the identity read is the device-id.json
// installationID — cached once, and "" (not an error) when the file is
// absent or malformed: a machine with no readable install id treats
// every thread as foreign.
func TestLocalInstallID(t *testing.T) {
	s := newTestSource(t, fixtureDataDir(t))
	if got := s.localInstallID(); got != fixtureInstallID {
		t.Errorf("localInstallID = %q, want fixture %q", got, fixtureInstallID)
	}
	s2 := newTestSource(t, t.TempDir()) // no device-id.json
	if got := s2.localInstallID(); got != "" {
		t.Errorf("localInstallID with no file = %q, want \"\"", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device-id.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newTestSource(t, dir).localInstallID(); got != "" {
		t.Errorf("localInstallID with malformed file = %q, want \"\"", got)
	}
}

// TestSourceBasics pins the offline interface duties: the M11 name and
// the M13 network model that gates sweeps.
func TestSourceBasics(t *testing.T) {
	s := New()
	if s.Name() != "amp" {
		t.Errorf("Name() = %q, want amp", s.Name())
	}
	if s.Model() != source.Network {
		t.Errorf("Model() = %q, want network", s.Model())
	}
}

// writeExecutable drops a minimal shell stub and returns its path — the
// "binary exists" half of Present's probe.
func writeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "amp-stub")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPresent(t *testing.T) {
	bin := writeExecutable(t, "#!/bin/sh\nexit 0\n")

	// Credential material present via a file → present.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewAt(bin, dir).Present(context.Background()); err != nil {
		t.Errorf("Present with secrets.json: %v", err)
	}
	// …via accounts.json and oauth/ too.
	for _, name := range []string{"accounts.json", "oauth"} {
		d := t.TempDir()
		if name == "oauth" {
			err := os.Mkdir(filepath.Join(d, name), 0o700)
			if err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(d, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := NewAt(bin, d).Present(context.Background()); err != nil {
			t.Errorf("Present with %s: %v", name, err)
		}
	}
	// …or via the env var alone.
	t.Setenv("AMP_API_KEY", "tok")
	if err := NewAt(bin, t.TempDir()).Present(context.Background()); err != nil {
		t.Errorf("Present with AMP_API_KEY: %v", err)
	}
}

func TestPresentNoCredentials(t *testing.T) {
	bin := writeExecutable(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("AMP_API_KEY", "")
	dir := t.TempDir() // nothing in it
	err := NewAt(bin, dir).Present(context.Background())
	if err == nil {
		t.Fatal("Present with binary but no credential material returned nil")
	}
	for _, want := range []string{"secrets.json", "accounts.json", "oauth", dir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Present error %q should name probed %q", err, want)
		}
	}
}

func TestPresentNoBinary(t *testing.T) {
	err := NewAt(filepath.Join(t.TempDir(), "no-such-amp"), t.TempDir()).Present(context.Background())
	if err == nil {
		t.Fatal("Present with unresolvable binary returned nil")
	}
	if !strings.Contains(err.Error(), "no-such-amp") {
		t.Errorf("Present error %q should name the probed binary", err)
	}
}
