package devin

import (
	"context"
	"database/sql"
	"encoding/json"
	"hash"
)

// The conversation fingerprint (M7's growth/staleness comparison): the
// sha256 recorded in session.json covers exactly the evidence whose
// change makes the committed artifact a wrong description of the
// session — and nothing more.
//
// Covered:
//   - session.main_chain_id — which chain IS the conversation; a head
//     move with identical rows (rewind, branch switch, legitimate
//     shortening) is a different conversation.
//   - session.working_directory — the correlation evidence the artifact
//     pins; a change here is a fact change (M10), never content.
//   - every message_nodes row and every tool_call_state row of the
//     session — the conversation content itself, native text verbatim.
//
// Not covered, deliberately: title, hidden, cogs_json, model,
// agent_mode, sessions.metadata, activity timestamps, prompt_history,
// subagent_heads — bookkeeping churn must never mark a curated
// transcript stale. A metadata-only change leaves the fingerprint (and
// so the Unchanged verdict, and the journal record) untouched.
//
// The same tag-prefixed projection is hashed at capture time over the
// exported bytes and recomputed at recapture time over a fresh read —
// the verdict is the artifact's own evidence, not a metadata proxy.
// Because each row lands in the artifact as exactly the marshaled JSON
// hashed here, the fingerprint is also reproducible offline: decode the
// artifact with a number-preserving decoder and re-marshal the rows.

// hashSessionEvidence covers the two session-level fields in scope.
func hashSessionEvidence(h hash.Hash, sess map[string]any) {
	b, _ := json.Marshal(map[string]any{
		"main_chain_id":     sess["main_chain_id"],
		"working_directory": sess["working_directory"],
	})
	_, _ = h.Write([]byte("sess\x00"))
	_, _ = h.Write(b)
}

// hashMarshaledRow feeds one marshaled export row into the fingerprint
// under its section tag.
func hashMarshaledRow(h hash.Hash, tag string, b []byte) {
	_, _ = h.Write([]byte(tag + "\x00"))
	_, _ = h.Write(b)
}

// conversationQueries are the tagged per-session SELECTs whose rows the
// fingerprint covers, in export order — shared by Capture's streamed
// hash and Unchanged's recompute so the two can never disagree.
var conversationQueries = []struct {
	tag   string
	query string
}{
	{"mn", `SELECT session_id, node_id, parent_node_id, chat_message, created_at, metadata
		FROM message_nodes WHERE session_id = ? ORDER BY node_id`},
	{"tc", `SELECT session_id, tool_call_id, tool_call_json, tool_call_update_json
		FROM tool_call_state WHERE session_id = ? ORDER BY tool_call_id`},
}

// hashConversation recomputes the fingerprint over a live snapshot:
// session-level evidence, then each conversation row marshaled exactly
// as the export emits it. Caller holds the read-only tx.
func hashConversation(ctx context.Context, tx *sql.Tx, sess map[string]any, h hash.Hash) error {
	hashSessionEvidence(h, sess)
	id := str(sess, "id")
	for _, q := range conversationQueries {
		if err := eachRow(ctx, tx, q.query, id, func(row map[string]any) error {
			b, err := json.Marshal(row)
			if err != nil {
				return err
			}
			hashMarshaledRow(h, q.tag, b)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// eachRow streams one per-session SELECT, calling fn per normalized
// row — the shared cursor loop export and fingerprinting use so a query
// can never drift between readers.
func eachRow(ctx context.Context, tx *sql.Tx, query, sessionID string, fn func(map[string]any) error) error {
	rows, err := tx.QueryContext(ctx, query, sessionID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalize(vals[i])
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}
