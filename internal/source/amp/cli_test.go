package amp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ampScript writes a shell stub for the real exec path. Cases match on
// the space-joined argv; FIXTURE is baked in as the export payload.
func ampScript(t *testing.T, fixtureRel string) string {
	t.Helper()
	fixture := filepath.Join(mustGetwd(t), "testdata", fixtureRel)
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  "threads export T-good") cat %q ;;
  "threads export T-huge") head -c 200000 /dev/zero | tr '\0' 'x' ;;
  "threads export T-slow") exec sleep 30 ;;
  "threads export T-gone") echo "Error: Thread T-gone does not exist." >&2; exit 1 ;;
  "threads export T-auth") echo "Error: not authenticated — run amp login" >&2; exit 1 ;;
  "threads list --include-archived --limit 500 --json") printf '[]' ;;
  "threads search"*) printf '[]' ;;
  *) echo "unexpected argv: $*" >&2; exit 2 ;;
esac
`, fixture)
	return writeExecutable(t, script)
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// TestRealRunnerHappyPath runs the actual subprocess code: LookPath on
// the stub, exec, streams captured, exit 0.
func TestRealRunnerHappyPath(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = ampScript(t, "exports/idle-local-client.json")
	res, err := s.cli(context.Background(), []string{"threads", "export", "T-good"}, s.exportBudget)
	if err != nil {
		t.Fatalf("cli: %v", err)
	}
	doc, err := ParseExport(res.stdout)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.ID != "T-01a050ef-71e8-74bc-9b63-c0ad20e3a068" {
		t.Errorf("doc id = %q", doc.ID)
	}
}

// TestRealRunnerCap kills an oversized export mid-stream — the bounded
// output guarantee.
func TestRealRunnerCap(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = ampScript(t, "exports/idle-local-client.json")
	s.exportBudget.maxBytes = 4096
	_, err := s.cli(context.Background(), []string{"threads", "export", "T-huge"}, s.exportBudget)
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errCap {
		t.Fatalf("want errCap, got %v", err)
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("cap error %q should name the bound", err)
	}
}

// TestRealRunnerTimeout kills a hanging call at its budget — and the
// error is an ordinary bounded failure, never context.DeadlineExceeded
// (which the run layer reads as fatal cancellation).
func TestRealRunnerTimeout(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = ampScript(t, "exports/idle-local-client.json")
	s.exportBudget.timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := s.cli(context.Background(), []string{"threads", "export", "T-slow"}, s.exportBudget)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errTimeout {
		t.Fatalf("want errTimeout, got %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Error("per-call timeout must not unwrap context.DeadlineExceeded — that is the fatal-cancel signal")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("timeout did not bound the call")
	}
}

// TestRealRunnerCancellation: the caller's own ctx teardown propagates
// as the context error — fatal per the contract, never reclassified.
func TestRealRunnerCancellation(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = ampScript(t, "exports/idle-local-client.json")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := s.cli(ctx, []string{"threads", "export", "T-slow"}, s.exportBudget)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestRealRunnerExitClassification pins stderr→kind mapping on the real
// exec path.
func TestRealRunnerExitClassification(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = ampScript(t, "exports/idle-local-client.json")
	ctx := context.Background()

	_, err := s.cli(ctx, []string{"threads", "export", "T-gone"}, s.exportBudget)
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errNotExist {
		t.Fatalf("T-gone: want errNotExist, got %v", err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error %q should carry the stderr text", err)
	}

	_, err = s.cli(ctx, []string{"threads", "export", "T-auth"}, s.exportBudget)
	ce = nil
	if !errors.As(err, &ce) || ce.kind != errAuth {
		t.Fatalf("T-auth: want errAuth, got %v", err)
	}
}

// TestRealRunnerNoBinary: an unresolvable binary is the absent transport.
func TestRealRunnerNoBinary(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.bin = filepath.Join(t.TempDir(), "no-such-amp")
	_, err := s.cli(context.Background(), []string{"threads", "list", "--json"}, s.listBudget)
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errSpawn {
		t.Fatalf("want errSpawn, got %v", err)
	}
}

// TestSeamCapEnforcement: an injected runner that over-returns still
// hits the cap at the seam boundary.
func TestSeamCapEnforcement(t *testing.T) {
	s := newTestSource(t, t.TempDir())
	s.exportBudget.maxBytes = 16
	s.run = func(_ context.Context, _ []string, _ callBudget) (cliResult, error) {
		return cliResult{stdout: make([]byte, 64)}, nil
	}
	_, err := s.cli(context.Background(), []string{"threads", "export", "T-x"}, s.exportBudget)
	var ce *cliError
	if !errors.As(err, &ce) || ce.kind != errCap {
		t.Fatalf("want errCap from an over-returning fake, got %v", err)
	}
}
