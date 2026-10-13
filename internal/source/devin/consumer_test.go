package devin_test

// Consumer-level tests: the public capture.Run path and the show/
// analyze readers, exercising devin through exactly the seams
// production uses (registry.All, Deps.Sources, journal paths). These
// live in the external test package so the file stays legal once the
// registry row lands and registry imports this package.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/devin"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
	"github.com/procrastivity/clast/internal/verbs/analyzeverb"
	"github.com/procrastivity/clast/internal/verbs/capture"
	"github.com/procrastivity/clast/internal/verbs/show"
)

// seatDevin registers a store-bound devin source for the duration of a
// test — the same registry.All mutation the repo's own capture tests
// use, so consumer code resolves devin-export-v1 exactly as it will
// after the Stage 4 row lands.
func seatDevin(t *testing.T, s *devin.Source) {
	t.Helper()
	prev := sourceregistry.All
	sourceregistry.All = append(append([]source.Source{}, prev...), s)
	t.Cleanup(func() { sourceregistry.All = prev })
}

func captureIntoJournal(t *testing.T, store string) (string, []capture.Captured) {
	t.Helper()
	root := t.TempDir()
	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792000000, 0).UTC() },
	}
	captured, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("source failures: %v", failed)
	}
	// Amber-fox's malformed node is the one expected diagnostic.
	if len(diags) > 1 {
		t.Fatalf("diags = %v", diags)
	}
	return root, captured
}

// TestRunEndToEnd validates the public capture path (the step-05 exit
// condition): an injected devin source over a disposable store captures
// into a temporary journal — session.json fields, the export artifact,
// diagnostics, and the unchanged verdict on the follow-up sweep.
func TestRunEndToEnd(t *testing.T) {
	store := devin.BuildStoreForTest(t, "edge-cases.sql")
	root, captured := captureIntoJournal(t, store)
	if len(captured) != 4 {
		t.Fatalf("captured %d sessions, want 4", len(captured))
	}

	key := journal.SessionKey{Harness: "devin", NativeID: "amber-fox"}
	dirs, err := filepath.Glob(filepath.Join(root, "sessions", "*", key.DirName()))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("session dir glob = %v, %v", dirs, err)
	}
	raw, err := os.ReadFile(filepath.Join(dirs[0], "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sess journal.Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatalf("session.json: %v", err)
	}
	if sess.Counts.User != 1 || sess.Counts.Assistant != 2 || !sess.Substantive {
		t.Errorf("session.json counts = %+v substantive=%v", sess.Counts, sess.Substantive)
	}
	if sess.Incomplete {
		t.Error("malformed-node evidence is node-level, not chain-level — the session must not flag incomplete")
	}
	if sess.Transcript.Format != "devin-export-v1" || sess.Transcript.Artifact != "transcript.json" ||
		sess.Transcript.Lines != 7 || sess.Transcript.SHA256 == "" {
		t.Errorf("fingerprint = %+v", sess.Transcript)
	}
	if !strings.HasSuffix(sess.SourcePath, "sessions.db#amber-fox") {
		t.Errorf("source_path = %q", sess.SourcePath)
	}
	if sess.StartedAt.Unix() != 1791700000 || sess.LastActiveAt.Unix() != 1791700300 {
		t.Errorf("timestamps = %v..%v", sess.StartedAt, sess.LastActiveAt)
	}
	if sess.Project != nil {
		t.Errorf("unregistered working dir must stay projectless, got %+v", sess.Project)
	}

	export, err := os.ReadFile(filepath.Join(dirs[0], "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(export, &doc); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	for _, sec := range []string{"message_nodes", "tool_call_state", "prompt_history", "subagent_heads"} {
		for _, row := range doc[sec].([]any) {
			if row.(map[string]any)["session_id"] != "amber-fox" {
				t.Errorf("%s row leaked from another session: %v", sec, row)
			}
		}
	}
	if !strings.Contains(string(export), "Canceled due to user interrupt") {
		t.Error("tool-result evidence absent from ATIF must be in the export")
	}

	// A follow-up sweep hits the source's own Unchanged verdict — the
	// file-tail fingerprint default could never answer on a dbPath#id
	// address — and every item stays silent.
	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792000000, 0).UTC() },
	}
	captured2, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(captured2) != 0 || len(diags) != 0 || len(failed) != 0 {
		t.Fatalf("unchanged sweep: captured=%v diags=%v failed=%v", captured2, diags, failed)
	}

	// Mutate ONE session in place — same timestamps, same row count,
	// only row content differs — and a third sweep must recapture
	// exactly that session while every other record stays untouched.
	devin.MutateStoreForTest(t, store,
		`UPDATE message_nodes SET chat_message = replace(chat_message, 'message_id', 'message_idx')
		 WHERE session_id = 'brisk-owl'`)
	captured3, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if len(failed) != 0 || len(captured3) != 1 || captured3[0].Key.NativeID != "brisk-owl" {
		t.Fatalf("mutation sweep: captured=%v diags=%v failed=%v, want only brisk-owl", captured3, diags, failed)
	}
	raw, err = os.ReadFile(filepath.Join(dirs[0], "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sessAfter journal.Session
	if err := json.Unmarshal(raw, &sessAfter); err != nil {
		t.Fatal(err)
	}
	if sessAfter.CapturedAt != sess.CapturedAt {
		t.Error("brisk-owl's mutation staled amber-fox's committed record")
	}

	// The store was read-only end to end.
	if _, err := os.Stat(store + "-journal"); !os.IsNotExist(err) {
		t.Error("capture wrote to the Devin store")
	}
}

// TestCommitFailurePreservesPrior exercises the journal write/commit
// failure through the filesystem seam — a recapture whose stage cannot
// land (session dir read-only) must abort as the run's internal error
// while the committed transcript and session.json stand untouched.
// Restoring permissions makes the next sweep offer the item again —
// no checkpoint state, no retry bookkeeping.
func TestCommitFailurePreservesPrior(t *testing.T) {
	store := devin.BuildStoreForTest(t, "edge-cases.sql")
	root, _ := captureIntoJournal(t, store)

	key := journal.SessionKey{Harness: "devin", NativeID: "brisk-owl"}
	dirs, err := filepath.Glob(filepath.Join(root, "sessions", "*", key.DirName()))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("session dir = %v, %v", dirs, err)
	}
	sessDir := dirs[0]
	transcriptBefore, err := os.ReadFile(filepath.Join(sessDir, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessionBefore, err := os.ReadFile(filepath.Join(sessDir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}

	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792000000, 0).UTC() },
	}
	devin.MutateStoreForTest(t, store,
		`UPDATE sessions SET working_directory = '/home/user/Code/moved' WHERE id = 'brisk-owl'`)
	if err := os.Chmod(sessDir, 0o555); err != nil {
		t.Fatal(err)
	}
	_, _, failed, err := capture.Run(context.Background(), deps)
	if err == nil {
		t.Fatal("a commit failure must surface as the run's error, not silence")
	}
	if len(failed) != 0 {
		t.Fatalf("a commit failure is not a source failure: %v", failed)
	}
	if got, _ := os.ReadFile(filepath.Join(sessDir, "transcript.json")); string(got) != string(transcriptBefore) {
		t.Error("failed recapture replaced the committed transcript")
	}
	if got, _ := os.ReadFile(filepath.Join(sessDir, "session.json")); string(got) != string(sessionBefore) {
		t.Error("failed recapture replaced session.json")
	}

	// Permissions restored: the next enumeration offers the item again
	// and the recapture lands — nothing stayed wedged.
	if err := os.Chmod(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	captured, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("recovery sweep: err=%v failed=%v", err, failed)
	}
	if len(captured) != 1 || captured[0].Key.NativeID != "brisk-owl" {
		t.Fatalf("recovery captured %v, want brisk-owl", captured)
	}
	// Only brisk-owl's working-directory diag-free path — its mutation
	// is pure fact churn, not transcript damage.
	_ = diags
}

