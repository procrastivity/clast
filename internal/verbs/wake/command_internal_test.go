package wake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/config"
)

// writeConfigOverride plants config.yaml under a fresh $XDG_CONFIG_HOME/clast
// tree and points the process there for the duration of the test —
// config.Load's override link (internal/verbs/asset/asset_test.go's
// writeOverride, mirrored here for config.yaml specifically).
func writeConfigOverride(t *testing.T, content string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "clast", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConfiguredAutoMinChars covers the SURFACE V8 amendment (2026-09-12):
// wake's --json payload carries the merged wake.auto_min_chars value.
// configuredAutoMinChars reads config.Load's own merged result — these
// cases exercise the read, not the merge itself (config.Load's Load
// already merges config.yaml over config.default.yaml, override winning
// key-by-key; TestConfiguredAutoMinChars_OverrideWinsOverShippedDefault
// below proves that merge lands the value this function then reads).
func TestConfiguredAutoMinChars(t *testing.T) {
	if v, err := configuredAutoMinChars(config.Config{}); err != nil || v != defaultAutoMinChars {
		t.Errorf("absent key: %v %v, want %d, nil", v, err, defaultAutoMinChars)
	}
	if v, err := configuredAutoMinChars(config.Config{"wake": map[string]any{"auto_min_chars": 90}}); err != nil || v != 90 {
		t.Errorf("explicit override: %v %v, want 90, nil", v, err)
	}
	// config.Load's actual shape: yaml.v3 types nested mappings as the
	// parent map's type, config.Config (mirrors capture.autoDismissNoop's
	// own dual-shape acceptance).
	if v, err := configuredAutoMinChars(config.Config{"wake": config.Config{"auto_min_chars": 90}}); err != nil || v != 90 {
		t.Errorf("nested config.Config override: %v %v, want 90, nil", v, err)
	}
	if v, err := configuredAutoMinChars(config.Config{"wake": map[string]any{}}); err != nil || v != defaultAutoMinChars {
		t.Errorf("section present, key absent: %v %v, want %d, nil", v, err, defaultAutoMinChars)
	}
	if _, err := configuredAutoMinChars(config.Config{"wake": "sixty"}); err == nil {
		t.Error("mistyped section accepted")
	}
	if _, err := configuredAutoMinChars(config.Config{"wake": map[string]any{"auto_min_chars": "sixty"}}); err == nil {
		t.Error("mistyped value accepted")
	}
}

// TestConfiguredAutoMinChars_OverrideWinsOverShippedDefault proves the
// merged-value resolution the V8 amendment requires: config.yaml's
// wake.auto_min_chars wins over config.default.yaml's shipped 60,
// reusing config.Load's own asset-chain resolution (asset.ReadDefault /
// asset.ReadOverride) rather than hand-building the merge — the same
// route `plumbing wake --json` runs through.
func TestConfiguredAutoMinChars_OverrideWinsOverShippedDefault(t *testing.T) {
	writeConfigOverride(t, "wake:\n  auto_min_chars: 120\n")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got, err := configuredAutoMinChars(cfg)
	if err != nil {
		t.Fatalf("configuredAutoMinChars: %v", err)
	}
	if got != 120 {
		t.Errorf("configuredAutoMinChars = %d, want 120 (config.yaml override over the shipped 60)", got)
	}
}
