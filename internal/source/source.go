// Package source owns the source seam (MODEL §7): the interface every
// harness source implements, plus the helpers file-tail sources share.
// One Go package per harness lives under internal/source/<harness>/,
// registered in internal/source/registry's one table (M12, C4.2's
// one-list-many-readers shape). The three duties — Discover, Correlate,
// Capture — are separable on purpose, mirroring duo's
// provider/correlator split: the evidence that binds a session to a
// working directory differs per harness, and pi and devin must slot in
// behind this interface without touching the truth layer.
//
// Boundaries: a source reads what its harness wrote and writes only the
// capture artifacts the verb hands it a destination for. It never
// resolves projects or clones (the registry's job, M17) and never
// composes session.json (the capture verb's job) — it only reports the
// Facts it alone can extract (M10).
package source

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/procrastivity/clast/internal/journal"
)

// StorageModel is M13's capture-posture axis: what "verbatim copy" means
// for a harness follows its storage model, not the harness itself. The
// interface carries it so sqlite-export and snapshot sources slot in
// later without an interface change; v1 implements only file-tail.
type StorageModel string

const (
	// FileTail marks a harness whose sessions are append-mostly files on
	// disk (claude, codex, pi): capture copies the file(s) verbatim,
	// sidecars included, and recapture re-copies grown files.
	FileTail StorageModel = "file-tail"
	// SQLiteExport marks a harness with no per-session file (opencode,
	// hermes, devin): capture writes a deterministic rows-as-JSON export.
	SQLiteExport StorageModel = "sqlite-export"
	// Snapshot marks a harness that rewrites its document whole (cursor):
	// capture replaces the copy wholesale, never assumes growth.
	Snapshot StorageModel = "snapshot"
	// Network marks a harness whose sessions live behind a network
	// endpoint rather than in local files (amp): capture fetches rather
	// than copies. The model doubles as the sweep gate — a network-model
	// source is never in a bare capture sweep until its
	// capture.<name>.auto opt-in is set (the shared capture-policy
	// contract); every other model is local and sweeps unconditionally.
	Network StorageModel = "network"
)

// Discovered is one live session enumerated by Discover: where it is,
// what its harness calls it, and when it last moved. It carries no
// parsed facts — extraction happens at Capture (M10), and correlation
// evidence is Correlate's own read.
type Discovered struct {
	// NativeID is the harness's own session id (M11) — a uuid for
	// claude. Format is harness-local; clast never parses it.
	NativeID string
	// Path is the session's primary source-native address — for a
	// file-tail source, the transcript file itself; for a network
	// source, the item's canonical URL (amp records
	// https://ampcode.com/threads/<id> — it is never openable as a
	// local path, which is exactly what Unchanged exists for).
	Path string
	// ModTime is Path's last-change hint — a file's mtime, a network
	// item's enumerated updatedAt — cheap liveness for callers that
	// order or window discovery. Never identity, never a captured fact.
	ModTime time.Time
}

// Facts is what Capture extracts from a session (M10): exactly the
// session.json fields only the source can know. The capture verb owns
// the rest of the document (machine, project, worktree, captured_at) and
// the write itself; when a harness cannot know a field (no branch in the
// transcript, say), its zero value stands, per M17's empty-means-no-fact
// convention.
type Facts struct {
	StartedAt    time.Time
	LastActiveAt time.Time
	Branch       string
	Counts       journal.SessionCounts
	Substantive  bool
	Transcript   journal.TranscriptFingerprint
	// Incomplete marks a capture whose own completeness check could not
	// prove the committed bytes cover the revision they record — a
	// mid-turn prefix, a raced read past its retry bound. The snapshot
	// is still faithful (it is what the source showed) and still
	// committed; the flag carries the shortfall into session.json
	// itself (session.incomplete) so it stays disclosed in the record
	// rather than only in a transient capture-time diagnostic. Sources
	// whose copy is whole by construction (a local file) never set it.
	Incomplete bool
}

// Diagnostic is one unreadable or skipped thing a source met while
// working: an unparseable session, a stat that failed, a line too
// mangled to count. Tolerant-parse posture (M12/V13): diagnostics go to
// stderr and never fail a run, so they are values, not errors.
type Diagnostic struct {
	// Path names what was being read; may carry a fragment ("…:line 12")
	// when the harness format makes that useful.
	Path string
	Err  error
}

