package devin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// Unchanged implements the OPTIONAL source.Unchanged seam — the
// recapture verdict from the store's own evidence, required here
// because Discovered.Path is a store address, not an openable file
// (the file-tail fingerprint default cannot answer for it).
//
// The verdict is the qualified fingerprint, recomputed — the one rule
// the contract allows no shortcut on. last_activity_at, row counts,
// highest node_id, and database mtime/hash can each prove a change
// happened; none can prove one did not (an in-place edit with an
// unmoved timestamp, a head move over identical rows). So the check
// re-reads the session's own evidence inside one read-only transaction
// and derives the recorded fingerprint over it:
//
//   - session row gone → changed (the per-item verdict is Capture's).
//   - message_nodes count ≠ prior Lines → changed, cheap early-out
//     (the count is implied by the hash; it just saves the full read
//     on the common "grew" path).
//   - recomputed fingerprint = prior SHA256 → unchanged — including the
//     metadata-only churn case: title/hidden/agent_mode/cogs and
//     activity-timestamp bumps never entered the projection, so they
//     cannot stale a committed transcript.
//   - anything else → changed.
//
// Nothing is retained across invocations: the store's own rows are the
// only revision evidence, so no memoization lifetime needs qualifying.
func (s *Source) Unchanged(ctx context.Context, d source.Discovered, prior journal.Session) (bool, error) {
	path, err := s.db()
	if err != nil {
		return false, err
	}
	db, err := openStore(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	tx, err := beginSnapshot(ctx, db)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	sess, ok, err := sessionRow(ctx, tx, d.NativeID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // vanished is not proof of stasis
	}
	var nodes int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_nodes WHERE session_id = ?`, d.NativeID).Scan(&nodes); err != nil {
		return false, err
	}
	if nodes != int64(prior.Transcript.Lines) {
		return false, nil
	}
	h := sha256.New()
	if err := hashConversation(ctx, tx, sess, h); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == prior.Transcript.SHA256, nil
}
