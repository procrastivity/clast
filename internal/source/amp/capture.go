package amp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// Capture implements source.Source for the network model (M13): the
// bounded-retry export (≤3 attempts over ~10s, contract step-02 §5)
// whose bytes are written verbatim as transcript.json while the
// projection hash and the M10 facts are computed from those exact bytes
// in the same pass — fingerprint and artifact can never diverge.
//
// Per-item failure modes per the contract: "does not exist" (deleted/
// expired) and every other terminal failure return as an error — the
// capture verb turns it into the per-item diagnostic (never retried,
// never a committed empty capture). A mid-turn doc is a faithful clean
// prefix, not an error: it is captured with a diagnostic note carrying
// the agent's reported state, and updatedAt-driven recapture converges
// it later.
func (s *Source) Capture(ctx context.Context, d source.Discovered, write source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	raw, doc, note, err := s.acquireExport(ctx, d)
	if err != nil {
		// A failed capture's id stays in the scan-state pending lane —
		// the contract's "Capture appends failed ids" rule realized by
		// pending-is-emitted — so a later sweep retries it. The terminal
		// gone-verdicts are the exception — a thread that provably does
		// not exist can never converge — and so is the quarantine
		// verdict: an export over the byte budget can never succeed at
		// this budget, so it settles instead of probing forever.
		if isErrKind(err, errNotExist) || isErrKind(err, errInvalidID) ||
			isErrKind(err, errQuarantined) || isErrKind(err, errCap) {
			s.settlePending(d.NativeID)
		} else {
			s.notePending(d.NativeID)
		}
		return source.Facts{}, nil, err
	}
	var diags []source.Diagnostic
	if note != "" {
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: errors.New("amp: " + note)})
	}
	if len(doc.Messages) == 0 {
		if n := s.reportedCount(d.NativeID); n > 0 {
			diags = append(diags, source.Diagnostic{Path: d.Path, Err: fmt.Errorf(
				"amp: export contains no messages but threads list reports %d user prompts", n)})
		}
	}
	if err := write(TranscriptFile, bytes.NewReader(raw)); err != nil {
		s.notePending(d.NativeID) // staged-or-not, nothing committed — the lane retries it
		return source.Facts{}, diags, err
	}
	facts := factsFromDoc(doc)
	// A nonempty note means the committed read is a flagged snapshot —
	// a mid-turn prefix or a raced read past its retry bound. The flag
	// rides into session.json (session.incomplete) so the shortfall
	// stays disclosed in the record itself, and Supersedes's repair
	// lane can tell a flagged commit from a clean one.
	facts.Incomplete = note != ""
	return facts, diags, nil
}

// Unchanged implements the OPTIONAL source.Unchanged seam — the
// fetch-free recapture verdict. The enumerated updatedAt and the stored
// export's updatedAt are the same revision field end to end, so a
// ModTime that has not moved past the recorded last_active_at means the
// captured copy is still current. Fetching to compare would defeat the
// point — and a fingerprint over the export's raw bytes would churn on
// the metadata bumps the projection deliberately ignores anyway.
//
// A confirmed-unchanged id also settles out of the pending set: prior
// came from the journal's own committed session.json, which is the
// strongest witness a commit can have — stronger than Capture's own
// success was at the time, since the artifact was only staged then and
// session.json had yet to commit (the crash window scanstate.go exists
// for).
func (s *Source) Unchanged(_ context.Context, d source.Discovered, prior journal.Session) (bool, error) {
	if d.ModTime.After(prior.LastActiveAt) {
		return false, nil
	}
	s.settlePending(d.NativeID)
	return true, nil
}

