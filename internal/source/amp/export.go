package amp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// ExportDoc is the minimal structural read of one `threads export`
// document — exactly the fields completeness, correlation, and facts
// need. The export's `v` is a per-thread revision counter (NOT a schema
// version — there is none), and two serialization epochs coexist, so
// parsing stays tolerant: every field is read-if-present and the stored
// artifact is always the verbatim bytes, never this projection of them.
type ExportDoc struct {
	ID        string `json:"id"`
	V         int    `json:"v"`
	Created   int64  `json:"created"` // epoch ms
	UpdatedAt string `json:"updatedAt"`

	Env struct {
		Initial *struct {
			WorkingDirectory string `json:"workingDirectory"`
			Hostname         string `json:"hostname"`
			Trees            []struct {
				URI string `json:"uri"`
			} `json:"trees"`
			Platform *struct {
				InstallationID string `json:"installationID"`
			} `json:"platform"`
		} `json:"initial"`
	} `json:"env"`

	Meta struct {
		ExecutorType        string `json:"executorType"`
		LastKnownAgentState *struct {
			State     string `json:"state"`
			MessageID string `json:"messageID"`
		} `json:"lastKnownAgentState"`
	} `json:"meta"`

	Messages []ExportMessage `json:"messages"`
}

// ExportMessage carries the per-message fields the completeness check,
// projection, and counts read, plus the read-side fields the transcript
// reader needs (timestamps, the model line) — all read-if-present, all
// ignored by the capture projection. Content and State stay raw — the
// fingerprint projection canonicalizes them itself.
type ExportMessage struct {
	Role              string          `json:"role"`
	MessageID         *int64          `json:"messageId"`
	ProtocolMessageID string          `json:"protocolMessageID"`
	State             json.RawMessage `json:"state"`
	Content           json.RawMessage `json:"content"`

	// Read-side fields (TranscriptReader/TranscriptRenderer only):
	// createdAt is the new epoch's per-message timestamp (RFC3339 ms);
	// meta.sentAt is the epoch-ms stamp user messages carry in both
	// epochs (the only per-message time the old epoch has); usage.model
	// is the effective model on assistant messages.
	CreatedAt string `json:"createdAt"`
	Meta      struct {
		SentAt int64 `json:"sentAt"`
	} `json:"meta"`
	Usage *struct {
		Model string `json:"model"`
	} `json:"usage"`
}

// ParseExport decodes one export document tolerantly — every field
// read-if-present (the build train can drift the schema again). A doc
// that is not even a JSON object carrying an id is the unparseable case
// the capture retry exists for.
func ParseExport(raw []byte) (*ExportDoc, error) {
	var doc ExportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.ID == "" {
		// `null` and `{}` unmarshal "successfully" into the struct — a
		// doc with no id is not an export.
		return nil, errors.New("export document carries no id")
	}
	return &doc, nil
}

// CompleteVerdict is the completeness check's three-way answer
// (contract step-02 §5): a doc can be captured clean, captured with a
// flag, or retried because a raced server view can still converge.
type CompleteVerdict int

const (
	// CompleteOK is a parseable, structurally consistent, lkas-anchored
	// doc (or lkas-absent — the old epoch).
	CompleteOK CompleteVerdict = iota
	// CompleteFlag is capturable now with a note: the agent's reported
	// position is mid-turn (streaming/tool_use) — a faithful clean
	// prefix, never corruption, and never worth a blocking wait.
	CompleteFlag
	// CompleteRetry is not yet complete; a bounded retry may converge.
	// Past the bound the doc is captured with the note anyway — the
	// prefix is still faithful.
	CompleteRetry
)

// OldEpoch reports whether the doc predates the new serialization epoch
// (step-02 §2): protocolMessageID exists only in the new epoch, so a
// non-empty doc with none is the old epoch (its messageIds are 0-based;
// completeness already infers that base implicitly). An empty doc is
// unevidenced — neither epoch.
func (d *ExportDoc) OldEpoch() bool {
	for _, m := range d.Messages {
		if m.ProtocolMessageID != "" {
			return false
		}
	}
	return len(d.Messages) > 0
}

