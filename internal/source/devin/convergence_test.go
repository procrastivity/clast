package devin_test

// Step-09 regression: the risky mutation/recovery contracts exercised
// through the public capture.Run path — curation surviving recapture,
// the crash window converging, project backfill on a previously
// projectless session, and the final silent steady state.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/devin"
	"github.com/procrastivity/clast/internal/verbs/analyzeverb"
	"github.com/procrastivity/clast/internal/verbs/capture"
)

// TestConvergenceMatrix runs the full mutation/failure sequence on a
// branched session (rapid-lark: abandoned branch, rewound chain,
// subagent head) with crashed-stork as the untouched control.
func TestConvergenceMatrix(t *testing.T) {
	store := devin.BuildStoreForTest(t, "branched-shortened.sql")
	root, captured := captureIntoJournal(t, store)
	if len(captured) != 2 {
		t.Fatalf("captured %d, want 2", len(captured))
	}
	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792100000, 0).UTC() },
	}
	larkDir, larkShard := sessionDir(t, root, "rapid-lark")
	storkDir, _ := sessionDir(t, root, "crashed-stork")
	larkKey := journal.SessionKey{Harness: "devin", NativeID: "rapid-lark"}
	storkSession, _ := os.ReadFile(filepath.Join(storkDir, "session.json"))
	larkTranscript, _ := os.ReadFile(filepath.Join(larkDir, "transcript.json"))

	// Curate rapid-lark through the normal journal path: entry plus a
	// curation document stamped on the current fingerprint.
	sess, ok, err := journal.ReadSession(root, larkShard, larkKey)
	if err != nil || !ok {
		t.Fatalf("ReadSession: %v %v", sess, err)
	}
	if err := journal.WriteEntry(root, larkShard, larkKey,
		[]byte("---\ntitle: rewound session\ntags: []\n---\nkept\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteCuration(root, larkShard, larkKey, journal.Curation{
		State: journal.StateCurated, At: deps.Now(), Machine: "m",
		TranscriptAtCuration: &journal.TranscriptStamp{
			Lines: sess.Transcript.Lines, SHA256: sess.Transcript.SHA256,
		},
	}); err != nil {
		t.Fatal(err)
	}
	curationPath := journal.CurationJSONPath(root, larkShard, larkKey)
	curationBefore, _ := os.ReadFile(curationPath)

	// Mutation at UNCHANGED timestamps — content + tool state — must
	// still recapture: the fingerprint sees what the metadata cannot.
	devin.MutateStoreForTest(t, store,
		`UPDATE message_nodes SET chat_message = replace(chat_message, 'Rewound answer', 'Rewound answer v2')
		 WHERE session_id = 'rapid-lark' AND node_id = 7`,
		`UPDATE tool_call_state SET tool_call_update_json =
		 '{"toolCallId":"exec:0#0a1b2c","status":"completed","_meta":{"cognition.ai/inferenceToolName":"exec"}}'
		 WHERE session_id = 'crashed-stork'`)
	got, _, failed, err := capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("mutated sweep: err=%v failed=%v", err, failed)
	}
	if len(got) != 2 {
		t.Fatalf("captured %v — both sessions changed, want both recaptured", got)
	}
	for _, c := range got {
		if !c.Recaptured {
			t.Errorf("%v not marked recaptured", c.Key)
		}
	}
	newTranscript, _ := os.ReadFile(filepath.Join(larkDir, "transcript.json"))
	if string(newTranscript) == string(larkTranscript) || !strings.Contains(string(newTranscript), "v2") {
		t.Error("recapture did not rewrite the transcript with the mutation")
	}
	after, _, _ := journal.ReadSession(root, larkShard, larkKey)
	if after.Transcript.SHA256 == sess.Transcript.SHA256 {
		t.Error("fingerprint did not move with content")
	}
	if got, _ := os.ReadFile(curationPath); string(got) != string(curationBefore) {
		t.Error("recapture rewrote curation.json")
	}
	if got, _ := os.ReadFile(filepath.Join(larkDir, "entry.md")); !strings.Contains(string(got), "kept") {
		t.Error("recapture lost entry.md")
	}
	if got, _ := os.ReadFile(filepath.Join(storkDir, "session.json")); string(got) == string(storkSession) {
		// crashed-stork DID change (tool completion) — its record must move.
		t.Error("crashed-stork's tool completion did not recapture")
	}
	storkSession, _ = os.ReadFile(filepath.Join(storkDir, "session.json"))

	// Shorten the active chain — head back to node 7, dropping the
	// notification. crashed-stork stays static: the control proves
	// per-session scoping.
	devin.MutateStoreForTest(t, store,
		`UPDATE sessions SET main_chain_id = 7 WHERE id = 'rapid-lark'`)
	got, _, failed, err = capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("shrink sweep: err=%v failed=%v", err, failed)
	}
	if len(got) != 1 || got[0].Key.NativeID != "rapid-lark" {
		t.Fatalf("shrink sweep captured %v, want only rapid-lark", got)
	}
	after, _, _ = journal.ReadSession(root, larkShard, larkKey)
	if after.Counts.Assistant != 1 {
		t.Errorf("shortened chain counts = %+v", after.Counts)
	}
	if after.Incomplete {
		t.Error("legitimate shrinkage flagged Incomplete")
	}
	if got, _ := os.ReadFile(filepath.Join(storkDir, "session.json")); string(got) != string(storkSession) {
		t.Error("rapid-lark's shrink staled the untouched control")
	}

	// Outage: poison the store — a collected source failure touching
	// nothing — then restore; the next sweep is silent.
	good, _ := os.ReadFile(store)
	if err := os.WriteFile(store, []byte("gone"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, failed, err = capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 1 {
		t.Fatalf("outage sweep: err=%v failed=%v, want one source failure", err, failed)
	}
	if got, _ := os.ReadFile(curationPath); string(got) != string(curationBefore) {
		t.Error("outage touched curation.json")
	}
	if err := os.WriteFile(store, good, 0o644); err != nil {
		t.Fatal(err)
	}

	// Steady state: two consecutive sweeps change no bytes anywhere.
	stable := treeHash(t, root)
	for i := 0; i < 2; i++ {
		got, diags, failed, err := capture.Run(context.Background(), deps)
		if err != nil || len(failed) != 0 || len(got) != 0 || len(diags) != 0 {
			t.Fatalf("steady sweep %d: captured=%v diags=%v failed=%v err=%v", i, got, diags, failed, err)
		}
	}
	if treeHash(t, root) != stable {
		t.Error("a silent sweep changed journal bytes")
	}
	// The curated session correctly reads stale — its stamp no longer
	// matches the mutated transcript; the document itself survives.
	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	var sawStale bool
	for _, it := range items {
		if it.Key == larkKey {
			sawStale = it.Stale()
		}
	}
	if !sawStale {
		t.Error("curated session should read stale after content moved")
	}
}

// TestProjectBackfillAfterRegistration: a session captured while its
// working_directory was unregistered lands projectless — and after the
// clone registers, the unchanged-content backfill path writes the
// project into session.json in place. Unregistered dirs and absent
// evidence stay projectless; no branch is ever guessed.
func TestProjectBackfillAfterRegistration(t *testing.T) {
	store := devin.BuildStoreForTest(t, "edge-cases.sql")
	repo := gitRepo(t)
	devin.MutateStoreForTest(t, store,
		fmt.Sprintf(`UPDATE sessions SET working_directory = '%s' WHERE id = 'amber-fox'`, repo))

	root, _ := captureIntoJournal(t, store)
	_, foxShard := sessionDir(t, root, "amber-fox")
	foxKey := journal.SessionKey{Harness: "devin", NativeID: "amber-fox"}
	sess, ok, err := journal.ReadSession(root, foxShard, foxKey)
	if err != nil || !ok {
		t.Fatal("no session")
	}
	if sess.Project != nil {
		t.Fatalf("unregistered clone resolved a project: %+v", sess.Project)
	}
	if sess.Branch != "" {
		t.Errorf("devin has no branch evidence — got %q", sess.Branch)
	}

	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792100000, 0).UTC() },
	}
	registerClone(t, root, "demo", repo)
	got, _, failed, err := capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("backfill sweep: err=%v failed=%v", err, failed)
	}
	var filled bool
	for _, c := range got {
		if c.Key.NativeID == "amber-fox" {
			if !c.ProjectBackfilled || c.Recaptured {
				t.Errorf("want a pure backfill, got %+v", c)
			}
			filled = true
		} else {
			t.Errorf("%v also captured — unchanged sessions must stay silent", c.Key)
		}
	}
	if !filled {
		t.Fatal("amber-fox was not backfilled")
	}
	after, _, _ := journal.ReadSession(root, foxShard, foxKey)
	if after.Project == nil || after.Project.Slug != "demo" || after.Project.Path != repo {
		t.Fatalf("backfilled project = %+v", after.Project)
	}
	if !after.CapturedAt.Equal(sess.CapturedAt) || after.Transcript != sess.Transcript {
		t.Error("backfill changed capture facts")
	}
	// Sessions whose evidence resolves nothing stay projectless —
	// brisk-owl's directory was never registered.
	_, owlShard := sessionDir(t, root, "brisk-owl")
	owl, _, _ := journal.ReadSession(root, owlShard,
		journal.SessionKey{Harness: "devin", NativeID: "brisk-owl"})
	if owl.Project != nil {
		t.Errorf("unregistered directory resolved a project: %+v", owl.Project)
	}
	// Backfilled + unchanged → the silent early exit again.
	got, _, _, err = capture.Run(context.Background(), deps)
	if err != nil || len(got) != 0 {
		t.Fatalf("post-backfill sweep: captured=%v err=%v", got, err)
	}
}

