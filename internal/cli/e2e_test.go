// End-to-end tests for the chassis: exit codes, error shape, and the
// manifest/install/uninstall/doctor loop, exercised through the actual
// built binary rather than in-process, so Cobra's own argument-parsing
// path is covered along with the exitcode/clasterr machinery (C2.7).
package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "clast-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(tmpDir, "clast")

	build := exec.Command("go", "build", "-o", binPath, "github.com/procrastivity/clast/cmd/clast")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building clast for e2e tests: %v\n%s", err, out)
		_ = os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

type result struct {
	stdout   string
	stderr   string
	exitCode int
}

// hermeticEnv is the process environment plus the caller's overrides, with
// every harness's skills-dir seam defaulted to its own per-test temp dir
// when the caller does not set it — the suite must never read this host's
// real skill installs (C2.7; wip found this live: a stamped tree from an
// older build failed doctor inside tests that never mentioned skills).
// XDG_CACHE_HOME joins this list with llm-verbs/step-05: `clast retro`
// writes its own fingerprinted summary cache under
// $XDG_CACHE_HOME/clast/retro/, and this suite must never read or write
// this host's own real cache tree either. XDG_CONFIG_HOME and TMPDIR join
// with the llm-verbs seal sweep (F6): config.Load reads
// $XDG_CONFIG_HOME/clast/config.yaml, and several tests in this file
// already set XDG_CONFIG_HOME explicitly (writeLLMConfig's own callers) —
// those explicit sets still win, since the loop below only defaults a
// name the caller's own env didn't already set; a test that forgets to
// set it now gets its own per-test temp dir instead of silently reading
// this host's real ~/.config/clast. TMPDIR is defensive: nothing in this
// tree currently shells out to a real $EDITOR against the host's temp dir
// (F2 removed the one path that did), but a stray future child process
// picking up the host's own TMPDIR is exactly the same class of leak.
// Add each new harness's seam var to this list when adding a target.
func hermeticEnv(t *testing.T, env []string) []string {
	t.Helper()
	out := append(os.Environ(), env...)
	for _, name := range []string{"CLAST_CLAUDE_SKILLS_DIR", "CLAST_JOURNAL_DIR", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "TMPDIR"} {
		set := false
		for _, e := range env {
			if strings.HasPrefix(e, name+"=") {
				set = true
				break
			}
		}
		if !set {
			out = append(out, name+"="+t.TempDir())
		}
	}
	return out
}

func run(t *testing.T, env []string, args ...string) result {
	t.Helper()
	return runIn(t, "", env, args...)
}

