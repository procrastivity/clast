package amp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// correlateSource is a fixture-identity source (device-id.json's
// installationID) with its export transport faked.
func correlateSource(t *testing.T, exports map[string][]byte) *Source {
	t.Helper()
	f := &fakeCLI{t: t, exports: exports}
	s := newTestSource(t, fixtureDataDir(t))
	s.run = f.run
	return s
}

// correlateFixture wires one export fixture and returns its verdict.
func correlateFixture(t *testing.T, s *Source, rel string) string {
	t.Helper()
	raw := mustFixture(t, rel)
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	s.run = f.run
	dir, diags, err := s.Correlate(context.Background(), discoveredForID(t, raw))
	if err != nil || len(diags) != 0 {
		t.Fatalf("Correlate(%s): diags=%v err=%v", rel, diags, err)
	}
	return dir
}

// TestCorrelateLocal: an export born under this machine's install id
// binds to its workingDirectory — the file:// URI decoded.
func TestCorrelateLocal(t *testing.T) {
	s := correlateSource(t, nil)
	got := correlateFixture(t, s, "exports/idle-archived-pinned.json")
	want := "/tmp/claude-1000/-home-dev-Code-terminal-multiplexers/0e7b572a-a744-475d-a93c-143b218b3a0f/scratchpad/ampb1/work1"
	if got != want {
		t.Errorf("Correlate = %q, want %q", got, want)
	}
}

// TestCorrelateDeclines: every reason a thread must NOT be guessed into
// a local clone returns "" — projectless but capturable.
func TestCorrelateDeclines(t *testing.T) {
	s := correlateSource(t, nil)
	for _, tc := range []struct {
		name, rel, why string
	}{
		{"foreign-install", "exports/idle-local-client.json", "installationID is a different install"},
		{"foreign-runner", "exports/foreign-runner.json", "other install AND other hostname"},
		{"sandbox", "exports/sandbox.json", "executorType sandbox"},
		{"virtual", "exports/virtual.json", "executorType virtual"},
		{"old-epoch", "exports/old-epoch.json", "foreign install, no lkas"},
	} {
		if got := correlateFixture(t, s, tc.rel); got != "" {
			t.Errorf("%s: Correlate = %q, want \"\" (%s)", tc.name, got, tc.why)
		}
	}
}

// TestCorrelateHostnameVeto: a matching install id is still declined
// when the export's hostname contradicts this machine — shared-install
// setups (synced dotfiles) must never map a remote path onto a local
// clone.
func TestCorrelateHostnameVeto(t *testing.T) {
	raw := patchedExport(t, "exports/idle-archived-pinned.json", func(m map[string]any) {
		init := m["env"].(map[string]any)["initial"].(map[string]any)
		init["hostname"] = "not-fixture-host"
	})
	s := correlateSource(t, nil)
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	s.run = f.run
	dir, _, err := s.Correlate(context.Background(), discoveredForID(t, raw))
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if dir != "" {
		t.Errorf("contradictory hostname must decline, got %q", dir)
	}
}

// TestCorrelateNoEnv: an export with no env block at all (projectless
// provenance) binds to nothing.
func TestCorrelateNoEnv(t *testing.T) {
	raw := patchedExport(t, "exports/idle-archived-pinned.json", func(m map[string]any) {
		delete(m, "env")
	})
	s := correlateSource(t, nil)
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	s.run = f.run
	dir, _, err := s.Correlate(context.Background(), discoveredForID(t, raw))
	if err != nil || dir != "" {
		t.Fatalf("env-less thread: dir=%q err=%v, want \"\"", dir, err)
	}
}

