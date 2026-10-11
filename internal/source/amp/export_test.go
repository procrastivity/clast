package amp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// parseFixture is ParseExport on a pinned export document.
func parseFixture(t *testing.T, rel string) *ExportDoc {
	t.Helper()
	doc, err := ParseExport(mustFixture(t, rel))
	if err != nil {
		t.Fatalf("ParseExport(%s): %v", rel, err)
	}
	return doc
}

// TestParseExport pins the minimal-metadata read: id, v (a revision
// counter, not a schema version), created, updatedAt, and the lkas
// anchor.
func TestParseExport(t *testing.T) {
	doc := parseFixture(t, "exports/idle-local-client.json")
	if doc.ID != "T-01a050ef-71e8-74bc-9b63-c0ad20e3a068" {
		t.Errorf("id = %q", doc.ID)
	}
	if doc.V != 19 {
		t.Errorf("v = %d, want 19", doc.V)
	}
	if doc.Created != 1788064264680 {
		t.Errorf("created = %d", doc.Created)
	}
	if doc.UpdatedAt != "2026-08-30T04:31:36.901Z" {
		t.Errorf("updatedAt = %q", doc.UpdatedAt)
	}
	lkas := doc.Meta.LastKnownAgentState
	if lkas == nil || lkas.State != "idle" || lkas.MessageID == "" {
		t.Fatalf("lkas = %+v", lkas)
	}
	if len(doc.Messages) != 4 {
		t.Errorf("messages = %d, want 4", len(doc.Messages))
	}
}

// TestParseExportMalformed: bytes that are not a JSON object carrying an
// id are the unparseable case — `null` and `{}` unmarshal cleanly into
// the struct yet are not exports.
func TestParseExportMalformed(t *testing.T) {
	for _, raw := range []string{
		"<html>error page</html>",
		`{"messages":`,
		`null`,
		`{}`,
		`{"messages":[]}`,
		`[]`,
	} {
		if _, err := ParseExport([]byte(raw)); err == nil {
			t.Errorf("ParseExport(%q) returned nil error", raw)
		}
	}
}

// TestCompleteness runs every pinned fixture through the verdict plus
// synthesized structural breaks — the table is the contract's
// completeness rule end to end.
func TestCompleteness(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want CompleteVerdict
		note string // "" = expect an empty note
	}{
		{"idle-local", mustFixture(t, "exports/idle-local-client.json"), CompleteOK, ""},
		{"idle-archived-pinned", mustFixture(t, "exports/idle-archived-pinned.json"), CompleteOK, ""},
		{"idle-mixed", mustFixture(t, "exports/idle-mixed-provenance.json"), CompleteOK, ""},
		{"idle-task-toolresult", mustFixture(t, "exports/idle-task-toolresult.json"), CompleteOK, ""},
		{"idle-mode-pinned", mustFixture(t, "exports/idle-mode-pinned.json"), CompleteOK, ""},
		// lkas absent → the old epoch — structure alone carries it.
		{"old-epoch", mustFixture(t, "exports/old-epoch.json"), CompleteOK, ""},
		// Mid-turn states are faithful clean prefixes: captured, flagged.
		{"midturn-streaming", mustFixture(t, "exports/midturn-streaming.json"), CompleteFlag, "streaming"},
		{"midturn-tooluse", mustFixture(t, "exports/midturn-tooluse.json"), CompleteFlag, "tool_use"},
		// Zero messages is retryable — a raced read can converge.
		{"empty-doc", mustFixture(t, "exports/edge-empty.json"), CompleteRetry, "no messages"},
		// Structural breaks.
		{
			"lkas-miss", []byte(`{"id":"T-x","messages":[
				{"role":"user","messageId":1,"protocolMessageID":"M-a","content":[{"type":"text","text":"hi"}]},
				{"role":"assistant","messageId":2,"protocolMessageID":"M-b","state":{"type":"complete"},"content":[]}],
				"meta":{"lastKnownAgentState":{"state":"idle","messageID":"M-not-present"}}}`),
			CompleteRetry, "does not contain that protocolMessageID",
		},
		{
			"messageId-gap", []byte(`{"id":"T-x","messages":[
				{"role":"user","messageId":1,"content":[]},
				{"role":"assistant","messageId":7,"state":{"type":"complete"},"content":[]}]}`),
			CompleteRetry, "breaks the contiguous sequence",
		},
		{
			"assistant-state-missing", []byte(`{"id":"T-x","messages":[
				{"role":"user","messageId":1,"content":[]},
				{"role":"assistant","messageId":2,"state":{"type":"streaming"},"content":[]}]}`),
			CompleteRetry, "lack a complete state",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseExport(tc.raw)
			if err != nil {
				t.Fatalf("ParseExport: %v", err)
			}
			got, note := doc.Completeness()
			if got != tc.want {
				t.Fatalf("verdict = %v (%q), want %v", got, note, tc.want)
			}
			if tc.note == "" {
				if note != "" {
					t.Errorf("note = %q, want empty", note)
				}
			} else if !strings.Contains(note, tc.note) {
				t.Errorf("note %q should contain %q", note, tc.note)
			}
		})
	}
}

