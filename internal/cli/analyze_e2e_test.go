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

	base, stop := startAnalyzeServe(t, env)
	got, status := httpGet(t, base)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", status)
	}
	if string(got) != string(want) {
		t.Error("served page differs from the --out file for the same window")
	}
	stop()
}

// startAnalyzeServe runs `clast analyze` in serve mode on an ephemeral port
// and returns the announced URL and a stop func that sends SIGINT and
// requires a clean exit 0. The process is killed at cleanup regardless.
func startAnalyzeServe(t *testing.T, env []string) (string, func()) {
	t.Helper()
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
	stop := func() {
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
	return announced.URL, stop
}

// httpGet fetches url and returns the body and status code.
func httpGet(t *testing.T, url string) ([]byte, int) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b, resp.StatusCode
}

// transcriptMarker sits in a later prompt, a tool result and a thinking
// block of the seeded transcript, and in nothing the overview shows.
const transcriptMarker = "ZQX-MARKER-7431"

const (
	transcriptSession = "7e57ab1e-0000-4000-8000-000000000001"
	transcriptAgent   = "a0b1c2d3e4f5a6b7"
)

// seedTranscriptWindow builds a fake Claude config dir holding one session
// with a subagent, then runs the real `clast plumbing capture` over it, so
// the transcript reaches the journal by the path production uses. The
// returned env has no LLM config: analyze is cache-only, so the overview
// shows the session as not summarized.
func seedTranscriptWindow(t *testing.T) []string {
	t.Helper()
	claudeDir := t.TempDir()
	proj := filepath.Join(claudeDir, "projects", "-tmp-widget")
	sub := filepath.Join(proj, transcriptSession, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"mode","mode":"normal","sessionId":"` + transcriptSession + `"}`,
		`{"type":"user","uuid":"u1","sessionId":"` + transcriptSession + `","cwd":"/tmp/widget","gitBranch":"main","timestamp":"2026-04-01T12:00:00Z","message":{"role":"user","content":"Fix the flaky widget test"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-04-01T12:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"pondering ` + transcriptMarker + `","signature":"sig"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-04-01T12:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"Looking at the test now."}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-04-01T12:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"go test ./widget"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-04-01T12:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"FAIL widget ` + transcriptMarker + `"}]}}`,
		`{"type":"system","subtype":"turn_duration","uuid":"m1","timestamp":"2026-04-01T12:00:05Z","durationMs":4200,"content":"4.2s"}`,
		`{"type":"assistant","uuid":"a4","timestamp":"2026-04-01T12:00:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent","name":"Agent","input":{"description":"dig into the flake","prompt":"find it"}}]}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-04-01T12:00:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent","content":"the clock is the culprit"}]}}`,
		`{"type":"user","uuid":"u4","timestamp":"2026-04-01T12:00:08Z","message":{"role":"user","content":"Thanks, now ship it ` + transcriptMarker + `"}}`,
		`{"type":"assistant","uuid":"a5","timestamp":"2026-04-01T12:00:09Z","message":{"role":"assistant","content":[{"type":"text","text":"Shipped."}]}}`,
	}
	agentLines := []string{
		`{"type":"user","uuid":"su1","timestamp":"2026-04-01T12:00:06.5Z","message":{"role":"user","content":"find it"}}`,
		`{"type":"assistant","uuid":"sa1","timestamp":"2026-04-01T12:00:06.8Z","message":{"role":"assistant","content":[{"type":"text","text":"Subagent found the wall clock."}]}}`,
	}
	writes := map[string]string{
		filepath.Join(proj, transcriptSession+".jsonl"):           strings.Join(lines, "\n") + "\n",
		filepath.Join(sub, "agent-"+transcriptAgent+".jsonl"):     strings.Join(agentLines, "\n") + "\n",
		filepath.Join(sub, "agent-"+transcriptAgent+".meta.json"): `{"agentType":"Explore","description":"dig into the flake","toolUseId":"toolu_agent","spawnDepth":1}`,
	}
	for path, content := range writes {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := []string{
		"CLAST_JOURNAL_DIR=" + t.TempDir(), "XDG_CONFIG_HOME=" + t.TempDir(), "XDG_CACHE_HOME=" + t.TempDir(),
		"CLAUDE_CONFIG_DIR=" + claudeDir, "CLAST_LLM_API_KEY=",
	}
	if r := run(t, env, "plumbing", "capture"); r.exitCode != 0 {
		t.Fatalf("plumbing capture: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	return env
}

func TestAnalyze_Serve_TranscriptPagesEndToEnd(t *testing.T) {
	env := seedTranscriptWindow(t)
	base, stop := startAnalyzeServe(t, env)
	defer stop()

	overview, status := httpGet(t, base)
	if status != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", status)
	}
	link := `href="/t/` + transcriptSession + `"`
	if !strings.Contains(string(overview), link) {
		t.Fatalf("overview lacks the transcript link %s; page:\n%s", link, overview)
	}

	page, status := httpGet(t, base+"t/"+transcriptSession)
	if status != http.StatusOK {
		t.Fatalf("GET transcript status = %d, want 200", status)
	}
	html := string(page)
	for _, want := range []string{
		"Fix the flaky widget test", // first prompt
		"Looking at the test now.",  // assistant text
		"Shipped.",
		"pondering " + transcriptMarker, // thinking, inside its details
		"FAIL widget " + transcriptMarker,
		"go test ./widget",
		"turn_duration",
		`href="/t/` + transcriptSession + `/agents/` + transcriptAgent + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("transcript page lacks %q", want)
		}
	}
	if !strings.Contains(html, `<details class="tool"`) || strings.Contains(html, `<details class="tool" open`) ||
		!strings.Contains(html, `<details class="think">`) || strings.Contains(html, `<details class="think" open`) {
		t.Error("tool calls and thinking must render as closed <details>")
	}
	if !strings.Contains(html, `<input type="checkbox" id="showmeta">`) || strings.Contains(html, `id="showmeta" checked`) {
		t.Error("meta records must be hidden until the show-meta toggle is checked")
	}

	agentPage, status := httpGet(t, base+"t/"+transcriptSession+"/agents/"+transcriptAgent)
	if status != http.StatusOK {
		t.Fatalf("GET subagent status = %d, want 200", status)
	}
	for _, want := range []string{
		"Subagent found the wall clock.",
		"Subagent",
		`href="/t/` + transcriptSession + `#call-toolu_agent"`, // back-link to the spawning call
	} {
		if !strings.Contains(string(agentPage), want) {
			t.Errorf("subagent page lacks %q", want)
		}
	}

	for _, path := range []string{"t/no-such-session", "t/" + transcriptSession + "/agents/no-such-agent"} {
		if _, status := httpGet(t, base+path); status != http.StatusNotFound {
			t.Errorf("GET /%s status = %d, want 404", path, status)
		}
	}
}