// TestCorruptStoreRecovery: poisoning the store between sweeps is a
// collected source failure that touches nothing committed; restoring
// the file makes the next sweep a silent unchanged pass — enumeration
// offers the item, the fingerprint verdict stands.
func TestCorruptStoreRecovery(t *testing.T) {
	store := devin.BuildStoreForTest(t, "basic.sql")
	root, captured := captureIntoJournal(t, store)
	if len(captured) != 1 {
		t.Fatalf("captured %d, want 1", len(captured))
	}
	good, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792000000, 0).UTC() },
	}

	if err := os.WriteFile(store, []byte("corrupted beyond recognition"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, failed, err := capture.Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("corrupt store must not abort the run: %v", err)
	}
	if len(failed) != 1 || failed[0].Source != "devin" {
		t.Fatalf("failed = %v, want the devin source failure", failed)
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "sessions", "*", "devin-quiet-meadow"))
	if len(dirs) != 1 {
		t.Fatalf("session dir = %v", dirs)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "transcript.json")); err != nil {
		t.Error("a source outage must not touch committed artifacts")
	}

	if err := os.WriteFile(store, good, 0o644); err != nil {
		t.Fatal(err)
	}
	captured, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 || len(captured) != 0 || len(diags) != 0 {
		t.Fatalf("restored sweep: captured=%v diags=%v failed=%v err=%v — want a silent unchanged pass",
			captured, diags, failed, err)
	}
}

