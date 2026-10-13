package devin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// mutate opens a store for writing — tests only; the source itself
// never holds a writable handle.
func mutate(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("mutate: %v\n%s", err, s)
		}
	}
}

// priorOf captures id and returns the session.json the sweep would
// have committed — the prior input Unchanged answers against.
func priorOf(t *testing.T, s *Source, id string) (journal.Session, source.Discovered) {
	t.Helper()
	facts, _ := mustCapture(t, s, id)
	return journal.Session{
		LastActiveAt: facts.LastActiveAt,
		Transcript:   facts.Transcript,
	}, source.Discovered{NativeID: id}
}

func unchanged(t *testing.T, s *Source, d source.Discovered, prior journal.Session) bool {
	t.Helper()
	ok, err := s.Unchanged(context.Background(), d, prior)
	if err != nil {
		t.Fatalf("Unchanged: %v", err)
	}
	return ok
}

// TestUnchangedContentEditSameMeta is the case a metadata shortcut
// cannot answer: a message edited in place while last_activity_at and
// the row count hold still must still report changed — only the
// fingerprint sees it.
func TestUnchangedContentEditSameMeta(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "quiet-meadow")
	mutate(t, path,
		`UPDATE message_nodes SET chat_message = replace(chat_message, 'FIXTURE-OK', 'EDITED-OUT')
		 WHERE session_id = 'quiet-meadow' AND node_id = 3`)
	if unchanged(t, s, d, prior) {
		t.Fatal("in-place content edit with unchanged metadata must report changed")
	}
}

// TestUnchangedToolResultCompletion: a tool_call_update_json landing
// after capture is conversation content — same timestamp, same count,
// still changed.
func TestUnchangedToolResultCompletion(t *testing.T) {
	path := buildStore(t, "branched-shortened.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "crashed-stork")
	mutate(t, path,
		`UPDATE tool_call_state SET tool_call_update_json =
		 '{"toolCallId":"exec:0#0a1b2c","status":"completed","_meta":{"cognition.ai/inferenceToolName":"exec"}}'
		 WHERE session_id = 'crashed-stork'`)
	if unchanged(t, s, d, prior) {
		t.Fatal("tool-result completion must report changed")
	}
}

// TestUnchangedMetadataChurn is the other half of the contract: title,
// hidden, agent_mode, cogs, session metadata AND last_activity_at may
// all move without a single conversation byte changing — the verdict
// stays unchanged and the journal record stays untouched.
func TestUnchangedMetadataChurn(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "quiet-meadow")
	mutate(t, path, `UPDATE sessions SET
		title = 'Renamed', hidden = 1, agent_mode = 'bypass',
		cogs_json = '{"volatile":true}', metadata = '{"total_credit_cost":9.9}',
		last_activity_at = last_activity_at + 9999
		WHERE id = 'quiet-meadow'`)
	if !unchanged(t, s, d, prior) {
		t.Fatal("metadata-only churn must not stale a committed transcript")
	}
}

// TestUnchangedHeadMove: main_chain_id pointing at an older node —
// the legitimate-shortening case — changes the conversation even
// though every row is identical.
func TestUnchangedHeadMove(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "quiet-meadow")
	mutate(t, path,
		`UPDATE sessions SET main_chain_id = 2, last_activity_at = last_activity_at WHERE id = 'quiet-meadow'`)
	if unchanged(t, s, d, prior) {
		t.Fatal("a head move with identical rows must report changed")
	}
}

// TestUnchangedWorkingDirectoryChange: correlation evidence is part of
// the fingerprint scope — a working_directory change re-captures so
// Correlate and the facts refresh.
func TestUnchangedWorkingDirectoryChange(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "quiet-meadow")
	mutate(t, path,
		`UPDATE sessions SET working_directory = '/home/user/Code/moved' WHERE id = 'quiet-meadow'`)
	if unchanged(t, s, d, prior) {
		t.Fatal("a working_directory change must report changed")
	}
}

// TestUnchangedUnrelatedSessionChurn: another session's rows, heads,
// and metadata never stale this session — session scoping holds.
func TestUnchangedUnrelatedSessionChurn(t *testing.T) {
	path := buildStore(t, "edge-cases.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "brisk-owl")
	mutate(t, path,
		`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at,metadata)
		 VALUES ('amber-fox',99,4,'{"message_id":"x","role":"user","content":"elsewhere","metadata":{"is_user_input":true}}',1791700999,'null')`,
		`UPDATE sessions SET last_activity_at = last_activity_at + 100, title = 'other' WHERE id = 'amber-fox'`,
		`DELETE FROM tool_call_state WHERE session_id = 'amber-fox'`)
	if !unchanged(t, s, d, prior) {
		t.Fatal("another session's churn must not stale this one")
	}
}

// TestUnchangedRowInsertionOrder: the same logical rows deleted and
// re-inserted in a different physical order (reassigning row_id) is no
// change at all — content-keyed ordering makes it invisible.
func TestUnchangedRowInsertionOrder(t *testing.T) {
	path := buildStore(t, "basic.sql")
	s := NewAt(path)
	prior, d := priorOf(t, s, "quiet-meadow")
	// Rebuild the forest in reverse insertion order.
	mutate(t, path,
		`CREATE TABLE tmp_nodes AS SELECT session_id,node_id,parent_node_id,chat_message,created_at,metadata
		 FROM message_nodes WHERE session_id = 'quiet-meadow'`,
		`DELETE FROM message_nodes WHERE session_id = 'quiet-meadow'`,
		`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at,metadata)
		 SELECT session_id,node_id,parent_node_id,chat_message,created_at,metadata
		 FROM tmp_nodes ORDER BY node_id DESC`,
		`DROP TABLE tmp_nodes`)
	if !unchanged(t, s, d, prior) {
		t.Fatal("insertion-order churn over identical rows must be unchanged")
	}
}

// TestFingerprintOfflineReproduction: the recorded sha256 must be
// derivable from the artifact alone — decode the committed document
// (number-preserving) and re-derive the projection.
func TestFingerprintOfflineReproduction(t *testing.T) {
	s := NewAt(buildStore(t, "basic.sql"))
	facts, a := mustCapture(t, s, "quiet-meadow")

	dec := json.NewDecoder(strings.NewReader(string(a["transcript.json"])))
	dec.UseNumber()
	var full struct {
		Session       map[string]any   `json:"session"`
		MessageNodes  []map[string]any `json:"message_nodes"`
		ToolCallState []map[string]any `json:"tool_call_state"`
	}
	if err := dec.Decode(&full); err != nil {
		t.Fatal(err)
	}

	h := sha256.New()
	b, _ := json.Marshal(map[string]any{
		"main_chain_id":     full.Session["main_chain_id"],
		"working_directory": full.Session["working_directory"],
	})
	h.Write([]byte("sess\x00"))
	h.Write(b)
	for _, sec := range []struct {
		tag  string
		rows []map[string]any
	}{{"mn", full.MessageNodes}, {"tc", full.ToolCallState}} {
		for _, row := range sec.rows {
			b, _ := json.Marshal(row)
			h.Write([]byte(sec.tag + "\x00"))
			h.Write(b)
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != facts.Transcript.SHA256 {
		t.Fatalf("offline recompute = %s, recorded = %s", got, facts.Transcript.SHA256)
	}
}
