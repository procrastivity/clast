package journal

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SessionStage stages a capture's artifacts before their session
// directory is known. Capture extracts a session's facts in the same
// pass that copies its transcript (M10/M13), but the day shard those
// artifacts land under derives from a fact (started_at) that pass
// produces — so artifacts stream into a staging directory under the
// journal root first, and Commit renames them into the session
// directory once the shard is known. Rename within the root keeps every
// landed file's appearance atomic (MODEL §4's discipline, extended to
// the harness-native copies); a crash before Commit leaves only a
// dot-prefixed staging directory that Walk never reads (it only walks
// sessions/).
type SessionStage struct {
	root     string
	dir      string
	relPaths []string
}

// StageSession creates a staging directory under root, creating the
// root (and its marker) first if needed.
func StageSession(root string) (*SessionStage, error) {
	if err := EnsureRoot(root); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, ".capture-")
	if err != nil {
		return nil, fmt.Errorf("journal: creating staging directory in %s: %w", root, err)
	}
	return &SessionStage{root: root, dir: dir}, nil
}

// Write streams one artifact to relPath (slash-separated, relative to
// the session directory — the transcript copy, a subagent sidecar)
// inside the stage. An absolute or root-escaping relPath is refused:
// the path comes from a source package, and the journal's layout is
// this package's to guard. The stage never opens what it lands (M9:
// artifacts are opaque bytes here); the artifact names themselves are
// the capturing source's, which is why none are spelled out in this
// file.
func (s *SessionStage) Write(relPath string, r io.Reader) error {
	if !filepath.IsLocal(filepath.FromSlash(relPath)) {
		return fmt.Errorf("journal: staged artifact path %q escapes the session directory", relPath)
	}
	path := filepath.Join(s.dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("journal: creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("journal: creating %s: %w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("journal: writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("journal: closing %s: %w", path, err)
	}
	s.relPaths = append(s.relPaths, relPath)
	return nil
}

// Commit renames every staged artifact into key's session directory
// under shard — each rename atomic, replacing any prior capture's copy
// (recapture re-copies, M13) — then removes the staging directory.
func (s *SessionStage) Commit(shard string, key SessionKey) error {
	dest := SessionDir(s.root, shard, key)
	for _, relPath := range s.relPaths {
		target := filepath.Join(dest, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("journal: creating %s: %w", filepath.Dir(target), err)
		}
		staged := filepath.Join(s.dir, filepath.FromSlash(relPath))
		if err := os.Rename(staged, target); err != nil {
			return fmt.Errorf("journal: landing %s: %w", target, err)
		}
	}
	return s.Discard()
}

// Discard removes the staging directory and anything still in it. Safe
// after Commit (which calls it) and after a partial failure alike.
func (s *SessionStage) Discard() error {
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("journal: removing staging directory %s: %w", s.dir, err)
	}
	return nil
}
