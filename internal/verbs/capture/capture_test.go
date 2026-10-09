package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/claude"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
)

const (
	liveID = "aaaaaaaa-1111-4111-8111-111111111111"
	noopID = "bbbbbbbb-2222-4222-8222-222222222222"
)

func line(cwd, uuid, typ, content, ts string) string {
	msg := fmt.Sprintf(`{"role":"user","content":%q}`, content)
	if typ == "assistant" {
		msg = fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"stop_reason":"end_turn"}`, content)
	}
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"type":%q,"message":%s,"uuid":%q,"timestamp":%q,"userType":"external","cwd":%q,"sessionId":"x","version":"2.1.226","gitBranch":"main"}`,
		typ, msg, uuid, ts, cwd) + "\n"
}

// writeHome builds a minimal claude config dir: one substantive session
// and one no-op session, both with cwd (an unregistered path in most
// tests, a real registered git repo in the backfill ones).
func writeHomeAt(t *testing.T, cwd string) string {
	t.Helper()
	home := t.TempDir()
	slug := filepath.Join(home, "projects", "-home-user-Code-demo")
	if err := os.MkdirAll(slug, 0o755); err != nil {
		t.Fatal(err)
	}
	live := line(cwd, "10000000-aaaa-4aaa-8aaa-000000000001", "user", "hi", "2026-09-01T10:00:00.000Z") +
		line(cwd, "10000000-aaaa-4aaa-8aaa-000000000002", "assistant", "hello", "2026-09-01T10:00:01.000Z")
	noop := line(cwd, "20000000-bbbb-4bbb-8bbb-000000000001", "user", "never mind", "2026-09-02T08:00:00.000Z")
	if err := os.WriteFile(filepath.Join(slug, liveID+".jsonl"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slug, noopID+".jsonl"), []byte(noop), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeHome(t *testing.T) string {
	t.Helper()
	return writeHomeAt(t, "/home/user/Code/demo")
}

// gitRepo creates a fresh git repo (no remotes) under t's temp dir and
// returns its absolute path — the internal/registry testutil pattern.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

// registerClone registers dir as a clone of a fresh project named slug,
// the way `clast init` would: project.json plus this machine's clones
// file, keyed by dir's git-common-dir.
func registerClone(t *testing.T, root, slug, dir string) {
	t.Helper()
	commonDir, err := registry.CommonDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := journal.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteProject(root, slug, journal.Project{
		ID: "01TESTPROJECT0000000000000", Slug: slug,
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{
		Machine: machine,
		Clones:  []journal.Clone{{ID: "01TESTCLONE00000000000000", GitCommonDir: commonDir, Label: slug}},
	}); err != nil {
		t.Fatal(err)
	}
}

func deps(t *testing.T, home, root string) Deps {
	t.Helper()
	return Deps{
		Root:            root,
		Sources:         []source.Source{claude.NewAt(home)},
		AutoDismissNoop: true,
		Now:             func() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) },
	}
}

// treeHash digests every file under root: byte-stability's measure.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h, "%s\n", path); err != nil {
			return err
		}
		h.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestRunCapturesAndAutoDismisses(t *testing.T) {
	home, root := writeHome(t), t.TempDir()

	captured, diags, _, err := Run(context.Background(), deps(t, home, root))
	if err != nil || len(diags) != 0 {
		t.Fatalf("Run: err=%v diags=%v", err, diags)
	}
	if len(captured) != 2 {
		t.Fatalf("captured %d sessions, want 2", len(captured))
	}

	byID := map[string]Captured{}
	for _, c := range captured {
		byID[c.Key.NativeID] = c
	}
	if c := byID[liveID]; c.AutoDismissed || c.Recaptured || c.Shard != "2026-09-01" {
		t.Errorf("live session: %+v", c)
	}
	if c := byID[noopID]; !c.AutoDismissed || c.Shard != "2026-09-02" {
		t.Errorf("noop session: %+v", c)
	}

	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	s, ok, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil || !ok {
		t.Fatalf("session.json: %v %v", ok, err)
	}
	if s.Counts.User != 1 || s.Counts.Assistant != 1 || !s.Substantive || s.Branch != "main" {
		t.Errorf("session facts: %+v", s)
	}
	if s.Transcript.Format != "claude-jsonl" || s.Transcript.Lines != 2 {
		t.Errorf("fingerprint: %+v", s.Transcript)
	}
	if s.Project != nil {
		t.Errorf("unregistered cwd resolved a project: %+v", s.Project)
	}

	noopKey := journal.SessionKey{Harness: "claude", NativeID: noopID}
	cur, present, err := journal.ReadCuration(root, "2026-09-02", noopKey)
	if err != nil || !present {
		t.Fatalf("curation.json: %v %v", present, err)
	}
	if cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != "auto:no-op" {
		t.Errorf("curation: %+v", cur)
	}
}

func TestRunIsIdempotentAndByteStable(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)

	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, root)

	// A later sweep with a different clock: nothing changed at the
	// source, so nothing may move in the journal.
	d.Now = func() time.Time { return time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC) }
	captured, diags, _, err := Run(context.Background(), d)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Run: err=%v diags=%v", err, diags)
	}
	if len(captured) != 0 {
		t.Errorf("second sweep captured %v, want nothing", captured)
	}
	if after := treeHash(t, root); after != before {
		t.Error("second sweep changed journal bytes")
	}
}

