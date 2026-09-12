package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// markerName is the store marker file, written at the journal root on
// first write (MODEL §3, M6). Its presence is how a reader tells a real
// journal tree from an empty or unrelated directory.
const markerName = "journal.json"

// markerSchemaVersion is the schema_version stamped into journal.json.
// MODEL §4 fixes schema_version: 1 for every store document, including
// this one.
const markerSchemaVersion = 1

// Marker is the store marker document, journal.json (MODEL §4).
type Marker struct {
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// now is time.Now, indirected (C2.6) so tests can assert a fixed
// created_at without sleeping.
var now = time.Now

// EnsureRoot creates root's directory tree and its journal.json marker the
// first time either is missing, and is a no-op otherwise — the
// init-on-first-write primitive every later write path (session capture,
// curation, breadcrumbs, project registration) calls before writing its
// own document (MODEL M6). It never rewrites an existing marker, so
// created_at is set exactly once.
func EnsureRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("journal: creating root %s: %w", root, err)
	}

	markerPath := filepath.Join(root, markerName)
	switch _, err := os.Stat(markerPath); {
	case err == nil:
		return nil // marker already present; nothing to do.
	case !os.IsNotExist(err):
		return fmt.Errorf("journal: checking marker %s: %w", markerPath, err)
	}

	marker := Marker{SchemaVersion: markerSchemaVersion, CreatedAt: now()}
	if err := writeJSONAtomic(markerPath, marker); err != nil {
		return fmt.Errorf("journal: writing marker: %w", err)
	}
	return nil
}

// ReadMarker reads root's journal.json marker. A missing journal (root or
// journal.json absent) is not an error: ok is false and err is nil — the
// read posture every query in this package follows (a read on a missing
// journal returns empty results, never an error).
func ReadMarker(root string) (marker Marker, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(root, markerName))
	if err != nil {
		if os.IsNotExist(err) {
			return Marker{}, false, nil
		}
		return Marker{}, false, fmt.Errorf("journal: reading marker: %w", err)
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return Marker{}, false, fmt.Errorf("journal: parsing marker %s: %w", filepath.Join(root, markerName), err)
	}
	return marker, true, nil
}

// writeJSONAtomic writes v to path as JSON via temp-file-and-rename, the
// atomic write discipline MODEL §4 requires of every store document. path's
// parent directory must already exist (EnsureRoot's job, not this one's).
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: encoding %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("journal: creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// Deliberate discard: a no-op once the rename below succeeds, and
	// there is nothing more useful to do with a cleanup failure here.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("journal: writing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("journal: closing %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("journal: renaming %s to %s: %w", tmpPath, path, err)
	}
	return nil
}
