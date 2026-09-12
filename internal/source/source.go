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
)

// Discovered is one live session enumerated by Discover: where it is,
// what its harness calls it, and when it last moved. It carries no
// parsed facts — extraction happens at Capture (M10), and correlation
// evidence is Correlate's own read.
type Discovered struct {
	// NativeID is the harness's own session id (M11) — a uuid for
	// claude. Format is harness-local; clast never parses it.
	NativeID string
	// Path is the session's primary on-disk artifact — for a file-tail
	// source, the transcript file itself.
	Path string
	// ModTime is Path's mtime, a cheap liveness hint for callers that
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
