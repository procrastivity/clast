// End-to-end tests for `clast analyze`: --out and serve modes through the
// built binary, with the retro summary cache filled by a real `clast retro`
// run against the llmtest stub, so retro's cache key and analyze's lookup
// are proven to agree across the two verbs.
package cli_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

const (
	analyzeDay     = "2026-04-01"
	analyzeSummary = "- Shipped: the analyze explorer"
)

// seedAnalyzeWindow builds the window the analyze e2e tests share: a
// curated session `clast retro` summarizes (filling the cache through the
// stub), then a second curated session added afterwards so it has no
// cached summary, and a dismissed session with a reason. It returns the
// env for the child processes and the stub, which the caller then points
// at failure: analyze must never reach it again.
func seedAnalyzeWindow(t *testing.T) ([]string, *llmtest.Server) {
	t.Helper()
	journalDir := t.TempDir()
	xdg := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}

	writeBriefEntry(t, journalDir, analyzeDay, journal.SessionKey{Harness: "claude", NativeID: "cached-01"},
		time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC), dev, "cached entry")

	stub := llmtest.New(t, analyzeSummary)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{
		"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "XDG_CACHE_HOME=" + t.TempDir(),
		"CLAST_LLM_API_KEY=sk-test-key",
	}
	if r := run(t, env, "retro", analyzeDay); r.exitCode != 0 {
		t.Fatalf("retro (cache fill): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests during retro, want 1", len(reqs))
	}

	writeBriefEntry(t, journalDir, analyzeDay, journal.SessionKey{Harness: "claude", NativeID: "uncached-01"},
		time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC), dev, "uncached entry")
	dismissed := journaltest.New(t)
	dismissed.Dismissed(analyzeDay, journal.SessionKey{Harness: "claude", NativeID: "dismissed-01"},
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "x"},
		time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 11, 30, 0, 0, time.UTC),
		"framework", "scratch work, nothing to keep")
	copyJournalTree(t, dismissed.Root(), journalDir)

	// From here on the endpoint fails every request, and the key is gone:
	// analyze succeeding proves it is cache-only.
	stub.Fail(500, "analyze must never call the LLM")
	env = append(env, "CLAST_LLM_API_KEY=")
	return env, stub
}

// copyJournalTree copies the fixture journal at src into dst (merging).
func copyJournalTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying fixture journal: %v", err)
	}
}

func TestAnalyze_Out_RendersCachedUncachedAndDismissed(t *testing.T) {
	env, stub := seedAnalyzeWindow(t)
	out := filepath.Join(t.TempDir(), "explorer.html")

	r := run(t, env, "analyze", analyzeDay, "--since", "-0d", "--out", out)
	if r.exitCode != 0 {
		t.Fatalf("analyze --out: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if want := "wrote " + out + "\n"; r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading --out file: %v", err)
	}
	for _, want := range []string{
		"the analyze explorer",           // cached retro summary (rendered from markdown)
		"not summarized",                 // the uncached session's hint
		"clast retro " + analyzeDay,      // ... naming the day to run
		"scratch work, nothing to keep",  // the dismissal reason
		"cached entry", "uncached entry", // both entry drafts
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Errorf("stub captured %d requests in total, want only retro's 1 (analyze is cache-only)", len(reqs))
	}

	js := run(t, env, "analyze", analyzeDay, "--since", "-0d", "--out", out, "--json")
	if js.exitCode != 0 {
		t.Fatalf("analyze --out --json: exit=%d, want 0; stderr=%q", js.exitCode, js.stderr)
	}
	var payload struct {
		Out         string `json:"out"`
		WindowStart string `json:"window_start"`
		Day         string `json:"day"`
	}
	if err := json.Unmarshal([]byte(js.stdout), &payload); err != nil {
		t.Fatalf("--json stdout is not one JSON value: %v; stdout=%q", err, js.stdout)
	}
	if payload.Out != out || payload.Day != analyzeDay || payload.WindowStart != analyzeDay {
		t.Errorf("payload = %+v, want out=%q window_start=day=%q", payload, out, analyzeDay)
	}
}

func TestAnalyze_OutAndAddr_IsUsageError(t *testing.T) {
	r := run(t, nil, "analyze", "--out", filepath.Join(t.TempDir(), "x.html"), "--addr", "127.0.0.1:0")
	if r.exitCode != 2 {
		t.Fatalf("exit = %d, want 2 (usage error); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "clast: ") {
		t.Errorf("stderr = %q, want it to start with %q", r.stderr, "clast: ")
	}
}

func TestAnalyze_Serve_MatchesOutAndStopsOnSIGINT(t *testing.T) {
	env, _ := seedAnalyzeWindow(t)
	out := filepath.Join(t.TempDir(), "explorer.html")
	if r := run(t, env, "analyze", analyzeDay, "--since", "-0d", "--out", out); r.exitCode != 0 {
		t.Fatalf("analyze --out: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	want, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "analyze", analyzeDay, "--since", "-0d", "--addr", "127.0.0.1:0", "--json")
	cmd.Env = hermeticEnv(t, env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting analyze: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // no-op once it has exited
	})

	// The first stdout line is the bound URL; a watchdog unblocks the read
	// if the process never prints one.
	watchdog := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	watchdog.Stop()
	if err != nil {
		t.Fatalf("reading the URL line: %v (got %q)", err, line)
	}
	var announced struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(line), &announced); err != nil || !strings.HasPrefix(announced.URL, "http://127.0.0.1:") {
		t.Fatalf("first stdout line = %q, want {\"url\":\"http://127.0.0.1:<port>/\"}", line)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(announced.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", announced.URL, err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	if string(got) != string(want) {
		t.Error("served page differs from the --out file for the same window")
	}

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("analyze after SIGINT: %v, want clean exit 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("analyze did not exit within 10s of SIGINT")
	}
}
