package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WalkItem is one session as enumerated by Walk: its identity, the shard
// it was found under on disk, its parsed session.json, and its curation
// facts — raw enough for a query verb to filter and derive from, without
// re-reading the directory itself.
type WalkItem struct {
	// Key is this session's identity (M11) — read from session.json's own
	// Harness/SessionID fields, never derived from the directory name.
	// loadWalkItem diagnoses (and drops) any session whose directory name
	// disagrees with what session.json records.
	Key SessionKey
	// Shard is the YYYY-MM-DD directory this session was found under.
	// Provenance only — never semantics (M8): callers that need a day
	// bucket derive one from Session.StartedAt and the current Cutoff,
	// they never read it off Shard.
	Shard string
	// Session is session.json, always present — Walk skips and counts
	// (never returns) a session directory whose session.json is missing
	// or malformed.
	Session Session
	// Curation is curation.json's content, or the zero value when
	// CurationPresent is false.
	Curation Curation
	// CurationPresent reports whether curation.json exists at all — the
	// MODEL §2 fact State derives its `captured` case from.
	CurationPresent bool
	// EntryExists reports whether entry.md exists. Only a stat: the M9
	// boundary means Walk never opens or parses it.
	EntryExists bool
}

// State returns this item's curation state per MODEL §2: StateCaptured
// when curation.json doesn't exist, else whatever state curation.json
// recorded.
func (w WalkItem) State() CurationState {
	if !w.CurationPresent {
		return StateCaptured
	}
	return w.Curation.State
}

// Stale reports M7: a curated session whose current session.json
// transcript fingerprint (lines, sha256) no longer matches the one
// curation.json recorded at curation time. Never stored — computed here,
// every time, from the two documents already on disk. A curated session
// with no recorded fingerprint (which Write/ReadCuration never produce,
// but a hand-edited file could) is defined not stale: there is nothing to
// compare it against.
func (w WalkItem) Stale() bool {
	if w.State() != StateCurated {
		return false
	}
	stamp := w.Curation.TranscriptAtCuration
	if stamp == nil {
		return false
	}
	return w.Session.Transcript.Lines != stamp.Lines || w.Session.Transcript.SHA256 != stamp.SHA256
}

// Walk enumerates every session under root/sessions/, in deterministic
// order (shard, then directory name). MODEL M4: the tree walked here IS
// the log — there is no manifest, so "what has been captured" is
// answered by walking, never by a side index. Walk does not open the
// transcript copy (TranscriptPath, M13) or entry.md's contents (the M9
// boundary — entry.md's presence is only stat'd) and it does not filter:
// that is the query verbs' job (SURFACE V17 etc.), not this package's.
//
// A missing journal root or missing sessions/ directory is not an error:
// it returns no items (the step-01 read posture). A shard directory not
// shaped YYYY-MM-DD, a session directory that doesn't even look like
// <harness>-<native-id> (no interior dash), a session directory whose
// session.json (or curation.json) is missing or malformed, or one whose
// session.json identity doesn't reproduce the directory name is skipped
// and counted in diags rather than failing the walk — MODEL §7's
// "tolerant of what it doesn't own" posture, extended here to the tree's
// own shape. The directory name's dash split is only ever a pre-read
// shape gate (M11): session.json's own Harness/SessionID fields are
// identity's only authority, because a naive first-dash split gets it
// wrong for a dashed harness name (claude-code, cursor-agent). Anything
// else found inside a session directory (stray files, an unreadable
// transcript copy) is simply ignored: this step only ever reads
// session.json, curation.json, and stats entry.md.
func Walk(root string) (items []WalkItem, diags []Diagnostic, err error) {
	sessionsDir := filepath.Join(root, "sessions")
	shardEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("journal: reading %s: %w", sessionsDir, err)
	}

	shards := dirNames(shardEntries)
	sort.Strings(shards)

	for _, shard := range shards {
		shardPath := filepath.Join(sessionsDir, shard)
		if _, err := time.Parse(dayShardLayout, shard); err != nil {
			diags = append(diags, Diagnostic{Path: shardPath, Err: fmt.Errorf("journal: %q is not a YYYY-MM-DD shard directory", shard)})
			continue
		}

		sessionEntries, err := os.ReadDir(shardPath)
		if err != nil {
			diags = append(diags, Diagnostic{Path: shardPath, Err: fmt.Errorf("journal: reading %s: %w", shardPath, err)})
			continue
		}

		names := dirNames(sessionEntries)
		sort.Strings(names)
		for _, dirName := range names {
			sessionDir := filepath.Join(shardPath, dirName)
			if !looksLikeSessionDirName(dirName) {
				diags = append(diags, Diagnostic{Path: sessionDir, Err: fmt.Errorf("journal: %q is not a <harness>-<native-id> session directory", dirName)})
				continue
			}

			item, diag, ok := loadWalkItem(root, shard, dirName)
			if !ok {
				diags = append(diags, diag)
				continue
			}
			items = append(items, item)
		}
	}
	return items, diags, nil
}

