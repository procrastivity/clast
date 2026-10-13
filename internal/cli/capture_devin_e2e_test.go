package cli_test

// Devin's registration e2e (devin-source/step-10): the source sits in
// registry.All as a local model, so the real binary's bare sweep picks
// it up by default, capture.exclude filters it, --harness overrides,
// and readers resolve devin-export-v1 through the same registry —
// all against a disposable store built by the same fixture SQL the
// package tests use, at the real XDG path, with no Devin executable
// anywhere on PATH.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// devinDataHome builds $XDG_DATA_HOME/devin/cli/sessions.db by
// replaying the qualified fixture — the same driver the source reads
// with — and returns the data-home dir for env. The executable is
// never involved: retention alone makes the store.
func devinDataHome(t *testing.T, fixture string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "devin", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	data, err := os.ReadFile(filepath.Join("..", "source", "devin", "testdata", "stores", fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := db.Exec(line); err != nil {
			t.Fatalf("fixture: %v\n%s", err, line)
		}
	}
	return home
}

// devinEnv: journal + config sealed, devin's data home pointed at the
// fixture, claude storage absent (an empty config dir), and PATH
// emptied — retained history must be readable with no Devin binary.
func devinEnv(t *testing.T, dataHome string) []string {
	t.Helper()
	return []string{
		"CLAST_JOURNAL_DIR=" + t.TempDir(),
		"XDG_CONFIG_HOME=" + t.TempDir(),
		"XDG_DATA_HOME=" + dataHome,
		"CLAUDE_CONFIG_DIR=" + t.TempDir(),
		"PATH=" + t.TempDir(),
	}
}

// TestPlumbingCapture_DevinLocalSweep: a bare sweep includes the local
// devin source by default — no opt-in — and a second sweep is the
// quiet no-op cron depends on.
func TestPlumbingCapture_DevinLocalSweep(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "basic.sql"))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "captured devin-quiet-meadow\n" {
		t.Fatalf("first sweep: exit=%d stdout=%q stderr=%q, want the quiet-meadow line",
			r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("second sweep: exit=%d stdout=%q stderr=%q, want quiet 0",
			r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_DevinExcludedButExplicit: capture.exclude skips
// devin in the sweep while --harness devin — itself the authorization —
// captures regardless, and the committed artifact stays readable
// through sessions and show --transcript under the same exclusion.
func TestPlumbingCapture_DevinExcludedButExplicit(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "basic.sql"))
	configHome := envValue(env, "XDG_CONFIG_HOME")
	if err := os.MkdirAll(filepath.Join(configHome, "clast"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "clast", "config.yaml"),
		[]byte("capture:\n  exclude: [devin]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("excluded sweep: exit=%d stdout=%q stderr=%q, want quiet 0",
			r.exitCode, r.stdout, r.stderr)
	}

	// The explicit path bypasses exclusion.
	r = run(t, env, "plumbing", "capture", "--harness", "devin")
	if r.exitCode != 0 || r.stdout != "captured devin-quiet-meadow\n" {
		t.Fatalf("explicit capture under exclusion: exit=%d stdout=%q stderr=%q",
			r.exitCode, r.stdout, r.stderr)
	}

	// Readers never consult the sweep filter.
	r = run(t, env, "plumbing", "sessions", "--since", "all", "--json")
	if r.exitCode != 0 || !strings.Contains(r.stdout, `"harness":"devin"`) ||
		!strings.Contains(r.stdout, "quiet-meadow") {
		t.Fatalf("sessions under exclusion: exit=%d stdout=%q", r.exitCode, r.stdout)
	}
	r = run(t, env, "plumbing", "show", "devin-quiet-meadow", "--transcript")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "FIXTURE-OK") {
		t.Fatalf("show --transcript under exclusion: exit=%d stdout=%q stderr=%q",
			r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_DevinExplicitUnavailable: the absent-vs-empty
// split on the explicit path — a missing store answers
// capture.source-unavailable exit 1 while a bare sweep stays quiet;
// a present-but-empty store is truthful silence on both paths.
func TestPlumbingCapture_DevinExplicitUnavailable(t *testing.T) {
	env := devinEnv(t, t.TempDir()) // no sessions.db at all

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("bare sweep on absent devin storage: exit=%d stdout=%q stderr=%q",
			r.exitCode, r.stdout, r.stderr)
	}
	r = run(t, env, "plumbing", "capture", "--harness", "devin", "--json")
	if r.exitCode != 1 {
		t.Fatalf("explicit capture on absent storage: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	env2 := parseErrorEnvelope(t, r.stderr)
	if env2.Error.Code != "capture.source-unavailable" ||
		!strings.Contains(env2.Error.Message, `"devin"`) ||
		!strings.Contains(env2.Error.Message, "sessions.db") {
		t.Errorf("envelope = %+v, want capture.source-unavailable naming devin and the probed store", env2.Error)
	}

	// Present-but-empty: the store exists with schema but no sessions.
	dataHome := devinDataHome(t, "basic.sql")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataHome, "devin", "cli", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	env = devinEnv(t, dataHome)
	r = run(t, env, "plumbing", "capture", "--harness", "devin", "--json")
	if r.exitCode != 0 {
		t.Fatalf("present-but-empty explicit capture: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Captured []json.RawMessage `json:"captured"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil || len(payload.Captured) != 0 {
		t.Fatalf("present-but-empty stdout=%q, want an empty captured list", r.stdout)
	}
}

// TestPlumbingCapture_DevinFailedSourceDisclosed: a corrupt devin store
// is one disclosed source failure — the claude source still captures,
// the sweep still exits 0.
func TestPlumbingCapture_DevinFailedSourceDisclosed(t *testing.T) {
	dataHome := t.TempDir()
	dir := filepath.Join(dataHome, "devin", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.db"),
		[]byte("this is not a sqlite store"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"CLAST_JOURNAL_DIR=" + t.TempDir(),
		"XDG_CONFIG_HOME=" + t.TempDir(),
		"XDG_DATA_HOME=" + dataHome,
		"CLAUDE_CONFIG_DIR=" + writeClaudeHome(t),
	}

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 {
		t.Fatalf("sweep with a failed devin store: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "captured claude-") {
		t.Errorf("stdout = %q, want the claude capture to proceed", r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "capture: devin: ") {
		t.Errorf("stderr = %q, want one 'capture: devin: <err>' disclosure", r.stderr)
	}
}

// TestPlumbingCapture_UnknownHarnessListsDevin pins the registry-driven
// validation error: a bad name names the implemented set, devin now in it.
func TestPlumbingCapture_UnknownHarnessListsDevin(t *testing.T) {
	env := devinEnv(t, t.TempDir())
	r := run(t, env, "plumbing", "capture", "--harness", "nope", "--json")
	if r.exitCode != 2 && r.exitCode != 1 {
		t.Fatalf("unknown harness: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stderr, "devin") || !strings.Contains(r.stderr, "claude") {
		t.Errorf("unknown-harness message %q should name the implemented set", r.stderr)
	}
}

// TestPlumbingCapture_MixedSweep: devin and claude capture side by
// side in one bare sweep; a missing devin store then stays quiet while
// claude keeps working — default local inclusion with absent-storage
// silence, per BDS-261.
func TestPlumbingCapture_MixedSweep(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "basic.sql"))
	env = append(env, "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 {
		t.Fatalf("mixed sweep: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "captured devin-quiet-meadow") ||
		!strings.Contains(r.stdout, "captured claude-") {
		t.Fatalf("mixed sweep stdout = %q, want devin and claude rows", r.stdout)
	}

	// Remove devin storage entirely: claude still captures and the
	// committed devin session is unaffected — a missing store is quiet.
	if err := os.RemoveAll(filepath.Join(envValue(env, "XDG_DATA_HOME"), "devin")); err != nil {
		t.Fatal(err)
	}
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || r.stderr != "" || r.stdout != "" {
		t.Fatalf("post-removal sweep: exit=%d stdout=%q stderr=%q, want quiet 0 (nothing changed)",
			r.exitCode, r.stdout, r.stderr)
	}
}

// TestPlumbingCapture_DevinIncompatibleDisclosed: a schema-incompatible
// store is a real enumeration failure — disclosed while healthy sources
// continue, and the same store under --harness is the exit-1 verdict.
func TestPlumbingCapture_DevinIncompatibleDisclosed(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "incompatible.sql"))
	env = append(env, "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture", "--json")
	if r.exitCode != 0 {
		t.Fatalf("incompatible-store sweep: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Captured    []json.RawMessage `json:"captured"`
		Unavailable []struct {
			Source string `json:"source"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout not one JSON value: %v", err)
	}
	if len(payload.Unavailable) != 1 || payload.Unavailable[0].Source != "devin" {
		t.Errorf("unavailable = %+v, want the devin row", payload.Unavailable)
	}
	if len(payload.Captured) == 0 {
		t.Error("incompatible devin must not deny claude's captures")
	}

	r = run(t, env, "plumbing", "capture", "--harness", "devin", "--json")
	if r.exitCode != 1 {
		t.Fatalf("explicit capture on incompatible store: exit=%d, want 1", r.exitCode)
	}
	if code := parseErrorEnvelope(t, r.stderr).Error.Code; code != "capture.source-unavailable" {
		t.Errorf("error code = %q, want capture.source-unavailable", code)
	}
}

// TestPlumbingCapture_DevinItemDiagnostic: a per-session anomaly
// (amber-fox's malformed chat_message) is a stderr diagnostic — the
// sweep still captures every healthy session across both sources.
func TestPlumbingCapture_DevinItemDiagnostic(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "edge-cases.sql"))
	env = append(env, "CLAUDE_CONFIG_DIR="+writeClaudeHome(t))

	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 {
		t.Fatalf("item-diag sweep: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	for _, want := range []string{"devin-amber-fox", "devin-brisk-owl", "devin-hidden-wren", "devin-empty-rook", "claude-"} {
		if !strings.Contains(r.stdout, "captured "+want) {
			t.Errorf("stdout missing %q: %q", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "amber-fox") || !strings.Contains(r.stderr, "malformed") {
		t.Errorf("stderr = %q, want the amber-fox malformed-node diagnostic", r.stderr)
	}
}

// TestPlumbingCapture_DevinDeletedStoreReaders: delete the store after
// capture — sessions, show --transcript, and analyze keep serving the
// committed artifact, because the journal is the durable truth.
func TestPlumbingCapture_DevinDeletedStoreReaders(t *testing.T) {
	env := devinEnv(t, devinDataHome(t, "basic.sql"))
	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "captured devin-quiet-meadow") {
		t.Fatalf("capture: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	if err := os.RemoveAll(filepath.Join(envValue(env, "XDG_DATA_HOME"), "devin")); err != nil {
		t.Fatal(err)
	}

	r = run(t, env, "plumbing", "sessions", "--since", "all", "--json")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "quiet-meadow") {
		t.Fatalf("sessions after store deletion: exit=%d stdout=%q", r.exitCode, r.stdout)
	}
	r = run(t, env, "plumbing", "show", "devin-quiet-meadow", "--transcript")
	if r.exitCode != 0 || !strings.Contains(r.stdout, "FIXTURE-OK") {
		t.Fatalf("show --transcript after store deletion: exit=%d stdout=%q stderr=%q",
			r.exitCode, r.stdout, r.stderr)
	}
	// analyze renders the day window containing quiet-meadow — its
	// created_at is 2026-10-10T02:40Z, before the 04:00 cutoff, so the
	// M8 day bucket is 2026-10-09; a two-day window covers either.
	out := filepath.Join(t.TempDir(), "analyze.html")
	r = run(t, env, "analyze", "2026-10-10", "--since", "-1d", "--out", out)
	if r.exitCode != 0 {
		t.Fatalf("analyze after store deletion: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	page, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(page), "quiet-meadow") {
		t.Errorf("analyze page missing the committed session: len=%d err=%v", len(page), err)
	}
}

// TestPlumbingCapture_DevinConfigValidation: the implemented-name set
// reaches config validation — a typo'd exclude names devin among the
// implemented, and a mistyped capture.devin.auto errors on the key.
func TestPlumbingCapture_DevinConfigValidation(t *testing.T) {
	env := devinEnv(t, t.TempDir())
	configHome := envValue(env, "XDG_CONFIG_HOME")
	write := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(configHome, "clast"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configHome, "clast", "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("capture:\n  exclude: [dvien]\n")
	r := run(t, env, "plumbing", "capture")
	if r.exitCode != 1 || !strings.Contains(r.stderr, "devin") {
		t.Fatalf("typo'd exclude: exit=%d stderr=%q, want the implemented set incl. devin", r.exitCode, r.stderr)
	}

	write("capture:\n  devin:\n    auto: \"yes\"\n")
	r = run(t, env, "plumbing", "capture")
	if r.exitCode != 1 || !strings.Contains(r.stderr, "capture.devin.auto") {
		t.Fatalf("mistyped devin.auto: exit=%d stderr=%q, want the key named", r.exitCode, r.stderr)
	}
}

func envValue(env []string, name string) string {
	for _, e := range env {
		if strings.HasPrefix(e, name+"=") {
			return strings.TrimPrefix(e, name+"=")
		}
	}
	return ""
}