// runIn is run, with the child process's working directory set to dir (the
// registry verbs' e2e cases need to run inside a fixture git clone).
// An empty dir inherits this test process's own working directory, same as
// run.
func runIn(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv(t, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

// runWithStdin is run, with stdin fed from the given string — the state-verbs
// curate verb is the first to read stdin (SURFACE V14), so no prior e2e
// helper threads one through.
func runWithStdin(t *testing.T, env []string, stdin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = hermeticEnv(t, env)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

func TestUsageError_BadFlag(t *testing.T) {
	r := run(t, nil, "--bogus")
	if r.exitCode != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty on failure", r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "clast: ") {
		t.Fatalf("stderr = %q, want it to start with %q", r.stderr, "clast: ")
	}
}

func TestVersion_JSON(t *testing.T) {
	r := run(t, nil, "version", "--json")
	if r.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("version --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Version == "" {
		t.Fatalf("version --json reported an empty version")
	}
}

func TestManifest_JSON_DeclaresContractAndDigest(t *testing.T) {
	r := run(t, nil, "manifest", "--json")
	if r.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var m struct {
		SchemaVersion  int    `json:"schemaVersion"`
		Contract       string `json:"contract"`
		ManifestDigest string `json:"manifest_digest"`
		Verbs          []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("manifest --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if m.Contract == "" {
		t.Fatalf("manifest declares no contract version (C3.6)")
	}
	if !strings.HasPrefix(m.ManifestDigest, "sha256:") {
		t.Fatalf("manifest_digest = %q, want a sha256: prefix (C3.4)", m.ManifestDigest)
	}
	if len(m.Verbs) == 0 {
		t.Fatalf("manifest lists no verbs")
	}
	for _, v := range m.Verbs {
		if v.Kind == "" {
			t.Fatalf("verb %q carries no surface kind (C3.2)", v.Name)
		}
	}
}

// TestManifest_DigestCommitsToTheDocument checks C3.4's substance, not just
// its prefix: the emitted manifest_digest must be a sha256 over the exact
// bytes the caller received, with the digest field held empty. It does the
// blanking on the raw stdout rather than by re-encoding a decoded document,
// so it reproduces what an external consumer can do and stays independent
// of the producing code's own marshalling.
//
// The property is what makes the asset list load-bearing: without it, an
// edited shipped asset could leave the digest where it was.
func TestManifest_DigestCommitsToTheDocument(t *testing.T) {
	r := run(t, nil, "manifest", "--json")
	if r.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var m struct {
		ManifestDigest string `json:"manifest_digest"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("manifest --json stdout is not one JSON value: %v", err)
	}

	emitted := strings.TrimRight(r.stdout, "\n")
	blanked := strings.Replace(emitted, `"manifest_digest":"`+m.ManifestDigest+`"`, `"manifest_digest":""`, 1)
	if blanked == emitted {
		t.Fatalf("manifest_digest field not found verbatim in stdout; stdout=%q", emitted)
	}
	sum := sha256.Sum256([]byte(blanked))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if m.ManifestDigest != want {
		t.Fatalf("manifest_digest = %q, want %q — the digest does not commit to the document it rides in (C3.4)", m.ManifestDigest, want)
	}
}

// TestManifest_IsDeterministic pins the premise manifest_digest's usefulness
// as a comparable identity rests on: the same binary must emit the same
// document every time, so neither the verb walk nor the asset walk may vary
// with map or filesystem iteration order.
func TestManifest_IsDeterministic(t *testing.T) {
	first := run(t, nil, "manifest", "--json")
	if first.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", first.exitCode, first.stderr)
	}
	for i := range 2 {
		again := run(t, nil, "manifest", "--json")
		if again.stdout != first.stdout {
			t.Fatalf("run %d differs from the first; the manifest is not deterministic", i+2)
		}
	}
}

// TestInstallLoop drives the full install lifecycle against a hermetic
// skills dir: install writes a stamped tree, a re-install reports current,
// a hand-edit flips install to a refusal (exit 3) and doctor keeps
// advising, --force recovers, and uninstall removes exactly the tree.
func TestInstallLoop(t *testing.T) {
	skills := t.TempDir()
	env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}

	if r := run(t, env, "install", "claude-code"); r.exitCode != 0 {
		t.Fatalf("install: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	skillDir := filepath.Join(skills, "clast")
	for _, f := range []string{"SKILL.md", ".claude-plugin/plugin.json", ".clast-manifest-stamp.json"} {
		if _, err := os.Stat(filepath.Join(skillDir, f)); err != nil {
			t.Fatalf("after install, %s: %v", f, err)
		}
	}

	if r := run(t, env, "install", "claude-code", "--json"); r.exitCode != 0 || !strings.Contains(r.stdout, `"status":"current"`) {
		t.Fatalf("re-install: exit=%d stdout=%q, want status current", r.exitCode, r.stdout)
	}

	if r := run(t, env, "doctor"); r.exitCode != 0 {
		t.Fatalf("doctor on a current install: exit=%d stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}

	skillMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, env, "install", "claude-code"); r.exitCode != 3 {
		t.Fatalf("install over a hand-edited tree: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}
	if r := run(t, env, "install", "claude-code", "--force"); r.exitCode != 0 {
		t.Fatalf("install --force: exit=%d stderr=%q", r.exitCode, r.stderr)
	}

	if r := run(t, env, "uninstall", "claude-code"); r.exitCode != 0 {
		t.Fatalf("uninstall: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("after uninstall, %s still exists", skillDir)
	}
}

func TestUnknownHarness_JSONEnvelope(t *testing.T) {
	r := run(t, nil, "install", "no-such-harness", "--json")
	if r.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty on failure", r.stdout)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.stderr), &envelope); err != nil {
		t.Fatalf("stderr is not the {\"error\":...} envelope: %v; stderr=%q", err, r.stderr)
	}
	if envelope.Error.Code != "validation.unknown-harness" {
		t.Fatalf("error code = %q, want validation.unknown-harness", envelope.Error.Code)
	}
}

// errorEnvelope is the {"error":{"code","message"}} shape a structured
// error renders on stderr under --json (C2.5).
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseErrorEnvelope decodes stderr as the --json error envelope, failing
// the test if it is not one JSON value of that shape.
func parseErrorEnvelope(t *testing.T, stderr string) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("stderr is not the {\"error\":...} envelope: %v; stderr=%q", err, stderr)
	}
	return envelope
}

// installClean installs claude-code cleanly into skills and returns its
// install directory, the fixture every refusing-state setup below starts
// from.
func installClean(t *testing.T, skills string, env []string) string {
	t.Helper()
	if r := run(t, env, "install", "claude-code"); r.exitCode != 0 {
		t.Fatalf("install: exit=%d stderr=%q", r.exitCode, r.stderr)
	}
	return filepath.Join(skills, "clast")
}

// makeUnownedConflict puts a file at claude-code's install path with no
// stamp beside it — Status's UnownedConflict: content the tool never
// wrote (C4.5).
func makeUnownedConflict(t *testing.T, skills string, _ []string) string {
	t.Helper()
	skillDir := filepath.Join(skills, "clast")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "foreign.txt"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// makeModified installs cleanly, then hand-edits a generated file —
// Status's Modified: the disk no longer matches the stamp (C4.5).
func makeModified(t *testing.T, skills string, env []string) string {
	t.Helper()
	skillDir := installClean(t, skills, env)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// makeIncompatibleUnparseable installs cleanly, then corrupts the stamp's
// JSON — Status's Incompatible via manifest.ErrStampUnparseable (C4.5,
// §1.2).
func makeIncompatibleUnparseable(t *testing.T, skills string, env []string) string {
	t.Helper()
	skillDir := installClean(t, skills, env)
	stampPath := filepath.Join(skillDir, ".clast-manifest-stamp.json")
	if err := os.WriteFile(stampPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// makeIncompatibleSchemaVersion installs cleanly, then rewrites the
// stamp's schemaVersion to a value this binary does not recognize —
// Status's Incompatible via the schemaVersion mismatch (C4.5, §1.2).
func makeIncompatibleSchemaVersion(t *testing.T, skills string, env []string) string {
	t.Helper()
	skillDir := installClean(t, skills, env)
	stampPath := filepath.Join(skillDir, ".clast-manifest-stamp.json")
	raw, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	var stamp map[string]any
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	stamp["schemaVersion"] = 999
	out, err := json.Marshal(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// refusalCases is the fixture table both TestInstall_RefusalCodes and
// TestUninstall_RefusalCodes drive: one row per refusing state, each
// naming the code Status maps it to (C4.5 §1.4).
var refusalCases = []struct {
	name  string
	setup func(t *testing.T, skills string, env []string) string
	code  string
}{
	{"unowned_conflict", makeUnownedConflict, "refusal.unowned-harness-target"},
	{"modified", makeModified, "refusal.modified-harness-target"},
	{"incompatible unparseable stamp", makeIncompatibleUnparseable, "refusal.incompatible-harness-target"},
	{"incompatible schemaVersion", makeIncompatibleSchemaVersion, "refusal.incompatible-harness-target"},
}

// TestInstall_RefusalCodes asserts that install refuses each of the three
// unsafe states with its own code and exit 3, names --force in the
// message (naming what it would do to that state's content), and that
// --force still overwrites regardless of which state refused it.
func TestInstall_RefusalCodes(t *testing.T) {
	for _, c := range refusalCases {
		t.Run(c.name, func(t *testing.T) {
			skills := t.TempDir()
			env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
			c.setup(t, skills, env)

			r := run(t, env, "install", "claude-code", "--json")
			if r.exitCode != 3 {
				t.Fatalf("install: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
			}
			if r.stdout != "" {
				t.Fatalf("stdout = %q, want empty on failure", r.stdout)
			}
			envelope := parseErrorEnvelope(t, r.stderr)
			if envelope.Error.Code != c.code {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, c.code)
			}
			if !strings.Contains(envelope.Error.Message, "--force") {
				t.Fatalf("install refusal message = %q, want it to name --force", envelope.Error.Message)
			}

			if r := run(t, env, "install", "claude-code", "--force"); r.exitCode != 0 {
				t.Fatalf("install --force over %s: exit=%d stderr=%q", c.name, r.exitCode, r.stderr)
			}
		})
	}
}

// TestUninstall_RefusalCodes asserts that uninstall refuses each of the
// same three unsafe states with its own code and exit 3, and that — since
// uninstall has no --force — its message never names it.
func TestUninstall_RefusalCodes(t *testing.T) {
	for _, c := range refusalCases {
		t.Run(c.name, func(t *testing.T) {
			skills := t.TempDir()
			env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
			c.setup(t, skills, env)

			r := run(t, env, "uninstall", "claude-code", "--json")
			if r.exitCode != 3 {
				t.Fatalf("uninstall: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
			}
			if r.stdout != "" {
				t.Fatalf("stdout = %q, want empty on failure", r.stdout)
			}
			envelope := parseErrorEnvelope(t, r.stderr)
			if envelope.Error.Code != c.code {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, c.code)
			}
			if strings.Contains(envelope.Error.Message, "--force") {
				t.Fatalf("uninstall refusal message = %q, want it not to name --force (uninstall has none)", envelope.Error.Message)
			}
		})
	}
}

// TestUninstall_EmptyDirNoStamp_NotFound asserts that an existing but
// empty, unstamped install directory reads as Missing (C4.5 §1.4: it used
// to be a refusal) and so uninstall reports not-found, exit 1, not a
// refusal.
func TestUninstall_EmptyDirNoStamp_NotFound(t *testing.T) {
	skills := t.TempDir()
	env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
	skillDir := filepath.Join(skills, "clast")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, "uninstall", "claude-code", "--json")
	if r.exitCode != 1 {
		t.Fatalf("uninstall on an empty, unstamped directory: exit=%d, want 1 (not-found); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.harness-not-installed" {
		t.Fatalf("error code = %q, want not-found.harness-not-installed", envelope.Error.Code)
	}
}

// TestInstall_MalformedDescriptionOverride pins the error for a user
// override of the skill description that is empty or spans lines: a
// structured validation code with exit 1, not a plain error that the
// fallback path reports as usage (C2.4, C2.5).
func TestInstall_MalformedDescriptionOverride(t *testing.T) {
	for name, content := range map[string]string{
		"two lines": "first line\nsecond line\n",
		"empty":     "\n",
	} {
		t.Run(name, func(t *testing.T) {
			xdg := t.TempDir()
			override := filepath.Join(xdg, "clast", "templates", "skills", "claude-code", "description.txt")
			if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(override, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + t.TempDir(), "XDG_CONFIG_HOME=" + xdg}

			r := run(t, env, "install", "claude-code", "--json")
			if r.exitCode != 1 {
				t.Fatalf("exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
			}
			if code := parseErrorEnvelope(t, r.stderr).Error.Code; code != "validation.skill-description" {
				t.Fatalf("error code = %q, want validation.skill-description", code)
			}
		})
	}
}

// TestInstallAll_OneRefused drives the bare `install` (no harness name)
// path against a refused target. The skeleton's registry lists exactly
// one harness, so refusing it also refuses the whole run: each per-target
// result carries its own refusal code in the results JSON on stdout, and
// the closing summary error on stderr carries the shared
// refusal.harness-targets-refused code (C4.5, C4.6).
func TestInstallAll_OneRefused(t *testing.T) {
	skills := t.TempDir()
	env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
	makeUnownedConflict(t, skills, env)

	r := run(t, env, "install", "--json")
	if r.exitCode != 3 {
		t.Fatalf("bare install: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}

	var payload struct {
		Results []struct {
			Harness string `json:"harness"`
			Status  string `json:"status"`
			Error   *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	found := false
	for _, res := range payload.Results {
		if res.Harness != "claude-code" {
			continue
		}
		found = true
		if res.Status != "refused" {
			t.Fatalf("claude-code result status = %q, want refused", res.Status)
		}
		if res.Error == nil || res.Error.Code != "refusal.unowned-harness-target" {
			t.Fatalf("claude-code result error = %+v, want code refusal.unowned-harness-target", res.Error)
		}
	}
	if !found {
		t.Fatalf("no claude-code result in %+v", payload.Results)
	}

	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "refusal.harness-targets-refused" {
		t.Fatalf("error code = %q, want refusal.harness-targets-refused", envelope.Error.Code)
	}
}

// makeMissing is the doctor state fixture for Missing: nothing installed.
func makeMissing(t *testing.T, skills string, _ []string) string {
	t.Helper()
	return filepath.Join(skills, "clast")
}

// makeStale installs cleanly, then tampers with one generated file's disk
// content and its stamp entry together, to the same wrong value — disk
// still matches the stamp (no Modified), but the current binary's own
// output for that file (untouched) no longer matches the stamp: Status's
// Stale, the binary-vs-stamp question alone (C4.6). A plain hand-edit
// would also break disk-vs-stamp and read as Modified instead, which is
// why this rewrites the stamp entry too.
func makeStale(t *testing.T, skills string, env []string) string {
	t.Helper()
	skillDir := installClean(t, skills, env)

	staleContent := []byte("stale content\n")
	sum := sha256.Sum256(staleContent)
	staleHash := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), staleContent, 0o644); err != nil {
		t.Fatal(err)
	}

	stampPath := filepath.Join(skillDir, ".clast-manifest-stamp.json")
	raw, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	var stamp map[string]any
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	files, ok := stamp["files"].(map[string]any)
	if !ok {
		t.Fatalf("stamp %q has no files object: %v", stampPath, stamp)
	}
	files["SKILL.md"] = staleHash
	out, err := json.Marshal(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// TestDoctor_JSON_States drives doctor --json across all six drift states
// and asserts the reported target state, the finding codes, and the exit
// code: 1 only for Incompatible (the one state whose finding keeps the
// "refusal." prefix and so fails the run, C4.7), 0 for every other state.
func TestDoctor_JSON_States(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, skills string, env []string) string
		wantState string
		wantExit  int
		wantCodes []string
	}{
		{"missing", makeMissing, "missing", 0, nil},
		{"current", installClean, "current", 0, nil},
		{"stale", makeStale, "stale", 0, []string{"advisory.stale-harness-artifact"}},
		{"modified", makeModified, "modified", 0, []string{"advisory.modified-harness-target"}},
		{"unowned_conflict", makeUnownedConflict, "unowned_conflict", 0, []string{"advisory.unowned-harness-target"}},
		{"incompatible unparseable stamp", makeIncompatibleUnparseable, "incompatible", 1, []string{"refusal.incompatible-harness-target"}},
		{"incompatible schemaVersion", makeIncompatibleSchemaVersion, "incompatible", 1, []string{"refusal.incompatible-harness-target"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skills := t.TempDir()
			env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
			c.setup(t, skills, env)

			r := run(t, env, "doctor", "--json")
			if r.exitCode != c.wantExit {
				t.Fatalf("doctor --json: exit=%d, want %d; stdout=%q stderr=%q", r.exitCode, c.wantExit, r.stdout, r.stderr)
			}

			var payload struct {
				Findings []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"findings"`
				Targets []struct {
					Harness string `json:"harness"`
					Dir     string `json:"dir"`
					State   string `json:"state"`
				} `json:"targets"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
				t.Fatalf("doctor --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
			}
			if len(payload.Targets) != 1 || payload.Targets[0].Harness != "claude-code" {
				t.Fatalf("targets = %+v, want exactly one claude-code target", payload.Targets)
			}
			if payload.Targets[0].State != c.wantState {
				t.Fatalf("targets[0].state = %q, want %q", payload.Targets[0].State, c.wantState)
			}

			gotCodes := map[string]bool{}
			for _, f := range payload.Findings {
				gotCodes[f.Code] = true
			}
			for _, code := range c.wantCodes {
				if !gotCodes[code] {
					t.Errorf("findings = %+v, want a finding with code %q", payload.Findings, code)
				}
			}
			if len(c.wantCodes) == 0 && len(payload.Findings) != 0 {
				t.Errorf("findings = %+v, want none", payload.Findings)
			}

			if c.wantExit == 1 {
				envelope := parseErrorEnvelope(t, r.stderr)
				if envelope.Error.Code != "doctor.findings-present" {
					t.Errorf("error code = %q, want doctor.findings-present", envelope.Error.Code)
				}
			} else if r.stderr != "" {
				t.Errorf("stderr = %q, want empty on a passing doctor run", r.stderr)
			}
		})
	}
}

// TestDoctor_TextMode_StateLine asserts the text-mode "<harness>: <state>
// at <dir>" line appears ahead of the "no issues found" line.
func TestDoctor_TextMode_StateLine(t *testing.T) {
	skills := t.TempDir()
	env := []string{"CLAST_CLAUDE_SKILLS_DIR=" + skills}
	skillDir := filepath.Join(skills, "clast")

	r := run(t, env, "doctor")
	if r.exitCode != 0 {
		t.Fatalf("doctor: exit=%d, want 0; stdout=%q stderr=%q", r.exitCode, r.stdout, r.stderr)
	}
	wantLine := fmt.Sprintf("claude-code: missing at %s", skillDir)
	stateIdx := strings.Index(r.stdout, wantLine)
	if stateIdx < 0 {
		t.Fatalf("doctor stdout = %q, want it to contain %q", r.stdout, wantLine)
	}
	issuesIdx := strings.Index(r.stdout, "no issues found")
	if issuesIdx < 0 {
		t.Fatalf("doctor stdout = %q, want it to still say no issues found", r.stdout)
	}
	if stateIdx > issuesIdx {
		t.Fatalf("doctor stdout = %q, want the state line ahead of the findings/no-issues line", r.stdout)
	}
}

// --- registry: `clast init` (SURFACE V26) ---

// initGitRepo creates a fresh git repo (no remotes) under t's temp dir, at
// the given relative name, and returns its absolute path. Mirrors
// internal/registry's own testutil_test.go newRepo — no mocks for anything
// git-backed.
func initGitRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
		}
	}
	return dir
}

func addGitRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	cmd := exec.Command("git", "remote", "add", name, url)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add (in %s): %v\n%s", dir, err, out)
	}
}

// addWorktree adds a linked worktree of mainDir at worktreeDir, on a new
// branch, and returns worktreeDir. Mirrors internal/registry's own
// testutil_test.go addWorktree.
func addWorktree(t *testing.T, mainDir, worktreeDir, branch string) string {
	t.Helper()
	for _, args := range [][]string{
		{"commit", "--allow-empty", "-q", "-m", "init"},
		{"worktree", "add", "-q", "-b", branch, worktreeDir},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = mainDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v (in %s): %v\n%s", args, mainDir, err, out)
		}
	}
	return worktreeDir
}

type initPayload struct {
	Project struct {
		ID     string `json:"id"`
		Slug   string `json:"slug"`
		Remote string `json:"remote"`
	} `json:"project"`
	Clone struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"clone"`
	Status string `json:"status"`
}

// TestInit_JSON_HappyPath drives a fresh, remote-bearing clone through
// `clast init --json`: exit 0, status "created", the project keyed by the
// normalized remote and slugged from the repo directory's basename, and
// the clone labeled the same way.
func TestInit_JSON_HappyPath(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	r := runIn(t, dir, env, "init", "--json")
	if r.exitCode != 0 {
		t.Fatalf("init --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload initPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("init --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Status != "created" {
		t.Errorf("status = %q, want %q", payload.Status, "created")
	}
	if payload.Project.Slug != "widget" {
		t.Errorf("project.slug = %q, want %q", payload.Project.Slug, "widget")
	}
	if payload.Project.Remote != "github.com/acme/widget" {
		t.Errorf("project.remote = %q, want the normalized origin", payload.Project.Remote)
	}
	if payload.Clone.Label != "widget" {
		t.Errorf("clone.label = %q, want %q", payload.Clone.Label, "widget")
	}
	if payload.Project.ID == "" || payload.Clone.ID == "" {
		t.Errorf("project/clone id empty in payload %+v", payload)
	}

	if _, err := os.Stat(filepath.Join(journalDir, "projects", "widget", "project.json")); err != nil {
		t.Errorf("project.json not written: %v", err)
	}
}

// TestInit_Rerun_ReportsCurrent drives init twice against the same clone
// and journal: the second run reports status "current" (V26's seal
// condition) with exit 0 and the same project/clone identity as the first.
func TestInit_Rerun_ReportsCurrent(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	first := runIn(t, dir, env, "init", "--json")
	if first.exitCode != 0 {
		t.Fatalf("first init: exit=%d, want 0; stderr=%q", first.exitCode, first.stderr)
	}
	var firstPayload initPayload
	if err := json.Unmarshal([]byte(first.stdout), &firstPayload); err != nil {
		t.Fatalf("first init --json stdout: %v; stdout=%q", err, first.stdout)
	}

	second := runIn(t, dir, env, "init", "--json")
	if second.exitCode != 0 {
		t.Fatalf("second init: exit=%d, want 0; stderr=%q", second.exitCode, second.stderr)
	}
	var secondPayload initPayload
	if err := json.Unmarshal([]byte(second.stdout), &secondPayload); err != nil {
		t.Fatalf("second init --json stdout: %v; stdout=%q", err, second.stdout)
	}
	if secondPayload.Status != "current" {
		t.Errorf("second run status = %q, want %q", secondPayload.Status, "current")
	}
	if secondPayload.Clone.ID != firstPayload.Clone.ID || secondPayload.Project.ID != firstPayload.Project.ID {
		t.Errorf("second run resolved a different project/clone: %+v vs %+v", secondPayload, firstPayload)
	}

	// Human mode's current case prints exactly that state.
	third := runIn(t, dir, env, "init")
	if third.exitCode != 0 {
		t.Fatalf("third init (human mode): exit=%d, want 0; stderr=%q", third.exitCode, third.stderr)
	}
	if strings.TrimSpace(third.stdout) != "current" {
		t.Errorf("human-mode current stdout = %q, want exactly %q", third.stdout, "current")
	}
}

// TestInit_NoIdentityRemote_JSONEnvelope drives a refusal through the CLI:
// a clone with remotes configured, none named origin and no
// --identity-remote given, refuses with validation.no-identity-remote,
// exit 1, empty stdout, and the standard --json error envelope.
func TestInit_NoIdentityRemote_JSONEnvelope(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "upstream", "git@github.com:acme/widget.git")

	r := runIn(t, dir, env, "init", "--json")
	if r.exitCode != 1 {
		t.Fatalf("init --json: exit=%d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.no-identity-remote" {
		t.Fatalf("error code = %q, want validation.no-identity-remote", envelope.Error.Code)
	}
}

// --- plumbing: `clast plumbing whereami`/`projects`/`clones` (SURFACE V22/V23) ---

// registerClone runs `clast init` in dir against env's journal, failing the
// test on anything but a clean exit — the fixture step every plumbing test
// below starts from (registration itself is init's own Matter, already
// covered above; these tests take it as a given).
func registerClone(t *testing.T, dir string, env []string) {
	t.Helper()
	if r := runIn(t, dir, env, "init"); r.exitCode != 0 {
		t.Fatalf("init (fixture): exit=%d stderr=%q", r.exitCode, r.stderr)
	}
}

type whereamiPayload struct {
	Project struct {
		ID     string `json:"id"`
		Slug   string `json:"slug"`
		Remote string `json:"remote"`
	} `json:"project"`
	Clone struct {
		ID           string `json:"id"`
		Label        string `json:"label"`
		GitCommonDir string `json:"git_common_dir"`
	} `json:"clone"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Machine  string `json:"machine"`
}

// TestWhereami_JSON_HappyPath drives `plumbing whereami --json` from a
// registered clone's main worktree: worktree "" and a real branch name,
// alongside the project/clone identity init itself just registered.
func TestWhereami_JSON_HappyPath(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	registerClone(t, dir, env)

	r := runIn(t, dir, env, "plumbing", "whereami", "--json")
	if r.exitCode != 0 {
		t.Fatalf("whereami --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload whereamiPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("whereami --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Project.Slug != "widget" {
		t.Errorf("project.slug = %q, want %q", payload.Project.Slug, "widget")
	}
	if payload.Project.Remote != "github.com/acme/widget" {
		t.Errorf("project.remote = %q, want the normalized origin", payload.Project.Remote)
	}
	if payload.Clone.Label != "widget" {
		t.Errorf("clone.label = %q, want %q", payload.Clone.Label, "widget")
	}
	if payload.Clone.GitCommonDir == "" {
		t.Error("clone.git_common_dir is empty")
	}
	if payload.Worktree != "" {
		t.Errorf("worktree = %q, want \"\" (main worktree)", payload.Worktree)
	}
	if payload.Branch == "" {
		t.Error("branch is empty, want the repo's default branch name")
	}
	if payload.Machine == "" {
		t.Error("machine is empty")
	}
}

// TestWhereami_LinkedWorktree drives `plumbing whereami --json` from a
// linked worktree of a registered clone: it resolves the same owning
// clone, with worktree filled by the worktree's own name (M17).
func TestWhereami_LinkedWorktree(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	main := initGitRepo(t, "widget")
	addGitRemote(t, main, "origin", "git@github.com:acme/widget.git")
	registerClone(t, main, env)

	wt := addWorktree(t, main, main+"-feature", "feature")

	r := runIn(t, wt, env, "plumbing", "whereami", "--json")
	if r.exitCode != 0 {
		t.Fatalf("whereami --json (worktree): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload whereamiPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("whereami --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Worktree != filepath.Base(wt) {
		t.Errorf("worktree = %q, want %q", payload.Worktree, filepath.Base(wt))
	}
	if payload.Branch != "feature" {
		t.Errorf("branch = %q, want %q", payload.Branch, "feature")
	}
	if payload.Clone.Label != "widget" {
		t.Errorf("clone.label = %q, want the owning clone's label %q", payload.Clone.Label, "widget")
	}
}

// TestWhereami_Unregistered_RefusalEnvelope drives `plumbing whereami` from
// a git repo that was never registered: refusal.unknown-clone, exit 3,
// empty stdout, message naming `clast init`.
func TestWhereami_Unregistered_RefusalEnvelope(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")

	r := runIn(t, dir, env, "plumbing", "whereami", "--json")
	if r.exitCode != 3 {
		t.Fatalf("whereami --json: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "refusal.unknown-clone" {
		t.Fatalf("error code = %q, want refusal.unknown-clone", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, "clast init") {
		t.Errorf("message = %q, want it to name `clast init`", envelope.Error.Message)
	}
}

type projectsPayload struct {
	Projects []struct {
		Slug       string `json:"slug"`
		Remote     string `json:"remote"`
		CloneCount int    `json:"clone_count"`
	} `json:"projects"`
}

// TestPlumbingProjects_JSON_ListsAcrossTwoProjects registers two separate
// projects and confirms `plumbing projects --json` lists both, each with
// its own remote and a clone count of 1.
func TestPlumbingProjects_JSON_ListsAcrossTwoProjects(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	widget := initGitRepo(t, "widget")
	addGitRemote(t, widget, "origin", "git@github.com:acme/widget.git")
	registerClone(t, widget, env)

	gadget := initGitRepo(t, "gadget")
	addGitRemote(t, gadget, "origin", "git@github.com:acme/gadget.git")
	registerClone(t, gadget, env)

	r := runIn(t, widget, env, "plumbing", "projects", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing projects --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload projectsPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing projects --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Projects) != 2 {
		t.Fatalf("projects = %+v, want exactly 2", payload.Projects)
	}
	bySlug := map[string]int{}
	for _, p := range payload.Projects {
		bySlug[p.Slug] = p.CloneCount
		if p.Remote == "" {
			t.Errorf("project %q has empty remote, want the normalized origin", p.Slug)
		}
	}
	if bySlug["widget"] != 1 || bySlug["gadget"] != 1 {
		t.Errorf("clone counts by slug = %+v, want widget=1 gadget=1", bySlug)
	}
}

// TestPlumbingProjects_Human_NoProjects confirms the empty-registry case
// prints a plain line rather than an empty table.
func TestPlumbingProjects_Human_NoProjects(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "projects")
	if r.exitCode != 0 {
		t.Fatalf("plumbing projects: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "no projects registered" {
		t.Errorf("stdout = %q, want %q", r.stdout, "no projects registered")
	}
}

type clonesPayload struct {
	Clones []struct {
		ProjectSlug  string `json:"project_slug"`
		ID           string `json:"id"`
		Label        string `json:"label"`
		GitCommonDir string `json:"git_common_dir"`
		Machine      string `json:"machine"`
		Current      bool   `json:"current"`
	} `json:"clones"`
}

// TestPlumbingClones_WithArgument_ScopesToNamedProject confirms an
// explicit project locator (slug) scopes the listing to that project only,
// even when run from inside a different registered clone.
func TestPlumbingClones_WithArgument_ScopesToNamedProject(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	widget := initGitRepo(t, "widget")
	addGitRemote(t, widget, "origin", "git@github.com:acme/widget.git")
	registerClone(t, widget, env)

	gadget := initGitRepo(t, "gadget")
	addGitRemote(t, gadget, "origin", "git@github.com:acme/gadget.git")
	registerClone(t, gadget, env)

	r := runIn(t, gadget, env, "plumbing", "clones", "widget", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing clones widget --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload clonesPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing clones --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Clones) != 1 || payload.Clones[0].ProjectSlug != "widget" {
		t.Fatalf("clones = %+v, want exactly one, scoped to widget", payload.Clones)
	}
	// Run from inside gadget's clone, so widget's own clone must not be
	// marked current even though it is the only row.
	if payload.Clones[0].Current {
		t.Error("clones[0].current = true, want false (cwd is gadget's clone, not widget's)")
	}
}

// TestPlumbingClones_WithArgument_UnknownProject confirms an unresolvable
// project locator maps to validation.unknown-locator, exit 1 (not a
// refusal — nothing declined on principle, the argument just names
// nothing).
func TestPlumbingClones_WithArgument_UnknownProject(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "clones", "no-such-project", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing clones no-such-project --json: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.unknown-locator" {
		t.Fatalf("error code = %q, want validation.unknown-locator", envelope.Error.Code)
	}
}

// TestPlumbingClones_NoArgument_InsideClone_ScopesToCurrentProject drives
// `plumbing clones` with no argument from inside a registered clone: only
// that clone's own project's rows come back, and its own row is marked
// current.
func TestPlumbingClones_NoArgument_InsideClone_ScopesToCurrentProject(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	widget := initGitRepo(t, "widget")
	addGitRemote(t, widget, "origin", "git@github.com:acme/widget.git")
	registerClone(t, widget, env)

	gadget := initGitRepo(t, "gadget")
	addGitRemote(t, gadget, "origin", "git@github.com:acme/gadget.git")
	registerClone(t, gadget, env)

	r := runIn(t, widget, env, "plumbing", "clones", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing clones --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload clonesPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing clones --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Clones) != 1 || payload.Clones[0].ProjectSlug != "widget" {
		t.Fatalf("clones = %+v, want exactly one, scoped to widget", payload.Clones)
	}
	if !payload.Clones[0].Current {
		t.Error("clones[0].current = false, want true (cwd is this exact clone)")
	}
}

// TestPlumbingClones_NoArgument_OutsideAnyProject_ListsEverything drives
// `plumbing clones` with no argument from a directory that is not any
// registered clone (not even inside a git repository at all): every
// project's clones come back, none marked current.
func TestPlumbingClones_NoArgument_OutsideAnyProject_ListsEverything(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	widget := initGitRepo(t, "widget")
	addGitRemote(t, widget, "origin", "git@github.com:acme/widget.git")
	registerClone(t, widget, env)

	gadget := initGitRepo(t, "gadget")
	addGitRemote(t, gadget, "origin", "git@github.com:acme/gadget.git")
	registerClone(t, gadget, env)

	outside := t.TempDir()
	r := runIn(t, outside, env, "plumbing", "clones", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing clones --json (outside any project): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload clonesPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing clones --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Clones) != 2 {
		t.Fatalf("clones = %+v, want exactly 2 (every project)", payload.Clones)
	}
	for _, c := range payload.Clones {
		if c.Current {
			t.Errorf("clone %+v marked current, want none marked (cwd resolves to no clone at all)", c)
		}
	}
}

// TestPlumbingClones_Human_NoClones confirms the empty-registry case
// prints a plain line rather than an empty table.
func TestPlumbingClones_Human_NoClones(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "clones")
	if r.exitCode != 0 {
		t.Fatalf("plumbing clones: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "no clones registered" {
		t.Errorf("stdout = %q, want %q", r.stdout, "no clones registered")
	}
}

// TestPlumbingBare_PrintsHelp_ExitZero confirms `clast plumbing` (no
// subcommand) prints the namespace's own help and exits 0 (V2) — the same
// path `clast plumbing --help` takes, since Cobra gives an unrunnable
// command no help/no-args distinction.
func TestPlumbingBare_PrintsHelp_ExitZero(t *testing.T) {
	r := run(t, nil, "plumbing")
	if r.exitCode != 0 {
		t.Fatalf("plumbing (bare): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "clast plumbing") {
		t.Errorf("stdout = %q, want it to contain the namespace's own help", r.stdout)
	}
}

// --- porcelain: `clast breadcrumb <text>` (SURFACE V27, write side) ---

// TestBreadcrumb_RegisteredClone_ScopesToProject drives `clast breadcrumb
// <text>` from a registered clone: the crumb lands scoped to that
// project's slug, readable back via `plumbing breadcrumbs`.
func TestBreadcrumb_RegisteredClone_ScopesToProject(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	registerClone(t, dir, env)

	r := runIn(t, dir, env, "breadcrumb", "check migration before deploy", "--json")
	if r.exitCode != 0 {
		t.Fatalf("breadcrumb: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Slug *string `json:"slug"`
		Text string  `json:"text"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("breadcrumb --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Slug == nil || *payload.Slug != "widget" {
		t.Errorf("slug = %v, want %q", payload.Slug, "widget")
	}

	list := runIn(t, dir, env, "plumbing", "breadcrumbs", "--project", "widget", "--json")
	if list.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs --project widget: exit=%d, want 0; stderr=%q", list.exitCode, list.stderr)
	}
	if !strings.Contains(list.stdout, "check migration before deploy") {
		t.Errorf("plumbing breadcrumbs stdout = %q, want it to contain the crumb just written", list.stdout)
	}
}

// TestBreadcrumb_Global_WritesSlugNull drives `clast breadcrumb --global`
// from an unregistered directory: it succeeds (global bypasses clone
// resolution entirely) and writes slug: null.
func TestBreadcrumb_Global_WritesSlugNull(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	outside := t.TempDir()

	r := runIn(t, outside, env, "breadcrumb", "bump the cache version", "--global", "--json")
	if r.exitCode != 0 {
		t.Fatalf("breadcrumb --global: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Slug *string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("breadcrumb --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Slug != nil {
		t.Errorf("slug = %v, want nil (global)", payload.Slug)
	}
}

// TestBreadcrumb_UnregisteredCwd_RefusalEnvelope drives `clast
// breadcrumb` (no --global) from an unregistered git repo: refusal.
// unknown-clone, exit 3, naming both --global and `clast init`.
func TestBreadcrumb_UnregisteredCwd_RefusalEnvelope(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	dir := initGitRepo(t, "unregistered")

	r := runIn(t, dir, env, "breadcrumb", "a note", "--json")
	if r.exitCode != 3 {
		t.Fatalf("breadcrumb: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "refusal.unknown-clone" {
		t.Fatalf("error code = %q, want refusal.unknown-clone", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, "--global") || !strings.Contains(envelope.Error.Message, "clast init") {
		t.Errorf("message = %q, want it to name both --global and `clast init`", envelope.Error.Message)
	}
}

// --- plumbing: `clast plumbing sessions` (SURFACE V17) ---

// TestPlumbingSessions_Human_NoSessions confirms an empty journal prints a
// plain line rather than an empty table.
func TestPlumbingSessions_Human_NoSessions(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "sessions")
	if r.exitCode != 0 {
		t.Fatalf("plumbing sessions: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "no sessions" {
		t.Errorf("stdout = %q, want %q", r.stdout, "no sessions")
	}
}

// TestPlumbingSessions_JSON_FiltersAndSorts seeds a fixture journal
// directly (journaltest, sharing internal/journal's own write primitives)
// under CLAST_JOURNAL_DIR, then drives the real binary: --state curated
// narrows to the curated session, and the full session.json fact set
// rides alongside state/stale/title in the --json payload.
func TestPlumbingSessions_JSON_FiltersAndSorts(t *testing.T) {
	fx := journaltest.New(t)
	captured := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	curated := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	fx.Captured("2026-09-10", captured,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	)
	fx.Curated("2026-09-11", curated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "a curated session",
	)
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "sessions", "--state", "curated", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing sessions --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Sessions []struct {
			Harness   string `json:"harness"`
			SessionID string `json:"session_id"`
			State     string `json:"state"`
			Stale     bool   `json:"stale"`
			Title     string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing sessions --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want exactly 1 (curated only)", payload.Sessions)
	}
	got := payload.Sessions[0]
	if got.SessionID != curated.NativeID {
		t.Errorf("session_id = %q, want %q", got.SessionID, curated.NativeID)
	}
	if got.State != "curated" {
		t.Errorf("state = %q, want curated", got.State)
	}
	if got.Stale {
		t.Error("stale = true, want false")
	}
	if got.Title != "a curated session" {
		t.Errorf("title = %q, want %q", got.Title, "a curated session")
	}
}

// TestPlumbingSessions_UnknownHarness_JSONEnvelope confirms --harness
// validates against the source registry (V30).
func TestPlumbingSessions_UnknownHarness_JSONEnvelope(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "sessions", "--harness", "no-such-harness", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing sessions --harness no-such-harness: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.unknown-harness" {
		t.Fatalf("error code = %q, want validation.unknown-harness", envelope.Error.Code)
	}
}

// --- plumbing: `clast plumbing show <session>` (SURFACE V18) ---

// TestPlumbingShow_JSON_DefaultView seeds a curated fixture session and
// drives `plumbing show <locator> --json`: the {session, curation, stale,
// entry} shape, with curation.state and entry.title/body all present.
func TestPlumbingShow_JSON_DefaultView(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	fx.Curated("2026-09-11", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "a curated session",
	)
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "show", key.DirName(), "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing show --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Session struct {
			SessionID string `json:"session_id"`
		} `json:"session"`
		Curation struct {
			State string `json:"state"`
		} `json:"curation"`
		Stale bool `json:"stale"`
		Entry struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"entry"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing show --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Session.SessionID != key.NativeID {
		t.Errorf("session.session_id = %q, want %q", payload.Session.SessionID, key.NativeID)
	}
	if payload.Curation.State != "curated" {
		t.Errorf("curation.state = %q, want curated", payload.Curation.State)
	}
	if payload.Stale {
		t.Error("stale = true, want false")
	}
	if payload.Entry.Title != "a curated session" {
		t.Errorf("entry.title = %q, want %q", payload.Entry.Title, "a curated session")
	}
}

// TestPlumbingShow_Transcript_JSON drives `plumbing show <locator>
// --transcript --json`: the {turns} shape, rendered through the claude
// source's renderer over a fixture transcript copy.
func TestPlumbingShow_Transcript_JSON(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "with-transcript-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	).WithTranscript("2026-09-10", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello from the transcript"}}`+"\n"))
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "show", key.DirName(), "--transcript", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing show --transcript --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Turns []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing show --transcript --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Turns) != 1 || payload.Turns[0].Text != "hello from the transcript" {
		t.Fatalf("turns = %+v, want one turn with the rendered text", payload.Turns)
	}
}

// TestPlumbingShow_UnknownLocator_JSONEnvelope confirms not-found.session.
func TestPlumbingShow_UnknownLocator_JSONEnvelope(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "show", "claude-nonexistent", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing show claude-nonexistent: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.session" {
		t.Fatalf("error code = %q, want not-found.session", envelope.Error.Code)
	}
}

// TestPlumbingShow_UnknownTranscriptFormat_JSONEnvelope confirms
// validation.unknown-transcript-format when the session's own recorded
// format has no registered renderer on this build.
func TestPlumbingShow_UnknownTranscriptFormat_JSONEnvelope(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "codex", NativeID: "unrendered-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "nonexistent-format", Lines: 1, SHA256: "a"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	).WithTranscript("2026-09-10", key, []byte("whatever"))
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "show", key.DirName(), "--transcript", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing show --transcript: exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.unknown-transcript-format" {
		t.Fatalf("error code = %q, want validation.unknown-transcript-format", envelope.Error.Code)
	}
}

// --- plumbing: `clast plumbing breadcrumbs` (SURFACE V19, list side) ---

// TestPlumbingBreadcrumbs_JSON_ScopesByProjectAndGlobal seeds a fixture
// day with a project-scoped and a global crumb, then confirms --project
// and --global each narrow to the right one, and neither flag lists both.
func TestPlumbingBreadcrumbs_JSON_ScopesByProjectAndGlobal(t *testing.T) {
	fx := journaltest.New(t)
	slug := "clast"
	fx.Breadcrumb("framework", time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC), &slug, "project note")
	fx.Breadcrumb("framework", time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC), nil, "global note")
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	type payload struct {
		Breadcrumbs []struct {
			Slug *string `json:"slug"`
			Text string  `json:"text"`
		} `json:"breadcrumbs"`
	}

	r := run(t, env, "plumbing", "breadcrumbs", "--day", "2026-09-11", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var all payload
	if err := json.Unmarshal([]byte(r.stdout), &all); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(all.Breadcrumbs) != 2 {
		t.Fatalf("no-flag breadcrumbs = %+v, want 2 (every crumb in the bucket)", all.Breadcrumbs)
	}

	r = run(t, env, "plumbing", "breadcrumbs", "--day", "2026-09-11", "--project", "clast", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs --project: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var scoped payload
	if err := json.Unmarshal([]byte(r.stdout), &scoped); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(scoped.Breadcrumbs) != 1 || scoped.Breadcrumbs[0].Text != "project note" {
		t.Fatalf("--project clast breadcrumbs = %+v, want exactly the project-scoped crumb", scoped.Breadcrumbs)
	}

	r = run(t, env, "plumbing", "breadcrumbs", "--day", "2026-09-11", "--global", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs --global: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var global payload
	if err := json.Unmarshal([]byte(r.stdout), &global); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(global.Breadcrumbs) != 1 || global.Breadcrumbs[0].Text != "global note" {
		t.Fatalf("--global breadcrumbs = %+v, want exactly the global crumb", global.Breadcrumbs)
	}
}

// TestPlumbingBreadcrumbs_ProjectAndGlobalMutuallyExclusive confirms
// combining --project and --global is a usage error (exit 2), Cobra's own
// mutually-exclusive-flag-group machinery.
func TestPlumbingBreadcrumbs_ProjectAndGlobalMutuallyExclusive(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "breadcrumbs", "--project", "clast", "--global")
	if r.exitCode != 2 {
		t.Fatalf("plumbing breadcrumbs --project --global: exit=%d, want 2 (usage); stderr=%q", r.exitCode, r.stderr)
	}
}

// TestPlumbingBreadcrumbs_Human_NoCrumbs confirms the empty-bucket case
// prints a plain line rather than an empty table.
func TestPlumbingBreadcrumbs_Human_NoCrumbs(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "breadcrumbs")
	if r.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "no breadcrumbs" {
		t.Errorf("stdout = %q, want %q", r.stdout, "no breadcrumbs")
	}
}

// --- plumbing: `clast plumbing stats` (SURFACE V21) ---

// TestPlumbingStats_JSON_CountsFourAxes seeds a small fixture and confirms
// the four fixed axes come back correctly, defaulting to "all" (not the
// since config key).
func TestPlumbingStats_JSON_CountsFourAxes(t *testing.T) {
	fx := journaltest.New(t)
	a := journal.SessionKey{Harness: "claude", NativeID: "a"}
	b := journal.SessionKey{Harness: "codex", NativeID: "b"}
	fx.Captured("2026-09-10", a,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	)
	fx.Dismissed("2026-09-11", b,
		journal.TranscriptFingerprint{Format: "codex-jsonl", Lines: 1, SHA256: "b"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 0, 5, 0, time.UTC),
		"framework", "auto:no-op",
	)
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "stats", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing stats --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Total     int            `json:"total"`
		ByState   map[string]int `json:"by_state"`
		ByHarness map[string]int `json:"by_harness"`
		ByProject map[string]int `json:"by_project"`
		ByDay     map[string]int `json:"by_day"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing stats --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Total != 2 {
		t.Errorf("total = %d, want 2 (--since defaults to all)", payload.Total)
	}
	if payload.ByState["captured"] != 1 || payload.ByState["dismissed"] != 1 {
		t.Errorf("by_state = %+v, want captured=1 dismissed=1", payload.ByState)
	}
	if payload.ByHarness["claude"] != 1 || payload.ByHarness["codex"] != 1 {
		t.Errorf("by_harness = %+v, want claude=1 codex=1", payload.ByHarness)
	}
	if payload.ByProject["-"] != 2 {
		t.Errorf("by_project = %+v, want -=2 (neither session has a project)", payload.ByProject)
	}
	if payload.ByDay["2026-09-10"] != 1 || payload.ByDay["2026-09-11"] != 1 {
		t.Errorf("by_day = %+v, want one per day", payload.ByDay)
	}
}

// TestPlumbingStats_Human_EmptyJournal confirms the empty-journal case
// still prints a zero count rather than nothing.
func TestPlumbingStats_Human_EmptyJournal(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "stats")
	if r.exitCode != 0 {
		t.Fatalf("plumbing stats: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "sessions: 0" {
		t.Errorf("stdout = %q, want %q", r.stdout, "sessions: 0")
	}
}

// --- query-verbs seal sweep ---

// TestSeal_ManifestCarriesEveryNewVerb confirms `clast manifest --json`
// carries kind/usage/outputSchema (C3.2/C3.7/C3.8) for every verb
// query-verbs added, plus shape-documents' plumbing asset, wake, brief,
// and retro (joining the same table rather than a forked one): plumbing
// kind on every case, the Use line's positional recorded verbatim on
// show/breadcrumb/asset/brief/retro (C3.8), and OutputSchema filled
// exactly where V35 says a consumer exists (sessions, show, asset, wake,
// brief, retro) and left unfilled everywhere else (breadcrumbs, stats,
// breadcrumb — C3.7: never speculatively).
func TestSeal_ManifestCarriesEveryNewVerb(t *testing.T) {
	r := run(t, nil, "manifest", "--json")
	if r.exitCode != 0 {
		t.Fatalf("manifest --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var m struct {
		Verbs []struct {
			Name         string          `json:"name"`
			Kind         string          `json:"kind"`
			Usage        string          `json:"usage"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("manifest --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}

	cases := []struct {
		name       string
		wantKind   string
		wantUsage  string
		wantSchema bool
	}{
		{"plumbing sessions", "plumbing", "", true},
		{"plumbing show", "plumbing", "<session>", true},
		{"plumbing breadcrumbs", "plumbing", "", false},
		{"plumbing stats", "plumbing", "", false},
		{"breadcrumb", "plumbing", "<text>", false},
		{"plumbing asset", "plumbing", "<path>", true},
		{"plumbing wake", "plumbing", "", true},
		{"plumbing brief", "plumbing", "[<project>]", true},
		{"plumbing retro", "plumbing", "[<day>]", true},
		// The three top-level shape verbs (SURFACE V1/V9-V11, llm-verbs/
		// step-07 seal sweep): kind llm (not plumbing, C3.2's audience/
		// determinism axes cross here), unqualified names (top level, not
		// under the plumbing namespace), and — like every schema-less
		// plumbing query above — no outputSchema at all: C3.7's
		// no-speculative-schema posture applies to these three exactly the
		// way query-verbs' own manifest assertions established for
		// breadcrumbs/stats (no consumer parses wake/brief/retro's --json
		// today).
		{"wake", "llm", "[--auto]", false},
		{"brief", "llm", "[<project>]", false},
		{"retro", "llm", "[<day>]", false},
	}
	byName := map[string]struct {
		Name         string
		Kind         string
		Usage        string
		OutputSchema json.RawMessage
	}{}
	for _, v := range m.Verbs {
		byName[v.Name] = struct {
			Name         string
			Kind         string
			Usage        string
			OutputSchema json.RawMessage
		}{v.Name, v.Kind, v.Usage, v.OutputSchema}
	}

	for _, c := range cases {
		v, ok := byName[c.name]
		if !ok {
			t.Errorf("manifest carries no verb named %q", c.name)
			continue
		}
		if v.Kind != c.wantKind {
			t.Errorf("%s: kind = %q, want %q (C3.2)", c.name, v.Kind, c.wantKind)
		}
		if v.Usage != c.wantUsage {
			t.Errorf("%s: usage = %q, want %q (C3.8)", c.name, v.Usage, c.wantUsage)
		}
		gotSchema := len(v.OutputSchema) > 0
		if gotSchema != c.wantSchema {
			t.Errorf("%s: outputSchema present = %v, want %v (V35/C3.7)", c.name, gotSchema, c.wantSchema)
		}
		if gotSchema {
			var probe any
			if err := json.Unmarshal(v.OutputSchema, &probe); err != nil {
				t.Errorf("%s: outputSchema is not valid JSON: %v", c.name, err)
			}
		}
	}
}

// clastSessionProject is the frozen project every well-formed fixture
// session in this file's composed-filter tests carries, so "matches every
// axis but one" fixtures only have to spell out the one axis they differ
// on.
var clastSessionProject = journal.SessionProject{ID: "01P", Slug: "clast", Clone: "01C", Label: "dev", Path: "/x"}

// TestSeal_FiltersComposeAcrossEveryAxis seeds a fixture journal with
// sessions that differ on every axis `sessions` filters (state, stale,
// harness, project, machine, day) and drives one call combining all of
// them at once — the composition property step-02's shared Filter layer
// exists for, exercised end to end through the built binary rather than
// only unit-tested against internal/query directly.
//
// --since is deliberately left at "all" here: query.ResolveSince resolves
// any other value against the real time.Now() inside the built binary,
// which this suite has no seam to override (every other e2e case that
// touches --since uses "all" for the same reason). Proving --since itself
// composes with the rest of Filter without that wall-clock coupling is
// TestApply_FiltersComposeAcrossEveryNonDayAxis's job, in
// internal/query/filter_test.go, where cutoff and "now" are both
// caller-supplied.
func TestSeal_FiltersComposeAcrossEveryAxis(t *testing.T) {
	fx := journaltest.New(t)

	// target matches every axis the composed call below restricts on,
	// including --stale: V17/M7 says --stale composes with --state
	// curated rather than replacing it, so the one session meant to
	// survive every filter must itself be both curated AND stale.
	target := journal.SessionKey{Harness: "claude", NativeID: "target-01"}
	fx.CuratedStale("2026-09-11", target,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "target-grown"},
		journal.TranscriptStamp{Lines: 3, SHA256: "target-at-curation"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "the one that matches everything",
	).WithProject("2026-09-11", target, clastSessionProject).
		WithMachine("2026-09-11", target, "framework")

	// Differs on state (dismissed, not curated).
	wrongState := journal.SessionKey{Harness: "claude", NativeID: "wrong-state-01"}
	fx.Dismissed("2026-09-11", wrongState,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "ws"},
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 5, 0, time.UTC),
		"framework", "manual",
	).WithProject("2026-09-11", wrongState, clastSessionProject).
		WithMachine("2026-09-11", wrongState, "framework")

	// Differs on staleness alone (curated, but fresh) — the actual seal
	// gap this test closes: without this fixture, a --stale that silently
	// replaced --state curated instead of composing with it (V17/M7)
	// would go undetected, since it would still return exactly {target}.
	freshCurated := journal.SessionKey{Harness: "claude", NativeID: "fresh-curated-01"}
	fx.Curated("2026-09-11", freshCurated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "fresh"},
		time.Date(2026, 9, 11, 9, 20, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 20, 0, 0, time.UTC),
		"framework", "curated but not stale",
	).WithProject("2026-09-11", freshCurated, clastSessionProject).
		WithMachine("2026-09-11", freshCurated, "framework")

	// Differs on harness (codex, not claude).
	wrongHarness := journal.SessionKey{Harness: "codex", NativeID: "wrong-harness-01"}
	fx.Curated("2026-09-11", wrongHarness,
		journal.TranscriptFingerprint{Format: "codex-jsonl", Lines: 10, SHA256: "wh"},
		time.Date(2026, 9, 11, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 15, 0, 0, time.UTC),
		"framework", "wrong harness",
	).WithProject("2026-09-11", wrongHarness, clastSessionProject).
		WithMachine("2026-09-11", wrongHarness, "framework")

	// Differs on project alone.
	wrongProject := journal.SessionKey{Harness: "claude", NativeID: "wrong-project-01"}
	fx.CuratedStale("2026-09-11", wrongProject,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "wp-grown"},
		journal.TranscriptStamp{Lines: 3, SHA256: "wp-at-curation"},
		time.Date(2026, 9, 11, 9, 25, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 25, 0, 0, time.UTC),
		"framework", "wrong project",
	).WithProject("2026-09-11", wrongProject, journal.SessionProject{ID: "02P", Slug: "other", Clone: "02C", Label: "dev", Path: "/y"}).
		WithMachine("2026-09-11", wrongProject, "framework")

	// Differs on machine alone.
	wrongMachine := journal.SessionKey{Harness: "claude", NativeID: "wrong-machine-01"}
	fx.CuratedStale("2026-09-11", wrongMachine,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "wm-grown"},
		journal.TranscriptStamp{Lines: 3, SHA256: "wm-at-curation"},
		time.Date(2026, 9, 11, 9, 35, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 35, 0, 0, time.UTC),
		"framework", "wrong machine",
	).WithProject("2026-09-11", wrongMachine, clastSessionProject).
		WithMachine("2026-09-11", wrongMachine, "elsewhere")

	// Differs on day (09-10, not 09-11).
	wrongDay := journal.SessionKey{Harness: "claude", NativeID: "wrong-day-01"}
	fx.Curated("2026-09-10", wrongDay,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "wd"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
		"framework", "wrong day",
	).WithProject("2026-09-10", wrongDay, clastSessionProject).
		WithMachine("2026-09-10", wrongDay, "framework")

	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}
	r := run(t, env, "plumbing", "sessions",
		"--state", "curated",
		"--stale",
		"--harness", "claude",
		"--project", "clast",
		"--machine", "framework",
		"--day", "2026-09-11",
		"--since", "all",
		"--json",
	)
	if r.exitCode != 0 {
		t.Fatalf("plumbing sessions (composed filters): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].SessionID != target.NativeID {
		t.Fatalf("composed filter result = %+v, want exactly %q", payload.Sessions, target.NativeID)
	}
}

// TestSeal_StaleAloneFiltersCorrectly drives `--stale` with no other
// filter set, confirming it restricts to exactly the stale sessions
// (M7) rather than, say, being ignored absent --state (query.Filter's
// own TestApply_StaleAloneNeedsNoStateFilter unit-tests this at the
// query layer; this seals the same property through the built binary).
func TestSeal_StaleAloneFiltersCorrectly(t *testing.T) {
	fx := journaltest.New(t)

	stale := journal.SessionKey{Harness: "claude", NativeID: "stale-only-01"}
	fx.CuratedStale("2026-09-11", stale,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 3, SHA256: "at-curation"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "stale",
	)

	fresh := journal.SessionKey{Harness: "claude", NativeID: "fresh-only-01"}
	fx.Curated("2026-09-11", fresh,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "fresh"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "fresh",
	)

	captured := journal.SessionKey{Harness: "claude", NativeID: "captured-only-01"}
	fx.Captured("2026-09-11", captured,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "cap"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
	)

	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}
	r := run(t, env, "plumbing", "sessions", "--stale", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing sessions --stale: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].SessionID != stale.NativeID {
		t.Fatalf("--stale result = %+v, want exactly %q", payload.Sessions, stale.NativeID)
	}
}

// TestSeal_V34CodesEndToEnd drives one representative case per V34 code
// this Matter's verbs raise, confirming the code and the exit category
// (C2.4) together, end to end through the built binary. query-verbs' own
// step 09 seeded the first five cases (its own seal sweep); shape-documents'
// step 06 seal sweep adds the three codes this Matter's own verbs raise
// that weren't already on the table: not-found.asset (`plumbing asset`),
// and brief's own validation.not-a-git-repo/validation.unknown-locator
// pair (V8's "how V10 and V22 read together" finding).
func TestSeal_V34CodesEndToEnd(t *testing.T) {
	fx := journaltest.New(t)
	a := journal.SessionKey{Harness: "claude", NativeID: "ambiguous-a"}
	b := journal.SessionKey{Harness: "claude", NativeID: "ambiguous-b"}
	fx.Captured("2026-09-10", a, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	fx.Captured("2026-09-10", b, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "b"}, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	unrendered := journal.SessionKey{Harness: "codex", NativeID: "unrendered"}
	fx.Captured("2026-09-10", unrendered, journal.TranscriptFingerprint{Format: "nonexistent-format", Lines: 1, SHA256: "c"}, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)).
		WithTranscript("2026-09-10", unrendered, []byte("x"))
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	unregisteredDir := initGitRepo(t, "unregistered-seal")
	outsideGitDir := t.TempDir() // no .git anywhere above it

	// llm-verbs/step-07 seal sweep: this Matter's own V34 codes, end to
	// end — validation.llm-not-configured and validation.llm-api-key-missing
	// (internal/llm.NewClient's own two-check order) plus
	// brief.llm-request-failed/retro.llm-request-failed (the two shape
	// verbs whose flow has no "skip on failure" escape hatch, so a request
	// failure always surfaces as a real process error — unlike wake's own
	// wake.llm-request-failed/wake.prompt-unavailable, which flows/wake.md
	// §2's Auto mode section and RunInteractive's own disposition loop
	// both convert into a skip before it ever reaches command.go, so
	// neither wake code can appear as a process-level exit at all; that
	// pair stays unit-tested only, in wakeverb_test.go, by design).
	// Each needs a project with real material so the run reaches past the
	// empty-before-client gate.
	llmCodesJournal := t.TempDir()
	if err := journal.WriteProject(llmCodesJournal, "llmcodes", journal.Project{ID: "p-llmcodes", Slug: "llmcodes"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	llmCodesCutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	llmCodesDay := llmCodesCutoff.DayOf(time.Now())
	writeBriefEntry(t, llmCodesJournal, string(llmCodesDay),
		journal.SessionKey{Harness: "claude", NativeID: "llmcodes-01"}, noonOn(t, llmCodesDay),
		journal.SessionProject{ID: "p-llmcodes", Slug: "llmcodes", Clone: "c1", Label: "dev", Path: "/dev"},
		"an entry for the V34 codes sweep")

	xdgNoConfig := t.TempDir()    // no config.yaml at all
	xdgUnreachable := t.TempDir() // base_url/model set, no API key
	writeLLMConfig(t, xdgUnreachable, "http://127.0.0.1:1", "gpt-test")

	failingStub := llmtest.New(t, "unused")
	failingStub.Fail(500, "seal sweep: must fail so the request-failed code surfaces")
	xdgFailingStub := t.TempDir()
	writeLLMConfig(t, xdgFailingStub, failingStub.URL(), "gpt-test")

	cases := []struct {
		name     string
		args     []string
		env      []string
		dir      string
		wantCode string
		wantExit int
	}{
		{"not-found.session", []string{"plumbing", "show", "claude-nonexistent", "--json"}, env, "", "not-found.session", 1},
		{"validation.ambiguous-locator", []string{"plumbing", "show", "claude-ambiguous", "--json"}, env, "", "validation.ambiguous-locator", 1},
		{"validation.unknown-harness", []string{"plumbing", "sessions", "--harness", "no-such-harness", "--json"}, env, "", "validation.unknown-harness", 1},
		{"validation.unknown-transcript-format", []string{"plumbing", "show", unrendered.DirName(), "--transcript", "--json"}, env, "", "validation.unknown-transcript-format", 1},
		{"refusal.unknown-clone", []string{"breadcrumb", "a note", "--json"}, []string{"CLAST_JOURNAL_DIR=" + t.TempDir()}, unregisteredDir, "refusal.unknown-clone", 3},
		{"not-found.asset", []string{"plumbing", "asset", "no/such/asset.md", "--json"}, env, "", "not-found.asset", 1},
		{"validation.not-a-git-repo", []string{"plumbing", "brief", "--json"}, []string{"CLAST_JOURNAL_DIR=" + t.TempDir()}, outsideGitDir, "validation.not-a-git-repo", 1},
		{"validation.unknown-locator", []string{"plumbing", "brief", "no-such-project", "--json"}, []string{"CLAST_JOURNAL_DIR=" + t.TempDir()}, "", "validation.unknown-locator", 1},
		{
			"validation.llm-not-configured",
			[]string{"brief", "llmcodes", "--json"},
			[]string{"CLAST_JOURNAL_DIR=" + llmCodesJournal, "XDG_CONFIG_HOME=" + xdgNoConfig, "CLAST_LLM_API_KEY=sk-test-key"},
			"", "validation.llm-not-configured", 1,
		},
		{
			"validation.llm-api-key-missing",
			[]string{"brief", "llmcodes", "--json"},
			[]string{"CLAST_JOURNAL_DIR=" + llmCodesJournal, "XDG_CONFIG_HOME=" + xdgUnreachable, "CLAST_LLM_API_KEY="},
			"", "validation.llm-api-key-missing", 1,
		},
		{
			"brief.llm-request-failed",
			[]string{"brief", "llmcodes", "--json"},
			[]string{"CLAST_JOURNAL_DIR=" + llmCodesJournal, "XDG_CONFIG_HOME=" + xdgFailingStub, "CLAST_LLM_API_KEY=sk-test-key"},
			"", "brief.llm-request-failed", 1,
		},
		{
			"retro.llm-request-failed",
			[]string{"retro", string(llmCodesDay), "--json"},
			[]string{"CLAST_JOURNAL_DIR=" + llmCodesJournal, "XDG_CONFIG_HOME=" + xdgFailingStub, "CLAST_LLM_API_KEY=sk-test-key"},
			"", "retro.llm-request-failed", 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runIn(t, c.dir, c.env, c.args...)
			if r.exitCode != c.wantExit {
				t.Fatalf("exit=%d, want %d; stderr=%q", r.exitCode, c.wantExit, r.stderr)
			}
			envelope := parseErrorEnvelope(t, r.stderr)
			if envelope.Error.Code != c.wantCode {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, c.wantCode)
			}
		})
	}
}

// TestRootHelp_DoesNotListPlumbingVerbs confirms bare `clast --help` lists
// the `plumbing` namespace entry itself but none of the verbs registered
// under it (V2: plumbing verbs are never porcelain).
func TestRootHelp_DoesNotListPlumbingVerbs(t *testing.T) {
	r := run(t, nil, "--help")
	if r.exitCode != 0 {
		t.Fatalf("--help: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "plumbing") {
		t.Errorf("stdout = %q, want it to list the plumbing namespace entry", r.stdout)
	}
	for _, verb := range []string{"whereami", "projects", "clones"} {
		// "sessions" is deliberately not checked here: the root command's own
		// Short description ("capture agent sessions...") already contains
		// the word, which would make this a false positive rather than a
		// real signal about the plumbing verb.
		if strings.Contains(r.stdout, verb) {
			t.Errorf("stdout = %q, want it NOT to list plumbing verb %q among the porcelain", r.stdout, verb)
		}
	}
}

// --- plumbing: `clast plumbing curate`/`dismiss`/`undismiss` (SURFACE V14/V15) ---

// stateVerbsEnv seeds a fixture journal via journaltest (H4: authored
// fresh, never from the bash implementation or live data) and returns the
// CLAST_JOURNAL_DIR env line pointing the real binary at it, alongside the
// fixture itself for further authoring.
func stateVerbsFixture(t *testing.T) (*journaltest.Fixture, []string) {
	t.Helper()
	fx := journaltest.New(t)
	return fx, []string{"CLAST_JOURNAL_DIR=" + fx.Root()}
}

// wellFormedEntryDoc is a minimal, valid entry.md document (V14: frontmatter
// parses, title present).
func wellFormedEntryDoc(title string) string {
	return "---\ntitle: " + title + "\ntags: [a, b]\n---\n\nsome body text\n"
}

func writeEntryFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entry.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type curateResultPayload struct {
	Harness           string `json:"harness"`
	SessionID         string `json:"session_id"`
	Shard             string `json:"shard"`
	PriorState        string `json:"prior_state"`
	ReplacedDismissal bool   `json:"replaced_dismissal"`
}

// TestPlumbingCurate_JSON_FromFile_HappyPath drives curate over a plain
// captured session via --file: exit 0, the --json payload names the
// session and its prior state, entry.md lands on disk, and curation.json
// reads curated with the transcript fingerprint stamped.
func TestPlumbingCurate_JSON_FromFile_HappyPath(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "abc"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	path := writeEntryFile(t, wellFormedEntryDoc("fixing the flaky test"))
	r := run(t, env, "plumbing", "curate", "claude-8f3a", "--file", path, "--json")
	if r.exitCode != 0 {
		t.Fatalf("curate --file --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload curateResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("curate --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Harness != "claude" || payload.SessionID != "8f3a" {
		t.Errorf("payload = %+v, want harness=claude session_id=8f3a", payload)
	}
	if payload.PriorState != "captured" {
		t.Errorf("prior_state = %q, want %q", payload.PriorState, "captured")
	}
	if payload.ReplacedDismissal {
		t.Error("replaced_dismissal = true, want false")
	}

	root := fx.Root()
	got, err := os.ReadFile(journal.EntryPath(root, "2026-09-11", key))
	if err != nil {
		t.Fatalf("reading entry.md: %v", err)
	}
	if string(got) != wellFormedEntryDoc("fixing the flaky test") {
		t.Errorf("entry.md = %q, want the submitted document verbatim", got)
	}
	curation, ok, err := journal.ReadCuration(root, "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.State != journal.StateCurated {
		t.Errorf("curation.State = %q, want %q", curation.State, journal.StateCurated)
	}
	if curation.TranscriptAtCuration == nil || curation.TranscriptAtCuration.Lines != 10 {
		t.Errorf("curation.TranscriptAtCuration = %+v, want Lines=10", curation.TranscriptAtCuration)
	}
}

// TestPlumbingCurate_FromStdin_HappyPath drives curate with no --file: the
// document comes from stdin.
func TestPlumbingCurate_FromStdin_HappyPath(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := runWithStdin(t, env, wellFormedEntryDoc("from stdin"), "plumbing", "curate", "claude-8f3a", "--json")
	if r.exitCode != 0 {
		t.Fatalf("curate (stdin) --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	got, err := os.ReadFile(journal.EntryPath(fx.Root(), "2026-09-11", key))
	if err != nil {
		t.Fatalf("reading entry.md: %v", err)
	}
	if string(got) != wellFormedEntryDoc("from stdin") {
		t.Errorf("entry.md = %q, want the stdin document verbatim", got)
	}
}

// TestPlumbingCurate_FromDismissed_ReplacesDismissal_NoUndismissCeremony
// drives curate over a dismissed session: the dismissal is replaced
// outright by a curated curation.json in one call (V14; the matter's own
// seal condition names this edge explicitly).
func TestPlumbingCurate_FromDismissed_ReplacesDismissal_NoUndismissCeremony(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"laptop", "not useful")

	path := writeEntryFile(t, wellFormedEntryDoc("revived"))
	r := run(t, env, "plumbing", "curate", "claude-8f3a", "--file", path, "--json")
	if r.exitCode != 0 {
		t.Fatalf("curate --file --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload curateResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("curate --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.PriorState != "dismissed" || !payload.ReplacedDismissal {
		t.Errorf("payload = %+v, want prior_state=dismissed replaced_dismissal=true", payload)
	}

	curation, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key)
	if err != nil || !ok || curation.State != journal.StateCurated {
		t.Fatalf("curation after curate-from-dismissed: state=%q ok=%v err=%v, want curated", curation.State, ok, err)
	}
	if curation.Reason != nil {
		t.Errorf("curation.Reason = %v, want nil (the dismissal reason must not survive)", curation.Reason)
	}
}

// TestPlumbingCurate_FromCurated_ReCuratesAndClearsStaleness drives curate
// over an already-curated, stale session: the fresh curation.json's
// fingerprint matches the CURRENT transcript, so the session reads
// not-stale immediately afterward (M7) — the other legal transition V14
// draws besides curate-from-dismissed.
func TestPlumbingCurate_FromCurated_ReCuratesAndClearsStaleness(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	current := journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "new"}
	stale := journal.TranscriptStamp{Lines: 10, SHA256: "old"}
	fx.CuratedStale("2026-09-11", key, current, stale,
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"laptop", "old title")

	path := writeEntryFile(t, wellFormedEntryDoc("refreshed"))
	r := run(t, env, "plumbing", "curate", "claude-8f3a", "--file", path, "--json")
	if r.exitCode != 0 {
		t.Fatalf("curate --file --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload curateResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("curate --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.PriorState != "curated" {
		t.Errorf("prior_state = %q, want %q", payload.PriorState, "curated")
	}

	curation, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key)
	if err != nil || !ok {
		t.Fatalf("ReadCuration: ok=%v err=%v", ok, err)
	}
	if curation.TranscriptAtCuration == nil || curation.TranscriptAtCuration.Lines != 20 || curation.TranscriptAtCuration.SHA256 != "new" {
		t.Errorf("curation.TranscriptAtCuration = %+v, want the refreshed current fingerprint {20 new}", curation.TranscriptAtCuration)
	}
}

// TestPlumbingCurate_InvalidFrontmatter_JSONEnvelope drives curate with a
// document whose frontmatter doesn't parse: validation.entry-frontmatter,
// exit 1, empty stdout.
func TestPlumbingCurate_InvalidFrontmatter_JSONEnvelope(t *testing.T) {
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
		journal.TranscriptFingerprint{Lines: 1, SHA256: "x"}, time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := runWithStdin(t, env, "not an entry at all", "plumbing", "curate", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("curate (invalid frontmatter): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.entry-frontmatter" {
		t.Errorf("error code = %q, want validation.entry-frontmatter", envelope.Error.Code)
	}
}

// TestPlumbingCurate_MissingTitle_JSONEnvelope drives curate with a
// document whose frontmatter parses but carries no title:
// validation.entry-title, exit 1.
func TestPlumbingCurate_MissingTitle_JSONEnvelope(t *testing.T) {
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", journal.SessionKey{Harness: "claude", NativeID: "8f3a"},
		journal.TranscriptFingerprint{Lines: 1, SHA256: "x"}, time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := runWithStdin(t, env, "---\ntags: []\n---\n\nbody\n", "plumbing", "curate", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("curate (missing title): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.entry-title" {
		t.Errorf("error code = %q, want validation.entry-title", envelope.Error.Code)
	}
}

// TestPlumbingCurate_UnreadableFile_NotFoundEntryFile drives curate with a
// --file naming a path that doesn't exist: not-found.entry-file, exit 1.
func TestPlumbingCurate_UnreadableFile_NotFoundEntryFile(t *testing.T) {
	_, env := stateVerbsFixture(t)
	missing := filepath.Join(t.TempDir(), "never-written.md")
	r := run(t, env, "plumbing", "curate", "claude-8f3a", "--file", missing, "--json")
	if r.exitCode != 1 {
		t.Fatalf("curate --file (missing): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.entry-file" {
		t.Errorf("error code = %q, want not-found.entry-file", envelope.Error.Code)
	}
}

// TestPlumbingCurate_UnknownLocator_NotFoundSession drives curate against a
// locator matching no session: not-found.session, exit 1.
func TestPlumbingCurate_UnknownLocator_NotFoundSession(t *testing.T) {
	_, env := stateVerbsFixture(t)
	r := runWithStdin(t, env, wellFormedEntryDoc("x"), "plumbing", "curate", "claude-nope", "--json")
	if r.exitCode != 1 {
		t.Fatalf("curate (unknown locator): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.session" {
		t.Errorf("error code = %q, want not-found.session", envelope.Error.Code)
	}
}

type dismissResultPayload struct {
	Harness   string `json:"harness"`
	SessionID string `json:"session_id"`
	Shard     string `json:"shard"`
	Reason    string `json:"reason"`
	Redismiss bool   `json:"redismiss"`
}

// TestPlumbingDismiss_JSON_DefaultReason_HappyPath drives dismiss with no
// --reason: the default "manual" is recorded.
func TestPlumbingDismiss_JSON_DefaultReason_HappyPath(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := run(t, env, "plumbing", "dismiss", "claude-8f3a", "--json")
	if r.exitCode != 0 {
		t.Fatalf("dismiss --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload dismissResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("dismiss --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.Reason != "manual" {
		t.Errorf("reason = %q, want %q", payload.Reason, "manual")
	}
	if payload.Redismiss {
		t.Error("redismiss = true, want false")
	}

	curation, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key)
	if err != nil || !ok || curation.State != journal.StateDismissed {
		t.Fatalf("curation after dismiss: state=%q ok=%v err=%v, want dismissed", curation.State, ok, err)
	}
}

// TestPlumbingDismiss_Redismiss_ReplacesReason drives dismiss twice with
// different reasons: the second call succeeds and replaces the stored
// reason (settled by planning finding).
func TestPlumbingDismiss_Redismiss_ReplacesReason(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"laptop", "first reason")

	r := run(t, env, "plumbing", "dismiss", "claude-8f3a", "--reason", "second reason", "--json")
	if r.exitCode != 0 {
		t.Fatalf("dismiss --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload dismissResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("dismiss --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if !payload.Redismiss {
		t.Error("redismiss = false, want true")
	}
	curation, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key)
	if err != nil || !ok || curation.Reason == nil || *curation.Reason != "second reason" {
		t.Fatalf("curation after re-dismiss = %+v ok=%v err=%v, want reason \"second reason\"", curation, ok, err)
	}
}

// TestPlumbingDismiss_RefusesCurated_JSONEnvelope drives dismiss against a
// curated session: validation.curated, exit 1.
func TestPlumbingDismiss_RefusesCurated_JSONEnvelope(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Curated("2026-09-11", key, journal.TranscriptFingerprint{Lines: 5, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"laptop", "a title")

	r := run(t, env, "plumbing", "dismiss", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("dismiss (curated): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.curated" {
		t.Errorf("error code = %q, want validation.curated", envelope.Error.Code)
	}
}

// TestPlumbingDismiss_RefusesReservedReason_JSONEnvelope drives dismiss
// with the capture-reserved auto:no-op reason: validation.reserved-reason,
// exit 1.
func TestPlumbingDismiss_RefusesReservedReason_JSONEnvelope(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := run(t, env, "plumbing", "dismiss", "claude-8f3a", "--reason", "auto:no-op", "--json")
	if r.exitCode != 1 {
		t.Fatalf("dismiss (reserved reason): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.reserved-reason" {
		t.Errorf("error code = %q, want validation.reserved-reason", envelope.Error.Code)
	}
}

type undismissResultPayload struct {
	Harness   string `json:"harness"`
	SessionID string `json:"session_id"`
	Shard     string `json:"shard"`
}

// TestPlumbingUndismiss_JSON_HappyPath drives undismiss over a dismissed
// session: curation.json is removed, returning it to captured.
func TestPlumbingUndismiss_JSON_HappyPath(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"laptop", "not useful")

	r := run(t, env, "plumbing", "undismiss", "claude-8f3a", "--json")
	if r.exitCode != 0 {
		t.Fatalf("undismiss --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload undismissResultPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("undismiss --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.SessionID != "8f3a" {
		t.Errorf("session_id = %q, want %q", payload.SessionID, "8f3a")
	}
	if _, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key); err != nil || ok {
		t.Errorf("ReadCuration after undismiss: ok=%v err=%v, want ok=false (captured)", ok, err)
	}
}

// TestPlumbingUndismiss_AcceptsAutoNoOpDismissal drives undismiss over a
// session dismissed with capture's reserved auto:no-op reason: V15's only
// precondition is state dismissed, so this succeeds.
func TestPlumbingUndismiss_AcceptsAutoNoOpDismissal(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "noop1"}
	fx, env := stateVerbsFixture(t)
	fx.Dismissed("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"framework", "auto:no-op")

	r := run(t, env, "plumbing", "undismiss", "claude-noop1", "--json")
	if r.exitCode != 0 {
		t.Fatalf("undismiss (auto:no-op): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
}

// TestPlumbingUndismiss_RefusesNotDismissed_JSONEnvelope drives undismiss
// against a captured (never dismissed) session: validation.not-dismissed,
// exit 1.
func TestPlumbingUndismiss_RefusesNotDismissed_JSONEnvelope(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Lines: 1, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC))

	r := run(t, env, "plumbing", "undismiss", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("undismiss (not dismissed): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.not-dismissed" {
		t.Errorf("error code = %q, want validation.not-dismissed", envelope.Error.Code)
	}
}

// TestPlumbingUndismiss_RefusesCurated_JSONEnvelope drives undismiss
// against a curated session — a second, distinct not-dismissed origin
// state from the captured case above, since a curated session is also
// never dismissed: validation.not-dismissed, exit 1, curation left
// untouched.
func TestPlumbingUndismiss_RefusesCurated_JSONEnvelope(t *testing.T) {
	key := journal.SessionKey{Harness: "claude", NativeID: "8f3a"}
	fx, env := stateVerbsFixture(t)
	fx.Curated("2026-09-11", key, journal.TranscriptFingerprint{Lines: 5, SHA256: "x"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC),
		"laptop", "a title")

	r := run(t, env, "plumbing", "undismiss", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("undismiss (curated): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.not-dismissed" {
		t.Errorf("error code = %q, want validation.not-dismissed", envelope.Error.Code)
	}
	curation, ok, err := journal.ReadCuration(fx.Root(), "2026-09-11", key)
	if err != nil || !ok || curation.State != journal.StateCurated {
		t.Errorf("curation after refused undismiss: state=%q ok=%v err=%v, want unchanged curated", curation.State, ok, err)
	}
}

// TestPlumbingStateVerbs_Manifest_UsageDeclaresSessionArg confirms curate,
// dismiss, and undismiss each declare the C3.8 "<session>" positional
// usage in the manifest, carry the plumbing surface kind, and leave
// OutputSchema unfilled (V35: none of the three is among the filled-schema
// verbs).
func TestPlumbingStateVerbs_Manifest_UsageDeclaresSessionArg(t *testing.T) {
	r := run(t, nil, "manifest", "--json")
	if r.exitCode != 0 {
		t.Fatalf("manifest --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var m struct {
		Verbs []struct {
			Name         string          `json:"name"`
			Kind         string          `json:"kind"`
			Usage        string          `json:"usage"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("manifest --json stdout: %v; stdout=%q", err, r.stdout)
	}
	want := map[string]bool{"plumbing curate": true, "plumbing dismiss": true, "plumbing undismiss": true}
	found := map[string]bool{}
	for _, v := range m.Verbs {
		if !want[v.Name] {
			continue
		}
		found[v.Name] = true
		if v.Kind != "plumbing" {
			t.Errorf("%s: kind = %q, want plumbing", v.Name, v.Kind)
		}
		if v.Usage != "<session>" {
			t.Errorf("%s: usage = %q, want %q (C3.8)", v.Name, v.Usage, "<session>")
		}
		if len(v.OutputSchema) != 0 {
			t.Errorf("%s: outputSchema = %s, want unfilled (V35/C3.7)", v.Name, v.OutputSchema)
		}
	}
	for name := range want {
		if !found[name] {
			t.Errorf("manifest verbs = %+v, missing %q", m.Verbs, name)
		}
	}
}

// TestPlumbingCurate_BadJournalDirConfig_TranslatedToClasterr drives curate
// against a config.yaml whose journal_dir is not a string: the command
// layer wraps journal.Root's plain error as validation.config rather than
// letting it fall through to the chassis's untranslated-error path (exit
// 2, no --json envelope) — the gap this matter's finding calls out and
// explicitly works around at its own three command layers.
func TestPlumbingCurate_BadJournalDirConfig_TranslatedToClasterr(t *testing.T) {
	xdg := t.TempDir()
	configDir := filepath.Join(xdg, "clast")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("journal_dir: 123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An explicit, empty CLAST_JOURNAL_DIR satisfies hermeticEnv's "already
	// set" check (it would otherwise default the seam to a temp dir, which
	// — being a non-empty override — wins over config's journal_dir before
	// journal.Root ever looks at it) while itself being journal.Root's
	// "no override" value, so config.yaml's malformed journal_dir is what
	// actually gets read.
	env := []string{"XDG_CONFIG_HOME=" + xdg, "CLAST_JOURNAL_DIR="}

	r := runWithStdin(t, env, wellFormedEntryDoc("x"), "plumbing", "curate", "claude-8f3a", "--json")
	if r.exitCode != 1 {
		t.Fatalf("curate (bad journal_dir config): exit=%d, want 1 (validation, not a bare usage error); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.config" {
		t.Errorf("error code = %q, want validation.config", envelope.Error.Code)
	}
}

// --- plumbing: `clast plumbing asset <path>` (SURFACE V25, shape-documents) ---

// embeddedAssetContent reads assets/agent-guidance.md straight off disk, the
// oracle every embedded-link assertion below compares the CLI's output
// against — that file ships with the tree and is always embedded (M18's
// last-resort link), regardless of what the test's own $XDG_CONFIG_HOME or
// installed-share-tree fixtures otherwise put in front of it.
func embeddedAssetContent(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "agent-guidance.md"))
	if err != nil {
		t.Fatalf("reading assets/agent-guidance.md fixture: %v", err)
	}
	return data
}

// installedLayout copies the already-built e2e binary into <prefix>/bin/clast
// and returns prefix: internal/asset.DefaultDir resolves the shipped-default
// directory purely from the running executable's own path
// (<prefix>/bin/clast -> <prefix>/share/clast/assets), so putting a copy of
// the binary at that exact relative layout is the one seam an e2e test can
// drive to make the binary's own DefaultDir() resolve into a share tree this
// test controls — no source rebuild needed, a copy of the binary suffices.
func installedLayout(t *testing.T) string {
	t.Helper()
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(binPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(filepath.Join(binDir, "clast"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	return prefix
}

// runInstalled is run, against the binary copy installedLayout placed at
// prefix/bin/clast rather than the suite's shared binPath.
func runInstalled(t *testing.T, prefix string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(filepath.Join(prefix, "bin", "clast"), args...)
	cmd.Env = hermeticEnv(t, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

type assetPayload struct {
	Path         string `json:"path"`
	Link         string `json:"link"`
	ResolvedFrom string `json:"resolved_from"`
	SHA256       string `json:"sha256"`
	Content      string `json:"content"`
}

// TestPlumbingAsset_JSON_EmbeddedLink drives a path present only in the
// embedded fallback (no override, no installed share tree in this process's
// own layout): link "embedded", resolved_from the "embedded" sentinel (V25),
// and content/sha256 matching the file shipped on disk.
func TestPlumbingAsset_JSON_EmbeddedLink(t *testing.T) {
	xdg := t.TempDir() // empty: no override present
	env := []string{"XDG_CONFIG_HOME=" + xdg}

	r := run(t, env, "plumbing", "asset", "agent-guidance.md", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload assetPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing asset --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Path != "agent-guidance.md" {
		t.Errorf("path = %q, want %q", payload.Path, "agent-guidance.md")
	}
	if payload.Link != "embedded" {
		t.Errorf("link = %q, want %q", payload.Link, "embedded")
	}
	if payload.ResolvedFrom != "embedded" {
		t.Errorf("resolved_from = %q, want %q (V25)", payload.ResolvedFrom, "embedded")
	}
	want := embeddedAssetContent(t)
	if payload.Content != string(want) {
		t.Errorf("content mismatch: got %d bytes, want the shipped file's %d bytes", len(payload.Content), len(want))
	}
	wantSum := sha256.Sum256(want)
	if payload.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("sha256 = %q, want the digest of the shipped file", payload.SHA256)
	}
}

// TestPlumbingAsset_HumanMode_PrintsResolvedBytesOnly asserts human mode's
// byte promise (V25): stdout is exactly the resolved content, nothing else.
func TestPlumbingAsset_HumanMode_PrintsResolvedBytesOnly(t *testing.T) {
	env := []string{"XDG_CONFIG_HOME=" + t.TempDir()}

	r := run(t, env, "plumbing", "asset", "agent-guidance.md")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	want := embeddedAssetContent(t)
	if r.stdout != string(want) {
		t.Errorf("stdout does not equal the resolved bytes verbatim (got %d bytes, want %d)", len(r.stdout), len(want))
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty without -v", r.stderr)
	}
}

// TestPlumbingAsset_Verbose_StderrNamesLink asserts -v adds exactly one
// stderr line naming the chain link, while stdout keeps carrying only the
// resolved bytes (V25).
func TestPlumbingAsset_Verbose_StderrNamesLink(t *testing.T) {
	env := []string{"XDG_CONFIG_HOME=" + t.TempDir()}

	r := run(t, env, "plumbing", "asset", "agent-guidance.md", "-v")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset -v: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	want := embeddedAssetContent(t)
	if r.stdout != string(want) {
		t.Errorf("stdout does not equal the resolved bytes verbatim under -v")
	}
	if !strings.Contains(r.stderr, "embedded") {
		t.Errorf("stderr = %q, want it to name the embedded link", r.stderr)
	}
	if strings.Count(r.stderr, "\n") != 1 {
		t.Errorf("stderr = %q, want exactly one line", r.stderr)
	}
}

// TestPlumbingAsset_OverridePresent_WinsOverEmbedded plants a
// $XDG_CONFIG_HOME/clast override at the same relative path as an embedded
// asset and asserts the override's own content and link answer — shadow by
// name (C5.2), an override takes effect with no re-install (M18).
func TestPlumbingAsset_OverridePresent_WinsOverEmbedded(t *testing.T) {
	xdg := t.TempDir()
	overrideDir := filepath.Join(xdg, "clast")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(overrideDir, "agent-guidance.md")
	overrideContent := "e2e override content\n"
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"XDG_CONFIG_HOME=" + xdg}

	r := run(t, env, "plumbing", "asset", "agent-guidance.md", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset --json (override): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload assetPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing asset --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Link != "override" {
		t.Errorf("link = %q, want %q", payload.Link, "override")
	}
	if payload.ResolvedFrom != overridePath {
		t.Errorf("resolved_from = %q, want the override's own disk path %q", payload.ResolvedFrom, overridePath)
	}
	if payload.Content != overrideContent {
		t.Errorf("content = %q, want the override's own content %q", payload.Content, overrideContent)
	}
}

// TestPlumbingAsset_ShippedLink_ResolvesFromInstalledShareTree drives the
// middle link of the chain end to end: a binary installed at
// <prefix>/bin/clast with a sibling <prefix>/share/clast/assets/<path> (the
// buildGoModule layout internal/asset.DefaultDir assumes) resolves that
// file with link "shipped" — the surface word for the TRAP:
// internal/asset's own Source.String() calls this same link "default",
// which must never leak here (V25).
func TestPlumbingAsset_ShippedLink_ResolvesFromInstalledShareTree(t *testing.T) {
	prefix := installedLayout(t)
	shareDir := filepath.Join(prefix, "share", "clast", "assets")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shippedPath := filepath.Join(shareDir, "flows", "retro.md")
	if err := os.MkdirAll(filepath.Dir(shippedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	shippedContent := "shipped flow content\n"
	if err := os.WriteFile(shippedPath, []byte(shippedContent), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"XDG_CONFIG_HOME=" + t.TempDir()} // no override present

	r := runInstalled(t, prefix, env, "plumbing", "asset", "flows/retro.md", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset --json (shipped): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload assetPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing asset --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Link != "shipped" {
		t.Errorf("link = %q, want %q (never internal/asset's own \"default\")", payload.Link, "shipped")
	}
	if payload.ResolvedFrom != shippedPath {
		t.Errorf("resolved_from = %q, want the installed share path %q", payload.ResolvedFrom, shippedPath)
	}
	if payload.Content != shippedContent {
		t.Errorf("content = %q, want the shipped file's own content %q", payload.Content, shippedContent)
	}
}

// TestPlumbingAsset_UnknownPath_NotFoundAsset drives a path absent from
// every link of the chain: not-found.asset, exit 1, empty stdout (V25).
func TestPlumbingAsset_UnknownPath_NotFoundAsset(t *testing.T) {
	env := []string{"XDG_CONFIG_HOME=" + t.TempDir()}

	r := run(t, env, "plumbing", "asset", "no/such/asset.md", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing asset (unknown path): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.asset" {
		t.Errorf("error code = %q, want not-found.asset", envelope.Error.Code)
	}
}

// TestPlumbingAsset_PathTraversal_NotFoundAsset drives the seal-review
// finding's own reproduction straight through the built binary: a
// positional that climbs above the asset chain roots must not be joined
// onto them and read — not-found.asset, exit 1, empty stdout, same as any
// other unresolvable path (V25), never an arbitrary file's content with a
// falsified "override" link.
func TestPlumbingAsset_PathTraversal_NotFoundAsset(t *testing.T) {
	env := []string{"XDG_CONFIG_HOME=" + t.TempDir()}

	r := run(t, env, "plumbing", "asset", "../../../../etc/hostname", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing asset (traversal): exit=%d, want 1; stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "not-found.asset" {
		t.Errorf("error code = %q, want not-found.asset", envelope.Error.Code)
	}
}

// --- flow assets: assets/flows/{wake,brief,retro}.md (SURFACE V6, shape-documents) ---

// flowAssetFixtureContent reads assets/flows/<shape>.md straight off disk —
// the same fixture-reading pattern embeddedAssetContent already uses for
// agent-guidance.md — the oracle every embedded-link assertion below
// compares the CLI's output against.
func flowAssetFixtureContent(t *testing.T, shape string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "flows", shape+".md"))
	if err != nil {
		t.Fatalf("reading assets/flows/%s.md fixture: %v", shape, err)
	}
	return data
}

// TestPlumbingAsset_FlowAssets_EmbeddedLink_ServesExactBytes drives `plumbing
// asset flows/<shape>.md` for each of the three shape-documents flow assets
// against a bare binary (no override, no installed share tree in this
// process's own layout): link "embedded", and content/sha256 matching the
// file shipped on disk (M18's last-resort link, C5.1) — the seal condition
// this step's Matter names for `asset` serving all three flows.
func TestPlumbingAsset_FlowAssets_EmbeddedLink_ServesExactBytes(t *testing.T) {
	for _, shape := range []string{"wake", "brief", "retro"} {
		t.Run(shape, func(t *testing.T) {
			path := "flows/" + shape + ".md"
			env := []string{"XDG_CONFIG_HOME=" + t.TempDir()}

			r := run(t, env, "plumbing", "asset", path, "--json")
			if r.exitCode != 0 {
				t.Fatalf("plumbing asset %s --json: exit=%d, want 0; stderr=%q", path, r.exitCode, r.stderr)
			}
			var payload assetPayload
			if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
				t.Fatalf("plumbing asset --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
			}
			if payload.Path != path {
				t.Errorf("path = %q, want %q", payload.Path, path)
			}
			if payload.Link != "embedded" {
				t.Errorf("link = %q, want %q", payload.Link, "embedded")
			}
			if payload.ResolvedFrom != "embedded" {
				t.Errorf("resolved_from = %q, want %q (V25)", payload.ResolvedFrom, "embedded")
			}
			want := flowAssetFixtureContent(t, shape)
			if payload.Content != string(want) {
				t.Errorf("content mismatch for %s: got %d bytes, want the shipped file's %d bytes", path, len(payload.Content), len(want))
			}
			wantSum := sha256.Sum256(want)
			if payload.SHA256 != hex.EncodeToString(wantSum[:]) {
				t.Errorf("sha256 = %q, want the digest of the shipped file", payload.SHA256)
			}

			human := run(t, env, "plumbing", "asset", path)
			if human.exitCode != 0 {
				t.Fatalf("plumbing asset %s: exit=%d, want 0; stderr=%q", path, human.exitCode, human.stderr)
			}
			if human.stdout != string(want) {
				t.Errorf("human mode stdout for %s does not equal the resolved bytes verbatim", path)
			}
		})
	}
}

// TestManifest_JSON_ListsFlowAssetsWithChecksums asserts `manifest --json`
// enumerates all three new flow assets, each with a checksum matching the
// shipped file's own content — the seal condition this step's Matter names
// ("asset serves all three flows and names the chain link").
func TestManifest_JSON_ListsFlowAssetsWithChecksums(t *testing.T) {
	r := run(t, nil, "manifest", "--json")
	if r.exitCode != 0 {
		t.Fatalf("manifest --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var m struct {
		Assets []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("manifest --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}

	byPath := make(map[string]string, len(m.Assets))
	for _, a := range m.Assets {
		byPath[a.Path] = a.SHA256
	}

	for _, shape := range []string{"wake", "brief", "retro"} {
		path := "flows/" + shape + ".md"
		sum, ok := byPath[path]
		if !ok {
			t.Errorf("manifest assets do not list %q", path)
			continue
		}
		if sum == "" {
			t.Errorf("manifest asset %q carries no checksum", path)
			continue
		}
		want := flowAssetFixtureContent(t, shape)
		wantSum := sha256.Sum256(want)
		if sum != hex.EncodeToString(wantSum[:]) {
			t.Errorf("manifest asset %q sha256 = %q, want the digest of the shipped file", path, sum)
		}
	}
}

// TestFlowAssets_StructuralFormat pins the flow-asset format's two
// mechanically-checkable invariants (the Brief's binding format spec,
// shape-documents/brief.md): the title line is exactly "# <shape> — flow",
// and step §1's body names the same shape's document verb, `clast plumbing
// <shape> --json` — every C7.4 cross-reference into a flow depends on §1
// meaning what it says. This is deliberately not a flow-format parser: it
// checks two fixed strings per file, nothing about step numbering or later
// sections.
func TestFlowAssets_StructuralFormat(t *testing.T) {
	for _, shape := range []string{"wake", "brief", "retro"} {
		t.Run(shape, func(t *testing.T) {
			content := string(flowAssetFixtureContent(t, shape))

			wantTitle := "# " + shape + " — flow"
			if !strings.HasPrefix(content, wantTitle+"\n") {
				t.Errorf("flows/%s.md does not start with %q", shape, wantTitle)
			}

			sectionIdx := strings.Index(content, "## §1 —")
			if sectionIdx == -1 {
				t.Fatalf("flows/%s.md has no \"## §1 —\" heading", shape)
			}
			nextIdx := strings.Index(content[sectionIdx+1:], "\n## ")
			var section string
			if nextIdx == -1 {
				section = content[sectionIdx:]
			} else {
				section = content[sectionIdx : sectionIdx+1+nextIdx]
			}
			wantCall := "clast plumbing " + shape + " --json"
			if !strings.Contains(section, wantCall) {
				t.Errorf("flows/%s.md §1 does not name %q", shape, wantCall)
			}
		})
	}
}

// TestSeal_OverrideAppliesWithNoReinstall is the shape-documents Matter's
// own override-liveness seal proof: "an override in $XDG_CONFIG_HOME takes
// effect with no re-install." binPath (TestMain, top of this file) is built
// exactly once for the whole suite; this test writes a MODIFIED
// flows/wake.md into a fake $XDG_CONFIG_HOME/clast/flows/ only after that
// build, then drives that SAME binary — no rebuild, no second install step,
// nothing but a file dropped where M18's resolution chain already looks.
// The other half of the seal condition ("asset serves all three flows and
// names the chain link") is already proven for the embedded link by
// TestPlumbingAsset_FlowAssets_EmbeddedLink_ServesExactBytes (all three
// shapes) and for the shipped link by
// TestPlumbingAsset_ShippedLink_ResolvesFromInstalledShareTree (flows/
// retro.md, via installedLayout) — this test only needed to add the
// missing override half, and only needs one shape to prove the chain
// itself is override-live.
func TestSeal_OverrideAppliesWithNoReinstall(t *testing.T) {
	xdg := t.TempDir()
	overrideDir := filepath.Join(xdg, "clast", "flows")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(overrideDir, "wake.md")
	overrideContent := "# wake — flow\n\noverridden for the seal sweep; no re-install, no rebuild.\n"
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"XDG_CONFIG_HOME=" + xdg}

	r := run(t, env, "plumbing", "asset", "flows/wake.md", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing asset flows/wake.md --json (override): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload assetPayload
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing asset --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Link != "override" {
		t.Errorf("link = %q, want %q (M18: an override takes effect with no re-install)", payload.Link, "override")
	}
	if payload.ResolvedFrom != overridePath {
		t.Errorf("resolved_from = %q, want the override's own disk path %q", payload.ResolvedFrom, overridePath)
	}
	if payload.Content != overrideContent {
		t.Errorf("content = %q, want the override's own content verbatim", payload.Content)
	}

	human := run(t, env, "plumbing", "asset", "flows/wake.md")
	if human.exitCode != 0 {
		t.Fatalf("plumbing asset flows/wake.md (override, human): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if human.stdout != overrideContent {
		t.Errorf("human mode stdout = %q, want the override's own content verbatim", human.stdout)
	}
}

// --- plumbing: `clast plumbing wake` (SURFACE V8/V20, shape-documents) ---

// TestPlumbingWake_JSON_GroupsProjectsAndFiltersWorkingSet seeds a fixture
// journal spanning two projects plus a projectless session, each carrying
// one of every MODEL §2 state (captured, curated-and-stale, curated-and-
// fresh, dismissed), and drives the real binary: the JSON working set
// includes only the captured and stale-curated rows (V8), ordered
// project-grouped with the most-recently-active project first and
// chronological within each group (V7/V8) — a fresh-curated or dismissed
// row never appears at all.
func TestPlumbingWake_JSON_GroupsProjectsAndFiltersWorkingSet(t *testing.T) {
	fx := journaltest.New(t)

	alphaOld := journal.SessionKey{Harness: "claude", NativeID: "alpha-old"}
	alphaStale := journal.SessionKey{Harness: "claude", NativeID: "alpha-stale"}
	alphaFresh := journal.SessionKey{Harness: "claude", NativeID: "alpha-fresh"}
	alphaDismissed := journal.SessionKey{Harness: "claude", NativeID: "alpha-dismissed"}
	betaOnly := journal.SessionKey{Harness: "claude", NativeID: "beta-only"}
	unprojected := journal.SessionKey{Harness: "claude", NativeID: "unprojected"}

	// Project "alpha": most recent qualifying session (alpha-stale) at
	// 2026-09-12T09:00, so alpha's group must sort before beta's.
	fx.Captured("2026-09-10", alphaOld,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a-old"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	).WithProject("2026-09-10", alphaOld, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	fx.CuratedStale("2026-09-12", alphaStale,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 10, SHA256: "original"},
		time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		"framework", "alpha's stale entry",
	).WithProject("2026-09-12", alphaStale, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	fx.Curated("2026-09-11", alphaFresh,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		"framework", "alpha's fresh entry",
	).WithProject("2026-09-11", alphaFresh, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	fx.Dismissed("2026-09-11", alphaDismissed,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "c"},
		time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 11, 8, 30, 0, 0, time.UTC),
		"framework", "manual",
	).WithProject("2026-09-11", alphaDismissed, journal.SessionProject{ID: "p-alpha", Slug: "alpha", Clone: "c", Label: "alpha", Path: "/alpha"})

	// Project "beta": most recent (only) qualifying session at
	// 2026-09-11T08:00 — earlier than alpha's, so beta sorts after alpha.
	fx.Captured("2026-09-11", betaOnly,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "d"},
		time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC),
	).WithProject("2026-09-11", betaOnly, journal.SessionProject{ID: "p-beta", Slug: "beta", Clone: "c", Label: "beta", Path: "/beta"})

	// No project at all: its own bucket, ordered by its own recency —
	// earliest of all here, so it sorts last.
	fx.Captured("2026-09-09", unprojected,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "e"},
		time.Date(2026, 9, 9, 7, 0, 0, 0, time.UTC),
	)

	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}
	r := run(t, env, "plumbing", "wake", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing wake --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}

	var payload struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			State     string `json:"state"`
			Stale     bool   `json:"stale"`
			Title     string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}

	wantOrder := []string{alphaOld.NativeID, alphaStale.NativeID, betaOnly.NativeID, unprojected.NativeID}
	if len(payload.Sessions) != len(wantOrder) {
		t.Fatalf("sessions = %+v, want exactly %d rows: %v", payload.Sessions, len(wantOrder), wantOrder)
	}
	for i, want := range wantOrder {
		if payload.Sessions[i].SessionID != want {
			t.Errorf("sessions[%d].session_id = %q, want %q (project-grouped, most-recent project first, chronological within group)",
				i, payload.Sessions[i].SessionID, want)
		}
	}

	byID := map[string]struct {
		State string
		Stale bool
		Title string
	}{}
	for _, s := range payload.Sessions {
		byID[s.SessionID] = struct {
			State string
			Stale bool
			Title string
		}{s.State, s.Stale, s.Title}
	}
	if got := byID[alphaStale.NativeID]; got.State != "curated" || !got.Stale || got.Title != "alpha's stale entry" {
		t.Errorf("alpha-stale row = %+v, want curated+stale with its title", got)
	}
	if got := byID[alphaOld.NativeID]; got.State != "captured" || got.Stale {
		t.Errorf("alpha-old row = %+v, want captured, not stale", got)
	}
	for _, excluded := range []string{alphaFresh.NativeID, alphaDismissed.NativeID} {
		if _, present := byID[excluded]; present {
			t.Errorf("session %q must be excluded from the working set (fresh-curated/dismissed)", excluded)
		}
	}
}

// TestPlumbingWake_Human_NoSessions confirms the empty case reads as a
// document, not a listing's own "no sessions" one-liner.
func TestPlumbingWake_Human_NoSessions(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "wake", "--since", "all")
	if r.exitCode != 0 {
		t.Fatalf("plumbing wake: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "Nothing in the working set") {
		t.Errorf("stdout = %q, want it to report an empty working set", r.stdout)
	}
}

// TestPlumbingWake_Human_GroupsUnderProjectHeadings confirms the human
// document groups by project heading (V8: a readable markdown document,
// not sessions' one-line-per-row listing).
func TestPlumbingWake_Human_GroupsUnderProjectHeadings(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	fx.Captured("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	).WithProject("2026-09-10", key, journal.SessionProject{ID: "p-clast", Slug: "clast", Clone: "c", Label: "clast", Path: "/clast"})
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	r := run(t, env, "plumbing", "wake", "--since", "all")
	if r.exitCode != 0 {
		t.Fatalf("plumbing wake: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "## clast/clast") {
		t.Errorf("stdout = %q, want a project heading naming clast/clast", r.stdout)
	}
	if !strings.Contains(r.stdout, key.DirName()) {
		t.Errorf("stdout = %q, want the session's locator listed", r.stdout)
	}
}

// TestPlumbingWake_JSON_AutoMinChars_ShippedDefault covers the SURFACE V8
// amendment (2026-09-12, llm-verbs step-03): `plumbing wake --json` carries
// the merged wake.auto_min_chars value. With no config.yaml override
// present, the payload carries the shipped default (60, assets/
// config.default.yaml).
func TestPlumbingWake_JSON_AutoMinChars_ShippedDefault(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + t.TempDir()}

	r := run(t, env, "plumbing", "wake", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing wake --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		AutoMinChars int `json:"auto_min_chars"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.AutoMinChars != 60 {
		t.Errorf("auto_min_chars = %d, want 60 (assets/config.default.yaml's shipped default)", payload.AutoMinChars)
	}
}

// TestPlumbingWake_JSON_AutoMinChars_OverrideWinsOverShippedDefault plants
// a config.yaml overriding wake.auto_min_chars and asserts the merged
// value (override over shipped default) is what `--json` carries — the
// same merge `plumbing wake` runs its resolution through as any other
// config-reading verb (internal/config.Load).
func TestPlumbingWake_JSON_AutoMinChars_OverrideWinsOverShippedDefault(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	overridePath := filepath.Join(xdg, "clast", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(overridePath, []byte("wake:\n  auto_min_chars: 120\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg}

	r := run(t, env, "plumbing", "wake", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing wake --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		AutoMinChars int `json:"auto_min_chars"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.AutoMinChars != 120 {
		t.Errorf("auto_min_chars = %d, want 120 (config.yaml override over the shipped 60)", payload.AutoMinChars)
	}
}

// --- plumbing: `clast plumbing brief [<project>]` (SURFACE V8/V20, shape-documents) ---

// writeBriefEntry seeds one curated, projected session directly through
// journal's own write primitives — the same posture journaltest itself
// uses — at root (rather than a fresh journaltest.New root), so it lands
// in the SAME journal a real `clast init` registered the clone against.
func writeBriefEntry(t *testing.T, root, shard string, key journal.SessionKey, startedAt time.Time, proj journal.SessionProject, title string) {
	t.Helper()
	if err := journal.WriteSession(root, shard, key, journal.Session{
		Harness: key.Harness, SessionID: key.NativeID, Machine: "framework",
		Project: &proj, Branch: "main",
		StartedAt: startedAt, LastActiveAt: startedAt.Add(20 * time.Minute), CapturedAt: startedAt.Add(25 * time.Minute),
		Counts: journal.SessionCounts{User: 1, Assistant: 1}, Substantive: true,
		Transcript: journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "s-" + key.NativeID},
	}); err != nil {
		t.Fatalf("writeBriefEntry: WriteSession(%s): %v", key.DirName(), err)
	}
	curatedAt := startedAt.Add(30 * time.Minute)
	if err := journal.WriteCuration(root, shard, key, journal.Curation{
		State: journal.StateCurated, At: curatedAt, Machine: "framework",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: 5, SHA256: "s-" + key.NativeID},
	}); err != nil {
		t.Fatalf("writeBriefEntry: WriteCuration(%s): %v", key.DirName(), err)
	}
	body := "---\ntitle: " + title + "\ntags: []\n---\n\n" + title + " body.\n"
	if err := journal.WriteEntry(root, shard, key, []byte(body)); err != nil {
		t.Fatalf("writeBriefEntry: WriteEntry(%s): %v", key.DirName(), err)
	}
}

// TestPlumbingBrief_ProjectPositional_JSON_GroupsAndHoists drives
// `plumbing brief widget` (an explicit locator, no cwd registration
// needed) against a fixture journal with two workspaces, confirming the
// JSON groups by label, caps at 3 per group, and reports project/
// current_workspace/empty.
func TestPlumbingBrief_ProjectPositional_JSON_GroupsAndHoists(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}
	for i := 0; i < 4; i++ {
		key := journal.SessionKey{Harness: "claude", NativeID: fmt.Sprintf("dev-%02d", i)}
		shard := time.Date(2026, 9, 10+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		writeBriefEntry(t, journalDir, shard, key, time.Date(2026, 9, 10+i, 9, 0, 0, 0, time.UTC), dev, fmt.Sprintf("dev entry %d", i))
	}

	r := run(t, env, "plumbing", "brief", "widget", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing brief widget --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}

	var payload struct {
		Project          string `json:"project"`
		CurrentWorkspace string `json:"current_workspace"`
		Groups           []struct {
			Workspace string `json:"workspace"`
			Entries   []struct {
				Title string `json:"title"`
			} `json:"entries"`
		} `json:"groups"`
		Empty bool `json:"empty"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing brief --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Project != "widget" {
		t.Errorf("project = %q, want %q", payload.Project, "widget")
	}
	if payload.Empty {
		t.Error("empty = true, want false (entries are present)")
	}
	if len(payload.Groups) != 1 || payload.Groups[0].Workspace != "dev" {
		t.Fatalf("groups = %+v, want one group named %q", payload.Groups, "dev")
	}
	if len(payload.Groups[0].Entries) != 3 {
		t.Errorf("dev group entries = %d, want 3 (the per-group cap)", len(payload.Groups[0].Entries))
	}
}

// TestPlumbingBrief_CwdDefault_RegisteredClone_ResolvesAndHoists drives
// `plumbing brief` with no positional from a registered clone's own
// directory: the project defaults to the cwd's own project (V8/V22's
// whereami path) and that clone's label hoists first among the groups.
func TestPlumbingBrief_CwdDefault_RegisteredClone_ResolvesAndHoists(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	dir := initGitRepo(t, "widget")
	addGitRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	registerClone(t, dir, env)

	// A second, unrelated clone of the same project (a different label),
	// whose entry must NOT be hoisted first.
	other := journal.SessionProject{ID: "p-other", Slug: "widget", Clone: "c2", Label: "other", Path: "/other"}
	writeBriefEntry(t, journalDir, "2026-09-10",
		journal.SessionKey{Harness: "claude", NativeID: "other-01"},
		time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC), other, "other workspace's entry")

	// dir's own registered clone label is the directory basename
	// ("widget", per M16's default-label posture, TestWhereami_JSON_
	// HappyPath's own finding); mint this entry against that same label.
	mine := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "widget", Path: dir}
	writeBriefEntry(t, journalDir, "2026-09-11",
		journal.SessionKey{Harness: "claude", NativeID: "mine-01"},
		time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC), mine, "my workspace's entry")

	r := runIn(t, dir, env, "plumbing", "brief", "--since", "all", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing brief --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Project          string `json:"project"`
		CurrentWorkspace string `json:"current_workspace"`
		Groups           []struct {
			Workspace string `json:"workspace"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing brief --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Project != "widget" {
		t.Errorf("project = %q, want %q", payload.Project, "widget")
	}
	if payload.CurrentWorkspace != "widget" {
		t.Errorf("current_workspace = %q, want %q", payload.CurrentWorkspace, "widget")
	}
	if len(payload.Groups) != 2 || payload.Groups[0].Workspace != "widget" {
		t.Fatalf("groups = %+v, want [widget, other] (widget hoisted first)", payload.Groups)
	}
}

// TestPlumbingBrief_NoPositional_UnregisteredCwd_RefusesUnknownClone
// mirrors whereami/breadcrumb's own refusal.unknown-clone posture (V8/V22
// — "follow whereami's posture").
func TestPlumbingBrief_NoPositional_UnregisteredCwd_RefusesUnknownClone(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	dir := initGitRepo(t, "unregistered")

	r := runIn(t, dir, env, "plumbing", "brief", "--json")
	if r.exitCode != 3 {
		t.Fatalf("plumbing brief --json: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "refusal.unknown-clone" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "refusal.unknown-clone")
	}
}

// TestPlumbingBrief_NoPositional_NotAGitRepo_ValidationError mirrors
// whereami's own pre-flight check: a cwd outside any git repository is
// validation.not-a-git-repo, not a raw exec error.
func TestPlumbingBrief_NoPositional_NotAGitRepo_ValidationError(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	outside := t.TempDir()

	r := runIn(t, outside, env, "plumbing", "brief", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing brief --json: exit=%d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.not-a-git-repo" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "validation.not-a-git-repo")
	}
}

// TestPlumbingBrief_UnknownProjectPositional_ValidationError confirms an
// explicit <project> that matches no registered project is
// validation.unknown-locator (mirroring `clones`' own unknownProject).
func TestPlumbingBrief_UnknownProjectPositional_ValidationError(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "brief", "no-such-project", "--json")
	if r.exitCode != 1 {
		t.Fatalf("plumbing brief --json: exit=%d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.unknown-locator" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "validation.unknown-locator")
	}
}

// TestPlumbingBrief_Human_EmptyReportsReadably confirms the empty case
// (V7's short-circuit fact) reads as a document, not a bare "empty: true"
// with nothing else — no curated entries, no breadcrumbs, no sessions.
func TestPlumbingBrief_Human_EmptyReportsReadably(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	r := run(t, env, "plumbing", "brief", "widget", "--since", "all")
	if r.exitCode != 0 {
		t.Fatalf("plumbing brief: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "Nothing gathered") {
		t.Errorf("stdout = %q, want it to report the empty case readably", r.stdout)
	}

	jsonResult := run(t, env, "plumbing", "brief", "widget", "--since", "all", "--json")
	var payload struct {
		Empty bool `json:"empty"`
	}
	if err := json.Unmarshal([]byte(jsonResult.stdout), &payload); err != nil {
		t.Fatalf("plumbing brief --json stdout is not one JSON value: %v; stdout=%q", err, jsonResult.stdout)
	}
	if !payload.Empty {
		t.Error("empty = false, want true")
	}
}

// --- plumbing: `clast plumbing retro [<day>]` (SURFACE V8/V20, shape-documents) ---

// TestPlumbingRetro_DefaultDay_IsYesterday confirms the positional's
// default (V8: "default day: yesterday") without passing one: a session
// seeded at noon, local time, on the calendar date journal.Cutoff.DayOf
// resolves as "yesterday" relative to the real clock (the built binary
// has no now() seam, the same constraint every other --since e2e case
// documents) is picked up; a session on the day before that is not.
func TestPlumbingRetro_DefaultDay_IsYesterday(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	yesterday, err := cutoff.DayOf(time.Now()).AddDays(-1)
	if err != nil {
		t.Fatalf("AddDays: %v", err)
	}
	dayBefore, err := yesterday.AddDays(-1)
	if err != nil {
		t.Fatalf("AddDays: %v", err)
	}

	yesterdayAt := noonOn(t, yesterday)
	dayBeforeAt := noonOn(t, dayBefore)

	inWindow := journal.SessionKey{Harness: "claude", NativeID: "yesterday-01"}
	writeBriefEntry(t, fx.Root(), string(yesterday), inWindow, yesterdayAt,
		journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}, "yesterday's entry")
	excluded := journal.SessionKey{Harness: "claude", NativeID: "day-before-01"}
	writeBriefEntry(t, fx.Root(), string(dayBefore), excluded, dayBeforeAt,
		journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}, "day before's entry")

	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}
	r := run(t, env, "plumbing", "retro", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing retro --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}

	var payload struct {
		Day      string `json:"day"`
		Projects []struct {
			Project  string `json:"project"`
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Day != string(yesterday) {
		t.Errorf("day = %q, want %q (yesterday)", payload.Day, yesterday)
	}
	if len(payload.Projects) != 1 || len(payload.Projects[0].Sessions) != 1 || payload.Projects[0].Sessions[0].SessionID != inWindow.NativeID {
		t.Fatalf("projects = %+v, want only %s", payload.Projects, inWindow.NativeID)
	}
}

// noonOn returns local noon on day — safely inside any default cutoff
// (04:00), so it always resolves back to exactly day regardless of the
// host's own timezone (mirrors TestPlumbingRetro_DefaultDay_IsYesterday's
// own reasoning for why noon, not midnight, is the safe instant to seed).
func noonOn(t *testing.T, day journal.Day) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", string(day))
	if err != nil {
		t.Fatalf("parsing day %q: %v", day, err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.Local)
}

// TestPlumbingRetro_ExplicitDay_ScopesToThatDayOnly confirms an explicit
// <day> positional (V5's day grammar, YYYY-MM-DD) scopes strictly to that
// day, excluding a session on an adjacent day.
func TestPlumbingRetro_ExplicitDay_ScopesToThatDayOnly(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}

	onDay := journal.SessionKey{Harness: "claude", NativeID: "on-day"}
	writeBriefEntry(t, journalDir, "2026-03-10", onDay, time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC), dev, "on day")
	otherDay := journal.SessionKey{Harness: "claude", NativeID: "other-day"}
	writeBriefEntry(t, journalDir, "2026-03-11", otherDay, time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC), dev, "other day")

	r := run(t, env, "plumbing", "retro", "2026-03-10", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing retro 2026-03-10 --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Day      string `json:"day"`
		Projects []struct {
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Day != "2026-03-10" {
		t.Errorf("day = %q, want %q", payload.Day, "2026-03-10")
	}
	if len(payload.Projects) != 1 || len(payload.Projects[0].Sessions) != 1 || payload.Projects[0].Sessions[0].SessionID != onDay.NativeID {
		t.Fatalf("projects = %+v, want only %s", payload.Projects, onDay.NativeID)
	}
}

// TestPlumbingRetro_SinceWidensWindow confirms --since widens the day
// into a window ending at <day>: a session 2 days earlier is included
// under --since -3d, and window_start reports the resolved lower bound.
func TestPlumbingRetro_SinceWidensWindow(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}

	earlier := journal.SessionKey{Harness: "claude", NativeID: "earlier"}
	writeBriefEntry(t, journalDir, "2026-03-08", earlier, time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC), dev, "earlier")
	onDay := journal.SessionKey{Harness: "claude", NativeID: "on-day"}
	writeBriefEntry(t, journalDir, "2026-03-10", onDay, time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC), dev, "on day")
	tooEarly := journal.SessionKey{Harness: "claude", NativeID: "too-early"}
	writeBriefEntry(t, journalDir, "2026-03-06", tooEarly, time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), dev, "too early")

	r := run(t, env, "plumbing", "retro", "2026-03-10", "--since", "-3d", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing retro --since -3d --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Day         string `json:"day"`
		WindowStart string `json:"window_start"`
		Projects    []struct {
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.WindowStart != "2026-03-07" {
		t.Errorf("window_start = %q, want %q (2026-03-10 minus 3 days)", payload.WindowStart, "2026-03-07")
	}
	if len(payload.Projects) != 1 {
		t.Fatalf("projects = %+v, want 1", payload.Projects)
	}
	var ids []string
	for _, s := range payload.Projects[0].Sessions {
		ids = append(ids, s.SessionID)
	}
	if len(ids) != 2 {
		t.Fatalf("sessions = %v, want exactly [%s, %s]", ids, earlier.NativeID, onDay.NativeID)
	}
	byID := map[string]bool{}
	for _, id := range ids {
		byID[id] = true
	}
	if !byID[earlier.NativeID] || !byID[onDay.NativeID] {
		t.Errorf("sessions = %v, want %s and %s", ids, earlier.NativeID, onDay.NativeID)
	}
	if byID[tooEarly.NativeID] {
		t.Errorf("sessions = %v, want %s excluded (before the window)", ids, tooEarly.NativeID)
	}
}

// TestPlumbingRetro_PerProjectGrouping_SessionsEntriesBreadcrumbs seeds
// two projects, each with a curated session (state+title+entry body) and
// a project-scoped breadcrumb, confirming the full per-project shape V8
// specifies lands correctly end to end, and that the human document
// names both projects.
func TestPlumbingRetro_PerProjectGrouping_SessionsEntriesBreadcrumbs(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	if err := journal.WriteProject(journalDir, "acme", journal.Project{ID: "p-acme", Slug: "acme"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	widgetProj := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}
	acmeProj := journal.SessionProject{ID: "p-acme", Slug: "acme", Clone: "c2", Label: "dev", Path: "/acme"}
	widgetKey := journal.SessionKey{Harness: "claude", NativeID: "widget-01"}
	writeBriefEntry(t, journalDir, day, widgetKey, time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC), widgetProj, "widget's entry")
	acmeKey := journal.SessionKey{Harness: "claude", NativeID: "acme-01"}
	writeBriefEntry(t, journalDir, day, acmeKey, time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC), acmeProj, "acme's entry")

	widgetSlug, acmeSlug := "widget", "acme"
	if err := journal.AppendBreadcrumbAs(journalDir, "framework", journal.Breadcrumb{
		At: time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC), Slug: &widgetSlug, Text: "widget crumb",
	}); err != nil {
		t.Fatalf("AppendBreadcrumbAs: %v", err)
	}
	if err := journal.AppendBreadcrumbAs(journalDir, "framework", journal.Breadcrumb{
		At: time.Date(2026, 3, 10, 8, 30, 0, 0, time.UTC), Slug: &acmeSlug, Text: "acme crumb",
	}); err != nil {
		t.Fatalf("AppendBreadcrumbAs: %v", err)
	}
	if err := journal.AppendBreadcrumbAs(journalDir, "framework", journal.Breadcrumb{
		At: time.Date(2026, 3, 10, 8, 45, 0, 0, time.UTC), Slug: nil, Text: "a global note",
	}); err != nil {
		t.Fatalf("AppendBreadcrumbAs: %v", err)
	}

	r := run(t, env, "plumbing", "retro", "2026-03-10", "--json")
	if r.exitCode != 0 {
		t.Fatalf("plumbing retro --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Projects []struct {
			Project  string `json:"project"`
			Sessions []struct {
				Title string `json:"title"`
			} `json:"sessions"`
			Entries []struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"entries"`
			Breadcrumbs []struct {
				Text string `json:"text"`
			} `json:"breadcrumbs"`
		} `json:"projects"`
		GlobalBreadcrumbs []struct {
			Text string `json:"text"`
		} `json:"global_breadcrumbs"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Projects) != 2 || payload.Projects[0].Project != "acme" || payload.Projects[1].Project != "widget" {
		t.Fatalf("projects = %+v, want [acme, widget] (alphabetical)", payload.Projects)
	}
	acme := payload.Projects[0]
	if len(acme.Sessions) != 1 || acme.Sessions[0].Title != "acme's entry" {
		t.Errorf("acme sessions = %+v, want one titled %q", acme.Sessions, "acme's entry")
	}
	if len(acme.Entries) != 1 || acme.Entries[0].Title != "acme's entry" {
		t.Errorf("acme entries = %+v, want one titled %q", acme.Entries, "acme's entry")
	}
	if len(acme.Breadcrumbs) != 1 || acme.Breadcrumbs[0].Text != "acme crumb" {
		t.Errorf("acme breadcrumbs = %+v, want one %q", acme.Breadcrumbs, "acme crumb")
	}
	if len(payload.GlobalBreadcrumbs) != 1 || payload.GlobalBreadcrumbs[0].Text != "a global note" {
		t.Errorf("global_breadcrumbs = %+v, want one %q", payload.GlobalBreadcrumbs, "a global note")
	}

	human := run(t, env, "plumbing", "retro", "2026-03-10")
	if human.exitCode != 0 {
		t.Fatalf("plumbing retro: exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if !strings.Contains(human.stdout, "## acme") || !strings.Contains(human.stdout, "## widget") {
		t.Errorf("stdout = %q, want project headings for both acme and widget", human.stdout)
	}
	if !strings.Contains(human.stdout, "Global breadcrumbs") {
		t.Errorf("stdout = %q, want a Global breadcrumbs section", human.stdout)
	}
}

// TestPlumbingRetro_EmptyDay_ReportsReadably confirms an empty window
// reads as a document, not a bare empty structure with nothing else.
func TestPlumbingRetro_EmptyDay_ReportsReadably(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}

	r := run(t, env, "plumbing", "retro", "2026-03-10")
	if r.exitCode != 0 {
		t.Fatalf("plumbing retro: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	if !strings.Contains(r.stdout, "Nothing happened") {
		t.Errorf("stdout = %q, want it to report the empty window readably", r.stdout)
	}

	jsonResult := run(t, env, "plumbing", "retro", "2026-03-10", "--json")
	var payload struct {
		Projects          []json.RawMessage `json:"projects"`
		GlobalBreadcrumbs []json.RawMessage `json:"global_breadcrumbs"`
	}
	if err := json.Unmarshal([]byte(jsonResult.stdout), &payload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, jsonResult.stdout)
	}
	if len(payload.Projects) != 0 {
		t.Errorf("projects = %v, want none", payload.Projects)
	}
	if len(payload.GlobalBreadcrumbs) != 0 {
		t.Errorf("global_breadcrumbs = %v, want none", payload.GlobalBreadcrumbs)
	}
}

// --- cross-verb composition smoke (shape-documents seal sweep) ---

// TestSeal_DocumentTrioComposesAcrossCuration walks one fixture session
// through the state-machine-to-document-verbs seam no per-step test
// crossed: `plumbing wake` offers it while it's merely captured; once
// `plumbing curate` writes an entry for it, it leaves wake's working set
// (fresh curated, not stale, MODEL M7) and shows up both in `plumbing
// brief`'s gathered entries (grouped under its workspace label) and in
// `plumbing retro`'s day document (curated, with its title) for the day it
// was captured on. One test, not a matrix — the point is the seam, not
// re-covering each verb's own per-step assertions.
func TestSeal_DocumentTrioComposesAcrossCuration(t *testing.T) {
	fx := journaltest.New(t)
	root := fx.Root()
	fx.Project("clast", journal.Project{ID: "p-clast", Slug: "clast"})

	key := journal.SessionKey{Harness: "claude", NativeID: "trio-01"}
	const shard = "2026-09-05"
	startedAt := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	fx.Captured(shard, key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "trio"},
		startedAt,
	).WithProject(shard, key, journal.SessionProject{ID: "p-clast", Slug: "clast", Clone: "c1", Label: "dev", Path: "/dev"})

	env := []string{"CLAST_JOURNAL_DIR=" + root}

	// Before curation: wake's working set carries it (captured).
	wakeBefore := run(t, env, "plumbing", "wake", "--since", "all", "--json")
	if wakeBefore.exitCode != 0 {
		t.Fatalf("plumbing wake --json (before curate): exit=%d, want 0; stderr=%q", wakeBefore.exitCode, wakeBefore.stderr)
	}
	if !strings.Contains(wakeBefore.stdout, key.NativeID) {
		t.Fatalf("wake (before curate) = %q, want it to carry %q as part of the working set", wakeBefore.stdout, key.NativeID)
	}

	// Curate it — the state-machine write this seam pivots on.
	path := writeEntryFile(t, wellFormedEntryDoc("trio entry"))
	curateResult := run(t, env, "plumbing", "curate", key.DirName(), "--file", path, "--json")
	if curateResult.exitCode != 0 {
		t.Fatalf("plumbing curate --json: exit=%d, want 0; stderr=%q", curateResult.exitCode, curateResult.stderr)
	}

	// After curation: wake's working set no longer carries it (curated and
	// fresh — never revoked, but no longer offered either, V8).
	wakeAfter := run(t, env, "plumbing", "wake", "--since", "all", "--json")
	if wakeAfter.exitCode != 0 {
		t.Fatalf("plumbing wake --json (after curate): exit=%d, want 0; stderr=%q", wakeAfter.exitCode, wakeAfter.stderr)
	}
	if strings.Contains(wakeAfter.stdout, key.NativeID) {
		t.Errorf("wake (after curate) = %q, want %q gone from the working set (fresh curated)", wakeAfter.stdout, key.NativeID)
	}

	// brief's gathered entries now carry it, under its workspace label.
	briefResult := run(t, env, "plumbing", "brief", "clast", "--since", "all", "--json")
	if briefResult.exitCode != 0 {
		t.Fatalf("plumbing brief --json: exit=%d, want 0; stderr=%q", briefResult.exitCode, briefResult.stderr)
	}
	var briefPayload struct {
		Groups []struct {
			Workspace string `json:"workspace"`
			Entries   []struct {
				Title string `json:"title"`
			} `json:"entries"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(briefResult.stdout), &briefPayload); err != nil {
		t.Fatalf("plumbing brief --json stdout is not one JSON value: %v; stdout=%q", err, briefResult.stdout)
	}
	foundInBrief := false
	for _, g := range briefPayload.Groups {
		if g.Workspace != "dev" {
			continue
		}
		for _, e := range g.Entries {
			if e.Title == "trio entry" {
				foundInBrief = true
			}
		}
	}
	if !foundInBrief {
		t.Errorf("brief groups = %+v, want the curated entry gathered under workspace %q", briefPayload.Groups, "dev")
	}

	// retro's day document lists the session, curated, with its title, on
	// the day it was captured.
	retroResult := run(t, env, "plumbing", "retro", shard, "--json")
	if retroResult.exitCode != 0 {
		t.Fatalf("plumbing retro --json: exit=%d, want 0; stderr=%q", retroResult.exitCode, retroResult.stderr)
	}
	var retroPayload struct {
		Projects []struct {
			Project  string `json:"project"`
			Sessions []struct {
				SessionID string `json:"session_id"`
				State     string `json:"state"`
				Title     string `json:"title"`
			} `json:"sessions"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(retroResult.stdout), &retroPayload); err != nil {
		t.Fatalf("plumbing retro --json stdout is not one JSON value: %v; stdout=%q", err, retroResult.stdout)
	}
	foundInRetro := false
	for _, g := range retroPayload.Projects {
		if g.Project != "clast" {
			continue
		}
		for _, s := range g.Sessions {
			if s.SessionID == key.NativeID && s.State == "curated" && s.Title == "trio entry" {
				foundInRetro = true
			}
		}
	}
	if !foundInRetro {
		t.Errorf("retro projects = %+v, want %q listed curated with its title under project %q", retroPayload.Projects, key.NativeID, "clast")
	}
}

// --- porcelain: `clast brief [<project>]` (SURFACE V9/V10/V26–V29, llm-verbs/step-04) ---

// writeLLMConfig plants $XDG_CONFIG_HOME/clast/config.yaml with an llm
// section pointing at baseURL/model — the same config.yaml override link
// TestPlumbingWake_JSON_AutoMinChars_OverrideWinsOverShippedDefault already
// plants for a different key, mirrored here for llm.base_url/llm.model
// (SURFACE V12/V31). llmtest's stub runs in this test process; the child
// binary reaches it over real loopback HTTP, so no in-process seam is
// needed on the command side.
func writeLLMConfig(t *testing.T, xdg, baseURL, model string) {
	t.Helper()
	path := filepath.Join(xdg, "clast", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("llm:\n  base_url: %q\n  model: %q\n", baseURL, model)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBrief_JSON_HappyPath_AgainstStub drives `clast brief widget --json`
// end to end: a curated entry gathered today, an llm.base_url pointed at
// an in-process llmtest stub, exit 0, and the --json payload carrying the
// stub's canned completion as `brief` with `empty: false`. Human mode
// (no --json) prints the same completion verbatim to stdout (flows/
// brief.md §4's own "printed to stdout" presentation).
func TestBrief_JSON_HappyPath_AgainstStub(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	today := cutoff.DayOf(time.Now())
	writeBriefEntry(t, journalDir, string(today),
		journal.SessionKey{Harness: "claude", NativeID: "widget-01"}, noonOn(t, today),
		journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"},
		"today's entry")

	stub := llmtest.New(t, "here is your synthesized briefing")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := run(t, env, "brief", "widget", "--json")
	if r.exitCode != 0 {
		t.Fatalf("brief --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Project string `json:"project"`
		Empty   bool   `json:"empty"`
		Brief   string `json:"brief"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("brief --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Project != "widget" {
		t.Errorf("project = %q, want %q", payload.Project, "widget")
	}
	if payload.Empty {
		t.Error("empty = true, want false (a curated entry is present)")
	}
	if payload.Brief != "here is your synthesized briefing" {
		t.Errorf("brief = %q, want the stub's canned completion", payload.Brief)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1", len(reqs))
	}

	human := run(t, env, "brief", "widget")
	if human.exitCode != 0 {
		t.Fatalf("brief (human): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if strings.TrimSpace(human.stdout) != "here is your synthesized briefing" {
		t.Errorf("brief (human) stdout = %q, want the stub's canned completion verbatim", human.stdout)
	}
}

// TestBrief_EmptyState_NoLLMConfigNeeded_NoRequests drives `clast brief` for
// a registered project with nothing gathered: no curated entries, no
// breadcrumbs, no sessions. It plants an llm config that points at a stub
// configured to fail every request, and deliberately carries NO
// CLAST_LLM_API_KEY — if the command ever constructed an llm.Client or
// called Complete, it would fail (missing key, or the stub's 500). It
// doesn't: flows/brief.md §2's stop happens before either, so the run
// still exits 0, reports the empty case, and the stub sees zero requests.
func TestBrief_EmptyState_NoLLMConfigNeeded_NoRequests(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	stub := llmtest.New(t, "unused")
	stub.Fail(500, "must never be called for an empty brief")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY="}

	r := run(t, env, "brief", "widget", "--json")
	if r.exitCode != 0 {
		t.Fatalf("brief --json (empty): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Empty bool   `json:"empty"`
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("brief --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if !payload.Empty {
		t.Error("empty = false, want true")
	}
	if payload.Brief != "" {
		t.Errorf("brief = %q, want empty string in the empty case", payload.Brief)
	}
	if reqs := stub.Requests(); len(reqs) != 0 {
		t.Fatalf("stub captured %d requests, want 0 (the empty case must never reach the endpoint)", len(reqs))
	}

	human := run(t, env, "brief", "widget")
	if human.exitCode != 0 {
		t.Fatalf("brief (human, empty): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if !strings.Contains(human.stdout, "Nothing to brief") {
		t.Errorf("brief (human, empty) stdout = %q, want it to report the empty case readably", human.stdout)
	}
}

// TestBrief_MissingAPIKey_ErrorCode drives `clast brief` with a curated
// entry present (so the run reaches client construction) but no
// CLAST_LLM_API_KEY set: validation.llm-api-key-missing, exit 1 (V34: an
// llm/config error surfaces with its own code, not a partial document).
func TestBrief_MissingAPIKey_ErrorCode(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	today := cutoff.DayOf(time.Now())
	writeBriefEntry(t, journalDir, string(today),
		journal.SessionKey{Harness: "claude", NativeID: "widget-01"}, noonOn(t, today),
		journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"},
		"today's entry")

	writeLLMConfig(t, xdg, "http://127.0.0.1:1", "gpt-test") // unreachable; must never be dialed
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY="}

	r := run(t, env, "brief", "widget", "--json")
	if r.exitCode != 1 {
		t.Fatalf("brief --json: exit=%d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty on failure", r.stdout)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.llm-api-key-missing" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "validation.llm-api-key-missing")
	}
}

// TestBrief_LLMNotConfigured_ErrorCode drives `clast brief` with a curated
// entry present but no llm config at all: validation.llm-not-configured,
// exit 1 — the same V34 posture as the missing-key case above, pinned
// separately since NewClient checks base_url/model before the API key.
func TestBrief_LLMNotConfigured_ErrorCode(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir() // no config.yaml planted at all
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	today := cutoff.DayOf(time.Now())
	writeBriefEntry(t, journalDir, string(today),
		journal.SessionKey{Harness: "claude", NativeID: "widget-01"}, noonOn(t, today),
		journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"},
		"today's entry")

	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := run(t, env, "brief", "widget", "--json")
	if r.exitCode != 1 {
		t.Fatalf("brief --json: exit=%d, want 1 (validation); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "validation.llm-not-configured" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "validation.llm-not-configured")
	}
}

// TestBrief_UnregisteredCwd_RefusesUnknownClone confirms the top-level
// verb's own cwd-default resolution refuses exactly like `plumbing brief`
// does (both run through the same internal/verbs/brief.Run): an
// unregistered cwd is refusal.unknown-clone, exit 3, naming `clast init`.
func TestBrief_UnregisteredCwd_RefusesUnknownClone(t *testing.T) {
	journalDir := t.TempDir()
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir}
	dir := initGitRepo(t, "unregistered")

	r := runIn(t, dir, env, "brief", "--json")
	if r.exitCode != 3 {
		t.Fatalf("brief --json: exit=%d, want 3 (refusal); stderr=%q", r.exitCode, r.stderr)
	}
	envelope := parseErrorEnvelope(t, r.stderr)
	if envelope.Error.Code != "refusal.unknown-clone" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "refusal.unknown-clone")
	}
}

// --- porcelain: `clast retro [<day>]` (SURFACE V11, llm-verbs/step-05) ---

// writeCapturedOnlySession seeds a session with no curation.json at all
// (state `captured`) — retro's own "no entry body" case (flows/retro.md
// §2's Decision: it lists in §3 by state and title, with no summary).
func writeCapturedOnlySession(t *testing.T, root, shard string, key journal.SessionKey, startedAt time.Time, proj journal.SessionProject) {
	t.Helper()
	if err := journal.WriteSession(root, shard, key, journal.Session{
		Harness: key.Harness, SessionID: key.NativeID, Machine: "framework",
		Project: &proj, Branch: "main",
		StartedAt: startedAt, LastActiveAt: startedAt.Add(20 * time.Minute), CapturedAt: startedAt.Add(25 * time.Minute),
		Counts: journal.SessionCounts{User: 1, Assistant: 1}, Substantive: true,
		Transcript: journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "s-" + key.NativeID},
	}); err != nil {
		t.Fatalf("writeCapturedOnlySession: WriteSession(%s): %v", key.DirName(), err)
	}
}

// TestRetro_JSON_HappyPath_AgainstStub drives `clast retro <day> --json`
// against a fixture day with one curated session (an entry body to
// summarize) and one merely-captured session (no entry body, no
// summary): exit 0, the stub sees exactly one request, the curated
// session's entry carries the stub's canned completion as `summary`, and
// the captured-only session lists with no summary at all. Human mode
// prints a "Summaries" section carrying the same condensed text.
func TestRetro_JSON_HappyPath_AgainstStub(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	cacheHome := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}

	const day = "2026-04-01"
	curated := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	writeBriefEntry(t, journalDir, day, curated, time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC), dev, "shipped the retro cache")
	capturedOnly := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	writeCapturedOnlySession(t, journalDir, day, capturedOnly, time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC), dev)

	stub := llmtest.New(t, "- Shipped: the retro cache")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{
		"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "XDG_CACHE_HOME=" + cacheHome,
		"CLAST_LLM_API_KEY=sk-test-key",
	}

	r := run(t, env, "retro", day, "--json")
	if r.exitCode != 0 {
		t.Fatalf("retro --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Day      string `json:"day"`
		Projects []struct {
			Project  string `json:"project"`
			Sessions []struct {
				SessionID string `json:"session_id"`
				Title     string `json:"title"`
			} `json:"sessions"`
			Entries []struct {
				SessionID string `json:"session_id"`
				Title     string `json:"title"`
				Body      string `json:"body"`
				Summary   string `json:"summary"`
			} `json:"entries"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Project != "widget" {
		t.Fatalf("projects = %+v, want one project %q", payload.Projects, "widget")
	}
	proj := payload.Projects[0]
	if len(proj.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want both the curated and captured-only session listed", proj.Sessions)
	}
	if len(proj.Entries) != 1 || proj.Entries[0].SessionID != curated.NativeID {
		t.Fatalf("entries = %+v, want exactly the curated session", proj.Entries)
	}
	if proj.Entries[0].Summary != "- Shipped: the retro cache" {
		t.Errorf("entries[0].summary = %q, want the stub's canned completion", proj.Entries[0].Summary)
	}
	if proj.Entries[0].Body == "" {
		t.Error("entries[0].body = \"\", want the raw entry body still present (additive shape)")
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1 (one entry-bearing session)", len(reqs))
	}

	human := run(t, env, "retro", day)
	if human.exitCode != 0 {
		t.Fatalf("retro (human): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if !strings.Contains(human.stdout, "### Summaries") || !strings.Contains(human.stdout, "- Shipped: the retro cache") {
		t.Errorf("retro (human) stdout = %q, want a Summaries section carrying the condensed text", human.stdout)
	}
}

// TestRetro_EmptyWindow_NoLLMConfigNeeded_NoRequests drives `clast retro`
// for a day with no sessions at all: it plants an llm config pointing at
// a stub configured to fail every request and deliberately carries no
// CLAST_LLM_API_KEY — if the command ever constructed an llm.Client, it
// would fail. It doesn't: HasEntries is false, so command.go's own
// empty-before-client gate skips both NewClient and CacheDir entirely,
// and the run still exits 0 reporting the (otherwise) empty window.
func TestRetro_EmptyWindow_NoLLMConfigNeeded_NoRequests(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	stub := llmtest.New(t, "unused")
	stub.Fail(500, "must never be called for a window with no entry-bearing session")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{
		"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "XDG_CACHE_HOME=" + t.TempDir(),
		"CLAST_LLM_API_KEY=",
	}

	r := run(t, env, "retro", "2026-04-02", "--json")
	if r.exitCode != 0 {
		t.Fatalf("retro --json (empty window): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("retro --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if len(payload.Projects) != 0 {
		t.Errorf("projects = %v, want none", payload.Projects)
	}
	if reqs := stub.Requests(); len(reqs) != 0 {
		t.Fatalf("stub captured %d requests, want 0 (no entry-bearing session in the window)", len(reqs))
	}

	human := run(t, env, "retro", "2026-04-02")
	if human.exitCode != 0 {
		t.Fatalf("retro (human, empty window): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if !strings.Contains(human.stdout, "Nothing happened") {
		t.Errorf("retro (human, empty window) stdout = %q, want it to report the empty window readably", human.stdout)
	}
}

// TestRetro_Cache_HitOnSecondRun_RefreshOnThird drives the same day three
// times against one persistent $XDG_CACHE_HOME: run 1 calls the endpoint
// and populates the cache; run 2 (no --refresh) must hit the cache and
// make no request at all (V11: "an unchanged entry is not re-summarized
// on a later run"); run 3 (--refresh) must bypass the cache, call the
// endpoint again, and report the refreshed completion.
func TestRetro_Cache_HitOnSecondRun_RefreshOnThird(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()
	cacheHome := t.TempDir()
	if err := journal.WriteProject(journalDir, "widget", journal.Project{ID: "p-widget", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	dev := journal.SessionProject{ID: "p-widget", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"}
	const day = "2026-04-03"
	key := journal.SessionKey{Harness: "claude", NativeID: "cache-01"}
	writeBriefEntry(t, journalDir, day, key, time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC), dev, "cache me")

	stub := llmtest.New(t, "- Shipped: run one")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{
		"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "XDG_CACHE_HOME=" + cacheHome,
		"CLAST_LLM_API_KEY=sk-test-key",
	}

	firstRun := run(t, env, "retro", day, "--json")
	if firstRun.exitCode != 0 {
		t.Fatalf("retro --json (run 1): exit=%d, want 0; stderr=%q", firstRun.exitCode, firstRun.stderr)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests after run 1, want exactly 1", len(reqs))
	}
	if !strings.Contains(firstRun.stdout, "- Shipped: run one") {
		t.Errorf("run 1 stdout = %q, want the stub's first completion", firstRun.stdout)
	}

	// Run 2: fail every request — a cache hit must mean this is never
	// dialed.
	stub.Fail(500, "must never be called on run 2 (cache hit)")
	secondRun := run(t, env, "retro", day, "--json")
	if secondRun.exitCode != 0 {
		t.Fatalf("retro --json (run 2): exit=%d, want 0; stderr=%q", secondRun.exitCode, secondRun.stderr)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests after run 2, want still exactly 1 (cache hit, no new request)", len(reqs))
	}
	if !strings.Contains(secondRun.stdout, "- Shipped: run one") {
		t.Errorf("run 2 stdout = %q, want the cached first completion", secondRun.stdout)
	}

	// Run 3: --refresh must force a new request despite the cache entry.
	stub.SetResponse("- Shipped: run three, refreshed")
	thirdRun := run(t, env, "retro", day, "--refresh", "--json")
	if thirdRun.exitCode != 0 {
		t.Fatalf("retro --json (run 3, --refresh): exit=%d, want 0; stderr=%q", thirdRun.exitCode, thirdRun.stderr)
	}
	if reqs := stub.Requests(); len(reqs) != 2 {
		t.Fatalf("stub captured %d requests after run 3, want exactly 2 (run 1 + the --refresh request)", len(reqs))
	}
	if !strings.Contains(thirdRun.stdout, "- Shipped: run three, refreshed") {
		t.Errorf("run 3 stdout = %q, want the refreshed completion", thirdRun.stdout)
	}
}

// --- porcelain: `clast wake [--auto]` (SURFACE V9, llm-verbs/step-06) ---

// wakeSessionWithTranscript seeds a captured session, with a transcript
// copy on disk (so `plumbing show --transcript` can actually render it —
// unlike brief/retro, wake's §2 reads the transcript's own content, not
// just an entry body), at startedAt relative to time.Now() so it falls
// inside the top-level `wake` verb's own default `since` window (-14d,
// V9 exposes no --since flag at all).
func wakeSessionWithTranscript(t *testing.T, root, shard string, key journal.SessionKey, startedAt time.Time) {
	t.Helper()
	if err := journal.WriteSession(root, shard, key, journal.Session{
		Harness: key.Harness, SessionID: key.NativeID, Machine: "framework",
		Branch:       "main",
		StartedAt:    startedAt,
		LastActiveAt: startedAt.Add(20 * time.Minute),
		CapturedAt:   startedAt.Add(25 * time.Minute),
		Counts:       journal.SessionCounts{User: 2, Assistant: 2},
		Substantive:  true,
		Transcript:   journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "s-" + key.NativeID},
	}); err != nil {
		t.Fatalf("wakeSessionWithTranscript: WriteSession(%s): %v", key.DirName(), err)
	}
	path := journal.TranscriptPath(root, shard, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","message":{"content":"hello from ` + key.NativeID + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wakeQualifyingDraft is long enough (well over the shipped
// wake.auto_min_chars default of 60) to be accepted in both auto and
// interactive mode.
const wakeQualifyingDraft = "# Session: fixed the wake ordering bug\n\n" +
	"## Goal\nSort the working set by recency, matching the old skill's wanted order.\n\n" +
	"## What shipped\n- Reordered groupByProject to sort by most-recently-active project.\n\n" +
	"Suggested tags: bugfix, wake\n"

// TestWake_Auto_AcceptAndSkipInOneRun_AgainstStub drives `clast wake
// --auto --json` over a two-session working set: one session whose
// transcript copy is readable (drafts successfully, and — carrying the
// long wakeQualifyingDraft completion — is accepted) and one whose
// transcript copy is simply missing on disk (§2's read fails, so Draft
// itself fails; Auto mode's own rule is "a draft that fails to generate
// is skipped, not retried" — never an aborted run). The stub must see
// exactly one request: the broken session never reaches the LLM call at
// all. F3 (llm-verbs seal sweep) amends this test's own assertions: the
// broken session's skip must now carry its own skipped_draft_failed
// count (distinct from skipped_below_threshold) and a stderr diagnostic
// naming it — before the fix, this same failure was a silent plain skip,
// indistinguishable from a below-threshold one.
func TestWake_Auto_AcceptAndSkipInOneRun_AgainstStub(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()

	now := time.Now()
	ok := journal.SessionKey{Harness: "claude", NativeID: "wake-auto-ok"}
	broken := journal.SessionKey{Harness: "claude", NativeID: "wake-auto-broken"}
	wakeSessionWithTranscript(t, journalDir, now.Add(-3*time.Hour).Format("2006-01-02"), ok, now.Add(-3*time.Hour))
	// broken carries session.json but no transcript copy at all — show's
	// os.Open fails, so Draft fails before ever calling the endpoint.
	if err := journal.WriteSession(journalDir, now.Add(-2*time.Hour).Format("2006-01-02"), broken, journal.Session{
		Harness: broken.Harness, SessionID: broken.NativeID, Machine: "framework",
		Branch: "main", StartedAt: now.Add(-2 * time.Hour), LastActiveAt: now.Add(-2 * time.Hour).Add(20 * time.Minute),
		CapturedAt: now.Add(-2 * time.Hour).Add(25 * time.Minute), Counts: journal.SessionCounts{User: 1, Assistant: 1},
		Substantive: true, Transcript: journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "missing"},
	}); err != nil {
		t.Fatalf("WriteSession(broken): %v", err)
	}

	stub := llmtest.New(t, wakeQualifyingDraft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := run(t, env, "wake", "--auto", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --auto --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered            int `json:"considered"`
		Drafted               int `json:"drafted"`
		Accepted              int `json:"accepted"`
		Dismissed             int `json:"dismissed"`
		Skipped               int `json:"skipped"`
		SkippedBelowThreshold int `json:"skipped_below_threshold"`
		SkippedDraftFailed    int `json:"skipped_draft_failed"`
		ProjectsTouched       int `json:"projects_touched"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --auto --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 2 || payload.Drafted != 1 || payload.Accepted != 1 || payload.Skipped != 1 || payload.Dismissed != 0 {
		t.Fatalf("payload = %+v, want considered=2 drafted=1 accepted=1 skipped=1 dismissed=0", payload)
	}
	if payload.SkippedDraftFailed != 1 || payload.SkippedBelowThreshold != 0 {
		t.Fatalf("payload = %+v, want skipped_draft_failed=1 skipped_below_threshold=0 (F3)", payload)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1 (the broken session never reaches the endpoint)", len(reqs))
	}
	if !strings.Contains(r.stderr, broken.DirName()) {
		t.Errorf("stderr = %q, want a diagnostic naming the broken session %q (F3)", r.stderr, broken.DirName())
	}
	if !strings.Contains(r.stderr, "draft generation failed") {
		t.Errorf("stderr = %q, want a draft-generation-failed diagnostic (F3)", r.stderr)
	}

	// The accepted session is now curated; the broken one is untouched.
	okShow := run(t, env, "plumbing", "show", ok.DirName(), "--json")
	if okShow.exitCode != 0 {
		t.Fatalf("plumbing show %s: exit=%d stderr=%q", ok.DirName(), okShow.exitCode, okShow.stderr)
	}
	if !strings.Contains(okShow.stdout, `"state":"curated"`) {
		t.Errorf("accepted session show = %q, want state curated", okShow.stdout)
	}
	brokenShow := run(t, env, "plumbing", "show", broken.DirName(), "--json")
	if brokenShow.exitCode != 0 {
		t.Fatalf("plumbing show %s: exit=%d stderr=%q", broken.DirName(), brokenShow.exitCode, brokenShow.stderr)
	}
	if !strings.Contains(brokenShow.stdout, `"state":"captured"`) {
		t.Errorf("skipped session show = %q, want state captured (untouched)", brokenShow.stdout)
	}
}

// TestWake_Interactive_AcceptAndSkip_DrivenByStdin drives `clast wake
// --json` (no --auto) over a two-session working set, feeding the number
// menu over piped stdin: "1\n4\n4\n" accepts the first session (with no
// promotions) and skips the second.
func TestWake_Interactive_AcceptAndSkip_DrivenByStdin(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()

	now := time.Now()
	first := journal.SessionKey{Harness: "claude", NativeID: "wake-int-first"}
	second := journal.SessionKey{Harness: "claude", NativeID: "wake-int-second"}
	wakeSessionWithTranscript(t, journalDir, now.Add(-3*time.Hour).Format("2006-01-02"), first, now.Add(-3*time.Hour))
	wakeSessionWithTranscript(t, journalDir, now.Add(-2*time.Hour).Format("2006-01-02"), second, now.Add(-2*time.Hour))

	stub := llmtest.New(t, wakeQualifyingDraft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := runWithStdin(t, env, "1\n4\n4\n", "wake", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --json (interactive): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered int `json:"considered"`
		Accepted   int `json:"accepted"`
		Skipped    int `json:"skipped"`
		Dismissed  int `json:"dismissed"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 2 || payload.Accepted != 1 || payload.Skipped != 1 || payload.Dismissed != 0 {
		t.Fatalf("payload = %+v, want considered=2 accepted=1 skipped=1 dismissed=0", payload)
	}
	if reqs := stub.Requests(); len(reqs) != 2 {
		t.Fatalf("stub captured %d requests, want exactly 2 (one draft per session)", len(reqs))
	}
}

// TestWake_EmptyWorkingSet_NoLLMConfigNeeded drives `clast wake --json`
// against an empty journal: exit 0, an all-zero summary, and — since it
// plants an llm config pointed at a stub configured to fail every
// request, with no CLAST_LLM_API_KEY at all — proof that the empty-
// working-set stop happens before any llm.Client is ever constructed
// (mirrors TestBrief_EmptyState_NoLLMConfigNeeded_NoRequests and
// TestRetro_EmptyWindow_NoLLMConfigNeeded_NoRequests).
func TestWake_EmptyWorkingSet_NoLLMConfigNeeded(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()

	stub := llmtest.New(t, "unused")
	stub.Fail(500, "must never be called for an empty working set")
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY="}

	r := run(t, env, "wake", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --json (empty): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered int `json:"considered"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 0 {
		t.Errorf("considered = %d, want 0", payload.Considered)
	}
	if reqs := stub.Requests(); len(reqs) != 0 {
		t.Fatalf("stub captured %d requests, want 0 (the empty case must never reach the endpoint)", len(reqs))
	}

	human := run(t, env, "wake")
	if human.exitCode != 0 {
		t.Fatalf("wake (human, empty): exit=%d, want 0; stderr=%q", human.exitCode, human.stderr)
	}
	if !strings.Contains(human.stdout, "Nothing to curate") {
		t.Errorf("wake (human, empty) stdout = %q, want it to report the empty working set readably", human.stdout)
	}
}

// editedWakeDraft is a sequencedLLMStub's SECOND response — distinct from
// wakeQualifyingDraft so a test can tell whether the accepted entry
// carries the ORIGINAL draft or the regenerated one.
const editedWakeDraft = "# Session: revised per feedback\n\n" +
	"## Goal\nProve the Edit path's regenerated content is what actually gets curated.\n\n" +
	"Suggested tags: revised\n"

// sequencedLLMStub is a minimal OpenAI-compatible /chat/completions stub
// that answers its Nth request with responses[N] (its last response for
// any request beyond len(responses)) — llmtest.Server's own single canned
// response can't change mid-run, and driving `wake`'s Edit path (F2,
// llm-verbs seal sweep) through the actual built binary needs a first
// draft and a distinguishable regenerated one from that one process's own
// two requests.
func sequencedLLMStub(t *testing.T, responses ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		idx := n
		n++
		mu.Unlock()
		resp := responses[len(responses)-1]
		if idx < len(responses) {
			resp = responses[idx]
		}
		var out struct {
			Choices []struct {
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		out.Choices = make([]struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}, 1)
		out.Choices[0].Message.Role = "assistant"
		out.Choices[0].Message.Content = resp
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWake_Interactive_EditRegeneratesWithFeedback_AgainstStub drives
// `clast wake --json` (interactive) over a one-session working set,
// choosing Edit (menu choice "2") before Accept: F2 (llm-verbs seal
// sweep) replaced the old $EDITOR-on-a-temp-file mechanism with
// flows/wake.md §4's own "take the requested changes as feedback,
// regenerate the draft, and return to the decision" — this is that round
// trip end to end through the built binary (a sequenced stub stands in
// for the LLM, since a real process can't have its stub reconfigured
// mid-run), confirming the entry `plumbing curate` ultimately writes
// carries the REGENERATED draft's title, not the original one the stub
// returned first, and that the regeneration request itself carried the
// feedback line.
func TestWake_Interactive_EditRegeneratesWithFeedback_AgainstStub(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()

	now := time.Now()
	key := journal.SessionKey{Harness: "claude", NativeID: "wake-edit-01"}
	wakeSessionWithTranscript(t, journalDir, now.Add(-3*time.Hour).Format("2006-01-02"), key, now.Add(-3*time.Hour))

	stub := sequencedLLMStub(t, wakeQualifyingDraft, editedWakeDraft)
	writeLLMConfig(t, xdg, stub.URL, "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	// "2" (Edit) -> feedback line -> regenerated draft shown -> "1"
	// (Accept) -> "4" (no more promotions).
	r := runWithStdin(t, env, "2\nplease make it shorter\n1\n4\n", "wake", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --json (edit): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered int `json:"considered"`
		Accepted   int `json:"accepted"`
		Skipped    int `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --json stdout is not one JSON value: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 1 || payload.Accepted != 1 || payload.Skipped != 0 {
		t.Fatalf("payload = %+v, want considered=1 accepted=1 skipped=0", payload)
	}

	show := run(t, env, "plumbing", "show", key.DirName(), "--json")
	if show.exitCode != 0 {
		t.Fatalf("plumbing show: exit=%d, want 0; stderr=%q", show.exitCode, show.stderr)
	}
	var showPayload struct {
		Entry struct {
			Title string `json:"title"`
		} `json:"entry"`
	}
	if err := json.Unmarshal([]byte(show.stdout), &showPayload); err != nil {
		t.Fatalf("plumbing show --json stdout: %v; stdout=%q", err, show.stdout)
	}
	if showPayload.Entry.Title != "revised per feedback" {
		t.Errorf("entry.title = %q, want the REGENERATED draft's title, not the stub's original", showPayload.Entry.Title)
	}
}

// TestWake_Interactive_Dismiss_WritesManualReason_UndismissRevives drives
// `clast wake --json` (interactive), dismissing the one session in the
// working set (menu choice "3"): confirms wake's own dismiss path
// (interactive.go's disposition, dismiss.Run(..., dismiss.DefaultReason,
// ...)) writes the exact same default reason ("manual") `plumbing
// dismiss`'s own --reason flag defaults to (SURFACE V15), and that
// `plumbing undismiss` — sealed by state-verbs, in a different Matter —
// revives it straight back to captured exactly like any other dismissed
// session. This is the llm-verbs/state-verbs composition the step brief
// calls for: nothing about undismiss's own behavior is wake-specific, but
// nothing had driven a wake-authored dismissal into it before.
func TestWake_Interactive_Dismiss_WritesManualReason_UndismissRevives(t *testing.T) {
	journalDir := t.TempDir()
	xdg := t.TempDir()

	now := time.Now()
	key := journal.SessionKey{Harness: "claude", NativeID: "wake-dismiss-01"}
	shard := now.Add(-3 * time.Hour).Format("2006-01-02")
	wakeSessionWithTranscript(t, journalDir, shard, key, now.Add(-3*time.Hour))

	stub := llmtest.New(t, wakeQualifyingDraft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + journalDir, "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := runWithStdin(t, env, "3\n", "wake", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --json (dismiss): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Dismissed int `json:"dismissed"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.Dismissed != 1 {
		t.Fatalf("dismissed = %d, want 1", payload.Dismissed)
	}

	curation, ok, err := journal.ReadCuration(journalDir, shard, key)
	if err != nil || !ok || curation.State != journal.StateDismissed {
		t.Fatalf("curation after wake dismiss: state=%q ok=%v err=%v, want dismissed", curation.State, ok, err)
	}
	if curation.Reason == nil || *curation.Reason != "manual" {
		t.Fatalf("reason = %v, want \"manual\" (dismiss.DefaultReason, SURFACE V15)", curation.Reason)
	}

	undismiss := run(t, env, "plumbing", "undismiss", key.DirName(), "--json")
	if undismiss.exitCode != 0 {
		t.Fatalf("plumbing undismiss: exit=%d, want 0; stderr=%q", undismiss.exitCode, undismiss.stderr)
	}
	if _, ok, err := journal.ReadCuration(journalDir, shard, key); err != nil || ok {
		t.Fatalf("curation after undismiss: ok=%v err=%v, want ok=false (captured, no curation.json)", ok, err)
	}
}

// TestWake_Interactive_StaleDismissRefused_SkipsAndContinues_ThroughTopLevelVerb
// pins F1 (the seal blocker, llm-verbs seal sweep) end to end through the
// built binary: a stale curated session already carries an entry.md on
// disk, so `plumbing dismiss` (and so wake's own Dismiss path) refuses it
// (validation.curated) — flows/wake.md §6 walks a stale session through
// §2-§5 like a fresh one, and §4 offers Dismiss unconditionally, so the
// choice must stay offered and the refusal must be a per-session
// diagnostic on stderr, never a run-aborting error. This drives the stale
// session first (Dismiss, refused) and a fresh session after it (Accept):
// before the fix, dismiss.Run's error propagated straight out of the
// process (a non-zero exit, no §7 summary at all), and the fresh session
// below was never reached.
func TestWake_Interactive_StaleDismissRefused_SkipsAndContinues_ThroughTopLevelVerb(t *testing.T) {
	fx := journaltest.New(t)
	xdg := t.TempDir()

	now := time.Now()
	staleKey := journal.SessionKey{Harness: "claude", NativeID: "wake-stale-dismiss"}
	freshKey := journal.SessionKey{Harness: "claude", NativeID: "wake-fresh-after-stale"}
	staleShard := now.Add(-3 * time.Hour).Format("2006-01-02")
	freshShard := now.Add(-2 * time.Hour).Format("2006-01-02")

	// Curated against a shorter transcript than what's on disk now (M7:
	// stale = curated && transcript != transcript_at_curation) — so this
	// row already carries an entry.md, exactly the shape dismiss.Run
	// refuses.
	fx.CuratedStale(staleShard, staleKey,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 1, SHA256: "original"},
		now.Add(-3*time.Hour), now.Add(-2*time.Hour), "framework", "old title before the session grew",
	).WithTranscript(staleShard, staleKey, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello, grown"}}`+"\n"))
	fx.Captured(freshShard, freshKey, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "s-fresh"}, now.Add(-2*time.Hour)).
		WithTranscript(freshShard, freshKey, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello from fresh"}}`+"\n"))

	stub := llmtest.New(t, wakeQualifyingDraft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root(), "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	// Row order is chronological by StartedAt: staleKey (-3h) then
	// freshKey (-2h). "3" (Dismiss, refused -> skip) for the stale row,
	// then "1"/"4" (Accept, no promotions) for the fresh one.
	r := runWithStdin(t, env, "3\n1\n4\n", "wake", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --json (stale dismiss): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered int `json:"considered"`
		Accepted   int `json:"accepted"`
		Dismissed  int `json:"dismissed"`
		Skipped    int `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 2 || payload.Dismissed != 0 || payload.Skipped != 1 || payload.Accepted != 1 {
		t.Fatalf("payload = %+v, want considered=2 dismissed=0 skipped=1 accepted=1", payload)
	}
	if !strings.Contains(r.stderr, "cannot dismiss") {
		t.Errorf("stderr = %q, want a per-session diagnostic naming the refusal", r.stderr)
	}

	staleShow := run(t, env, "plumbing", "show", staleKey.DirName(), "--json")
	if staleShow.exitCode != 0 {
		t.Fatalf("plumbing show %s: exit=%d stderr=%q", staleKey.DirName(), staleShow.exitCode, staleShow.stderr)
	}
	if !strings.Contains(staleShow.stdout, `"state":"curated"`) {
		t.Errorf("stale session show = %q, want still curated (dismiss refused, unchanged)", staleShow.stdout)
	}
	freshShow := run(t, env, "plumbing", "show", freshKey.DirName(), "--json")
	if freshShow.exitCode != 0 {
		t.Fatalf("plumbing show %s: exit=%d stderr=%q", freshKey.DirName(), freshShow.exitCode, freshShow.stderr)
	}
	if !strings.Contains(freshShow.stdout, `"state":"curated"`) {
		t.Errorf("fresh session show = %q, want curated (accepted after the run continued past the refusal)", freshShow.stdout)
	}
}

// TestWake_Auto_StaleSession_ReCurates_ThroughTopLevelVerb drives `clast
// wake --auto --json` against a stale curated session (already curated,
// but its transcript grew since — MODEL M7) end to end through the built
// binary: flows/wake.md §6 says a stale session walks §2-§5 exactly like
// a fresh captured one, and accepting it re-curates in place rather than
// revoking the prior entry first. wakeverb's own unit test
// (TestRunAuto_StaleSession_OfferedAsRecuration) proves this against
// RunAuto directly; this is the same property through the actual process
// boundary — Cobra parsing, exitcode, a real llm.Client dialing a real
// loopback stub (the step brief's own ask: "stale re-curation through the
// top-level wake against a stale fixture").
func TestWake_Auto_StaleSession_ReCurates_ThroughTopLevelVerb(t *testing.T) {
	fx := journaltest.New(t)
	xdg := t.TempDir()

	now := time.Now()
	key := journal.SessionKey{Harness: "claude", NativeID: "wake-stale-01"}
	shard := now.Add(-3 * time.Hour).Format("2006-01-02")
	// Curated against a shorter transcript than what's on disk now (M7:
	// stale = curated && transcript != transcript_at_curation).
	fx.CuratedStale(shard, key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 1, SHA256: "original"},
		now.Add(-3*time.Hour), now.Add(-2*time.Hour), "framework", "old title before the session grew",
	).WithTranscript(shard, key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello, grown"}}`+"\n"))

	pre, ok, err := journal.ReadCuration(fx.Root(), shard, key)
	if err != nil || !ok || pre.State != journal.StateCurated {
		t.Fatalf("fixture precondition: curation = %+v ok=%v err=%v, want curated", pre, ok, err)
	}

	draft := "# Session: session grew, updated summary\n\n" +
		"## Goal\nCapture the additional turns the transcript picked up after curation.\n\n" +
		"## What shipped\n- Documented the newer turns.\n\n" +
		"Suggested tags: followup\n"
	stub := llmtest.New(t, draft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root(), "XDG_CONFIG_HOME=" + xdg, "CLAST_LLM_API_KEY=sk-test-key"}

	r := run(t, env, "wake", "--auto", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --auto --json (stale): exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	var payload struct {
		Considered int `json:"considered"`
		Accepted   int `json:"accepted"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("wake --auto --json stdout: %v; stdout=%q", err, r.stdout)
	}
	if payload.Considered != 1 || payload.Accepted != 1 {
		t.Fatalf("payload = %+v, want considered=1 accepted=1 (the stale session re-curated)", payload)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1", len(reqs))
	}

	show := run(t, env, "plumbing", "show", key.DirName(), "--json")
	if show.exitCode != 0 {
		t.Fatalf("plumbing show: exit=%d, want 0; stderr=%q", show.exitCode, show.stderr)
	}
	var showPayload struct {
		Curation struct {
			State string `json:"state"`
		} `json:"curation"`
		Stale bool `json:"stale"`
		Entry struct {
			Title string `json:"title"`
		} `json:"entry"`
	}
	if err := json.Unmarshal([]byte(show.stdout), &showPayload); err != nil {
		t.Fatalf("plumbing show --json stdout: %v; stdout=%q", err, show.stdout)
	}
	if showPayload.Curation.State != "curated" {
		t.Errorf("curation.state = %q, want curated", showPayload.Curation.State)
	}
	if showPayload.Stale {
		t.Error("stale = true, want false — re-curation must clear staleness")
	}
	if showPayload.Entry.Title != "session grew, updated summary" {
		t.Errorf("entry.title = %q, want the fresh re-curated draft's title, not the old one", showPayload.Entry.Title)
	}
}

// TestSeal_CrossFormParity_WakeAutoConsumesExactlyThePlumbingFacts is the
// V6 cross-form parity smoke the step brief calls for: SURFACE V6 says a
// shape is one flow, defined once, that both delivery forms read — so the
// verb form's own LLM request should carry exactly the facts the flow's
// §1-§2 plumbing calls themselves expose, not some independently derived
// rendering. This drives `plumbing wake --json`, `plumbing show
// --transcript --max-turn-chars 2000 --json`, and `plumbing breadcrumbs
// --json` raw against a fixture journal, then confirms `wake --auto`'s
// own request to the stub carries the exact transcript text `plumbing
// show` rendered and the exact breadcrumb text `plumbing breadcrumbs`
// listed. One test, not a parity framework.
func TestSeal_CrossFormParity_WakeAutoConsumesExactlyThePlumbingFacts(t *testing.T) {
	fx := journaltest.New(t)
	xdg := t.TempDir()

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	today := cutoff.DayOf(time.Now())
	yesterday, err := today.AddDays(-1)
	if err != nil {
		t.Fatalf("AddDays: %v", err)
	}

	key := journal.SessionKey{Harness: "claude", NativeID: "parity-01"}
	proj := journal.SessionProject{ID: "p-parity", Slug: "parity", Clone: "c1", Label: "dev", Path: "/dev"}
	fx.Captured(string(today), key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "s-parity"}, noonOn(t, today)).
		WithProject(string(today), key, proj).
		WithTranscript(string(today), key, []byte(`{"type":"user","uuid":"u1","message":{"content":"xyzzy-parity-marker: a distinctive line only the real transcript carries"}}`+"\n"))
	fx.Breadcrumb("framework", noonOn(t, yesterday), &proj.Slug, "plugh-parity-crumb: a distinctive breadcrumb only yesterday's note carries")

	env := []string{"CLAST_JOURNAL_DIR=" + fx.Root()}

	// §1 raw: the working set `plumbing wake` itself returns.
	wakeJSON := run(t, env, "plumbing", "wake", "--json")
	if wakeJSON.exitCode != 0 {
		t.Fatalf("plumbing wake --json: exit=%d, want 0; stderr=%q", wakeJSON.exitCode, wakeJSON.stderr)
	}
	var wakePayload struct {
		Sessions []struct {
			Harness   string `json:"harness"`
			SessionID string `json:"session_id"`
			Project   *struct {
				Slug string `json:"slug"`
			} `json:"project"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(wakeJSON.stdout), &wakePayload); err != nil {
		t.Fatalf("plumbing wake --json stdout: %v; stdout=%q", err, wakeJSON.stdout)
	}
	if len(wakePayload.Sessions) != 1 {
		t.Fatalf("plumbing wake --json sessions = %+v, want exactly 1", wakePayload.Sessions)
	}
	row := wakePayload.Sessions[0]
	if row.Project == nil || row.Project.Slug != "parity" {
		t.Fatalf("plumbing wake row project = %+v, want slug %q", row.Project, "parity")
	}
	locator := row.Harness + "-" + row.SessionID

	// §2 raw: the transcript flows/wake.md §2 says to read, capped at
	// --max-turn-chars 2000, plus yesterday's breadcrumbs for the
	// session's own project.
	showJSON := run(t, env, "plumbing", "show", locator, "--transcript", "--max-turn-chars", "2000", "--json")
	if showJSON.exitCode != 0 {
		t.Fatalf("plumbing show --transcript --json: exit=%d, want 0; stderr=%q", showJSON.exitCode, showJSON.stderr)
	}
	var showPayload struct {
		Turns []struct {
			Text string `json:"text"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(showJSON.stdout), &showPayload); err != nil {
		t.Fatalf("plumbing show --json stdout: %v; stdout=%q", err, showJSON.stdout)
	}
	if len(showPayload.Turns) != 1 || !strings.Contains(showPayload.Turns[0].Text, "xyzzy-parity-marker") {
		t.Fatalf("plumbing show turns = %+v, want the fixture's own transcript text", showPayload.Turns)
	}

	crumbsJSON := run(t, env, "plumbing", "breadcrumbs", "--day", "yesterday", "--project", row.Project.Slug, "--json")
	if crumbsJSON.exitCode != 0 {
		t.Fatalf("plumbing breadcrumbs --json: exit=%d, want 0; stderr=%q", crumbsJSON.exitCode, crumbsJSON.stderr)
	}
	var crumbsPayload struct {
		Breadcrumbs []struct {
			Text string `json:"text"`
		} `json:"breadcrumbs"`
	}
	if err := json.Unmarshal([]byte(crumbsJSON.stdout), &crumbsPayload); err != nil {
		t.Fatalf("plumbing breadcrumbs --json stdout: %v; stdout=%q", err, crumbsJSON.stdout)
	}
	if len(crumbsPayload.Breadcrumbs) != 1 || !strings.Contains(crumbsPayload.Breadcrumbs[0].Text, "plugh-parity-crumb") {
		t.Fatalf("plumbing breadcrumbs = %+v, want the fixture's own crumb", crumbsPayload.Breadcrumbs)
	}

	// Now drive the verb form: `wake --auto` must have consumed exactly
	// those two facts to build its LLM request — not some independently
	// re-derived rendering.
	stub := llmtest.New(t, wakeQualifyingDraft)
	writeLLMConfig(t, xdg, stub.URL(), "gpt-test")
	llmEnv := append(append([]string{}, env...), "XDG_CONFIG_HOME="+xdg, "CLAST_LLM_API_KEY=sk-test-key")

	r := run(t, llmEnv, "wake", "--auto", "--json")
	if r.exitCode != 0 {
		t.Fatalf("wake --auto --json: exit=%d, want 0; stderr=%q", r.exitCode, r.stderr)
	}
	reqs := stub.Requests()
	if len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1", len(reqs))
	}
	var userContent string
	for _, m := range reqs[0].Messages {
		if m.Role == "user" {
			userContent = m.Content
		}
	}
	if !strings.Contains(userContent, "xyzzy-parity-marker") {
		t.Errorf("wake --auto's own LLM request user content = %q, want it to carry plumbing show's exact transcript text", userContent)
	}
	if !strings.Contains(userContent, "plugh-parity-crumb") {
		t.Errorf("wake --auto's own LLM request user content = %q, want it to carry plumbing breadcrumbs' exact text", userContent)
	}
}
