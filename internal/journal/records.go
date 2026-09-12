package journal

import "time"

// Every type below is one MODEL §4 document (or, for Breadcrumb, one
// JSONL line within breadcrumbs/…). Field names and nullability match the
// MODEL §4 JSON examples exactly — they are the schemas. Whole-document
// writes go through writeDocument (temp-file-and-rename, MODEL §4/M5);
// reads go through readDocument, which returns (zero, false, nil) for a
// missing file, never an error (the step-01 posture, carried through
// here).

// SessionProject is session.json's "project" field: the project this
// session ran in, resolved and frozen at capture time. A later slug
// rename does not rewrite history — Slug and Label are the names in
// effect when this session was captured, not a live lookup (MODEL §4,
// M15).
type SessionProject struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Clone string `json:"clone"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// SessionCounts is session.json's "counts" field: turn counts by role.
type SessionCounts struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
}

// TranscriptFingerprint is session.json's "transcript" field: the
// transcript's format plus enough of its shape (line count, content hash)
// to detect growth after capture (M7).
type TranscriptFingerprint struct {
	Format string `json:"format"`
	Lines  int    `json:"lines"`
	SHA256 string `json:"sha256"`
}

// Session is session.json, capture-owned (MODEL §4). Project is a pointer
// because a captured session need not belong to a registered project (a
// judgment call: MODEL §4's example always shows one, but nothing in §3-6
// requires it — capture, a later Matter, decides when it is nil).
type Session struct {
	SchemaVersion int                   `json:"schema_version"`
	Harness       string                `json:"harness"`
	SessionID     string                `json:"session_id"`
	Machine       string                `json:"machine"`
	Project       *SessionProject       `json:"project,omitempty"`
	Worktree      string                `json:"worktree"`
	Branch        string                `json:"branch"`
	StartedAt     time.Time             `json:"started_at"`
	LastActiveAt  time.Time             `json:"last_active_at"`
	CapturedAt    time.Time             `json:"captured_at"`
	SourcePath    string                `json:"source_path"`
	Counts        SessionCounts         `json:"counts"`
	Substantive   bool                  `json:"substantive"`
	Transcript    TranscriptFingerprint `json:"transcript"`
}

// WriteSession writes session.json for key under shard, creating the
// journal root and the session directory as needed.
func WriteSession(root, shard string, key SessionKey, s Session) error {
	s.SchemaVersion = recordSchemaVersion
	return writeDocument(root, SessionJSONPath(root, shard, key), s)
}

// ReadSession reads session.json for key under shard. A missing file
// returns (Session{}, false, nil) — never an error.
func ReadSession(root, shard string, key SessionKey) (Session, bool, error) {
	return readDocument[Session](SessionJSONPath(root, shard, key))
}

// CurationState is curation.json's "state" field (MODEL §2), plus
// StateCaptured — the one value never written to a curation.json (its
// absence means captured, by construction), only ever produced by
// walk.go's derivation from "does curation.json exist at all".
type CurationState string

const (
	// StateCaptured marks a session with no curation.json at all —
	// derived, never stored (walk.go).
	StateCaptured CurationState = "captured"
	// StateCurated marks a session with an entry (entry.md present).
	StateCurated CurationState = "curated"
	// StateDismissed marks a session deliberately excluded, with a reason.
	StateDismissed CurationState = "dismissed"
)

// TranscriptStamp is curation.json's "transcript_at_curation" field: the
// transcript fingerprint recorded at curation time, compared against the
// current session.json Transcript to derive `stale` (M7). It carries only
// Lines and SHA256, narrower than TranscriptFingerprint — a session's
// harness/format never changes after capture, so Format does not enter
// the comparison (matches the MODEL §4 example exactly: no "format" key).
type TranscriptStamp struct {
	Lines  int    `json:"lines"`
	SHA256 string `json:"sha256"`
}

// Curation is curation.json, curation-owned (MODEL §4); its absence means
// `captured` (MODEL §2). Reason is a pointer (not omitempty) because the
// curated case writes it explicitly as JSON null, per the MODEL example —
// only the dismissed case gives it a value. TranscriptAtCuration is
// omitempty because the dismissed case omits it entirely.
type Curation struct {
	SchemaVersion        int              `json:"schema_version"`
	State                CurationState    `json:"state"`
	At                   time.Time        `json:"at"`
	Machine              string           `json:"machine"`
	Reason               *string          `json:"reason"`
	TranscriptAtCuration *TranscriptStamp `json:"transcript_at_curation,omitempty"`
}

// WriteCuration writes curation.json for key under shard.
func WriteCuration(root, shard string, key SessionKey, c Curation) error {
	c.SchemaVersion = recordSchemaVersion
	return writeDocument(root, CurationJSONPath(root, shard, key), c)
}

// ReadCuration reads curation.json for key under shard. A missing file
// returns (Curation{}, false, nil) — never an error; callers read that as
// `captured` (MODEL §2), the derivation itself belongs to a later step.
func ReadCuration(root, shard string, key SessionKey) (Curation, bool, error) {
	return readDocument[Curation](CurationJSONPath(root, shard, key))
}

// Project is project.json, shared across every machine with a clone of
// this project (MODEL §4, M15). Identity is ID; Slug is a mutable name,
// never a key. Remote and IdentityRemote are both "" for a remoteless,
// keyless project (M15) — legal, not an error.
type Project struct {
	SchemaVersion  int    `json:"schema_version"`
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Remote         string `json:"remote"`
	IdentityRemote string `json:"identity_remote"`
}

// WriteProject writes projects/<slug>/project.json.
func WriteProject(root, slug string, p Project) error {
	p.SchemaVersion = recordSchemaVersion
	return writeDocument(root, ProjectJSONPath(root, slug), p)
}

// ReadProject reads projects/<slug>/project.json. A missing file returns
// (Project{}, false, nil) — never an error.
func ReadProject(root, slug string) (Project, bool, error) {
	return readDocument[Project](ProjectJSONPath(root, slug))
}

// Clone is one entry in a ClonesFile: a registered working copy of a
// project on the machine that file belongs to (MODEL §4, M15). Identity
// is ID; GitCommonDir is the lookup key (worktree-invariant); Label is a
// mutable name, never a key (M16).
type Clone struct {
	ID           string `json:"id"`
	GitCommonDir string `json:"git_common_dir"`
	Label        string `json:"label"`
}

// ClonesFile is clones.<machine>.json, per-machine (MODEL §4, M5): only
// the named Machine ever writes this file.
type ClonesFile struct {
	SchemaVersion int     `json:"schema_version"`
	Machine       string  `json:"machine"`
	Clones        []Clone `json:"clones"`
}

// WriteClones writes projects/<slug>/clones.<machine>.json, keyed by
// c.Machine.
func WriteClones(root, slug string, c ClonesFile) error {
	c.SchemaVersion = recordSchemaVersion
	return writeDocument(root, ClonesJSONPath(root, slug, c.Machine), c)
}

// ReadClones reads projects/<slug>/clones.<machine>.json. A missing file
// returns (ClonesFile{}, false, nil) — never an error.
func ReadClones(root, slug, machine string) (ClonesFile, bool, error) {
	return readDocument[ClonesFile](ClonesJSONPath(root, slug, machine))
}

// Breadcrumb is one line of breadcrumbs/YYYY-MM-DD.<machine>.jsonl (MODEL
// §4). It carries no schema_version: MODEL §4 reserves that key for whole
// documents, and a breadcrumb line is not one — JSONL here is genuinely
// line-appended data. Slug is a pointer because `slug: null` marks a
// global crumb (not scoped to any project); day-bucket attribution from At
// happens at read time (M8), not here. Append mechanics and the
// cross-machine glob read are a later step; this type only fixes the line
// shape so it can round-trip.
type Breadcrumb struct {
	At   time.Time `json:"at"`
	Slug *string   `json:"slug"`
	Text string    `json:"text"`
}
