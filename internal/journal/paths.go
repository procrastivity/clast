package journal

import "path/filepath"

// SessionKey is a session's identity per M11: the (harness, native id)
// pair. Native id formats are harness-local (claude uuid, codex uuidv7,
// hermes YYYYMMDD_HHMMSS_<8hex>, …), so uniqueness across harnesses is
// nobody's promise — every query key and cache key carries the pair, this
// one included. It deliberately excludes the day shard: shard is a path
// detail chosen once at capture (M8), not part of a session's identity.
type SessionKey struct {
	Harness  string
	NativeID string
}

// DirName is the session's directory name within its day shard:
// <harness>-<native-id> (M11), e.g. "claude-8f3a…".
func (k SessionKey) DirName() string {
	return k.Harness + "-" + k.NativeID
}

// SessionDir returns a session's directory: sessions/<shard>/<harness>-
// <native-id>, under root. shard is the YYYY-MM-DD local-calendar-date
// shard chosen at capture time and passed in by the caller — this package
// never recomputes it from a timestamp (M8).
func SessionDir(root, shard string, key SessionKey) string {
	return filepath.Join(root, "sessions", shard, key.DirName())
}

// SessionJSONPath is the capture-owned session.json inside a session's
// directory (MODEL §4).
func SessionJSONPath(root, shard string, key SessionKey) string {
	return filepath.Join(SessionDir(root, shard, key), "session.json")
}

// SessionJSONPathByDir is SessionJSONPath for a raw, not-yet-trusted
// on-disk directory name rather than a SessionKey — Walk's own situation
// (walk.go): it has dirName before it has read session.json, and reading
// session.json is what it needs the path for in the first place, so it
// can't build a SessionKey first. Keeping this join here too, rather than
// hand-joining "sessions"/shard/dirName/"session.json" in walk.go, keeps
// the tree's layout named in exactly one place.
func SessionJSONPathByDir(root, shard, dirName string) string {
	return filepath.Join(root, "sessions", shard, dirName, "session.json")
}

// CurationJSONPath is the curation-owned curation.json inside a session's
// directory; its absence means the session is still `captured` (MODEL §2,
// §4).
func CurationJSONPath(root, shard string, key SessionKey) string {
	return filepath.Join(SessionDir(root, shard, key), "curation.json")
}

// EntryPath is the curated entry.md inside a session's directory,
// present iff the session is curated (MODEL §4). This package only ever
// names the path — parsing its frontmatter and body is a later step's
// concern.
func EntryPath(root, shard string, key SessionKey) string {
	return filepath.Join(SessionDir(root, shard, key), "entry.md")
}

// TranscriptPath is the capture-owned, harness-native transcript copy
// inside a session's directory (MODEL §4, M13). Per the M9 boundary this
// package never parses it — it is opaque bytes, copied in and streamed
// out by whatever verb shows turns.
func TranscriptPath(root, shard string, key SessionKey) string {
	return filepath.Join(SessionDir(root, shard, key), "transcript.jsonl")
}

// ProjectDir returns a project's directory, projects/<slug>, under root.
func ProjectDir(root, slug string) string {
	return filepath.Join(root, "projects", slug)
}

// ProjectJSONPath is the shared project.json inside a project's directory
// (MODEL §4, M15).
func ProjectJSONPath(root, slug string) string {
	return filepath.Join(ProjectDir(root, slug), "project.json")
}

// ClonesJSONPath is one machine's per-project clone registry,
// clones.<machine>.json — written only by that machine (M5).
func ClonesJSONPath(root, slug, machine string) string {
	return filepath.Join(ProjectDir(root, slug), "clones."+machine+".json")
}

// BreadcrumbsPath is one machine's breadcrumb file for a given day shard,
// breadcrumbs/YYYY-MM-DD.<machine>.jsonl — appended only by that machine;
// readers glob YYYY-MM-DD.*.jsonl across machines (M5). Only the path is
// named here; append mechanics and the read-side glob are a later step.
func BreadcrumbsPath(root, shard, machine string) string {
	return filepath.Join(root, "breadcrumbs", shard+"."+machine+".jsonl")
}