// exportSource wires a fake CLI keyed on the exports map.
func exportSource(t *testing.T, f *fakeCLI) *Source {
	t.Helper()
	s := newTestSource(t, t.TempDir())
	s.run = f.run
	return s
}

// TestExportOnceHappyPath: raw bytes plus the parsed minimal metadata —
// the Capture contract's per-item fetch.
func TestExportOnceHappyPath(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json")
	f := &fakeCLI{t: t, exports: map[string][]byte{
		"T-01a050ef-71e8-74bc-9b63-c0ad20e3a068": raw,
	}}
	s := exportSource(t, f)
	got, doc, err := s.exportOnce(context.Background(), docID(t, raw))
	if err != nil {
		t.Fatalf("exportOnce: %v", err)
	}
	if string(got) != string(raw) {
		t.Error("exportOnce must return the verbatim bytes")
	}
	if doc.V != 19 || len(doc.Messages) != 4 {
		t.Errorf("doc = v%d/%d msgs, want v19/4", doc.V, len(doc.Messages))
	}
}

func docID(t *testing.T, raw []byte) string {
	t.Helper()
	doc, err := ParseExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc.ID
}

// TestExportOnceMalformed: an unparseable payload or an id that is not
// the requested one is a per-call failure the retry layer classifies.
func TestExportOnceMalformed(t *testing.T) {
	f := &fakeCLI{t: t, exports: map[string][]byte{
		"T-bad":      []byte(`{"id":"T-bad","messages":`),
		"T-mismatch": mustFixture(t, "exports/idle-local-client.json"),
	}}
	s := exportSource(t, f)
	if _, _, err := s.exportOnce(context.Background(), "T-bad"); err == nil ||
		!strings.Contains(err.Error(), "malformed export") {
		t.Errorf("T-bad: want malformed-export error, got %v", err)
	}
	_, _, err := s.exportOnce(context.Background(), "T-mismatch")
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errInvalidID {
		t.Errorf("T-mismatch: want errInvalidID, got %v", err)
	}
}