// The overview carries no transcript text: a marker that lives only in the
// transcript is absent from "/" and from the --out file, present in the
// transcript page, and "/" still equals the --out file byte for byte.
func TestAnalyze_Overview_CarriesNoTranscriptContent(t *testing.T) {
	env := seedTranscriptWindow(t)
	out := filepath.Join(t.TempDir(), "explorer.html")
	if r := run(t, env, "analyze", analyzeDay, "--since", "-0d", "--out", out); r.exitCode != 0 {
		t.Fatalf("analyze --out: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	file, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	base, stop := startAnalyzeServe(t, env)
	defer stop()
	served, _ := httpGet(t, base)
	transcript, status := httpGet(t, base+"t/"+transcriptSession)
	if status != http.StatusOK {
		t.Fatalf("GET transcript status = %d, want 200", status)
	}

	if string(served) != string(file) {
		t.Error("served overview differs from the --out file for a window that includes a transcript")
	}
	for name, page := range map[string][]byte{"served overview": served, "--out file": file} {
		for _, leak := range []string{transcriptMarker, "FAIL widget", "Looking at the test now.", "Subagent found"} {
			if strings.Contains(string(page), leak) {
				t.Errorf("%s contains transcript content %q", name, leak)
			}
		}
	}
	if !strings.Contains(string(transcript), transcriptMarker) {
		t.Errorf("transcript page lacks the marker %q", transcriptMarker)
	}
}