// TestCrashWindowConverges is the artifact-before-session.json window:
// transcript.json committed, session.json lost mid-crash. Walk reads
// no session record, so the next sweep treats the item as new —
// captures again, lands convergent truth, claims no atomicity the
// journal does not provide.
func TestCrashWindowConverges(t *testing.T) {
	store := devin.BuildStoreForTest(t, "basic.sql")
	root, captured := captureIntoJournal(t, store)
	if len(captured) != 1 {
		t.Fatalf("captured %d, want 1", len(captured))
	}
	dir, shard := sessionDir(t, root, "quiet-meadow")
	key := journal.SessionKey{Harness: "devin", NativeID: "quiet-meadow"}
	transcript, _ := os.ReadFile(filepath.Join(dir, "transcript.json"))

	if err := os.Remove(filepath.Join(dir, "session.json")); err != nil {
		t.Fatal(err)
	}
	deps := capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792100000, 0).UTC() },
	}
	got, diags, failed, err := capture.Run(context.Background(), deps)
	if err != nil || len(failed) != 0 {
		t.Fatalf("recovery sweep: captured=%v failed=%v err=%v", got, failed, err)
	}
	// The torn directory is disclosed as a walk diagnostic — visible,
	// not silent — while the item converges on this same sweep.
	if len(diags) != 1 || !strings.Contains(diags[0].Err.Error(), "missing session.json") {
		t.Fatalf("walk must disclose the torn dir once, got %v", diags)
	}
	if len(got) != 1 || got[0].Key != key || got[0].Recaptured {
		t.Fatalf("recovery captured %+v — want a fresh (non-recapture) commit", got)
	}
	sess, ok, err := journal.ReadSession(root, shard, key)
	if err != nil || !ok {
		t.Fatal("session.json not restored")
	}
	if sess.Counts.User != 1 || sess.Counts.Assistant != 2 {
		t.Errorf("restored facts = %+v", sess.Counts)
	}
	got2, _ := os.ReadFile(filepath.Join(dir, "transcript.json"))
	if string(got2) != string(transcript) {
		t.Error("identical store content must reproduce identical bytes")
	}
	// One more sweep is silent — converged, not stuck re-offering.
	got, diags, failed, err = capture.Run(context.Background(), deps)
	if err != nil || len(got) != 0 || len(diags) != 0 || len(failed) != 0 {
		t.Fatalf("post-recovery sweep: captured=%v diags=%v failed=%v err=%v", got, diags, failed, err)
	}
}