// TestExportMemo: Correlate's fetch is memoized on (id, enumerated
// updatedAt) — one export per revision per run, shared with Capture's
// first attempt. A changed ModTime is a new revision and refetches.
func TestExportMemo(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json")
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	s := exportSource(t, f)
	d := discoveredForID(t, raw)

	if _, _, err := s.Correlate(context.Background(), d); err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if _, _, _, err := s.acquireExport(context.Background(), d); err != nil {
		t.Fatalf("acquireExport: %v", err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d, want 1 memoized fetch", n)
	}

	// Same id, newer enumerated revision → a fresh fetch.
	d.ModTime = d.ModTime.Add(time.Hour)
	if _, _, _, err := s.acquireExport(context.Background(), d); err != nil {
		t.Fatalf("acquireExport (new revision): %v", err)
	}
	if n := f.exportCalls(); n != 2 {
		t.Fatalf("exportCalls = %d, want 2 after revision change", n)
	}
}

// TestExportQuarantineSettlesAndPersists pins the step-12 over-cap
// lane: an export over the byte budget is a permanent failure at that
// budget — recorded in the checkpoint's quarantined map, settled out
// of pending, and refused fetch-free by the NEXT run's instance at the
// same-or-smaller budget. Only a raised budget reopens the fetch.
func TestExportQuarantineSettlesAndPersists(t *testing.T) {
	payload := []byte(`{"id":"T-x","v":9,"created":1788064264680,"updatedAt":"2026-10-01T08:00:00.000Z",` +
		`"messages":[{"role":"user","messageId":0,"protocolMessageID":"M-x-0","state":{"type":"complete"},` +
		`"content":[{"type":"text","text":"hi"}]}]}`)
	f := &fakeCLI{
		t:       t,
		catalog: []fakeThread{{id: "T-x", updatedAt: "2026-10-01T08:00:00.000Z"}},
		exports: map[string][]byte{"T-x": payload},
	}
	dataDir := t.TempDir()
	cacheDir := t.TempDir()

	s1 := newTestSource(t, dataDir)
	s1.run = f.run
	s1.cacheDir = cacheDir
	found, _, err := s1.Discover(context.Background())
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover: found=%v err=%v", found, err)
	}
	s1.exportBudget.maxBytes = 16 // the seam-boundary cap: the payload is far larger

	_, _, err = s1.Capture(context.Background(), found[0], dirWriter(t, t.TempDir()))
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errCap {
		t.Fatalf("Capture: want errCap, got %v", err)
	}
	st := readScanState(t, s1)
	if len(st.PendingIDs) != 0 {
		t.Fatalf("pending_ids = %v — a permanently-capped export settles", st.PendingIDs)
	}
	if st.Quarantined["T-x"] != 16 {
		t.Fatalf("quarantined = %v, want T-x recorded at cap 16", st.Quarantined)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d — a cap failure fails fast, no bound burn", n)
	}

	// The next run's instance — cold memory, same scope, same budget —
	// refuses the fetch without a call, and the id still settles.
	s2 := newTestSource(t, dataDir)
	s2.run = f.run
	s2.cacheDir = cacheDir
	s2.exportBudget.maxBytes = 16
	d := source.Discovered{NativeID: "T-x", ModTime: mustParseRFC(t, "2026-10-01T08:00:00.000Z")}
	_, _, err = s2.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if !errors.As(err, &ce) || ce.kind != errQuarantined {
		t.Fatalf("quarantined Capture: want errQuarantined, got %v", err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d — a recorded cap must not refetch", n)
	}

	// A raised budget is the only reopen: the document fits now.
	s2.exportBudget.maxBytes = 4096
	facts, _, err := s2.Capture(context.Background(), d, dirWriter(t, t.TempDir()))
	if err != nil {
		t.Fatalf("raised-budget Capture: %v", err)
	}
	if facts.Transcript.Lines != 1 {
		t.Errorf("captured facts = %+v, want the now-fitting document", facts)
	}
	if n := f.exportCalls(); n != 2 {
		t.Fatalf("exportCalls = %d, want 2 — the raised budget reopens one fetch", n)
	}
}

// TestAcquireRetryConverges: a raced read retries until a converged doc
// arrives — first the empty doc, then the idle one.
func TestAcquireRetryConverges(t *testing.T) {
	id := "T-01a050ef-71e8-74bc-9b63-c0ad20e3a068"
	f := &fakeCLI{t: t, exportSeq: map[string][]cliResult{
		id: {
			{stdout: mustFixture(t, "exports/edge-empty.json"), code: 0},
			{stdout: mustFixture(t, "exports/idle-local-client.json"), code: 0},
		},
	}}
	// edge-empty's own id differs from the requested one, which would
	// short-circuit as errInvalidID — rewrite it to the requested id so
	// the doc is a legitimate empty export of THIS thread.
	empty := strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", id, 1)
	f.exportSeq[id][0].stdout = []byte(empty)

	s := exportSource(t, f)
	d := discoveredForID(t, mustFixture(t, "exports/idle-local-client.json"))
	raw, doc, note, err := s.acquireExport(context.Background(), d)
	if err != nil {
		t.Fatalf("acquireExport: %v", err)
	}
	if note != "" {
		t.Errorf("converged doc should carry no note, got %q", note)
	}
	if len(doc.Messages) != 4 || string(raw) != string(mustFixture(t, "exports/idle-local-client.json")) {
		t.Error("want the converged idle document")
	}
	if n := f.exportCalls(); n != 2 {
		t.Errorf("exportCalls = %d, want 2 (retry converged)", n)
	}
}

// TestAcquireRetryExhausted: a doc that stays retryable past the bound
// is still captured — the last faithful read, with its note — rather
// than dropped.
func TestAcquireRetryExhausted(t *testing.T) {
	raw := []byte(strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", "T-empty", 1))
	f := &fakeCLI{t: t, exports: map[string][]byte{"T-empty": raw}}
	s := exportSource(t, f)
	d := discoveredForID(t, raw)

	got, doc, note, err := s.acquireExport(context.Background(), d)
	if err != nil {
		t.Fatalf("acquireExport: %v", err)
	}
	if len(doc.Messages) != 0 || !strings.Contains(note, "no messages") {
		t.Errorf("want the empty doc captured with its retry note, got msgs=%d note=%q",
			len(doc.Messages), note)
	}
	if string(got) != string(raw) {
		t.Error("captured bytes must be the last fetched document")
	}
	if n := f.exportCalls(); n != 3 {
		t.Errorf("exportCalls = %d, want 3 (bounded retries)", n)
	}
}

// TestAcquireMidTurnNoRetry: a mid-turn doc is CompleteFlag — captured
// on the spot with its state note, no retry burn.
func TestAcquireMidTurnNoRetry(t *testing.T) {
	id := "T-01a050ef-71e8-74bc-9b63-c0ad20e3a068"
	f := &fakeCLI{t: t, exports: map[string][]byte{
		id: mustFixture(t, "exports/midturn-streaming.json"),
	}}
	s := exportSource(t, f)
	d := discoveredForID(t, mustFixture(t, "exports/midturn-streaming.json"))
	raw, doc, note, err := s.acquireExport(context.Background(), d)
	if err != nil || len(doc.Messages) != 3 || !strings.Contains(note, "streaming") {
		t.Fatalf("want the mid-turn doc with its flag note, got msgs=%d note=%q err=%v",
			len(doc.Messages), note, err)
	}
	if string(raw) != string(mustFixture(t, "exports/midturn-streaming.json")) {
		t.Error("want the verbatim mid-turn bytes")
	}
	if n := f.exportCalls(); n != 1 {
		t.Errorf("exportCalls = %d — CompleteFlag must not retry", n)
	}
}

// TestAcquireLastGoodSurvives: a retryable doc followed by transport
// failures still returns the last good read with its note — a failed
// retry never discards a capturable candidate.
func TestAcquireLastGoodSurvives(t *testing.T) {
	raw := []byte(strings.Replace(
		string(mustFixture(t, "exports/edge-empty.json")),
		"T-01a0746b-4d17-75ba-8c23-3800bd2c7710", "T-empty", 1))
	f := &fakeCLI{t: t, exportSeq: map[string][]cliResult{
		"T-empty": {
			{stdout: raw, code: 0}, // parseable but CompleteRetry
			{code: 1, stderr: mustFixture(t, "errors/offline.txt")},
			{code: 1, stderr: mustFixture(t, "errors/offline.txt")},
		},
	}}
	s := exportSource(t, f)
	d := discoveredForID(t, raw)
	got, doc, note, err := s.acquireExport(context.Background(), d)
	if err != nil {
		t.Fatalf("a later transport failure must not strand a good doc: %v", err)
	}
	if doc == nil || len(doc.Messages) != 0 || !strings.Contains(note, "no messages") {
		t.Errorf("want the last good doc with its note, got doc=%v note=%q", doc, note)
	}
	if string(got) != string(raw) {
		t.Error("returned bytes must be the last good read")
	}
	if n := f.exportCalls(); n != 3 {
		t.Errorf("exportCalls = %d, want 3", n)
	}
}

// TestAcquireAllTransportFail: transport failure on every attempt is a
// real error after the bound — nothing faithful to store.
func TestAcquireAllTransportFail(t *testing.T) {
	f := &fakeCLI{t: t, exportSeq: map[string][]cliResult{
		"T-x": {
			{code: 1, stderr: mustFixture(t, "errors/offline.txt")},
			{code: 1, stderr: mustFixture(t, "errors/offline.txt")},
			{code: 1, stderr: mustFixture(t, "errors/offline.txt")},
		},
	}}
	s := exportSource(t, f)
	_, _, _, err := s.acquireExport(context.Background(), source.Discovered{NativeID: "T-x"})
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errOffline {
		t.Fatalf("want the offline error after retries, got %v", err)
	}
	if n := f.exportCalls(); n != 3 {
		t.Errorf("exportCalls = %d, want 3 (1 + 2 bounded retries)", n)
	}
}

// TestAcquireTerminal: does-not-exist, malformed id, and auth failures
// are terminal — one call, the classified error, no retry burn.
func TestAcquireTerminal(t *testing.T) {
	for name, tc := range map[string]struct {
		seq  cliResult
		kind errKind
	}{
		"does-not-exist": {
			cliResult{code: 1, stderr: mustFixture(t, "errors/export-nonexistent.txt")},
			errNotExist,
		},
		"malformed-id": {
			cliResult{code: 1, stderr: mustFixture(t, "errors/export-malformed-id.txt")},
			errInvalidID,
		},
		"auth": {
			cliResult{code: 1, stderr: []byte("Error: not authenticated — run `amp login`")},
			errAuth,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeCLI{t: t, exportSeq: map[string][]cliResult{"T-x": {tc.seq}}}
			s := exportSource(t, f)
			d := source.Discovered{NativeID: "T-x"}
			_, _, _, err := s.acquireExport(context.Background(), d)
			var ce *cliError
			if !errors.As(err, &ce) || ce.kind != tc.kind {
				t.Fatalf("want %v, got %v", tc.kind, err)
			}
			if n := f.exportCalls(); n != 1 {
				t.Errorf("exportCalls = %d — terminal errors must not retry", n)
			}
		})
	}
}