// Source is one harness's row of behavior behind the registry table:
// the three duties of M12. Implementations live under
// internal/source/<harness>/ and are stateless — every call carries its
// own inputs.
type Source interface {
	// Name is the registry key and the M11 identity prefix ("claude").
	Name() string
	// Model is the harness's M13 storage model, fixed per source.
	Model() StorageModel
	// Discover enumerates this machine's sessions for this harness. An
	// absent harness (its storage root does not exist) is nil, nil, nil —
	// not an error. Individually unreadable sessions land in diags.
	Discover(ctx context.Context) (found []Discovered, diags []Diagnostic, err error)
	// Correlate binds d to the working directory it ran in, from
	// harness-local evidence (claude: the cwd field in the transcript
	// beats path-segment decoding). "" means unknown — a session without
	// a directory is still capturable, just projectless.
	Correlate(ctx context.Context, d Discovered) (dir string, diags []Diagnostic, err error)
	// Capture copies d's transcript into the session directory per the
	// source's storage model (M13) — verbatim, sidecars included, via
	// write — and extracts Facts (M10). write places one artifact at a
	// relative path under the session directory; Capture never composes
	// journal paths itself. Recapture is the same call: file-tail
	// sources re-copy grown files.
	Capture(ctx context.Context, d Discovered, write WriteArtifact) (Facts, []Diagnostic, error)
}

// WriteArtifact places one capture artifact at relPath (slash-separated,
// relative to the session's directory — "transcript.jsonl",
// "subagents/agent-….jsonl") from r, atomically. The capture verb binds
// it to the journal's own write primitive, so sources stay out of
// journal path composition and tests can bind it to a scratch directory.
type WriteArtifact func(relPath string, r io.Reader) error

// Presence is an OPTIONAL interface a Source may also implement, in the
// TranscriptRenderer/TranscriptReader mold. nil = storage present;
// non-nil = unavailable — the error names the probed root/endpoint and
// reason, used verbatim in the diagnostic. Cheap and side-effect-free:
// no enumeration, no prompt, no fetch.
//
// Its one caller is capture's explicit --harness path, which asserts it
// before Discover so an absent storage root is disclosed
// (capture.source-unavailable) rather than silently exiting; sweeps
// never probe — quiet absence is Discover's own contract there. A
// source without the probe degrades to that same legacy behavior on the
// explicit path (empty Discover = quiet exit 0), so the interface stays
// strictly additive; every registered source is still expected to
// implement it (pinned by a registry conformance test).
type Presence interface {
	Present(ctx context.Context) error
}

// Unchanged is an OPTIONAL interface a Source may also implement, in the
// same mold: it answers the recapture check — "is the session already
// captured at this revision" — from the source's own cheap evidence
// instead of re-hashing bytes on disk. The default stays FingerprintFile
// on d.Path: a file-tail source re-reads its live file. A source whose
// Path is not openable locally (a network source's canonical URL —
// hashing bytes would mean fetching, which defeats the check's point)
// implements it so its recapture verdict stays free.
//
// prior is the session.json document the journal walk already read —
// the recorded last_active_at and transcript fingerprint. A true answer
// runs the same unchanged branch a fingerprint match runs: silent when
// the prior resolved a project, the project-backfill retry otherwise.
// Fetch-free expectation: implementations answer from evidence already
// in hand (amp: the enumerated updatedAt against the recorded
// last_active_at — the same revision field end to end); a source that
// would need a fetch to decide says "changed" and lets Capture do the
// real read.
type Unchanged interface {
	Unchanged(ctx context.Context, d Discovered, prior journal.Session) (bool, error)
}

// JournalScope is an OPTIONAL interface a Source may also implement, in
// the same mold as Presence and Unchanged: the capture verb hands it
// the run's resolved journal root once, before Discover, so a source
// whose progress bookkeeping lives outside the journal (amp's
// scan-state cache) can key that bookkeeping to the target — progress
// banked against one journal must never answer for a different one.
// Sources with no cross-run state need nothing and are never visited.
type JournalScope interface {
	ScopeJournal(root string)
}

// Supersede is an OPTIONAL interface a Source may also implement, in the
// same mold as Unchanged: on a recapture it decides whether the
// just-captured revision may replace the committed session — the veto
// that keeps a partial or stale read from overwriting a more complete
// committed capture merely because the source's metadata advanced. The
// verb calls it after Capture produced facts and before the staged
// artifacts commit: a false verdict discards the stage (the committed
// session.json and artifacts stand untouched) and reports why as a
// per-item diagnostic, leaving the id on whatever retry bookkeeping the
// source keeps — a veto declines a write, it never declares the thread
// done. A source without it keeps the default posture: a successful
// Capture always replaces the committed session.
type Supersede interface {
	// Supersedes reports whether the capture of d that produced facts
	// may replace prior (the committed session.json document). The
	// verdict compares the fresh read's own revision evidence against
	// what the journal already holds; why is disclosure text for the
	// diagnostic when ok is false.
	Supersedes(ctx context.Context, d Discovered, prior journal.Session, facts Facts) (ok bool, why string)
}

// Turn is one rendered conversation turn: who spoke, and what they said
// (possibly capped — see TranscriptRenderer.RenderTranscript).
type Turn struct {
	Role string
	Text string
}

