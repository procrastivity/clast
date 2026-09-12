package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SettingsPathEnv is an environment variable that, when set, overrides
// SettingsPath()'s result — a test seam only (C2.6), never read for any
// other purpose.
const SettingsPathEnv = "CLAST_CLAUDE_SETTINGS_PATH"

// SettingsPath returns the Claude Code settings.json file the splice edits:
// ~/.claude/settings.json (the same root SkillsDir and Available key off).
func SettingsPath() (string, error) {
	if p := os.Getenv(SettingsPathEnv); p != "" {
		return p, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudecode: locating home directory: %w", err)
	}
	return filepath.Join(rootDir(home), "settings.json"), nil
}

// sessionStartPath is settings.json's SessionStart hooks array, in gjson/
// sjson dot-path syntax.
const sessionStartPath = "hooks.SessionStart"

// Splice's outcome codes (registry.Harness.Splice wraps these into
// registry.SpliceOutcome).
const (
	// SpliceStatusSpliced means Splice wrote the hook entry: settings.json
	// (and, the first time only, its .bak) were just written.
	SpliceStatusSpliced = "spliced"
	// SpliceStatusAlreadySpliced means the exact ShimCommand was already
	// present under hooks.SessionStart; Splice touched nothing (idempotent
	// re-install, C4.8).
	SpliceStatusAlreadySpliced = "already-spliced"
)

// SpliceResult reports what one Splice call did.
type SpliceResult struct {
	// Path is SettingsPath()'s value — the file Splice read and, if it
	// wrote, wrote back.
	Path string
	// Status is SpliceStatusSpliced or SpliceStatusAlreadySpliced.
	Status string
}

// Splice idempotently installs the V33 SessionStart shim into
// settings.json's hooks.SessionStart array (SURFACE V32/V33, C4.8):
// gjson/sjson path edits on the raw bytes, never a map[string]any
// round-trip (which would destroy key order and number fidelity), so every
// unrelated key survives byte-for-byte.
//
// Idempotency rule: Splice searches every existing hooks.SessionStart[*]
// group's hooks[*].command for ShimCommand's exact bytes. If found,
// nothing is written and it reports SpliceStatusAlreadySpliced — a
// re-install (or two harnesses' installs racing) never duplicates the
// hook. Otherwise it appends one new group,
// `{"matcher":"","hooks":[{"type":"command","command":ShimCommand}]}`, to
// the end of hooks.SessionStart (creating hooks/hooks.SessionStart if
// either is absent) and reports SpliceStatusSpliced.
//
// Missing settings.json is treated as `{}` — an empty object splices in
// cleanly with no distinct code path. Before the first-ever write to an
// existing file, Splice copies its current bytes to a sibling .bak — but
// only that once: an existing .bak is never overwritten (C4.8), and a
// no-op (already spliced, or nothing to back up because the file did not
// exist) never creates one either.
func Splice() (SpliceResult, error) {
	path, err := SettingsPath()
	if err != nil {
		return SpliceResult{}, err
	}

	data, err := os.ReadFile(path)
	missing := false
	switch {
	case errors.Is(err, os.ErrNotExist):
		missing = true
		data = []byte("{}")
	case err != nil:
		return SpliceResult{}, fmt.Errorf("claudecode: reading %q: %w", path, err)
	}

	if !missing && !gjson.ValidBytes(data) {
		return SpliceResult{}, clasterr.New("validation.malformed-settings",
			fmt.Sprintf("%s is not valid JSON; fix it by hand before re-running `clast install %s`", path, Name))
	}

	if hasShimEntry(data) {
		return SpliceResult{Path: path, Status: SpliceStatusAlreadySpliced}, nil
	}

	if !missing {
		if err := writeBakOnce(path, data); err != nil {
			return SpliceResult{}, err
		}
	}

	entry, err := shimEntryJSON()
	if err != nil {
		return SpliceResult{}, err
	}
	out, err := sjson.SetRawBytes(data, sessionStartPath+".-1", entry)
	if err != nil {
		return SpliceResult{}, fmt.Errorf("claudecode: splicing %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return SpliceResult{}, fmt.Errorf("claudecode: creating %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return SpliceResult{}, fmt.Errorf("claudecode: writing %q: %w", path, err)
	}

	return SpliceResult{Path: path, Status: SpliceStatusSpliced}, nil
}

// hasShimEntry reports whether data's hooks.SessionStart array already
// carries a hook entry whose command is exactly ShimCommand, anywhere
// among its groups — the idempotency check a re-install relies on.
func hasShimEntry(data []byte) bool {
	found := false
	gjson.GetBytes(data, sessionStartPath).ForEach(func(_, group gjson.Result) bool {
		group.Get("hooks").ForEach(func(_, hook gjson.Result) bool {
			if hook.Get("command").String() == ShimCommand {
				found = true
				return false
			}
			return true
		})
		return !found
	})
	return found
}

// shimEntryJSON renders the one new hooks.SessionStart group Splice
// appends: an empty matcher (every session-start reason) firing exactly
// one command hook, ShimCommand.
func shimEntryJSON() ([]byte, error) {
	command, err := marshalJSONString(ShimCommand)
	if err != nil {
		return nil, fmt.Errorf("claudecode: encoding shim command: %w", err)
	}
	return []byte(`{"matcher":"","hooks":[{"type":"command","command":` + command + `}]}`), nil
}

// marshalJSONString renders s as a JSON string literal with HTML escaping
// off: encoding/json.Marshal's default HTML-safe mode would rewrite
// ShimCommand's `>`, `<`, and `&` bytes as `>`/`<`/`&`,
// which is needless obfuscation in a config file a human may open (it
// still parses to the same bytes, but reads as noise) — settings.json is
// not embedded in HTML/JS, so there is nothing here for that escaping to
// protect against.
func marshalJSONString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// writeBakOnce copies original to path+".bak", unless a .bak already
// exists there — C4.8's one-time backup: the first splice ever preserves
// the pre-splice file, and nothing after it ever overwrites that copy.
func writeBakOnce(path string, original []byte) error {
	bak := path + ".bak"
	switch _, err := os.Stat(bak); {
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return fmt.Errorf("claudecode: checking %q: %w", bak, err)
	}
	if err := os.WriteFile(bak, original, 0o644); err != nil {
		return fmt.Errorf("claudecode: writing %q: %w", bak, err)
	}
	return nil
}