// TestRunInsertionOrderInvariant proves the export is stable under
// physical reordering: the same logical rows inserted in a different
// order (which reassigns row_id and file layout) must produce a
// byte-identical artifact and fingerprint.
func TestRunInsertionOrderInvariant(t *testing.T) {
	ordered := devin.BuildStoreForTest(t, "basic.sql")
	shuffled := devin.BuildVariantForTest(t, "basic.sql", func(lines []string) []string {
		// Reverse only the message_nodes INSERT lines in place — the
		// schema and every other statement keeps its position, so the
		// same forest lands with different row_id assignments.
		idx := map[int]int{}
		var nodes []string
		for i, l := range lines {
			if strings.HasPrefix(l, "INSERT INTO message_nodes") {
				idx[i] = -1
				nodes = append(nodes, l)
			}
		}
		j := len(nodes) - 1
		for i := range idx {
			lines[i] = nodes[j]
			j--
		}
		return lines
	})
	_, a1, _, err := devin.CaptureForTest(t, devin.NewAt(ordered), "quiet-meadow")
	if err != nil {
		t.Fatal(err)
	}
	f2, a2, _, err := devin.CaptureForTest(t, devin.NewAt(shuffled), "quiet-meadow")
	if err != nil {
		t.Fatal(err)
	}
	if string(a1["transcript.json"]) != string(a2["transcript.json"]) {
		t.Fatal("insertion order changed the export — ordering is not content-keyed")
	}
	f1, _, _, err := devin.CaptureForTest(t, devin.NewAt(ordered), "quiet-meadow")
	if err != nil {
		t.Fatal(err)
	}
	if f1.Transcript.SHA256 != f2.Transcript.SHA256 {
		t.Error("insertion order changed the conversation fingerprint")
	}
}

// TestShowTranscriptOffline is the step-06 exit for the renderer seam:
// with the store never opened by the reader, `show --transcript`'s own
// resolution path (format registry → ArtifactName → RenderTranscript)
// produces the session's prose from the artifact alone.
func TestShowTranscriptOffline(t *testing.T) {
	store := devin.BuildStoreForTest(t, "basic.sql")
	src := devin.NewAt(store)
	seatDevin(t, src)
	root, _ := captureIntoJournal(t, store)

	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	item, err := journal.Resolve(items, "devin-quiet-meadow")
	if err != nil {
		t.Fatal(err)
	}
	// The readers take io.Reader of the committed artifact — nothing in
	// the call graph opens the store. Deleting the store proves it.
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	turns, err := show.Transcript(root, item, 0)
	if err != nil {
		t.Fatalf("show.Transcript: %v", err)
	}
	if len(turns) != 2 || turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("turns = %+v", turns)
	}
	if !strings.Contains(turns[0].Text, "FIXTURE-OK") || !strings.Contains(turns[1].Text, "FIXTURE-OK") {
		t.Errorf("turn text = %q / %q", turns[0].Text, turns[1].Text)
	}

	// A format nothing renders still refuses through the registry seam.
	if _, err := sourceregistry.ValidateTranscriptFormat("not-a-format"); err == nil {
		t.Error("unknown format must refuse")
	}
}

// TestAnalyzeGatherOffline is the structured-reader half: the analyze
// transcript view gathers events from the artifact with the store gone,
// pairing calls and results through the export's own evidence.
func TestAnalyzeGatherOffline(t *testing.T) {
	store := devin.BuildStoreForTest(t, "edge-cases.sql")
	seatDevin(t, devin.NewAt(store))
	root, _ := captureIntoJournal(t, store)
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}

	dirs, err := filepath.Glob(filepath.Join(root, "sessions", "*", "devin-amber-fox"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("session dir = %v", dirs)
	}
	page, err := analyzeverb.GatherTranscript(dirs[0], analyzeverb.Session{ID: "amber-fox"}, "")
	if err != nil {
		t.Fatalf("GatherTranscript: %v", err)
	}
	var calls, results, prompts int
	var sawCanceled, sawHookBlocked bool
	for _, ev := range page.Events {
		switch ev.Kind {
		case source.KindPrompt:
			prompts++
		case source.KindToolCall:
			calls++
			if ev.Tool != nil && ev.Tool.Result != nil && ev.Tool.Result.IsError {
				if strings.Contains(ev.Tool.Result.Text, "Canceled") {
					sawCanceled = true
				}
			}
		case source.KindToolResult:
			results++
			if strings.Contains(ev.Tool.Result.Text, "blocked by probe policy") {
				sawHookBlocked = true
			}
		}
	}
	if prompts != 1 || calls != 2 {
		t.Errorf("prompts=%d calls=%d, want 1/2", prompts, calls)
	}
	if !sawCanceled {
		t.Error("canceled exec_0 must surface as an errored call result")
	}
	if !sawHookBlocked || results != 1 {
		t.Errorf("update-only exec_1 must synthesize one orphan result, got %d", results)
	}
}