// TestCanceledRunIsFatal: a canceled context aborts the sweep outright —
// never a per-source failure, never a silent partial run.
func TestCanceledRunIsFatal(t *testing.T) {
	store := devin.BuildStoreForTest(t, "basic.sql")
	root, _ := captureIntoJournal(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, failed, err := capture.Run(ctx, capture.Deps{
		Root:    root,
		Sources: []source.Source{devin.NewAt(store)},
		Now:     func() time.Time { return time.Unix(1792100000, 0).UTC() },
	})
	if err == nil {
		t.Fatal("a canceled run must abort, not report")
	}
	if len(failed) != 0 {
		t.Errorf("cancellation collected as a source failure: %v", failed)
	}
}

// TestAnalyzeOfflineAfterRecapture closes the loop: after mutation and
// recapture, the offline reader sees the new conversation from the
// committed artifact alone.
func TestAnalyzeOfflineAfterRecapture(t *testing.T) {
	store := devin.BuildStoreForTest(t, "basic.sql")
	src := devin.NewAt(store)
	seatDevin(t, src)
	root, _ := captureIntoJournal(t, store)
	devin.MutateStoreForTest(t, store,
		`UPDATE message_nodes SET chat_message = replace(chat_message, 'FIXTURE-OK', 'POST-RECAPTURE')
		 WHERE session_id = 'quiet-meadow' AND node_id = 4`)
	if _, _, _, err := capture.Run(context.Background(), capture.Deps{
		Root:    root,
		Sources: []source.Source{src},
		Now:     func() time.Time { return time.Unix(1792100000, 0).UTC() },
	}); err != nil {
		t.Fatal(err)
	}
	dir, _ := sessionDir(t, root, "quiet-meadow")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	page, err := analyzeverb.GatherTranscript(dir, analyzeverb.Session{ID: "quiet-meadow"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, ev := range page.Events {
		if ev.Kind == source.KindAssistant && strings.Contains(ev.Text, "POST-RECAPTURE") {
			saw = true
		}
	}
	if !saw {
		t.Error("offline reader does not see the recaptured conversation")
	}
}
