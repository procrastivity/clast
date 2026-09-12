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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
// Add each new harness's seam var to this list when adding a target.
func hermeticEnv(t *testing.T, env []string) []string {
	t.Helper()
	out := append(os.Environ(), env...)
	for _, name := range []string{"CLAST_CLAUDE_SKILLS_DIR", "CLAST_JOURNAL_DIR"} {
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
		if strings.Contains(r.stdout, verb) {
			t.Errorf("stdout = %q, want it NOT to list plumbing verb %q among the porcelain", r.stdout, verb)
		}
	}
}
