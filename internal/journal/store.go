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

// recordSchemaVersion is the schema_version stamped into every record
// document defined in records.go (session.json, curation.json,
// project.json, clones.<machine>.json — MODEL §4). Kept distinct from
// markerSchemaVersion even though both are currently 1: each document's
// schema can version forward independently.
const recordSchemaVersion = 1

// readDocument reads and JSON-decodes path into a T. A missing file
// returns the zero value, false, nil — never an error, the same posture
// as ReadMarker. A malformed file is returned as an error; callers such as
// the walk (a later step) count that as a per-document diagnostic rather
// than treating it as fatal.
func readDocument[T any](path string) (T, bool, error) {
	var doc T
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, false, nil
		}
		return doc, false, fmt.Errorf("journal: reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, false, fmt.Errorf("journal: parsing %s: %w", path, err)
	}
	return doc, true, nil
}

// writeDocument ensures root (and its journal.json marker) exists, then
// writes v to path whole, atomically (MODEL §4). It creates path's parent
// directory as needed — a session or project directory may not exist yet
// on a document's first write.
func writeDocument(root, path string, v any) error {
	if err := EnsureRoot(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("journal: creating %s: %w", filepath.Dir(path), err)
	}
	return writeJSONAtomic(path, v)
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