func TestRunRecapturesGrowthAndRetractsAutoDismissal(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	noopPath := filepath.Join(home, "projects", "-home-user-Code-demo", noopID+".jsonl")
	f, err := os.OpenFile(noopPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line("/home/user/Code/demo", "20000000-bbbb-4bbb-8bbb-000000000002", "assistant", "resumed", "2026-09-02T09:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	captured, diags, _, err := Run(context.Background(), d)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Run: err=%v diags=%v", err, diags)
	}
	if len(captured) != 1 || !captured[0].Recaptured || captured[0].Key.NativeID != noopID {
		t.Fatalf("captured = %+v, want one recapture of the noop session", captured)
	}

	noopKey := journal.SessionKey{Harness: "claude", NativeID: noopID}
	s, ok, err := journal.ReadSession(root, "2026-09-02", noopKey)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if s.Transcript.Lines != 2 || !s.Substantive || s.Counts.Assistant != 1 {
		t.Errorf("recaptured facts: %+v", s)
	}
	if _, present, err := journal.ReadCuration(root, "2026-09-02", noopKey); err != nil || present {
		t.Errorf("auto:no-op dismissal not retracted: present=%v err=%v", present, err)
	}
}

func TestRunBackfillsProjectAfterRegistration(t *testing.T) {
	repo := gitRepo(t)
	home, root := writeHomeAt(t, repo), t.TempDir()
	d := deps(t, home, root)

	// Sweep 1: the clone is unregistered, both sessions land projectless
	// (and the noop one auto-dismissed).
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	before, ok, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if before.Project != nil {
		t.Fatalf("unregistered clone resolved a project: %+v", before.Project)
	}

	// The user runs `clast init`; sweep 2 backfills both sessions.
	registerClone(t, root, "demo", repo)
	captured, diags, _, err := Run(context.Background(), d)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Run: err=%v diags=%v", err, diags)
	}
	if len(captured) != 2 {
		t.Fatalf("captured %d sessions, want 2 backfills", len(captured))
	}
	for _, c := range captured {
		if !c.ProjectBackfilled || c.Recaptured || c.AutoDismissed {
			t.Errorf("want a pure backfill, got %+v", c)
		}
	}

	s, ok, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if s.Project == nil || s.Project.Slug != "demo" || s.Project.Path != repo || s.Project.Clone == "" {
		t.Fatalf("backfilled project: %+v", s.Project)
	}
	if s.Worktree != "" {
		t.Errorf("main worktree named: %q", s.Worktree)
	}
	if !s.CapturedAt.Equal(before.CapturedAt) || s.Transcript != before.Transcript {
		t.Errorf("backfill changed capture facts: %+v vs %+v", s, before)
	}

	// The noop session's auto:no-op dismissal stands untouched.
	noopKey := journal.SessionKey{Harness: "claude", NativeID: noopID}
	cur, present, err := journal.ReadCuration(root, "2026-09-02", noopKey)
	if err != nil || !present || cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != "auto:no-op" {
		t.Errorf("dismissal disturbed: present=%v cur=%+v err=%v", present, cur, err)
	}

	// Sweep 3: projected and unchanged — the silent early exit again.
	stable := treeHash(t, root)
	captured, diags, _, err = Run(context.Background(), d)
	if err != nil || len(diags) != 0 || len(captured) != 0 {
		t.Fatalf("third sweep: captured=%v diags=%v err=%v", captured, diags, err)
	}
	if after := treeHash(t, root); after != stable {
		t.Error("third sweep changed journal bytes")
	}
}

func TestRunBackfillResolvesWorktree(t *testing.T) {
	repo := gitRepo(t)
	git(t, repo, "commit", "--allow-empty", "-q", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "demo-wt")
	git(t, repo, "worktree", "add", "-q", "-b", "wt", worktree)

	home, root := writeHomeAt(t, worktree), t.TempDir()
	d := deps(t, home, root)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	registerClone(t, root, "demo", repo)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	s, ok, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if s.Project == nil || s.Project.Path != worktree || s.Worktree != "demo-wt" {
		t.Errorf("worktree backfill: project=%+v worktree=%q", s.Project, s.Worktree)
	}
}

func TestRunBackfillKeepsCurationFresh(t *testing.T) {
	repo := gitRepo(t)
	home, root := writeHomeAt(t, repo), t.TempDir()
	d := deps(t, home, root)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	// Curate the projectless session, stamping the current fingerprint.
	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	s, _, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteEntry(root, "2026-09-01", liveKey, []byte("---\ntitle: t\ntags: []\n---\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteCuration(root, "2026-09-01", liveKey, journal.Curation{
		State: journal.StateCurated, At: d.Now(), Machine: "m",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: s.Transcript.Lines, SHA256: s.Transcript.SHA256},
	}); err != nil {
		t.Fatal(err)
	}
	curationPath := journal.CurationJSONPath(root, "2026-09-01", liveKey)
	curationBefore, err := os.ReadFile(curationPath)
	if err != nil {
		t.Fatal(err)
	}

	registerClone(t, root, "demo", repo)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	curationAfter, err := os.ReadFile(curationPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(curationAfter) != string(curationBefore) {
		t.Error("backfill rewrote curation.json")
	}
	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Key != liveKey {
			continue
		}
		if it.Session.Project == nil {
			t.Error("curated session not backfilled")
		}
		if it.Stale() {
			t.Error("backfill made the curated session stale")
		}
	}
}

