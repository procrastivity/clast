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
	// Key is this session's identity (M11).
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
// answered by walking, never by a side index. Walk does not open
// transcript.jsonl or entry.md's contents (the M9 boundary — entry.md's
// presence is only stat'd) and it does not filter: that is the query
// verbs' job (SURFACE V17 etc.), not this package's.
//
// A missing journal root or missing sessions/ directory is not an error:
// it returns no items (the step-01 read posture). A shard directory not
// shaped YYYY-MM-DD, a session directory not shaped
// <harness>-<native-id>, or a session directory whose session.json (or
// curation.json) is missing or malformed is skipped and counted in diags
// rather than failing the walk — MODEL §7's "tolerant of what it doesn't
// own" posture, extended here to the tree's own shape. Anything else
// found inside a session directory (stray files, an unreadable
// transcript.jsonl) is simply ignored: this step only ever reads
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
			key, ok := parseSessionDirName(dirName)
			if !ok {
				diags = append(diags, Diagnostic{Path: sessionDir, Err: fmt.Errorf("journal: %q is not a <harness>-<native-id> session directory", dirName)})
				continue
			}

			item, diag, ok := loadWalkItem(root, shard, key)
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
// ok is false when session.json or curation.json is missing-but-should-
// exist-differently than expected or malformed, in which case diag names
// the problem and the caller counts it rather than returning the item.
func loadWalkItem(root, shard string, key SessionKey) (item WalkItem, diag Diagnostic, ok bool) {
	session, present, err := ReadSession(root, shard, key)
	if err != nil {
		return WalkItem{}, Diagnostic{Path: SessionJSONPath(root, shard, key), Err: err}, false
	}
	if !present {
		return WalkItem{}, Diagnostic{Path: SessionJSONPath(root, shard, key), Err: fmt.Errorf("journal: missing session.json")}, false
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

// parseSessionDirName recovers a SessionKey from a session directory name,
// <harness>-<native-id> (M11). Splitting on the first '-' is correct
// because a native id (a uuid, for claude) may itself contain dashes,
// while a harness name never does.
func parseSessionDirName(dirName string) (SessionKey, bool) {
	harness, nativeID, found := strings.Cut(dirName, "-")
	if !found || harness == "" || nativeID == "" {
		return SessionKey{}, false
	}
	return SessionKey{Harness: harness, NativeID: nativeID}, true
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