// loadWalkItem reads one session directory's documents into a WalkItem.
// dirName is the raw, on-disk directory name — session.json is read from
// it directly, and identity (Key) is built only from session.json's own
// Harness/SessionID fields afterward (M11's only authority), never from
// splitting dirName: a dashed harness name (claude-code, cursor-agent)
// would make a naive split wrong. ok is false when session.json or
// curation.json is missing or malformed, or when session.json's own
// identity doesn't reproduce dirName, in which case diag names the
// problem and the caller counts it rather than returning the item.
func loadWalkItem(root, shard, dirName string) (item WalkItem, diag Diagnostic, ok bool) {
	sessionPath := filepath.Join(root, "sessions", shard, dirName, "session.json")
	session, present, err := readDocument[Session](sessionPath)
	if err != nil {
		return WalkItem{}, Diagnostic{Path: sessionPath, Err: err}, false
	}
	if !present {
		return WalkItem{}, Diagnostic{Path: sessionPath, Err: fmt.Errorf("journal: missing session.json")}, false
	}

	key := SessionKey{Harness: session.Harness, NativeID: session.SessionID}
	if key.DirName() != dirName {
		return WalkItem{}, Diagnostic{Path: sessionPath, Err: fmt.Errorf("journal: session.json identity %q does not match its directory name %q", key.DirName(), dirName)}, false
	}

	curation, curationPresent, err := ReadCuration(root, shard, key)
	if err != nil {
		return WalkItem{}, Diagnostic{Path: CurationJSONPath(root, shard, key), Err: err}, false
	}

	entryPath := EntryPath(root, shard, key)
	_, statErr := os.Stat(entryPath)
	switch {
	case statErr == nil:
		return WalkItem{
			Key:             key,
			Shard:           shard,
			Session:         session,
			Curation:        curation,
			CurationPresent: curationPresent,
			EntryExists:     true,
		}, Diagnostic{}, true
	case os.IsNotExist(statErr):
		return WalkItem{
			Key:             key,
			Shard:           shard,
			Session:         session,
			Curation:        curation,
			CurationPresent: curationPresent,
			EntryExists:     false,
		}, Diagnostic{}, true
	default:
		return WalkItem{}, Diagnostic{Path: entryPath, Err: statErr}, false
	}
}

// looksLikeSessionDirName is the pre-read shape gate only: does dirName
// look at all like <harness>-<native-id> (an interior dash with both
// sides non-empty)? It deliberately does NOT build a SessionKey from the
// split — a dashed harness name (claude-code, cursor-agent) makes a naive
// first-dash split wrong, so identity is only ever read from session.json
// itself (loadWalkItem). This gate exists purely to keep a directory that
// plainly isn't shaped like a session at all (no dash whatsoever) out of
// loadWalkItem's error path.
func looksLikeSessionDirName(dirName string) bool {
	harness, nativeID, found := strings.Cut(dirName, "-")
	return found && harness != "" && nativeID != ""
}

// dirNames returns the names of entries that are directories, silently
// dropping any stray file — Walk only ever diagnoses a directory whose
// name doesn't fit the shape it should, never a plain file sitting where
// one wasn't expected.
func dirNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