func TestRunKeepsUserCurationOnRecapture(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	// The user dismisses the live session by hand, then it grows: the
	// user's decision stands.
	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	reason := "not interesting"
	if err := journal.WriteCuration(root, "2026-09-01", liveKey, journal.Curation{
		State: journal.StateDismissed, At: d.Now(), Machine: "m", Reason: &reason,
	}); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(home, "projects", "-home-user-Code-demo", liveID+".jsonl")
	f, err := os.OpenFile(livePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line("/home/user/Code/demo", "10000000-aaaa-4aaa-8aaa-000000000003", "assistant", "more", "2026-09-01T11:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	cur, present, err := journal.ReadCuration(root, "2026-09-01", liveKey)
	if err != nil || !present {
		t.Fatalf("user curation vanished: present=%v err=%v", present, err)
	}
	if cur.Reason == nil || *cur.Reason != "not interesting" {
		t.Errorf("user dismissal rewritten: %+v", cur)
	}
}

func TestRunAutoDismissOff(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	d.AutoDismissNoop = false
	captured, _, _, err := Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range captured {
		if c.AutoDismissed {
			t.Errorf("auto-dismissed with the knob off: %+v", c)
		}
	}
	noopKey := journal.SessionKey{Harness: "claude", NativeID: noopID}
	if _, present, _ := journal.ReadCuration(root, "2026-09-02", noopKey); present {
		t.Error("curation.json written with the knob off")
	}
}

func TestRunContinuesPastSourceFailure(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	boom := errors.New("storage unreachable")
	d.Sources = []source.Source{
		stubFailSource{stubSource{name: "ahead"}, boom},
		claude.NewAt(home),
		stubFailSource{stubSource{name: "behind"}, fmt.Errorf("wrap: %w", boom)},
	}

	captured, diags, failed, err := Run(context.Background(), d)
	if err != nil {
		t.Fatalf("sweep with failed sources: %v", err)
	}
	// Both failures are collected — before and after the healthy
	// source — in source order, errors carried verbatim, and never
	// leaked into the diagnostic channel.
	if len(failed) != 2 || failed[0].Source != "ahead" || failed[1].Source != "behind" {
		t.Fatalf("failed = %+v, want [ahead behind]", failed)
	}
	if !errors.Is(failed[0].Err, boom) || !errors.Is(failed[1].Err, boom) {
		t.Errorf("failure errors not carried verbatim: %v / %v", failed[0].Err, failed[1].Err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none — source failures are their own channel", diags)
	}
	// The healthy source between the two failures still captured both
	// sessions, and its durable records exist — one broken source never
	// denies another's valid capture.
	if len(captured) != 2 {
		t.Fatalf("captured = %v, want claude's two sessions", captured)
	}
	items, _, err := journal.Walk(root)
	if err != nil || len(items) != 2 {
		t.Fatalf("journal after mixed sweep: items=%d err=%v, want 2", len(items), err)
	}
}

func TestRunCancellationIsFatalNotPerSource(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	d.Sources = []source.Source{
		claude.NewAt(home),
		stubFailSource{stubSource{name: "torn"}, context.Canceled},
		stubFailSource{stubSource{name: "unreached"}, errors.New("must never surface")},
	}

	captured, _, failed, err := Run(context.Background(), d)
	// Cancellation aborts as a classified internal error — never a
	// per-source failure, and never silently swallowed.
	_ = assertClasterr(t, err, "internal.capture")
	if len(failed) != 0 {
		t.Errorf("cancellation collected as a per-source failure: %v", failed)
	}
	// Work committed before the cancel stands: partial results are
	// preserved on the return and durable in the journal.
	if len(captured) != 2 {
		t.Errorf("captured = %v, want claude's two committed sessions", captured)
	}
	items, _, werr := journal.Walk(root)
	if werr != nil || len(items) != 2 {
		t.Errorf("journal after canceled sweep: items=%d err=%v, want 2", len(items), werr)
	}

	// DeadlineExceeded and wrapped cancellations abort identically.
	for _, cerr := range []error{context.DeadlineExceeded, fmt.Errorf("probe: %w", context.Canceled)} {
		d.Sources = []source.Source{stubFailSource{stubSource{name: "torn"}, cerr}}
		if _, _, failed, err := Run(context.Background(), d); err == nil || len(failed) != 0 {
			t.Errorf("(%v): err=%v failed=%v — cancellation must abort, not collect", cerr, err, failed)
		}
	}

	// A context already dead before the first source aborts the sweep
	// even for a source that never consults ctx.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, failed, err := Run(ctx, d); err == nil || len(failed) != 0 {
		t.Errorf("pre-canceled ctx: err=%v failed=%v", err, failed)
	}

	// Cancellation surfacing inside a session's own duties aborts too —
	// it never lands as a per-item diagnostic.
	d.Sources = []source.Source{stubCancelCorrelateSource{stubSource{name: "torn"}}}
	_, diags, failed, err := Run(context.Background(), d)
	_ = assertClasterr(t, err, "internal.capture")
	if len(failed) != 0 || len(diags) != 0 {
		t.Errorf("correlate-level cancel: diags=%v failed=%v — want neither", diags, failed)
	}
}

func TestRunStoreWriteFailureIsInternal(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	// Poison the store root: journal.json as a symlink to itself makes
	// EnsureRoot's Stat fail with a non-NotExist error — a write-seam
	// failure reached only after the walk (sessions/ is absent, so the
	// walk stays quiet) and discovery both succeeded.
	if err := os.Symlink("journal.json", filepath.Join(root, "journal.json")); err != nil {
		t.Fatal(err)
	}

	captured, _, failed, err := Run(context.Background(), deps(t, home, root))
	// Fatal, and classified internal.* — never a raw error leaking to
	// the chassis's catch-all usage exit, never a per-source failure.
	_ = assertClasterr(t, err, "internal.capture")
	if len(failed) != 0 {
		t.Errorf("store-write failure collected as per-source failure: %v", failed)
	}
	if len(captured) != 0 {
		t.Errorf("captured = %v, want none — the run failed before any commit", captured)
	}
}

func TestNamedSourceValidation(t *testing.T) {
	if _, err := namedSource("devin"); err == nil {
		t.Fatal("unknown harness accepted")
	}
	src, err := namedSource("claude")
	if err != nil || src.Name() != "claude" {
		t.Fatalf("claude resolve: %v %v", src, err)
	}
}

// stubSource is a minimal source.Source for selection tests: Discover
// returns nothing (the row is all these tests need); Correlate/Capture
// are never reached.
type stubSource struct {
	name  string
	model source.StorageModel
}

func (s stubSource) Name() string               { return s.name }
func (s stubSource) Model() source.StorageModel { return s.model }
func (s stubSource) Discover(context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	return nil, nil, nil
}

func (s stubSource) Correlate(context.Context, source.Discovered) (string, []source.Diagnostic, error) {
	return "", nil, nil
}

func (s stubSource) Capture(context.Context, source.Discovered, source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	return source.Facts{}, nil, nil
}

// stubPresenceSource adds a canned source.Presence probe to stubSource.
type stubPresenceSource struct {
	stubSource
	err error
}

func (s stubPresenceSource) Present(context.Context) error { return s.err }

// stubFailSource is a stubSource whose Discover fails with a canned
// error — the per-source failure seam a sweep isolates, and (when the
// error is context teardown) the cancellation-must-abort seam.
type stubFailSource struct {
	stubSource
	err error
}

func (s stubFailSource) Discover(context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	return nil, nil, s.err
}

// stubCancelCorrelateSource enumerates one session whose Correlate then
// fails with context teardown — the per-item path where cancellation
// must still abort rather than become a diagnostic.
type stubCancelCorrelateSource struct {
	stubSource
}

func (s stubCancelCorrelateSource) Discover(context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	return []source.Discovered{{NativeID: "s-1", Path: "/nowhere/s-1"}}, nil, nil
}

func (s stubCancelCorrelateSource) Correlate(context.Context, source.Discovered) (string, []source.Diagnostic, error) {
	return "", nil, fmt.Errorf("correlate: %w", context.Canceled)
}

func TestSelectSweep(t *testing.T) {
	local := stubSource{name: "claude", model: source.FileTail}
	net := stubSource{name: "amp", model: source.Network}
	all := []source.Source{local, net}
	names := func(set []source.Source) []string {
		var out []string
		for _, s := range set {
			out = append(out, s.Name())
		}
		return out
	}

	// Bare: local walks, network waits for its opt-in.
	if got := names(selectSweep(all, nil, nil)); len(got) != 1 || got[0] != "claude" {
		t.Errorf("bare sweep = %v, want [claude]", got)
	}
	// The opt-in admits the network source; it is a no-op on the local one.
	if got := names(selectSweep(all, nil, map[string]bool{"amp": true, "claude": true})); len(got) != 2 {
		t.Errorf("opted-in sweep = %v, want both", got)
	}
	// Exclusion beats the opt-in and removes local sources alike.
	if got := selectSweep(all, map[string]bool{"amp": true, "claude": true}, map[string]bool{"amp": true}); len(got) != 0 {
		t.Errorf("excluded sweep = %v, want empty", names(got))
	}
	// The input table is never mutated — the reader-independence rule.
	if len(all) != 2 || all[0].Name() != "claude" || all[1].Name() != "amp" {
		t.Errorf("input mutated: %v", names(all))
	}
}

func TestCapturePolicyConfig(t *testing.T) {
	// Absent section: empty policy, no error.
	excluded, auto, err := capturePolicy(config.Config{})
	if err != nil || len(excluded) != 0 || len(auto) != 0 {
		t.Fatalf("absent section: excluded=%v auto=%v err=%v", excluded, auto, err)
	}

	// exclude and <name>.auto both parse (config.Config section, the
	// shape config.Load's nested mappings arrive in).
	excluded, auto, err = capturePolicy(config.Config{"capture": config.Config{
		"exclude": []any{"claude"},
		"claude":  config.Config{"auto": true},
	}})
	if err != nil || !excluded["claude"] || !auto["claude"] {
		t.Errorf("parsed policy: excluded=%v auto=%v err=%v", excluded, auto, err)
	}

	// An unimplemented name in either position errors, naming the value
	// and the implemented set.
	if _, _, err := capturePolicy(config.Config{"capture": config.Config{"exclude": []any{"pi"}}}); err == nil ||
		!strings.Contains(err.Error(), "pi") || !strings.Contains(err.Error(), "claude") {
		t.Errorf("unimplemented exclude name: %v", err)
	}
	if _, _, err := capturePolicy(config.Config{"capture": config.Config{"pi": config.Config{"auto": true}}}); err == nil ||
		!strings.Contains(err.Error(), "pi") || !strings.Contains(err.Error(), "claude") {
		t.Errorf("unimplemented auto source: %v", err)
	}
	// …while the implemented network source parses in both.
	if _, auto, err := capturePolicy(config.Config{"capture": config.Config{
		"exclude": []any{"amp"},
		"amp":     config.Config{"auto": true},
	}}); err != nil || !auto["amp"] {
		t.Errorf("implemented amp policy: auto=%v err=%v", auto, err)
	}

	// Mistyped values error rather than falling back silently.
	for name, cfg := range map[string]config.Config{
		"non-list exclude":    {"capture": config.Config{"exclude": "claude"}},
		"non-string item":     {"capture": config.Config{"exclude": []any{5}}},
		"mistyped auto":       {"capture": config.Config{"claude": config.Config{"auto": "yes"}}},
		"non-mapping capture": {"capture": "yes"},
	} {
		if _, _, err := capturePolicy(cfg); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	// Unknown scalar keys stay silently ignored (P6).
	if _, _, err := capturePolicy(config.Config{"capture": config.Config{"bogus": 5}}); err != nil {
		t.Errorf("unknown scalar key: %v", err)
	}
}

func TestCheckPresence(t *testing.T) {
	// A source without the OPTIONAL probe degrades to the legacy
	// quiet-on-empty behavior — no error from the check itself.
	if err := checkPresence(context.Background(), stubSource{name: "pi"}); err != nil {
		t.Errorf("source without Presence probe: %v", err)
	}
	// nil Present = present.
	if err := checkPresence(context.Background(), stubPresenceSource{stubSource{name: "pi"}, nil}); err != nil {
		t.Errorf("present source: %v", err)
	}
	// A Present error surfaces as capture.source-unavailable, naming the
	// source and carrying the probe's probed-root message verbatim.
	err := checkPresence(context.Background(), stubPresenceSource{stubSource{name: "pi"}, fmt.Errorf("/data/pi: not readable")})
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"pi"`) || !strings.Contains(ce.Message, "/data/pi: not readable") {
		t.Errorf("message %q should name the source and the probed root", ce.Message)
	}

	// Cancellation is fatal on the probe path too — never dressed up as
	// source-unavailable. A ctx already dead aborts before the probe…
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = assertClasterr(t, checkPresence(ctx, stubPresenceSource{stubSource{name: "pi"}, nil}), "internal.capture")
	// …and a probe whose error IS context teardown (a network source's
	// ctx-consulting credential check — the contract binds Present the
	// same as Discover) aborts identically.
	for _, cerr := range []error{context.Canceled, fmt.Errorf("probe: %w", context.DeadlineExceeded)} {
		_ = assertClasterr(t, checkPresence(context.Background(),
			stubPresenceSource{stubSource{name: "pi"}, cerr}), "internal.capture")
	}
}

// envSeams points every environment seam capture reads at per-test temp
// dirs: the journal root, the user config dir, and the claude storage
// root the registry source resolves at call time (CLAUDE_CONFIG_DIR is
// the harness's own convention — claude.root reads it).
func envSeams(t *testing.T, claudeHome string) (journalDir, configHome string) {
	t.Helper()
	journalDir, configHome = t.TempDir(), t.TempDir()
	t.Setenv("CLAST_JOURNAL_DIR", journalDir)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	return journalDir, configHome
}

// writeConfig installs a user config.yaml under configHome's
// $XDG_CONFIG_HOME/clast directory.
func writeConfig(t *testing.T, configHome, content string) {
	t.Helper()
	dir := filepath.Join(configHome, "clast")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCommand drives the capture cobra command in-process; RunE's return
// is the structured *clasterr.Error wherever the contract wraps a plain
// error (the exit-code mapping off it is the cli package's, e2e-tested
// there).
func runCommand(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCommandFlags(t, cliflags.Flags{}, args...)
}

// runCommandFlags is runCommand with the global flags (--json, -v)
// root's PersistentPreRunE would have threaded through cmd's context.
func runCommandFlags(t *testing.T, flags cliflags.Flags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := Command(&iostreams.Streams{Out: &out, Err: &errBuf})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetContext(cliflags.WithFlags(context.Background(), flags))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

// assertClasterr unwraps err as the *clasterr.Error the contract wraps
// and checks its machine token.
func assertClasterr(t *testing.T, err error, code string) *clasterr.Error {
	t.Helper()
	var ce *clasterr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != code {
		t.Fatalf("error code = %q, want %q", ce.Code, code)
	}
	return ce
}

func TestCommandBareSweepCapturesLocalSource(t *testing.T) {
	envSeams(t, writeHome(t))
	stdout, stderr, err := runCommand(t)
	if err != nil {
		t.Fatalf("bare capture: %v (stderr %q)", err, stderr)
	}
	if n := strings.Count(stdout, "captured claude-"); n != 2 {
		t.Errorf("stdout = %q, want two captured lines", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want quiet", stderr)
	}
}

func TestCommandExcludeSkipsSweep(t *testing.T) {
	journalDir, configHome := envSeams(t, writeHome(t))
	writeConfig(t, configHome, "capture:\n  exclude: [claude]\n")

	stdout, stderr, err := runCommand(t)
	if err != nil {
		t.Fatalf("excluded sweep: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("excluded sweep emitted output: stdout=%q stderr=%q", stdout, stderr)
	}
	items, _, err := journal.Walk(journalDir)
	if err != nil || len(items) != 0 {
		t.Fatalf("excluded sweep captured sessions: items=%v err=%v", items, err)
	}
}

func TestCommandExplicitBeatsExclusion(t *testing.T) {
	_, configHome := envSeams(t, writeHome(t))
	writeConfig(t, configHome, "capture:\n  exclude: [claude]\n")

	stdout, _, err := runCommand(t, "--harness", "claude")
	if err != nil {
		t.Fatalf("explicit capture on excluded source: %v", err)
	}
	if n := strings.Count(stdout, "captured claude-"); n != 2 {
		t.Errorf("stdout = %q, want two captured lines", stdout)
	}
}

func TestCommandUnknownNameInExcludeIsValidationConfig(t *testing.T) {
	_, configHome := envSeams(t, writeHome(t))
	writeConfig(t, configHome, "capture:\n  exclude: [pi]\n")

	_, _, err := runCommand(t)
	ce := assertClasterr(t, err, "validation.config")
	if !strings.Contains(ce.Message, "pi") || !strings.Contains(ce.Message, "claude") {
		t.Errorf("message %q should name the value and the implemented set", ce.Message)
	}
}

func TestCommandMalformedCaptureConfigIsValidationConfig(t *testing.T) {
	_, configHome := envSeams(t, writeHome(t))
	for name, content := range map[string]string{
		"non-mapping capture section": "capture: yes\n",
		"mistyped auto_dismiss_noop":  "capture:\n  auto_dismiss_noop: yes\n",
		"exclude not a list":          "capture:\n  exclude: claude\n",
		"unknown auto source":         "capture:\n  pi:\n    auto: true\n",
		"unparseable config":          "capture:\n\texclude: [claude]\n",
	} {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, configHome, content)
			_, _, err := runCommand(t)
			_ = assertClasterr(t, err, "validation.config")
		})
	}

	// A mistyped journal_dir is journal.Root's error, wrapped the same
	// way — CLAST_JOURNAL_DIR must step aside for config to be read.
	writeConfig(t, configHome, "journal_dir: 123\n")
	t.Setenv("CLAST_JOURNAL_DIR", "")
	_, _, err := runCommand(t)
	_ = assertClasterr(t, err, "validation.config")
}

func TestCommandExplicitAbsentStorageIsUnavailable(t *testing.T) {
	// A claude config dir with no projects/ root: storage is absent.
	envSeams(t, t.TempDir())

	// The sweep never probes: absent storage stays quiet.
	stdout, stderr, err := runCommand(t)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("bare sweep on absent storage: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}

	// The explicit path discloses it, naming the source and probed root.
	_, _, err = runCommand(t, "--harness", "claude")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"claude"`) || !strings.Contains(ce.Message, "projects") {
		t.Errorf("message %q should name the source and the probed root", ce.Message)
	}
}

func TestCommandUnknownHarnessBeatsBadConfig(t *testing.T) {
	_, configHome := envSeams(t, writeHome(t))
	writeConfig(t, configHome, "capture: yes\n")

	// Flag validation runs before config: the bad name reports
	// unknown-harness even though config.Load would fail next.
	_, _, err := runCommand(t, "--harness", "bogus")
	_ = assertClasterr(t, err, "validation.unknown-harness")
}

func TestCommandExclusionLeavesReadersIntact(t *testing.T) {
	journalDir, configHome := envSeams(t, writeHome(t))

	// First sweep captures both sessions normally.
	if _, _, err := runCommand(t); err != nil {
		t.Fatal(err)
	}

	// Excluding the source changes nothing for readers: the registry
	// stays the sole authority, and retained artifacts stay readable
	// through the seams sessions/show/analyze resolve with.
	writeConfig(t, configHome, "capture:\n  exclude: [claude]\n")
	if stdout, stderr, err := runCommand(t); err != nil || stdout != "" || stderr != "" {
		t.Fatalf("excluded sweep: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}

	if len(sourceregistry.All) != 2 || sourceregistry.Names[0] != "claude" || sourceregistry.Names[1] != "amp" {
		t.Errorf("registry table changed under exclusion: %v", sourceregistry.Names)
	}
	if _, ok := sourceregistry.Lookup("claude"); !ok {
		t.Error("Lookup(claude) failed after exclusion")
	}
	if _, err := sourceregistry.ValidateTranscriptFormat("claude-jsonl"); err != nil {
		t.Errorf("ValidateTranscriptFormat after exclusion: %v", err)
	}
	if _, ok := sourceregistry.LookupTranscriptReader("claude-jsonl"); !ok {
		t.Error("LookupTranscriptReader(claude-jsonl) failed after exclusion")
	}
	items, _, err := journal.Walk(journalDir)
	if err != nil || len(items) != 2 {
		t.Fatalf("retained sessions unreadable after exclusion: items=%v err=%v", items, err)
	}
}

// TestCommandSecondSweepIsQuiet pins the no-op sweep on the command
// surface: a second bare capture over unchanged transcripts emits
// nothing on either stream, exits successfully, and moves not one byte
// of the journal — the auto-dismissed session's curation.json included.
func TestCommandSecondSweepIsQuiet(t *testing.T) {
	journalDir, _ := envSeams(t, writeHome(t))

	if stdout, _, err := runCommand(t); err != nil || strings.Count(stdout, "captured claude-") != 2 {
		t.Fatalf("first sweep: stdout=%q err=%v, want two captured lines", stdout, err)
	}
	stable := treeHash(t, journalDir)

	stdout, stderr, err := runCommand(t)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("second sweep: stdout=%q stderr=%q err=%v, want quiet success", stdout, stderr, err)
	}
	if after := treeHash(t, journalDir); after != stable {
		t.Error("second sweep changed journal bytes")
	}
	cur, present, err := journal.ReadCuration(journalDir, "2026-09-02",
		journal.SessionKey{Harness: "claude", NativeID: noopID})
	if err != nil || !present || cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != autoNoopReason {
		t.Errorf("auto-dismissal disturbed by the no-op sweep: present=%v cur=%+v err=%v", present, cur, err)
	}
}

// TestCommandPartialCaptureSectionKeepsDefaults pins the top-level-merge
// consequence the per-key read-time-default pattern exists for: a user
// config whose capture: section carries only some keys replaces the
// shipped section wholesale, so capture.auto_dismiss_noop's absence must
// still read as its default (true) — the no-op session is dismissed.
func TestCommandPartialCaptureSectionKeepsDefaults(t *testing.T) {
	journalDir, configHome := envSeams(t, writeHome(t))
	writeConfig(t, configHome, "capture:\n  exclude: []\n")

	if _, _, err := runCommand(t); err != nil {
		t.Fatalf("sweep under a partial capture: section: %v", err)
	}
	cur, present, err := journal.ReadCuration(journalDir, "2026-09-02",
		journal.SessionKey{Harness: "claude", NativeID: noopID})
	if err != nil || !present || cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != autoNoopReason {
		t.Errorf("auto_dismiss_noop lost its default under a partial section: present=%v cur=%+v err=%v",
			present, cur, err)
	}
}

// TestCommandSweepIsolatesRegisteredSourceFailure exercises mixed-source
// failure isolation through the command surface itself, the seam Run's
// own TestRunContinuesPastSourceFailure cannot reach: a bare sweep over
// the real registry where one row cannot enumerate. The walk set is a
// copy of registry.All, so the only way to seat a second row is to lend
// the table a stub for the test's duration — restored unconditionally.
func TestCommandSweepIsolatesRegisteredSourceFailure(t *testing.T) {
	envSeams(t, writeHome(t))

	prev := sourceregistry.All
	sourceregistry.All = append(append([]source.Source{}, prev...),
		stubFailSource{stubSource{name: "brokensrc"}, errors.New("storage unreachable")})
	t.Cleanup(func() { sourceregistry.All = prev })

	stdout, stderr, err := runCommand(t)
	if err != nil {
		t.Fatalf("mixed-source sweep: %v", err)
	}
	// The healthy source still captures; the broken one is disclosed on
	// stderr and the run stays successful.
	if n := strings.Count(stdout, "captured claude-"); n != 2 {
		t.Errorf("stdout = %q, want claude's two captured lines despite the failed sibling", stdout)
	}
	if !strings.Contains(stderr, "capture: brokensrc: storage unreachable") {
		t.Errorf("stderr = %q, want the failed source's disclosure", stderr)
	}

	// The same failing row on the explicit path maps to the verdict —
	// capture.source-unavailable — the reportOutcome branch the real
	// table never reaches (claude's own Present always fails first).
	_, _, err = runCommand(t, "--harness", "brokensrc")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"brokensrc"`) || !strings.Contains(ce.Message, "storage unreachable") {
		t.Errorf("message %q should name the source and its failure", ce.Message)
	}
}

// TestRunGrowthFlagsCuratedSessionStale pins M7's detection from the
// capture side: a session curated against one transcript fingerprint
// goes stale when the live transcript grows — the recapture writes the
// new fingerprint into session.json while curation.json keeps the stamp
// recorded at curation, so WalkItem.Stale() flips.
func TestRunGrowthFlagsCuratedSessionStale(t *testing.T) {
	home, root := writeHome(t), t.TempDir()
	d := deps(t, home, root)
	if _, _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	// Curate the live session, stamping the current fingerprint — the
	// TestRunBackfillKeepsCurationFresh shape, minus the project.
	liveKey := journal.SessionKey{Harness: "claude", NativeID: liveID}
	s, _, err := journal.ReadSession(root, "2026-09-01", liveKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteCuration(root, "2026-09-01", liveKey, journal.Curation{
		State: journal.StateCurated, At: d.Now(), Machine: "m",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: s.Transcript.Lines, SHA256: s.Transcript.SHA256},
	}); err != nil {
		t.Fatal(err)
	}

	// The transcript grows; the next sweep recaptures it.
	livePath := filepath.Join(home, "projects", "-home-user-Code-demo", liveID+".jsonl")
	f, err := os.OpenFile(livePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line("/home/user/Code/demo", "10000000-aaaa-4aaa-8aaa-000000000003", "assistant", "more", "2026-09-01T11:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	captured, diags, _, err := Run(context.Background(), d)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Run: err=%v diags=%v", err, diags)
	}
	if len(captured) != 1 || !captured[0].Recaptured || captured[0].Key != liveKey {
		t.Fatalf("captured = %+v, want the grown session's recapture", captured)
	}

	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		if it.Key != liveKey {
			continue
		}
		found = true
		if it.State() != journal.StateCurated {
			t.Errorf("recapture disturbed curation state: %q", it.State())
		}
		if !it.Stale() {
			t.Error("curated-then-grown session not flagged stale")
		}
	}
	if !found {
		t.Fatal("grown session missing from the journal walk")
	}
}

func TestAutoDismissNoopConfig(t *testing.T) {
	if v, err := autoDismissNoop(config.Config{}); err != nil || !v {
		t.Errorf("absent key: %v %v", v, err)
	}
	if v, err := autoDismissNoop(config.Config{"capture": map[string]any{"auto_dismiss_noop": false}}); err != nil || v {
		t.Errorf("explicit false: %v %v", v, err)
	}
	// config.Load's actual shape: yaml.v3 types nested mappings as the
	// parent map's type, config.Config.
	if v, err := autoDismissNoop(config.Config{"capture": config.Config{"auto_dismiss_noop": false}}); err != nil || v {
		t.Errorf("nested config.Config false: %v %v", v, err)
	}
	if _, err := autoDismissNoop(config.Config{"capture": "yes"}); err == nil {
		t.Error("mistyped section accepted")
	}
	if _, err := autoDismissNoop(config.Config{"capture": map[string]any{"auto_dismiss_noop": "yes"}}); err == nil {
		t.Error("mistyped value accepted")
	}
}

// unreadableHome builds a claude config dir whose projects/ root exists
// but cannot be enumerated — it is a regular file, so ReadDir fails
// with a non-NotExist error. Deliberately distinct from the absent case
// (no projects/ at all), which stays quiet: storage that is present but
// unreadable is a disclosed source failure.
func unreadableHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "projects"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCommandSweepDisclosesSourceFailure(t *testing.T) {
	envSeams(t, unreadableHome(t))

	stdout, stderr, err := runCommand(t)
	if err != nil {
		t.Fatalf("sweep over an unreadable source: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want no captured rows", stdout)
	}
	// One per-source disclosure in the extended diag format; the run
	// itself stays successful (V13) while hiding nothing.
	if !strings.HasPrefix(stderr, "capture: claude: ") || !strings.Contains(stderr, "projects") {
		t.Errorf("stderr = %q, want one \"capture: claude: <err>\" line naming the unreadable root", stderr)
	}
}

func TestCommandSweepJSONDisclosesUnavailable(t *testing.T) {
	envSeams(t, unreadableHome(t))

	stdout, _, err := runCommandFlags(t, cliflags.Flags{JSON: true})
	if err != nil {
		t.Fatalf("json sweep over an unreadable source: %v", err)
	}
	var payload struct {
		Captured    []json.RawMessage `json:"captured"`
		Unavailable []struct {
			Source string `json:"source"`
			Error  string `json:"error"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, stdout)
	}
	if len(payload.Captured) != 0 {
		t.Errorf("captured = %s, want empty", payload.Captured)
	}
	if len(payload.Unavailable) != 1 || payload.Unavailable[0].Source != "claude" ||
		!strings.Contains(payload.Unavailable[0].Error, "projects") {
		t.Errorf("unavailable = %+v, want one claude row naming the failure", payload.Unavailable)
	}

	// A clean sweep's payload is unchanged — "unavailable" is additive
	// and appears only when non-empty.
	envSeams(t, writeHome(t))
	stdout, _, err = runCommandFlags(t, cliflags.Flags{JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "unavailable") {
		t.Errorf("clean sweep payload carries unavailable: %s", stdout)
	}
}

func TestCommandExplicitUnreadableIsSourceUnavailable(t *testing.T) {
	envSeams(t, unreadableHome(t))

	// Present probes the same root Discover would enumerate: an
	// unreadable one is disclosed as capture.source-unavailable naming
	// the source and the probed root — not a quiet exit 0, and (unlike
	// a sweep) a non-zero verdict.
	_, _, err := runCommand(t, "--harness", "claude")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"claude"`) || !strings.Contains(ce.Message, "projects") {
		t.Errorf("message %q should name the source and the probed root", ce.Message)
	}
}

func TestReportOutcomeMapsNamedFailure(t *testing.T) {
	// The explicit path's verdict: a collected per-source failure
	// becomes capture.source-unavailable. End-to-end the real source's
	// Presence probe fails first, so this branch is reached only by a
	// source whose probe is weaker than its Discover (the contract's
	// shape for amp: a credential check vs a transport call) — the
	// mapping is pinned here on its inputs.
	var out, errBuf bytes.Buffer
	streams := &iostreams.Streams{Out: &out, Err: &errBuf}
	failed := []SourceFailure{{Source: "amp", Err: errors.New("endpoint unreachable")}}

	err := reportOutcome(streams, stubSource{name: "amp"}, nil, failed, nil)
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"amp"`) || !strings.Contains(ce.Message, "endpoint unreachable") {
		t.Errorf("message %q should name the source and its failure", ce.Message)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty — the envelope IS the explicit path's disclosure", errBuf.String())
	}

	// The sweep path discloses the same failure as a continue-past
	// diagnostic and still returns success.
	errBuf.Reset()
	if err := reportOutcome(streams, nil, nil, failed, nil); err != nil {
		t.Errorf("sweep path returned %v, want nil (exit 0)", err)
	}
	if got := errBuf.String(); !strings.Contains(got, "capture: amp: endpoint unreachable") {
		t.Errorf("stderr = %q, want \"capture: amp: endpoint unreachable\"", got)
	}
}

func TestReportOutcomeDisclosesBeforeFatal(t *testing.T) {
	// A fatal abort never swallows the disclosures already collected:
	// diagnostics and sweep failures print before the run error
	// propagates.
	var out, errBuf bytes.Buffer
	streams := &iostreams.Streams{Out: &out, Err: &errBuf}
	fatal := errors.New("journal: writing marker: disk full")
	diags := []source.Diagnostic{{Path: "/x", Err: errors.New("skipped")}}
	failed := []SourceFailure{{Source: "amp", Err: errors.New("endpoint unreachable")}}

	err := reportOutcome(streams, nil, diags, failed, fatal)
	if err != fatal {
		t.Fatalf("run error = %v, want the fatal error passed through", err)
	}
	got := errBuf.String()
	if !strings.Contains(got, "capture: /x: skipped") || !strings.Contains(got, "capture: amp: endpoint unreachable") {
		t.Errorf("stderr = %q, want both disclosures", got)
	}
}
