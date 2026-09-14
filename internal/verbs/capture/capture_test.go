package capture

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/claude"
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

	captured, diags, err := Run(context.Background(), deps(t, home, root))
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

	if _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, root)

	// A later sweep with a different clock: nothing changed at the
	// source, so nothing may move in the journal.
	d.Now = func() time.Time { return time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC) }
	captured, diags, err := Run(context.Background(), d)
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
	if _, _, err := Run(context.Background(), d); err != nil {
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

	captured, diags, err := Run(context.Background(), d)
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
	if _, _, err := Run(context.Background(), d); err != nil {
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
	captured, diags, err := Run(context.Background(), d)
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
	captured, diags, err = Run(context.Background(), d)
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
	if _, _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	registerClone(t, root, "demo", repo)
	if _, _, err := Run(context.Background(), d); err != nil {
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
	if _, _, err := Run(context.Background(), d); err != nil {
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
	if _, _, err := Run(context.Background(), d); err != nil {
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
	if _, _, err := Run(context.Background(), d); err != nil {
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

	if _, _, err := Run(context.Background(), d); err != nil {
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
	captured, _, err := Run(context.Background(), d)
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

func TestResolveSourcesValidation(t *testing.T) {
	if _, err := resolveSources("devin"); err == nil {
		t.Fatal("unknown harness accepted")
	}
	all, err := resolveSources("")
	if err != nil || len(all) == 0 {
		t.Fatalf("bare resolve: %v %v", all, err)
	}
	one, err := resolveSources("claude")
	if err != nil || len(one) != 1 {
		t.Fatalf("claude resolve: %v %v", one, err)
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
