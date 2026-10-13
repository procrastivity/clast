package devin

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// The store side of the contract: every read happens inside one
// read-only transaction against a mode=ro handle, so the sessions row,
// the message forest, and the tool-call state are one consistent
// snapshot including committed WAL content. mode=ro can never create a
// missing database; immutable=1 is never used (the WAL is live state).
// busy_timeout bounds the reader lock wait; context propagates through
// every query; every handle closes on every path.

const (
	// busyTimeoutMS bounds the shared-cache lock wait against a live
	// writer; a read-only tx almost never waits, but a crashed-writer
	// recovery can hold the lock briefly.
	busyTimeoutMS = 2000

	// exportFormat is the M13 format string stamped on the artifact and
	// into session.json's transcript.format; readers key on it.
	exportFormat = "devin-export-v1"
	// exportFile is the transcript artifact's recorded name — the
	// transcript.artifact amendment, since a JSON export is not the
	// journal's transcript.jsonl default.
	exportFile = "transcript.json"
)

// requiredShapes is the schema-compatibility floor (the contract's
// v14+ gate): each table must answer the export's own SELECT — probing
// the exact columns the export reads means a missing column or table
// is detected by the read that needs it, and a store that passes is
// supported by construction. subagent_heads is intentionally absent:
// schema 16 lacks it and the export emits an empty section there.
var requiredShapes = []struct {
	table string
	cols  []string
}{
	{"sessions", []string{
		"id", "working_directory", "backend_type", "model", "agent_mode",
		"created_at", "last_activity_at", "title", "main_chain_id", "shell_last_seen_index",
		"cogs_json", "workspace_dirs", "hidden", "metadata",
	}},
	{"message_nodes", []string{"session_id", "node_id", "parent_node_id", "chat_message", "created_at", "metadata"}},
	{"tool_call_state", []string{"session_id", "tool_call_id", "tool_call_json", "tool_call_update_json"}},
	{"prompt_history", []string{"content", "timestamp", "session_id", "is_shell"}},
}

// openStore opens the Devin store read-only. The URI is a file: URL so
// paths with spaces or '?' survive; _pragma=busy_timeout applies at
// connect. The returned handle is lazy — the first query is the real
// open — so callers distinguish a missing store (stat'ed first, or
// surfaced by the open's own error) from a corrupt one (a query that
// fails on a present file).
func openStore(path string) (*sql.DB, error) {
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)", busyTimeoutMS),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("devin: opening store %s: %w", path, err)
	}
	// One session's rows under one tx is the workload; a wider pool
	// only invites parallel connections on a WAL-mode file.
	db.SetMaxOpenConns(1)
	return db, nil
}

// beginSnapshot opens the read-only transaction one export/discovery
// reads under. The caller owns Rollback (a committed read-only tx is
// fine to roll back — it never wrote).
func beginSnapshot(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("devin: starting read transaction: %w", err)
	}
	return tx, nil
}

// checkSchema runs the compatibility gate inside the snapshot tx: every
// required table must answer its export SELECT. The first failure names
// the table — an incompatible store is a source-level failure, never an
// empty discovery.
func checkSchema(ctx context.Context, tx *sql.Tx) error {
	for _, r := range requiredShapes {
		query := fmt.Sprintf("SELECT %s FROM %s LIMIT 0", joinCols(r.cols), r.table)
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("devin: incompatible store: %s: %w", r.table, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("devin: probing %s: %w", r.table, err)
		}
	}
	return nil
}

// hasTable reports whether an optional table exists (subagent_heads is
// schema ≥17; its absence exports an empty section).
func hasTable(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

// schemaVersion reports the store's refinery version for the export
// header — evidence, not a gate (the required-shape probes are the
// gate). A store without refinery_schema_history reports 0.
func schemaVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	ok, err := hasTable(ctx, tx, "refinery_schema_history")
	if err != nil || !ok {
		return 0, err
	}
	var v sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(version) FROM refinery_schema_history`).Scan(&v); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// queryRows streams one SELECT into rows of column→value maps. Values
// normalize to JSON-native types: TEXT stays string (native JSON text
// is byte-preserved by construction — a malformed chat_message can
// never fail the export), INTEGER stays int64, NULL stays nil.
func queryRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalize(vals[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// normalize reduces a driver value to the JSON-native set: nil for
// NULL, string for TEXT (modernc may hand []byte — both decode to the
// same JSON string), int64/float64 for numerics.
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return t
	case []byte:
		return string(t)
	case int64, float64, bool:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// sessionRow loads the one sessions row a snapshot is scoped to. The
// boolean is false when the session is gone (an /rm-session between
// Discover and Capture — the per-item failure, not an empty export).
func sessionRow(ctx context.Context, tx *sql.Tx, id string) (map[string]any, bool, error) {
	rows, err := queryRows(ctx, tx,
		`SELECT id, working_directory, backend_type, model, agent_mode,
			created_at, last_activity_at, title, main_chain_id, shell_last_seen_index,
			cogs_json, workspace_dirs, hidden, metadata
		FROM sessions WHERE id = ?`, id)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

func joinCols(cols []string) string {
	s := cols[0]
	for _, c := range cols[1:] {
		s += ", " + c
	}
	return s
}

// str/int64Of/nullInt64 read typed fields out of an export row — the
// same values the artifact carries, so facts can never diverge from
// the committed bytes.
func str(row map[string]any, col string) string {
	if s, ok := row[col].(string); ok {
		return s
	}
	return ""
}

func int64Of(row map[string]any, col string) int64 {
	if n, ok := row[col].(int64); ok {
		return n
	}
	return 0
}

func nullInt64(row map[string]any, col string) (int64, bool) {
	n, ok := row[col].(int64)
	return n, ok
}
