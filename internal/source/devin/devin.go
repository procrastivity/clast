// Package devin is the devin source (M12): Devin CLI sessions as rows
// in the global SQLite store at
// ${XDG_DATA_HOME:-~/.local/share}/devin/cli/sessions.db. SQLite-export
// posture (M13): the store has no per-session file, so capture writes a
// deterministic rows-as-JSON export (devin-export-v1) of one session's
// rows — the whole message forest, tool-call state, prompt history, and
// subagent heads — under a single read-only transaction. The parsing
// is ported-and-diverged from duo's devin store reader (S5, HANDOFF §7):
// same mode=ro open, same main_chain_id→root walk, but this package
// exports native rows rather than reconstructing turns, because the
// artifact — not a projection — is the captured evidence.
//
// The store is Devin-owned: this source never creates, migrates, or
// writes it, never invokes the Devin executable, and never reads the
// auto-written transcripts/*.json ATIF files (they can be stale or
// orphaned; the database is the only capture source). The contract
// lives in docs/devin-source/decisions.md.
package devin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/procrastivity/clast/internal/source"
)

// Name is the registry key and M11 identity prefix.
const Name = "devin"

// Source implements source.Source over the Devin CLI session store.
type Source struct {
	// dbPath is the explicit store path (the fixture seam); "" means
	// resolve ${XDG_DATA_HOME:-~/.local/share}/devin/cli/sessions.db at
	// call time, so the registry table's static New() can never fail —
	// only a duty call can, and it reports the failure as a duty error.
	// There is no env seam of clast's own here: XDG_DATA_HOME is the
	// platform convention the harness itself follows.
	dbPath string
}

// New returns the devin source over the real store, resolved lazily.
func New() *Source {
	return &Source{}
}

// NewAt returns the devin source over an explicit store path — the
// fixture seam. Production never sets it (there is no user-facing
// store-path option, per the contract).
func NewAt(dbPath string) *Source {
	return &Source{dbPath: dbPath}
}

// db resolves the store path per Source.dbPath's contract.
func (s *Source) db() (string, error) {
	if s.dbPath != "" {
		return s.dbPath, nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("devin: resolving home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "devin", "cli", "sessions.db"), nil
}

// sessionPath is a session's source-native address for Discovered.Path
// and diagnostics: the store it lives in plus its session id. A SQLite
// store has no per-session file to point at; the fragment keeps every
// session's address distinct and self-identifying in session.json's
// source_path. It is never openable as a path — the source's own
// Unchanged verdict replaces the file-tail fingerprint default.
func sessionPath(dbPath, id string) string {
	return dbPath + "#" + id
}

// Name implements source.Source.
func (s *Source) Name() string { return Name }

// Model implements source.Source: devin is sqlite-export (M13).
func (s *Source) Model() source.StorageModel { return source.SQLiteExport }

// Present implements source.Presence: devin's storage is the one store
// file Discover reads, so "present" means that file exists and is
// readable — the same probe Discover's first step makes. The error is
// returned unwrapped: os.PathError already names the probed path and
// the reason, which is what capture.source-unavailable prints verbatim.
// An incompatible or corrupt store is still "present" here — that
// verdict belongs to the schema gate in Discover, matching the
// contract's disclosure split.
func (s *Source) Present(_ context.Context) error {
	path, err := s.db()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}
