package cli_test

// Pi's registration e2e (pi-source/step-10): the source sits in
// registry.All as a local file-tail model, so the real binary's bare
// sweep picks it up by default, capture.exclude filters it, --harness
// overrides, and readers resolve pi-jsonl-v3 through the same registry.
// Every case runs against fixture trees copied into temp dirs at the
// real default path (<HOME>/.pi/agent/sessions), with no Pi executable
// anywhere on PATH and nothing written under the real ~/.pi.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	piBasic    = "01900000-0000-7000-8000-000000000001"
	piBadLines = "01900000-0000-7000-8000-000000000009"
)

// piFixtureDir is the pi package's checked-in fixture root.
func piFixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "source", "pi", "testdata", "roots", "percwd"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// piHome copies the named fixtures (by header id, matched on the file
// name) into <home>/.pi/agent/sessions, keeping the per-cwd directory
// layout, and returns the temp HOME. An empty ids list copies nothing
// but still creates the sessions directory.
func piHome(t *testing.T, ids ...string) string {
	t.Helper()
	home := t.TempDir()
	dst := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	root := piFixtureDir(t)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, id := range ids {
			if strings.Contains(d.Name(), id) {
				rel, _ := filepath.Rel(root, p)
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				target := filepath.Join(dst, rel)
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				return os.WriteFile(target, b, 0o644)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// piEnv: journal and config sealed, HOME pointed at the Pi fixture
// home, claude storage absent, and PATH emptied — retained history must
// be capturable and readable with no Pi binary.
func piEnv(t *testing.T, home string) []string {
	t.Helper()
	return []string{
		"CLAST_JOURNAL_DIR=" + t.TempDir(),
		"XDG_CONFIG_HOME=" + t.TempDir(),
		"XDG_DATA_HOME=" + t.TempDir(),
		"CLAUDE_CONFIG_DIR=" + t.TempDir(),
		"HOME=" + home,
		"PATH=" + t.TempDir(),
	}
}

func writePiConfig(t *testing.T, env []string, body string) {
	t.Helper()
	dir := filepath.Join(envValue(env, "XDG_CONFIG_HOME"), "clast")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPlumbingCapture_PiLocalSweep: a bare sweep includes the local pi
// source by default — no opt-in, no executable — and a second sweep is
// the quiet no-op cron depends on.
func TestPlumbingCapture_PiLocalSweep(t *testing.T) {
	env := piEnv(t, piHome(t, piBasic))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "captured pi-"+piBasic+"\n" || r.stderr != "" {
		t.Fatalf("first sweep: exit=%d stdout=%q stderr=%q, want one pi capture line",
			r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("second sweep: exit=%d stdout=%q stderr=%q, want quiet 0",
			r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_PiMixedSweep: pi and claude capture side by side,
// the other sources are unaffected, and removing pi storage afterwards
// leaves the next sweep quiet.
func TestPlumbingCapture_PiMixedSweep(t *testing.T) {
	home := piHome(t, piBasic)
	env := append(piEnv(t, home), "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stderr != "" {
		t.Fatalf("mixed sweep: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "captured pi-"+piBasic) || !strings.Contains(r.stdout, "captured claude-") {
		t.Fatalf("mixed sweep stdout = %q, want pi and claude rows", r.stdout)
	}
	if strings.Contains(r.stdout, "devin-") || strings.Contains(r.stdout, "amp-") {
		t.Errorf("mixed sweep stdout = %q, want no devin or amp rows", r.stdout)
	}

	if err := os.RemoveAll(filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("post-removal sweep: exit=%d stdout=%q stderr=%q, want quiet 0", r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_PiExcludedButExplicit: capture.exclude skips pi in
// the sweep while --harness pi — itself the authorization — captures
// regardless, and the committed session stays readable under the same
// exclusion.
func TestPlumbingCapture_PiExcludedButExplicit(t *testing.T) {
	env := piEnv(t, piHome(t, piBasic))
	writePiConfig(t, env, "capture:\n  exclude: [pi]\n")

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("excluded sweep: exit=%d stdout=%q stderr=%q, want quiet 0", r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "capture", "--harness", "pi")
	if r.exitCode != 0 || r.stdout != "captured pi-"+piBasic+"\n" {
		t.Fatalf("explicit capture under exclusion: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}

	r = run(t, env, "plumbing", "sessions", "--since", "all", "--json")
	if r.exitCode != 0 || !strings.Contains(r.stdout, `"harness":"pi"`) || !strings.Contains(r.stdout, piBasic) {
		t.Fatalf("sessions under exclusion: exit=%d stdout=%q", r.exitCode, r.stdout)
	}
	r = run(t, env, "plumbing", "show", "pi-"+piBasic, "--transcript")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "FIXTURE-OK") {
		t.Fatalf("show --transcript under exclusion: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_PiExplicitUnavailable: the absent-versus-empty
// split — absent storage is quiet in a sweep and capture.source-
// unavailable (exit 1) when named; an existing empty store is truthful
// silence on both paths.
func TestPlumbingCapture_PiExplicitUnavailable(t *testing.T) {
	env := piEnv(t, t.TempDir()) // no ~/.pi at all

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("bare sweep on absent pi storage: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "capture", "--harness", "pi", "--json")
	if r.exitCode != 1 {
		t.Fatalf("explicit capture on absent storage: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	e := parseErrorEnvelope(t, r.stderr)
	if e.Error.Code != "capture.source-unavailable" || !strings.Contains(e.Error.Message, `"pi"`) ||
		!strings.Contains(e.Error.Message, ".pi") {
		t.Errorf("envelope = %+v, want capture.source-unavailable naming pi and the probed root", e.Error)
	}

	// Existing but empty root: not unavailable on either path.
	env = piEnv(t, piHome(t))
	r = run(t, env, "plumbing", "capture", "--harness", "pi", "--json")
	if r.exitCode != 0 {
		t.Fatalf("empty-root explicit capture: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Captured []json.RawMessage `json:"captured"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil || len(payload.Captured) != 0 {
		t.Fatalf("empty-root stdout=%q, want an empty captured list", r.stdout)
	}
}

// TestPlumbingCapture_PiUnenumerableDisclosed: a Pi root that exists but
// cannot be listed is one disclosed source failure — the claude source
// still captures and the sweep exits 0 — and the same root under
// --harness pi is the exit-1 verdict.
func TestPlumbingCapture_PiUnenumerableDisclosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not deny access")
	}
	home := piHome(t, piBasic)
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.Chmod(sessions, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessions, 0o755) })
	env := append(piEnv(t, home), "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture", "--json")
	if r.exitCode != 0 {
		t.Fatalf("sweep with an unenumerable pi root: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Captured    []json.RawMessage `json:"captured"`
		Unavailable []struct {
			Source string `json:"source"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout not one JSON value: %v: %q", err, r.stdout)
	}
	if len(payload.Unavailable) != 1 || payload.Unavailable[0].Source != "pi" {
		t.Errorf("unavailable = %+v, want the pi row", payload.Unavailable)
	}
	if len(payload.Captured) == 0 {
		t.Error("unenumerable pi must not deny claude's captures")
	}
	if !strings.HasPrefix(r.stderr, "capture: pi: ") {
		t.Errorf("stderr = %q, want one 'capture: pi: <err>' disclosure", r.stderr)
	}

	// Human mode: the claude row on stdout, the disclosure on stderr.
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || !strings.Contains(r.stderr, "capture: pi: ") {
		t.Fatalf("human sweep: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}

	r = run(t, env, "plumbing", "capture", "--harness", "pi", "--json")
	if r.exitCode != 1 {
		t.Fatalf("explicit capture on unenumerable root: exit=%d, want 1", r.exitCode)
	}
	if code := parseErrorEnvelope(t, r.stderr).Error.Code; code != "capture.source-unavailable" {
		t.Errorf("error code = %q, want capture.source-unavailable", code)
	}
}

// TestPlumbingCapture_PiItemDiagnostic: a per-file anomaly (the
// bad-lines fixture) is captured with a stderr diagnostic; the sweep
// exits 0 and the healthy sessions are unaffected.
func TestPlumbingCapture_PiItemDiagnostic(t *testing.T) {
	env := piEnv(t, piHome(t, piBasic, piBadLines))
	env = append(env, "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 {
		t.Fatalf("item-diag sweep: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	for _, want := range []string{"pi-" + piBasic, "pi-" + piBadLines, "claude-"} {
		if !strings.Contains(r.stdout, "captured "+want) {
			t.Errorf("stdout missing %q: %q", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, piBadLines) || !strings.Contains(r.stderr, "unparseable line") {
		t.Errorf("stderr = %q, want the bad-lines diagnostic", r.stderr)
	}
	if strings.Contains(r.stderr, piBasic) {
		t.Errorf("stderr = %q, the healthy session must not be diagnosed", r.stderr)
	}
}

// TestPlumbingCapture_PiDeletedStoreReaders: delete the Pi store after
// capture — sessions, show --transcript, and analyze keep serving the
// committed artifact, with pi excluded as well.
func TestPlumbingCapture_PiDeletedStoreReaders(t *testing.T) {
	home := piHome(t, piBasic)
	env := piEnv(t, home)
	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "captured pi-"+piBasic) {
		t.Fatalf("capture: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	if err := os.RemoveAll(filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}
	writePiConfig(t, env, "capture:\n  exclude: [pi]\n")

	r = run(t, env, "plumbing", "sessions", "--since", "all", "--json")
	if r.exitCode != 0 || !strings.Contains(r.stdout, piBasic) {
		t.Fatalf("sessions after store deletion: exit=%d stdout=%q", r.exitCode, r.stdout)
	}
	r = run(t, env, "plumbing", "sessions", "--since", "all", "--harness", "pi")
	if r.exitCode != 0 || !strings.Contains(r.stdout, piBasic) {
		t.Fatalf("sessions --harness pi after store deletion: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "show", "pi-"+piBasic, "--transcript")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "FIXTURE-OK") {
		t.Fatalf("show --transcript after store deletion: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	out := filepath.Join(t.TempDir(), "analyze.html")
	r = run(t, env, "analyze", "2026-01-10", "--since", "-1d", "--out", out)
	if r.exitCode != 0 {
		t.Fatalf("analyze after store deletion: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	page, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(page), piBasic[:8]) {
		t.Errorf("analyze page missing the committed session: len=%d err=%v", len(page), err)
	}
}

// TestPlumbingCapture_PiConfigValidation: pi is in the implemented set
// config validation names, and capture.pi.auto behaves exactly as for
// the other local source — a boolean is accepted and inert (pi still
// sweeps), a mistyped value errors on the key.
func TestPlumbingCapture_PiConfigValidation(t *testing.T) {
	env := piEnv(t, piHome(t, piBasic))

	writePiConfig(t, env, "capture:\n  exclude: [pj]\n")
	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 1 || !strings.Contains(r.stderr, "implemented: claude, amp, devin, pi") {
		t.Fatalf("typo'd exclude: exit=%d stderr=%q, want the implemented set incl. pi", r.exitCode, r.stderr)
	}

	writePiConfig(t, env, "capture:\n  pi:\n    auto: \"yes\"\n")
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 1 || !strings.Contains(r.stderr, "capture.pi.auto") {
		t.Fatalf("mistyped pi.auto: exit=%d stderr=%q, want the key named", r.exitCode, r.stderr)
	}

	// Inert on a local source: auto:false does not keep pi out of a sweep.
	writePiConfig(t, env, "capture:\n  pi:\n    auto: false\n")
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "captured pi-"+piBasic+"\n" {
		t.Fatalf("pi.auto false: exit=%d stdout=%q stderr=%q, want pi captured (inert key)", r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_PiUnknownHarnessListsPi pins the registry-driven
// validation error: a bad name names the implemented set, pi in it.
func TestPlumbingCapture_PiUnknownHarnessListsPi(t *testing.T) {
	env := piEnv(t, t.TempDir())
	r := run(t, env, "plumbing", "capture", "--harness", "nope", "--json")
	if r.exitCode != 1 && r.exitCode != 2 {
		t.Fatalf("unknown harness: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stderr, "claude, amp, devin, pi") {
		t.Errorf("unknown-harness message %q should name the implemented set", r.stderr)
	}
}