// Completeness applies the contract rule: lkas anchors (state "idle"
// requires its messageID in the doc's protocolMessageIDs) plus the two
// structural checks — contiguous messageIds (1..N new epoch, 0..N-1 old)
// and a complete state on every assistant message. Structural gaps are
// retryable the same way the lkas race is: a later read can converge.
func (d *ExportDoc) Completeness() (CompleteVerdict, string) {
	msgs := d.Messages
	if len(msgs) == 0 {
		return CompleteRetry, "export contains no messages"
	}

	pmids := make(map[string]struct{}, len(msgs))
	var gaps []string
	base := int64(-1) // set from the first messageId; -1 = unset
	badState := 0
	for i, m := range msgs {
		if m.ProtocolMessageID != "" {
			pmids[m.ProtocolMessageID] = struct{}{}
		}
		switch {
		case m.MessageID == nil:
			gaps = append(gaps, fmt.Sprintf("messages[%d] carries no messageId", i))
		case base < 0:
			base = *m.MessageID
		case *m.MessageID != base+int64(i):
			gaps = append(gaps, fmt.Sprintf("messages[%d].messageId %d breaks the contiguous sequence from %d",
				i, *m.MessageID, base))
		}
		if m.Role == "assistant" {
			var st struct {
				Type string `json:"type"`
			}
			if len(m.State) == 0 || json.Unmarshal(m.State, &st) != nil || st.Type != "complete" {
				badState++
			}
		}
	}
	if len(gaps) > 0 {
		return CompleteRetry, strings.Join(gaps, "; ")
	}
	if badState > 0 {
		return CompleteRetry, fmt.Sprintf("%d assistant message(s) lack a complete state", badState)
	}

	lkas := d.Meta.LastKnownAgentState
	if lkas == nil {
		// Old epoch: the structural checks carry the whole verdict.
		return CompleteOK, ""
	}
	if lkas.State == "idle" {
		if _, ok := pmids[lkas.MessageID]; !ok {
			// The server view raced ahead of the read — the
			// bounded-retry case that can converge.
			return CompleteRetry, fmt.Sprintf("lkas reports idle at %s but the export does not contain that protocolMessageID",
				lkas.MessageID)
		}
		return CompleteOK, ""
	}
	// streaming / tool_use / a parked approval: a faithful clean prefix.
	// The agent's position rides along as the diagnostic note; recapture
	// converges via updatedAt, never via a blocking wait.
	return CompleteFlag, fmt.Sprintf("agent state %q (mid-turn snapshot)", lkas.State)
}

// exportRetryWaits backs the bounded retry: at most 3 attempts spread
// over ~10s of waiting (contract step-02 §5) — a raced server view or
// transient transport failure can converge inside it; beyond it the
// last doc (if any) is captured with the note, or the last error
// surfaces as the item's diagnostic.
var exportRetryWaits = []time.Duration{3 * time.Second, 7 * time.Second}

// exportOnce fetches one `threads export` document — the per-item read.
// A nil-error return carries bytes that parse and carry the requested
// id; anything else (transport, timeout, cap, non-zero exit, malformed
// JSON, id mismatch) is the error the retry loop classifies.
func (s *Source) exportOnce(ctx context.Context, id string) ([]byte, *ExportDoc, error) {
	op := "amp threads export " + id
	res, err := s.cli(ctx, []string{"threads", "export", id}, s.exportBudget)
	if err != nil {
		if isErrKind(err, errCap) {
			// An export over the byte budget is unreadable at this
			// budget and only ever grows — record the quarantine so
			// later sweeps spend zero fetches on it.
			s.quarantineExport(id)
		}
		return nil, nil, err
	}
	doc, err := ParseExport(res.stdout)
	if err != nil {
		return nil, nil, &cliError{kind: errExit, op: op, detail: "malformed export document: " + err.Error()}
	}
	if doc.ID != id {
		return nil, nil, &cliError{
			kind: errInvalidID, op: op,
			detail: "export document carries id " + doc.ID,
		}
	}
	return res.stdout, doc, nil
}