// TranscriptRenderer is an OPTIONAL interface a Source may also
// implement: rendering a captured transcript copy's turns for `show
// --transcript` (SURFACE V18) — the one sanctioned transcript read (M9).
// It is deliberately never a fourth duty on Source itself: capture's
// Brief seals Discover/Correlate/Capture as the three-duty interface pi
// and devin must slot into unchanged, and a source with no renderer yet
// is still fully capturable — only V18's display path cares whether one
// exists. Resolution keys on session.json's own transcript.format (M10),
// never on harness name, since format (not harness) is what determines
// how to parse a copy — see internal/source/registry.LookupTranscriptRenderer.
type TranscriptRenderer interface {
	// TranscriptFormats lists the transcript.format values (M10) this
	// source knows how to render — ordinarily exactly the one format its
	// own Capture stamps, declared as a list so a source whose storage
	// model changes format over its history can still render older
	// captures under their original format string.
	TranscriptFormats() []string
	// RenderTranscript parses r (the raw transcript copy's bytes,
	// streamed) into a sequence of Turns. maxChars, when > 0, caps each
	// turn's Text at that many runes (V7's prompt-budget truncation,
	// `--max-turn-chars`) — never bytes, so a capped turn never ends
	// mid-codepoint.
	RenderTranscript(r io.Reader, maxChars int) ([]Turn, error)
}

// EventKind classifies one Event of a structured transcript read. The set
// is deliberately small and source-agnostic: a harness's dozens of
// bookkeeping record types collapse into KindMeta with a Label, so a
// viewer never has to know any one harness's vocabulary.
type EventKind string

const (
	// KindPrompt is the human speaking. Injected turns (task
	// notifications, peer messages, local-command caveats) are KindMeta.
	KindPrompt EventKind = "prompt"
	// KindAssistant is assistant prose.
	KindAssistant EventKind = "assistant"
	// KindThinking is assistant reasoning; Redacted marks a block that
	// carries no readable text (only a signature, or a redacted block).
	KindThinking EventKind = "thinking"
	// KindToolCall is one tool invocation with its paired result (Tool.
	// Result), or nil when the run ended before a result was written.
	KindToolCall EventKind = "tool_call"
	// KindToolResult is an orphan: a result whose call is not in the
	// stream (a truncated or forked copy). Paired results never appear
	// as their own event.
	KindToolResult EventKind = "tool_result"
	// KindMeta is everything else: bookkeeping, system notices, injected
	// context. Label says what; Text is a short readable payload or "".
	KindMeta EventKind = "meta"
)

// Event is one item of a structured transcript, in stream order.
type Event struct {
	Kind EventKind
	// Time is the record's own timestamp; zero when it carries none.
	Time time.Time
	// ID is the harness's entry id, when it has one.
	ID string
	// Text is the prose (prompt, assistant, thinking) or the meta payload.
	Text string
	// Images counts image blocks that Text cannot carry; a viewer shows a
	// placeholder, since transcripts hold the pixels only as base64.
	Images int
	// Redacted marks a KindThinking event with no readable text.
	Redacted bool
	// Label names a KindMeta event's sub-kind ("system:turn_duration",
	// "attachment:date", "mode"). Empty for every other kind.
	Label string
	// Tool is set for KindToolCall and KindToolResult.
	Tool *ToolCall
}

// ToolCall is one tool invocation and, when it was written, its result.
type ToolCall struct {
	// ID is the pairing key (tool_use_id).
	ID   string
	Name string
	// Input is the call's arguments as raw JSON, for the viewer to
	// pretty-print; nil for an orphan result.
	Input  json.RawMessage
	Result *ToolResult
}

// ToolResult is a tool's output.
type ToolResult struct {
	Time    time.Time
	Text    string
	Images  int
	IsError bool
}

// Subagent is one subagent transcript captured beside a session's own,
// with the linkage back to the parent's tool call that spawned it.
type Subagent struct {
	// ID is the harness's subagent id.
	ID string
	// Path is the transcript's path relative to the session directory
	// (slash-separated, as WriteArtifact placed it).
	Path        string
	AgentType   string
	Description string
	// ToolUseID matches ToolCall.ID of the parent's spawning call; ""
	// when the sidecar metadata is missing.
	ToolUseID string
}

// TranscriptReader is an OPTIONAL interface a Source may also implement:
// a structured read of a captured transcript copy for the analyze
// explorer's transcript view. It sits beside TranscriptRenderer, not in
// place of it — the renderer is `show --transcript`'s deliberately thin,
// capped prose; this keeps tool calls, thinking, and meta records that
// the renderer drops. Same resolution rule: keyed on session.json's
// transcript.format, see internal/source/registry.LookupTranscriptReader.
type TranscriptReader interface {
	// TranscriptFormats lists the transcript.format values this source
	// can read structurally.
	TranscriptFormats() []string
	// ReadTranscript parses r (a transcript copy, streamed) into Events
	// in stream order. Tolerant: an unparseable line is skipped. On a
	// read failure it returns the events gathered so far with the error.
	ReadTranscript(r io.Reader) ([]Event, error)
	// ListSubagents lists the subagent transcripts captured under
	// sessionDir, sorted by ID. A session without any is nil, nil.
	ListSubagents(sessionDir string) ([]Subagent, error)
}
