package devin

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// buildStore replays a testdata/stores/*.sql file — one statement per
// line — into a disposable database under t.TempDir and returns its
// path. Tests never open a real Devin store, a sibling repository, or
// the sqlite3 CLI; the driver under test builds the fixture itself.
func buildStore(t *testing.T, fixture string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	execFixture(t, db, fixture)
	return path
}

func execFixture(t *testing.T, db *sql.DB, fixture string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "stores", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := db.Exec(line); err != nil {
			t.Fatalf("%s line %d: %v\n%s", fixture, i+1, err, line)
		}
	}
}

// buildStoreWithInsertOrder builds a fixture store with the statement
// lines permuted by reorder — proving exports depend on content, not
// physical layout.
func buildStoreWithInsertOrder(t *testing.T, fixture string, reorder func([]string) []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	data, err := os.ReadFile(filepath.Join("testdata", "stores", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	for i, line := range reorder(lines) {
		if _, err := db.Exec(line); err != nil {
			t.Fatalf("%s reordered line %d: %v\n%s", fixture, i+1, err, line)
		}
	}
	return path
}

func mustDiscover(t *testing.T, s *Source) []source.Discovered {
	t.Helper()
	found, diags, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, d := range diags {
		t.Fatalf("unexpected diagnostic: %s: %v", d.Path, d.Err)
	}
	return found
}

func TestDiscoverAbsentIsQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	found, diags, err := NewAt(path).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if found != nil || diags != nil {
		t.Fatalf("absent store: found=%v diags=%v, want both nil", found, diags)
	}
	// mode=ro must never create the file it was asked to read.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Discover created the absent store: stat err %v", err)
	}
}

func TestDiscoverBasic(t *testing.T) {
	found := mustDiscover(t, NewAt(buildStore(t, "basic.sql")))
	if len(found) != 1 {
		t.Fatalf("found %d sessions, want 1", len(found))
	}
	d := found[0]
	if d.NativeID != "quiet-meadow" {
		t.Errorf("NativeID = %q", d.NativeID)
	}
	if !strings.HasSuffix(d.Path, "#quiet-meadow") || !strings.Contains(d.Path, "sessions.db") {
		t.Errorf("Path = %q, want <store>#quiet-meadow", d.Path)
	}
	if want := time.Unix(1791600120, 0).UTC(); !d.ModTime.Equal(want) {
		t.Errorf("ModTime = %v, want %v", d.ModTime, want)
	}
}

func TestDiscoverEdgeCasesEnumeratesHiddenAndEmpty(t *testing.T) {
	found := mustDiscover(t, NewAt(buildStore(t, "edge-cases.sql")))
	want := []string{"amber-fox", "brisk-owl", "empty-rook", "hidden-wren"}
	if len(found) != len(want) {
		t.Fatalf("found %d sessions, want %d", len(found), len(want))
	}
	for i, d := range found {
		if d.NativeID != want[i] {
			t.Errorf("found[%d] = %q, want %q (ORDER BY id)", i, d.NativeID, want[i])
		}
	}
}

func TestDiscoverIncompatibleIsFailureNotEmpty(t *testing.T) {
	found, _, err := NewAt(buildStore(t, "incompatible.sql")).Discover(context.Background())
	if err == nil {
		t.Fatal("incompatible store must fail, not enumerate empty")
	}
	if found != nil {
		t.Errorf("found = %v on failure", found)
	}
	if !strings.Contains(err.Error(), "incompatible") {
		t.Errorf("error = %v, want it to name the incompatibility", err)
	}
}

func TestDiscoverCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := NewAt(buildStore(t, "basic.sql")).Discover(ctx)
	if err == nil {
		t.Fatal("canceled context must abort discovery")
	}
}

func TestPresent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "sessions.db")
	if err := NewAt(missing).Present(context.Background()); err == nil {
		t.Fatal("absent store must fail Present")
	}
	if err := NewAt(buildStore(t, "basic.sql")).Present(context.Background()); err != nil {
		t.Fatalf("present store failed Present: %v", err)
	}
}

func TestCorrelate(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	dir, diags, err := s.Correlate(context.Background(), source.Discovered{NativeID: "quiet-meadow"})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	if dir != "/home/user/Code/demo" {
		t.Errorf("dir = %q", dir)
	}
}

func TestCorrelateVanishedSession(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	_, _, err := s.Correlate(context.Background(), source.Discovered{NativeID: "ghost-session"})
	if err == nil || !strings.Contains(err.Error(), "no longer present") {
		t.Fatalf("vanished session: err = %v, want per-item failure", err)
	}
}

func TestUnchangedFingerprintVerdict(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	d := source.Discovered{NativeID: "quiet-meadow"}
	facts, _ := mustCapture(t, s, "quiet-meadow")
	prior := journal.Session{
		LastActiveAt: facts.LastActiveAt,
		Transcript:   facts.Transcript,
	}
	ok, err := s.Unchanged(context.Background(), d, prior)
	if err != nil || !ok {
		t.Fatalf("same revision: ok=%v err=%v, want unchanged", ok, err)
	}

	prior.Transcript.Lines = 6
	if ok, err := s.Unchanged(context.Background(), d, prior); err != nil || ok {
		t.Fatalf("row-count drift: ok=%v err=%v, want changed", ok, err)
	}

	prior.Transcript.Lines = 7
	prior.Transcript.SHA256 = "not-the-recorded-hash"
	if ok, err := s.Unchanged(context.Background(), d, prior); err != nil || ok {
		t.Fatalf("fingerprint drift: ok=%v err=%v, want changed", ok, err)
	}

	prior.Transcript.SHA256 = facts.Transcript.SHA256
	if ok, err := s.Unchanged(context.Background(),
		source.Discovered{NativeID: "ghost"}, prior); err != nil || ok {
		t.Fatalf("vanished session: ok=%v err=%v, want changed (not an error)", ok, err)
	}
}

// TestWALVisibleCommits proves the reader sees committed content still
// living in the WAL: a writer connection with checkpointing disabled
// holds committed frames in -wal while the mode=ro reader enumerates.
func TestWALVisibleCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer func() { _ = writer.Close() }()
	writer.SetMaxOpenConns(1)
	if _, err := writer.Exec(`CREATE TABLE t(x)`); err != nil {
		t.Fatalf("init wal: %v", err)
	}
	execFixture(t, writer, "basic.sql")
	// Commit another change so the interesting rows sit in -wal, and
	// confirm sidecars exist before the reader opens.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("no wal sidecar to read through: %v", err)
	}
	found := mustDiscover(t, NewAt(path))
	if len(found) != 1 || found[0].NativeID != "quiet-meadow" {
		t.Fatalf("found = %v through live WAL", found)
	}
}

// TestLockedReadBounded proves the busy_timeout bound: a writer holding
// an exclusive lock in rollback-journal mode makes the read-only open
// fail after the bounded wait rather than hanging.
func TestLockedReadBounded(t *testing.T) {
	path := buildStore(t, "basic.sql")
	blocker, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(DELETE)&_pragma=locking_mode(EXCLUSIVE)")
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	blocker.SetMaxOpenConns(1)
	if _, err := blocker.Exec(`INSERT INTO app_state VALUES ('lock-test','1')`); err != nil {
		t.Fatalf("acquire exclusive lock: %v", err)
	}
	start := time.Now()
	_, _, err = NewAt(path).Discover(context.Background())
	if err == nil {
		t.Fatal("locked store must fail, not enumerate")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("read waited %v — busy_timeout bound not applied", elapsed)
	}
}