// exportFor is the memoized single-shot fetch Correlate uses: one export
// per enumerated revision per run (contract step-03 §9). The memo keys
// on (id, enumerated updatedAt) — a Discovered carrying a different
// ModTime is a different known revision and fetches fresh.
func (s *Source) exportFor(ctx context.Context, d source.Discovered) ([]byte, *ExportDoc, error) {
	if s.exportQuarantined(d.NativeID) {
		// A recorded cap failure at a budget this run cannot beat: the
		// fetch is refused without a call and the id settles — the
		// pending lane exists for work that can still converge.
		s.settlePending(d.NativeID)
		return nil, nil, &cliError{
			kind: errQuarantined,
			op:   "amp threads export " + d.NativeID,
			detail: fmt.Sprintf("export exceeds the %d-byte budget — quarantined until the budget grows",
				s.exportBudget.maxBytes),
		}
	}
	s.mu.Lock()
	if s.memo.id == d.NativeID && s.memo.modTime.Equal(d.ModTime) {
		raw, doc := s.memo.raw, s.memo.doc
		s.mu.Unlock()
		return raw, doc, nil
	}
	s.mu.Unlock()

	raw, doc, err := s.exportOnce(ctx, d.NativeID)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.memo = memoExport{id: d.NativeID, modTime: d.ModTime, raw: raw, doc: doc}
	s.mu.Unlock()
	return raw, doc, nil
}

// acquireExport is Capture's fetch: the memo first, then bounded
// retries. A nil-error return always carries capturable bytes — a
// retryable verdict past the bound still returns the last doc with its
// note; only fetch-level failures (transport, timeout, cap, malformed
// doc — nothing faithful to store) surface as errors, and terminal
// ones (does-not-exist, invalid id, auth, missing binary) fail fast
// without consuming retries.
func (s *Source) acquireExport(ctx context.Context, d source.Discovered) (raw []byte, doc *ExportDoc, note string, err error) {
	var lastErr error
	for attempt := 0; attempt <= len(exportRetryWaits); attempt++ {
		if attempt > 0 {
			if err := s.wait(ctx, exportRetryWaits[attempt-1]); err != nil {
				return nil, nil, "", err // caller ctx died while waiting — fatal upstream
			}
		}
		var attemptRaw []byte
		var attemptDoc *ExportDoc
		var attemptErr error
		if attempt == 0 {
			attemptRaw, attemptDoc, attemptErr = s.exportFor(ctx, d)
		} else {
			// A fresh read, not the memo — the memoized doc may be
			// exactly the raced one we are retrying.
			attemptRaw, attemptDoc, attemptErr = s.exportOnce(ctx, d.NativeID)
			if attemptErr == nil {
				s.mu.Lock()
				s.memo = memoExport{id: d.NativeID, modTime: d.ModTime, raw: attemptRaw, doc: attemptDoc}
				s.mu.Unlock()
			}
		}
		if attemptErr != nil {
			if terminalExportErr(attemptErr) {
				return nil, nil, "", attemptErr
			}
			lastErr = attemptErr
			continue
		}
		// A parseable doc on this attempt is a candidate artifact even
		// when its completeness verdict says retry — keep the freshest
		// so a later transport failure cannot strand an empty return.
		raw, doc = attemptRaw, attemptDoc
		switch verdict, vnote := attemptDoc.Completeness(); verdict {
		case CompleteOK:
			return raw, doc, "", nil
		case CompleteFlag:
			return raw, doc, vnote, nil
		default:
			// CompleteRetry — retry; if the bound is exhausted this
			// doc is still what gets captured, with the note.
			note = vnote
		}
	}
	if doc != nil {
		return raw, doc, note, nil
	}
	return nil, nil, "", lastErr
}
