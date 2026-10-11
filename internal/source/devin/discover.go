package devin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// Discover enumerates the store's sessions under one read-only
// transaction: schema gate first (an incompatible or corrupt store is
// a source-level failure, never an empty discovery), then every
// sessions row — hidden included (hidden is devin's list filtering,
// not deletion). The uniform rule holds: error = could not enumerate
// at all, Diagnostic = items skipped.
func (s *Source) Discover(ctx context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	path, err := s.db()
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil // absent harness: quiet
		}
		return nil, nil, err
	}
	db, err := openStore(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = db.Close() }()
	tx, err := beginSnapshot(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkSchema(ctx, tx); err != nil {
		return nil, nil, err
	}

	rows, err := queryRows(ctx, tx,
		`SELECT id, last_activity_at FROM sessions ORDER BY id`)
	if err != nil {
		return nil, nil, fmt.Errorf("devin: enumerating sessions: %w", err)
	}
	var found []source.Discovered
	var diags []source.Diagnostic
	for _, row := range rows {
		id := str(row, "id")
		if id == "" {
			diags = append(diags, source.Diagnostic{
				Path: path, Err: errors.New("devin: sessions row with empty id"),
			})
			continue
		}
		found = append(found, source.Discovered{
			NativeID: id,
			Path:     sessionPath(path, id),
			ModTime:  time.Unix(int64Of(row, "last_activity_at"), 0).UTC(),
		})
	}
	return found, diags, nil
}

// Correlate binds a session to its working_directory — the harness's
// own recorded project directory. A session that vanished between
// Discover and Correlate is the per-item failure; an empty directory
// is "" (unknown), still capturable.
func (s *Source) Correlate(ctx context.Context, d source.Discovered) (string, []source.Diagnostic, error) {
	path, err := s.db()
	if err != nil {
		return "", nil, err
	}
	db, err := openStore(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = db.Close() }()
	tx, err := beginSnapshot(ctx, db)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, ok, err := sessionRow(ctx, tx, d.NativeID)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, fmt.Errorf("devin: session %s no longer present in the store", d.NativeID)
	}
	return str(row, "working_directory"), nil, nil
}
