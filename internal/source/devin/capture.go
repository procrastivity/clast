package devin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"strconv"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/tidwall/gjson"
)

// Capture implements source.Source for the sqlite-export model (M13):
// one read-only transaction holds the session row, its whole message
// forest, tool-call state, prompt history, and subagent heads; the
// rows stream into transcript.json as one deterministic devin-export-v1
// document through an io.Pipe — one marshaled row in flight, never a
// whole-document buffer. The M10 facts and the conversation-scoped
// fingerprint are computed from the same snapshot in the same pass, so
// fingerprint and artifact can never diverge.
//
// Per-item failure modes per the contract: a session that vanished
// between Discover and Capture, a store that turned unreadable, and a
// mid-export read failure are errors — the capture verb turns each
// into the per-item diagnostic and nothing commits. A broken chain
// (missing parent, cycle, dangling head) and malformed chat_message
// JSON are not failures: the snapshot is still faithful, the anomaly
// rides as a Diagnostic plus the Incomplete flag in session.json.
func (s *Source) Capture(ctx context.Context, d source.Discovered, write source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	path, err := s.db()
	if err != nil {
		return source.Facts{}, nil, err
	}
	db, err := openStore(path)
	if err != nil {
		return source.Facts{}, nil, err
	}
	defer func() { _ = db.Close() }()
	tx, err := beginSnapshot(ctx, db)
	if err != nil {
		return source.Facts{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	sess, ok, err := sessionRow(ctx, tx, d.NativeID)
	if err != nil {
		return source.Facts{}, nil, fmt.Errorf("devin: reading session %s: %w", d.NativeID, err)
	}
	if !ok {
		return source.Facts{}, nil, fmt.Errorf("devin: session %s no longer present in the store", d.NativeID)
	}
	version, err := schemaVersion(ctx, tx)
	if err != nil {
		return source.Facts{}, nil, fmt.Errorf("devin: reading schema version: %w", err)
	}
	hasSub, err := hasTable(ctx, tx, "subagent_heads")
	if err != nil {
		return source.Facts{}, nil, fmt.Errorf("devin: probing subagent_heads: %w", err)
	}

	// Pass 1, same tx: chain analysis over node_id/parent_node_id plus
	// minimal chat_message reads — small per-node state (ints + role
	// flags), never the full row set in memory.
	counts, diags, incomplete, nodeCount, err := analyzeChain(ctx, tx, sessionPath(path, d.NativeID), sess)
	if err != nil {
		return source.Facts{}, nil, err
	}

	// Pass 2, same tx: the export streams through the pipe while the
	// conversation-scoped fingerprint accumulates — session-level chain
	// and correlation evidence first, then the conversation rows as they
	// are emitted. The producer owns the tx for the rest of the call.
	pr, pw := io.Pipe()
	h := sha256.New()
	hashSessionEvidence(h, sess)
	produced := make(chan error, 1)
	go func() {
		produced <- exportSnapshot(ctx, tx, pw, sess, version, hasSub, h)
	}()
	writeErr := write(exportFile, pr)
	_ = pr.Close() // unblock a producer mid-write if the stage failed
	prodErr := <-produced
	if writeErr != nil {
		return source.Facts{}, diags, writeErr
	}
	if prodErr != nil {
		return source.Facts{}, diags, fmt.Errorf("devin: exporting session %s: %w", d.NativeID, prodErr)
	}

	facts := source.Facts{
		StartedAt:    time.Unix(int64Of(sess, "created_at"), 0).UTC(),
		LastActiveAt: time.Unix(int64Of(sess, "last_activity_at"), 0).UTC(),
		Counts:       counts,
		Substantive:  counts.Assistant >= 1,
		Incomplete:   incomplete,
		Transcript: journal.TranscriptFingerprint{
			Format:   exportFormat,
			Lines:    nodeCount,
			SHA256:   hex.EncodeToString(h.Sum(nil)),
			Artifact: exportFile,
		},
	}
	return facts, diags, nil
}

// analyzeChain walks the snapshot's forest once, session-scoped: every
// message_nodes row's parent link plus the minimal chat_message fields
// the counts need. The active chain is derived, never scanned — from
// main_chain_id back through parent_node_id to the NULL root — so
// abandoned branches and rebuild copies never inflate the counts, and
// a broken chain reports Incomplete instead of silently reading as a
// shorter conversation.
func analyzeChain(ctx context.Context, tx *sql.Tx, sessPath string, sess map[string]any) (journal.SessionCounts, []source.Diagnostic, bool, int, error) {
	rows, err := queryRows(ctx, tx,
		`SELECT node_id, parent_node_id, chat_message FROM message_nodes WHERE session_id = ?`,
		str(sess, "id"))
	if err != nil {
		return journal.SessionCounts{}, nil, false, 0,
			fmt.Errorf("devin: reading message_nodes for %s: %w", str(sess, "id"), err)
	}
	type meta struct {
		parent    int64
		hasParent bool
		user      bool
		assistant bool
	}
	metas := make(map[int64]meta, len(rows))
	var diags []source.Diagnostic
	for _, row := range rows {
		nid := int64Of(row, "node_id")
		m := meta{}
		if p, ok := nullInt64(row, "parent_node_id"); ok {
			m.parent, m.hasParent = p, true
		}
		raw := str(row, "chat_message")
		if !gjson.Valid(raw) {
			diags = append(diags, source.Diagnostic{
				Path: sessPath + ":node " + strconv.FormatInt(nid, 10),
				Err:  fmt.Errorf("devin: malformed chat_message JSON"),
			})
		} else {
			switch gjson.Get(raw, "role").String() {
			case "user":
				// Only an explicit is_user_input marks a human turn —
				// auto-"continue" and compaction payloads ride as
				// role:user but are not prompts.
				m.user = gjson.Get(raw, "metadata.is_user_input").Bool()
			case "assistant":
				// cache_keepalive and empty-finishing messages still
				// count — every on-chain assistant message does.
				m.assistant = true
			}
		}
		metas[nid] = m
	}

	var counts journal.SessionCounts
	incomplete := false
	if head, hasHead := nullInt64(sess, "main_chain_id"); hasHead {
		seen := make(map[int64]bool, len(metas))
		for id := head; ; {
			if seen[id] {
				diags = append(diags, source.Diagnostic{Path: sessPath, Err: fmt.Errorf(
					"devin: chain cycle at node %d — captured the walked prefix", id,
				)})
				incomplete = true
				break
			}
			seen[id] = true
			m, ok := metas[id]
			if !ok {
				diags = append(diags, source.Diagnostic{Path: sessPath, Err: fmt.Errorf(
					"devin: chain references missing node %d — captured the walked prefix", id,
				)})
				incomplete = true
				break
			}
			if m.user {
				counts.User++
			}
			if m.assistant {
				counts.Assistant++
			}
			if !m.hasParent {
				break
			}
			id = m.parent
		}
	}
	// A NULL main_chain_id is an empty session — no chain, no anomaly.
	return counts, diags, incomplete, len(rows), nil
}

// exportSnapshot writes the devin-export-v1 document: header, the
// sessions row verbatim, then each section's rows in deterministic
// order — node_id, tool_call_id, prompt_history.id, agent_id — so the
// artifact is byte-identical for identical store content regardless of
// insertion order. The conversation fingerprint hashes the marshaled
// message_nodes and tool_call_state rows as they are emitted. The
// writer closes on every path, with the read error where there is one.
func exportSnapshot(ctx context.Context, tx *sql.Tx, w *io.PipeWriter, sess map[string]any, version int64, hasSub bool, h hash.Hash) (retErr error) {
	defer func() {
		if retErr != nil {
			_ = w.CloseWithError(retErr)
		}
	}()
	id := str(sess, "id")

	// The document opens with the header fields, then the sections —
	// written field by field so the object's own braces stay balanced.
	fmtJSON, _ := json.Marshal(exportFormat)
	idJSON, _ := json.Marshal(id)
	if _, err := fmt.Fprintf(w, `{"format":%s,"store_schema_version":%d,"session_id":%s`,
		fmtJSON, version, idJSON); err != nil {
		return err
	}
	if _, err := w.Write([]byte(`,"session":`)); err != nil {
		return err
	}
	if b, err := json.Marshal(sess); err != nil {
		return err
	} else if _, err := w.Write(b); err != nil {
		return err
	}

	// Columns whose values are insertion-order artifacts
	// (message_nodes.row_id, prompt_history.id) are deliberately not
	// exported: two stores holding the same logical rows in different
	// physical order must produce byte-identical artifacts, and the
	// semantic keys — node_id, tool_call_id, agent_id, and
	// timestamp+content — order every section on content alone. The two
	// conversation sections share fingerprint.go's queries so the
	// recompute path can never drift from what the export hashed.
	sections := []struct {
		key   string
		query string
		tag   string // "" = not part of the conversation fingerprint
	}{
		{"message_nodes", conversationQueries[0].query, conversationQueries[0].tag},
		{"tool_call_state", conversationQueries[1].query, conversationQueries[1].tag},
		{
			"prompt_history",
			`SELECT content, timestamp, session_id, is_shell
			 FROM prompt_history WHERE session_id = ? ORDER BY timestamp, content, is_shell`, "",
		},
		{
			"subagent_heads",
			`SELECT session_id, agent_id, chain_node_id, updated_at
			 FROM subagent_heads WHERE session_id = ? ORDER BY agent_id`, "",
		},
	}
	for _, sec := range sections {
		if _, err := w.Write([]byte(`,"` + sec.key + `":[`)); err != nil {
			return err
		}
		if sec.key != "subagent_heads" || hasSub {
			if err := streamRows(ctx, tx, sec.query, id, sec.tag, h, w); err != nil {
				return err
			}
		}
		if _, err := w.Write([]byte(`]`)); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte(`}`)); err != nil {
		return err
	}
	return w.Close()
}

// streamRows runs one section's SELECT and writes each row's marshaled
// JSON into the array the caller opened — tagged sections also feed the
// conversation fingerprint. One row is in flight at a time.
func streamRows(ctx context.Context, tx *sql.Tx, query, sessionID, tag string, h hash.Hash, w io.Writer) error {
	first := true
	return eachRow(ctx, tx, query, sessionID, func(row map[string]any) error {
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if tag != "" {
			hashMarshaledRow(h, tag, b)
		}
		if !first {
			if _, err := w.Write([]byte(`,`)); err != nil {
				return err
			}
		}
		first = false
		_, err = w.Write(b)
		return err
	})
}
