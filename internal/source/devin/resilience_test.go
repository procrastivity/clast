package devin

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

// TestSnapshotIsolationUnderWriter is the concurrent-writer half of
// the matrix, synchronized by channels rather than timing: a read tx
// that has already pinned its snapshot keeps seeing exactly that
// committed state while a live WAL writer commits more work — session
// row, head, nodes and tool state can never be a mixture of two
// commits. When the snapshot ends, a fresh tx sees the later commit —
// a later committed state is a later valid snapshot, not an error.
func TestSnapshotIsolationUnderWriter(t *testing.T) {
	path := buildStore(t, "basic.sql")
	// The writer flips the fixture store to WAL — the mode a live Devin
	// process leaves it in — and stays open so the sidecars live.
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	writer.SetMaxOpenConns(1)
	if _, err := writer.Exec(`INSERT INTO app_state VALUES ('iso','1')`); err != nil {
		t.Fatal(err)
	}

	db, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	tx, err := beginSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	count := func(tx *sql.Tx) int64 {
		t.Helper()
		var n int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM message_nodes WHERE session_id = 'quiet-meadow'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count(tx) // first read pins the snapshot
	if before != 7 {
		t.Fatalf("fixture nodes = %d, want 7", before)
	}

	// Commit mid-snapshot: a new node, a head move, and new tool state —
	// the exact mixture the export must never see half of.
	if _, err := writer.Exec(`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at,metadata)
		VALUES ('quiet-meadow',9,4,'{"role":"assistant","content":"late"}',1791600200,'null')`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`UPDATE sessions SET main_chain_id = 9 WHERE id = 'quiet-meadow'`); err != nil {
		t.Fatal(err)
	}
	if got := count(tx); got != before {
		t.Fatalf("snapshot saw %d nodes after a mid-read commit, want %d — mixed commits", got, before)
	}
	var head int64
	if err := tx.QueryRowContext(ctx,
		`SELECT main_chain_id FROM sessions WHERE id = 'quiet-meadow'`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if head != 4 {
		t.Fatalf("snapshot head moved to %d — the read mixed two commits", head)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	// A fresh snapshot reads the later committed state — growth is
	// validity, not corruption.
	tx2, err := beginSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx2.Rollback() }()
	if got := count(tx2); got != 8 {
		t.Fatalf("fresh snapshot = %d nodes, want 8 (the later commit)", got)
	}
}

// TestCorruptStoreIsFailure: a present-but-corrupt file is a
// source-level failure — never empty successful discovery, never a
// quiet skip.
func TestCorruptStoreIsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite store at all, just bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, _, err := NewAt(path).Discover(context.Background())
	if err == nil {
		t.Fatalf("corrupt store enumerated %v sessions — want a source failure", found)
	}
}

// TestCaptureWriteFailureUnblocksProducer: a stage.Write that refuses
// mid-stream must surface as the capture's error — no partial
// artifact, no wedged producer, no leak. The next healthy capture on
// the same store proceeds normally.
func TestCaptureWriteFailureUnblocksProducer(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	d := source.Discovered{NativeID: "quiet-meadow"}
	sentinel := errors.New("stage refused the write")
	failWrite := func(string, io.Reader) error { return sentinel }
	if _, _, err := s.Capture(context.Background(), d, failWrite); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the stage's own failure verbatim", err)
	}
	if _, _, err := s.Capture(context.Background(), d, artifacts{}.writer); err != nil {
		t.Fatalf("healthy capture after a write failure: %v", err)
	}
}

// TestCaptureLegitimateShrinkage: a head moved backward is a real
// capture — shorter counts, no Incomplete (shrinkage is valid state,
// not a torn read), and the artifact records the new head.
func TestCaptureLegitimateShrinkage(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	mutate(t, path, `UPDATE sessions SET main_chain_id = 2 WHERE id = 'quiet-meadow'`)
	facts, _ := mustCapture(t, s, "quiet-meadow")
	if facts.Counts.User != 1 || facts.Counts.Assistant != 1 {
		t.Fatalf("shrunken chain counts = %+v, want 1u/1a", facts.Counts)
	}
	if facts.Incomplete {
		t.Error("legitimate shrinkage must not flag Incomplete")
	}
	if facts.Transcript.Lines != 7 {
		t.Errorf("forest rows still all export: Lines = %d, want 7", facts.Transcript.Lines)
	}
}
