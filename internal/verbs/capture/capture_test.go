package capture

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/claude"
)

const (
	liveID = "aaaaaaaa-1111-4111-8111-111111111111"
	noopID = "bbbbbbbb-2222-4222-8222-222222222222"
)

func line(uuid, typ, content, ts string) string {
	msg := fmt.Sprintf(`{"role":"user","content":%q}`, content)
	if typ == "assistant" {
		msg = fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"stop_reason":"end_turn"}`, content)
	}
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"type":%q,"message":%s,"uuid":%q,"timestamp":%q,"userType":"external","cwd":"/home/user/Code/demo","sessionId":"x","version":"2.1.226","gitBranch":"main"}`,
		typ, msg, uuid, ts) + "\n"
}

// writeHome builds a minimal claude config dir: one substantive session
// and one no-op session.
func writeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	slug := filepath.Join(home, "projects", "-home-user-Code-demo")
	if err := os.MkdirAll(slug, 0o755); err != nil {
		t.Fatal(err)
	}
	live := line("10000000-aaaa-4aaa-8aaa-000000000001", "user", "hi", "2026-09-01T10:00:00.000Z") +
		line("10000000-aaaa-4aaa-8aaa-000000000002", "assistant", "hello", "2026-09-01T10:00:01.000Z")
	noop := line("20000000-bbbb-4bbb-8bbb-000000000001", "user", "never mind", "2026-09-02T08:00:00.000Z")
	if err := os.WriteFile(filepath.Join(slug, liveID+".jsonl"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slug, noopID+".jsonl"), []byte(noop), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
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
	if _, err := f.WriteString(line("20000000-bbbb-4bbb-8bbb-000000000002", "assistant", "resumed", "2026-09-02T09:00:00.000Z")); err != nil {
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
	if _, err := f.WriteString(line("10000000-aaaa-4aaa-8aaa-000000000003", "assistant", "more", "2026-09-01T11:00:00.000Z")); err != nil {
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
	if _, err := autoDismissNoop(config.Config{"capture": "yes"}); err == nil {
		t.Error("mistyped section accepted")
	}
	if _, err := autoDismissNoop(config.Config{"capture": map[string]any{"auto_dismiss_noop": "yes"}}); err == nil {
		t.Error("mistyped value accepted")
	}
}