// TestCorrelateVerdictCacheSurvivesInstances pins the step-12
// checkpoint verdict: a correlation answer — "" included, projectless
// is a verdict — is recorded in the scope's scan state keyed on the
// enumerated revision and the answering host. A fresh Source instance
// over the same scope (the next sweep's process) reads it back and
// spends zero exports re-answering; a moved revision or another host
// is new evidence and fetches again.
func TestCorrelateVerdictCacheSurvivesInstances(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json") // foreign install → the "" verdict
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	dataDir := fixtureDataDir(t)
	cacheDir := t.TempDir()
	d := discoveredForID(t, raw)

	newInstance := func() *Source {
		s := newTestSource(t, dataDir)
		s.run = f.run
		s.cacheDir = cacheDir
		return s
	}

	// Run one pays the export and records the verdict.
	s1 := newInstance()
	if dir, _, err := s1.Correlate(context.Background(), d); err != nil || dir != "" {
		t.Fatalf("Correlate: dir=%q err=%v, want the projectless verdict", dir, err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d, want the one real fetch", n)
	}

	// Run two — a new instance with cold memory — answers from the
	// checkpoint: the same id + enumerated revision + host, so the
	// recorded "" returns with zero fetches.
	s2 := newInstance()
	if dir, _, err := s2.Correlate(context.Background(), d); err != nil || dir != "" {
		t.Fatalf("cached Correlate: dir=%q err=%v", dir, err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d — a recorded verdict must not refetch", n)
	}

	// A moved enumerated revision is a different question → fresh fetch.
	d2 := d
	d2.ModTime = d2.ModTime.Add(time.Hour)
	if _, _, err := s2.Correlate(context.Background(), d2); err != nil {
		t.Fatalf("Correlate (moved revision): %v", err)
	}
	if n := f.exportCalls(); n != 2 {
		t.Fatalf("exportCalls = %d, want 2 — a new revision refetches", n)
	}

	// The verdict is a local path answer — another host can never
	// borrow it. A third instance on a different hostname refetches.
	s3 := newInstance()
	s3.hostname = func() (string, error) { return "other-host", nil }
	if _, _, err := s3.Correlate(context.Background(), d); err != nil {
		t.Fatalf("Correlate (other host): %v", err)
	}
	if n := f.exportCalls(); n != 3 {
		t.Fatalf("exportCalls = %d, want 3 — another host's verdict is never borrowed", n)
	}
}

// TestCorrelateVerdictScopeIsolation: a verdict recorded under one
// journal scope never answers under another — the checkpoint is
// per-scope, so a different journal target fetches fresh.
func TestCorrelateVerdictScopeIsolation(t *testing.T) {
	raw := mustFixture(t, "exports/idle-local-client.json")
	f := &fakeCLI{t: t, exports: map[string][]byte{docID(t, raw): raw}}
	dataDir := fixtureDataDir(t)
	cacheDir := t.TempDir()
	d := discoveredForID(t, raw)

	s1 := newTestSource(t, dataDir)
	s1.run = f.run
	s1.cacheDir = cacheDir
	s1.ScopeJournal(t.TempDir())
	if _, _, err := s1.Correlate(context.Background(), d); err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if n := f.exportCalls(); n != 1 {
		t.Fatalf("exportCalls = %d", n)
	}

	// Same install, different journal target → a different scope's
	// checkpoint: no verdict recorded there.
	s2 := newTestSource(t, dataDir)
	s2.run = f.run
	s2.cacheDir = cacheDir
	s2.ScopeJournal(t.TempDir())
	if _, _, err := s2.Correlate(context.Background(), d); err != nil {
		t.Fatalf("Correlate (other scope): %v", err)
	}
	if n := f.exportCalls(); n != 2 {
		t.Fatalf("exportCalls = %d — a foreign scope must fetch its own answer", n)
	}
}

// TestCorrelateNoLocalInstall: without device-id.json every thread is
// foreign — and the verdict is free: no export fetch at all.
func TestCorrelateNoLocalInstall(t *testing.T) {
	s := newTestSource(t, t.TempDir()) // no device-id.json
	f := &fakeCLI{t: t}
	s.run = f.run
	dir, diags, err := s.Correlate(context.Background(), discoveredForID(t,
		mustFixture(t, "exports/idle-archived-pinned.json")))
	if err != nil || dir != "" || len(diags) != 0 {
		t.Fatalf("Correlate with no local install: dir=%q diags=%v err=%v", dir, diags, err)
	}
	if n := len(f.calls); n != 0 {
		t.Errorf("a no-local-install verdict must not fetch; %d calls made", n)
	}
}

// TestCorrelateExportFailure: a fetch failure on the correlate path is
// a real error — it is never swallowed into "projectless".
func TestCorrelateExportFailure(t *testing.T) {
	s := correlateSource(t, nil)
	s.run = func(_ context.Context, _ []string, _ callBudget) (cliResult, error) {
		return cliResult{code: 1, stderr: mustFixture(t, "errors/offline.txt")}, nil
	}
	d := discoveredForID(t, mustFixture(t, "exports/idle-archived-pinned.json"))
	_, _, err := s.Correlate(context.Background(), d)
	if err == nil {
		t.Fatal("transport failure must surface, not degrade to projectless")
	}
}

// TestFileURIToPath pins the URI→path decoding rules: file:// only,
// local host only, absolute only.
func TestFileURIToPath(t *testing.T) {
	for uri, want := range map[string]string{
		"file:///home/dev/proj":          "/home/dev/proj",
		"file:///home/dev/a%20b":         "/home/dev/a b",
		"file://localhost/home/dev/proj": "/home/dev/proj",
		"file://remotehost/home/dev":     "",
		"https://example.com/x":          "",
		"ssh://host/path":                "",
		"relative/path":                  "",
		"":                               "",
		"file://":                        "",
	} {
		if got := fileURIToPath(uri); got != want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", uri, got, want)
		}
	}
}

// patchedExport rewrites one field of a fixture doc for a correlation
// case the pinned set doesn't carry (e.g. a matching install id plus a
// foreign hostname).
func patchedExport(t *testing.T, rel string, patch func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(mustFixture(t, rel), &m); err != nil {
		t.Fatal(err)
	}
	patch(m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