// Supersedes implements the OPTIONAL source.Supersede seam — the
// recapture veto. Committed truth is replaced only by a document
// provably not worse:
//
//   - strictly ahead — the fetched doc's own updatedAt is after the
//     recorded last_active_at AND it carries at least as many messages
//     as the committed transcript recorded; or
//   - a same-revision repair — the committed capture was itself
//     flagged incomplete (session.json's incomplete marker) and the
//     re-read carries at least as many messages at the same stamp: a
//     fuller view of the same revision, or the same content finally
//     reading clean.
//
// Anything else — a doc stamped behind the committed revision, a doc
// carrying fewer messages, or a same-revision re-read of a clean
// commit — is refused: append-only export evidence (step-02 §1:
// mid-turn docs are clean prefixes, messages never disappear) makes a
// shorter or not-ahead read proof of a partial or stale read, and
// metadata churn can never justify losing committed content. A refusal
// discards the staged bytes; the id stays pending (emitted ids are
// pending) so the next sweep re-reads and converges.
func (s *Source) Supersedes(_ context.Context, d source.Discovered, prior journal.Session, facts source.Facts) (bool, string) {
	if facts.Transcript.Lines < prior.Transcript.Lines {
		return false, fmt.Sprintf(
			"amp: refusing to replace the committed capture of %s: the fresh export carries %d messages where the journal already holds %d — a shorter read is a partial read, not a shrink",
			d.NativeID, facts.Transcript.Lines, prior.Transcript.Lines)
	}
	if facts.LastActiveAt.After(prior.LastActiveAt) {
		return true, ""
	}
	// Same revision stamp: only a flagged commit may be superseded in
	// place — by a fuller re-read of the same revision, or by the same
	// content finally reading clean. An identical still-flagged
	// re-read rewrites nothing (churn, not a repair), and a clean
	// commit's equal re-read is contradictory evidence, not progress.
	if facts.LastActiveAt.Equal(prior.LastActiveAt) && prior.Incomplete &&
		(facts.Transcript.Lines > prior.Transcript.Lines || !facts.Incomplete) {
		return true, ""
	}
	return false, fmt.Sprintf(
		"amp: refusing to replace the committed capture of %s: the fresh export's updatedAt %s is not ahead of the committed %s — a stale or same-revision read proves nothing new",
		d.NativeID, facts.LastActiveAt.Format(time.RFC3339Nano), prior.LastActiveAt.Format(time.RFC3339Nano))
}

// factsFromDoc computes the M10 facts and the contract's content-scoped
// fingerprint (format B): the sha256 covers a canonical
// one-line-per-message projection of messages[], so metadata churn —
// archive/pin/rename, v, updatedAt — can never report a conversation
// change that did not happen. Lines carries the message count for this
// format.
func factsFromDoc(doc *ExportDoc) source.Facts {
	proj := projectMessages(doc)
	sum := sha256.Sum256(proj)

	var user, assistant int
	for _, m := range doc.Messages {
		switch m.Role {
		case "assistant":
			assistant++
		case "user":
			if isPrompt(m) {
				user++
			}
		}
	}
	var updatedAt time.Time
	if t, err := time.Parse(time.RFC3339Nano, doc.UpdatedAt); err == nil {
		updatedAt = t
	}
	var startedAt time.Time
	if doc.Created != 0 {
		startedAt = time.UnixMilli(doc.Created).UTC()
	}
	return source.Facts{
		StartedAt:    startedAt,
		LastActiveAt: updatedAt,
		Branch:       "", // the export's repo sha is a commit, not a branch — evidence stays in the artifact
		Counts:       journal.SessionCounts{User: user, Assistant: assistant},
		Substantive:  assistant >= 1,
		Transcript: journal.TranscriptFingerprint{
			Format:   TranscriptFormat,
			Lines:    len(doc.Messages),
			SHA256:   hex.EncodeToString(sum[:]),
			Artifact: TranscriptFile, // "transcript.json" — the amendment's recorded name
		},
	}
}

// isPrompt counts the way amp's own messageCount does (contract step-02
// §2): a user-role message carrying at least one text block and no
// tool_result block — a human prompt, not a tool-result carrier.
func isPrompt(m ExportMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return false
	}
	hasText := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			return false
		case "text":
			hasText = true
		}
	}
	return hasText
}

// projectMessages renders the fingerprint projection (contract step-03
// §7): one canonical JSON line per messages[] element, in array order —
// {content, id, role, state?} where id is protocolMessageID, else
// "mid:<messageId>" (the old epoch, which lacks pmids), else the array
// index. Everything not on the line — v, updatedAt, title,
// pinned/archived, meta.*, lkas.*, readAt, usage, per-message
// timestamps, userState, protocolMessageVersion — is excluded as
// volatile or non-conversation by construction, and the artifact stores
// the verbatim bytes regardless.
func projectMessages(doc *ExportDoc) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for i, m := range doc.Messages {
		line := map[string]any{
			"content": canonical(m.Content),
			"id":      entryID(m, i),
			"role":    m.Role,
		}
		if len(m.State) > 0 {
			line["state"] = canonical(m.State)
		}
		// bytes.Buffer never errors; map keys marshal sorted, so the
		// line is canonical by construction.
		_ = enc.Encode(line)
	}
	return buf.Bytes()
}

// canonical re-encodes one raw JSON value compactly with object keys
// sorted — decoded with UseNumber so numbers round-trip exactly. An
// undecodable value degrades to its raw string form rather than failing
// the projection.
func canonical(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return string(raw)
	}
	return v
}

// entryID is the message's stable id for the projection (M14):
// protocolMessageID where present, "mid:<messageId>" in the old epoch
// that lacks pmids, and the array index when neither exists.
func entryID(m ExportMessage, i int) string {
	if m.ProtocolMessageID != "" {
		return m.ProtocolMessageID
	}
	if m.MessageID != nil {
		return fmt.Sprintf("mid:%d", *m.MessageID)
	}
	return fmt.Sprintf("idx:%d", i)
}
