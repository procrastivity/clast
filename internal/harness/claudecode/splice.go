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
	"github.com/procrastivity/clast/internal/harness"
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
		return SpliceResult{}, clasterr.New(harness.CodeMalformedSplice,
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

// SpliceStatus probes settings.json for the shim entry's drift state,
// read-only (SURFACE V28, step-04): unlike Splice, it never writes, so a
// bare `clast doctor` run can never install or repair the hook as a side
// effect of reporting on it. States, in order:
//
//   - No settings.json at all, or one that exists but carries no
//     recognizable shim entry: harness.SpliceAbsent. A missing file reads
//     the same as an empty one Splice would treat as `{}` — neither is
//     drift on its own; checks.go decides that by weighing it against
//     whether any skill is actually installed.
//   - A settings.json that exists but is not valid JSON:
//     harness.SpliceMalformed.
//   - A hooks.SessionStart hook whose command is exactly ShimCommand:
//     harness.SpliceCurrent.
//   - A hooks.SessionStart hook whose command contains shimMarker but is
//     not exactly ShimCommand — someone edited the pinned string in
//     place: harness.SpliceTampered.
func SpliceStatus() (harness.SpliceProbe, error) {
	path, err := SettingsPath()
	if err != nil {
		return harness.SpliceProbe{}, err
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return harness.SpliceProbe{Path: path, State: harness.SpliceAbsent}, nil
	case err != nil:
		return harness.SpliceProbe{}, fmt.Errorf("claudecode: reading %q: %w", path, err)
	}

	if !gjson.ValidBytes(data) {
		return harness.SpliceProbe{Path: path, State: harness.SpliceMalformed}, nil
	}
	if hasShimEntry(data) {
		return harness.SpliceProbe{Path: path, State: harness.SpliceCurrent}, nil
	}
	if hasTamperedShimEntry(data) {
		return harness.SpliceProbe{Path: path, State: harness.SpliceTampered}, nil
	}
	return harness.SpliceProbe{Path: path, State: harness.SpliceAbsent}, nil
}

// hasTamperedShimEntry reports whether data's hooks.SessionStart array
// carries a hook whose command contains shimMarker — it looks like it was
// meant to be the clast shim — but is not exactly ShimCommand. Used only by
// SpliceStatus: hasShimEntry already ruled out an exact match by the time
// this runs.
func hasTamperedShimEntry(data []byte) bool {
	found := false
	gjson.GetBytes(data, sessionStartPath).ForEach(func(_, group gjson.Result) bool {
		group.Get("hooks").ForEach(func(_, hook gjson.Result) bool {
			cmd := hook.Get("command").String()
			if cmd != ShimCommand && strings.Contains(cmd, shimMarker) {
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

// Unsplice's outcome codes (registry.Harness.Unsplice wraps these into
// registry.SpliceOutcome, the same shape Splice's codes ride in).
const (
	// UnspliceStatusUnspliced means Unsplice found and removed the shim
	// hook entry: settings.json was just rewritten.
	UnspliceStatusUnspliced = "unspliced"
	// UnspliceStatusNotSpliced means there was nothing to remove — no
	// settings.json, or a settings.json with no hook entry carrying
	// ShimCommand's exact bytes. Unsplice touched nothing.
	UnspliceStatusNotSpliced = "not-spliced"
)

// Unsplice reverses exactly what Splice adds (SURFACE V32/V33, C4.8):
// it removes the one hooks.SessionStart[*].hooks[*] entry whose command
// byte-matches ShimCommand, via the same gjson/sjson path-edit discipline
// Splice uses, so every unrelated byte — other keys, other hooks, other
// SessionStart groups — survives untouched.
//
// Symmetric with Splice's idempotency: a missing settings.json, or one
// with no matching hook, is a no-op (UnspliceStatusNotSpliced) rather than
// a diagnostic. C4.7's "refuse on a missing stamp" posture governs a
// stamped tree (the skills UninstallSkill removes); the splice target
// carries no stamp of its own (registry.SpliceOutcome's doc comment) and
// so is not bound by that clause. Splice itself never errors on an
// already-spliced or missing-file input — it reports a status and moves
// on — and Unsplice mirrors that: calling `clast uninstall claude-code`
// against a settings.json a human already hand-edited the hook out of
// (or that never existed) must not fail the run.
//
// Emptied-group rule: removing the matching hook can leave its enclosing
// hooks.SessionStart[i] group's own "hooks" array empty. When that
// happens, Unsplice removes that whole group entry too — leaving an
// empty `{"matcher":"","hooks":[]}` litter behind would be a group Splice
// never wrote in that shape. A sibling group's other hooks (or a
// SessionStart group some other tool added) are left completely alone.
// The same emptying check then cascades one level up at each container
// Splice itself auto-vivifies: if removing the group leaves
// hooks.SessionStart itself an empty array, that key is removed; if that
// then leaves "hooks" an empty object, that key is removed too. This is
// what makes an install/uninstall round trip restore settings.json to its
// exact pre-install bytes when hooks/hooks.SessionStart were both wholly
// absent before install: Splice created both from nothing, so Unsplice
// deleting them again is exactly undoing that creation.
//
// The trade this buys, stated plainly (F2 — the prior wording here claimed
// the opposite): Splice's sjson path write auto-vivifies straight INTO an
// already-present empty container the exact same way it creates a new
// one, so a settings.json a human wrote as `"hooks": {}` or `"hooks":
// {"SessionStart": []}` before install *does* reach Unsplice — the shim
// lands inside the human's own container, now indistinguishable from one
// Splice made itself. The cascade has no way to tell "I created this"
// from "this was already here, empty, on purpose," so it deletes the
// human's key right along with its own: `{"hooks":{}}` before install
// becomes `{}` after an install/uninstall round trip, not the human's
// `{"hooks":{}}` restored, and the same happens to a pre-existing
// `"hooks":{"SessionStart":[]}`. See splice_test.go/unsplice_test.go's
// human_empty_hooks_container* and human_empty_session_start_array*
// goldens for both shapes pinned byte-for-byte. This is accepted, not
// fixed: telling the two cases apart would mean Splice recording which
// containers it created versus reused, extra bookkeeping to protect an
// empty container that carries no behavior of its own either way.
//
// The .bak Splice writes on a settings.json's first-ever splice is never
// touched here — not restored over the current file, not deleted.
// Splice's own doc comment already frames it as a one-time snapshot
// (C4.8); once written it is the user's file, kept only as their own
// escape hatch if they want it, and Unsplice has no business deciding
// whether to give it back.
func Unsplice() (UnspliceResult, error) {
	path, err := SettingsPath()
	if err != nil {
		return UnspliceResult{}, err
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return UnspliceResult{Path: path, Status: UnspliceStatusNotSpliced}, nil
	case err != nil:
		return UnspliceResult{}, fmt.Errorf("claudecode: reading %q: %w", path, err)
	}

	if !gjson.ValidBytes(data) {
		return UnspliceResult{}, clasterr.New(harness.CodeMalformedSplice,
			fmt.Sprintf("%s is not valid JSON; fix it by hand before re-running `clast uninstall %s`", path, Name))
	}

	groupIdx, hookIdx, found := findShimEntry(data)
	if !found {
		return UnspliceResult{Path: path, Status: UnspliceStatusNotSpliced}, nil
	}

	out, err := removeShimEntry(data, groupIdx, hookIdx)
	if err != nil {
		return UnspliceResult{}, err
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return UnspliceResult{}, fmt.Errorf("claudecode: writing %q: %w", path, err)
	}

	return UnspliceResult{Path: path, Status: UnspliceStatusUnspliced}, nil
}

// UnspliceResult reports what one Unsplice call did.
type UnspliceResult struct {
	// Path is SettingsPath()'s value — the file Unsplice read and, if it
	// wrote, wrote back.
	Path string
	// Status is UnspliceStatusUnspliced or UnspliceStatusNotSpliced.
	Status string
}

// findShimEntry locates the first hooks.SessionStart[groupIdx].hooks[hookIdx]
// entry whose command is exactly ShimCommand, walking groups and their
// hooks in order — the same scan hasShimEntry runs, just reporting the
// position instead of a bare bool. ok is false, with groupIdx/hookIdx
// meaningless, when no such entry exists.
func findShimEntry(data []byte) (groupIdx, hookIdx int, ok bool) {
	groups := gjson.GetBytes(data, sessionStartPath).Array()
	for gi, group := range groups {
		hooks := group.Get("hooks").Array()
		for hi, hook := range hooks {
			if hook.Get("command").String() == ShimCommand {
				return gi, hi, true
			}
		}
	}
	return 0, 0, false
}

// removeShimEntry deletes hooks.SessionStart[groupIdx].hooks[hookIdx] from
// data, then cascades the emptied-group rule upward: an emptied group is
// removed from hooks.SessionStart, an emptied hooks.SessionStart is
// removed from hooks, and an emptied hooks is removed entirely — each
// check only fires when that container is genuinely empty, so a
// non-empty sibling group, sibling hook, or sibling top-level hooks.*
// key (like PreToolUse) is left exactly as it was.
func removeShimEntry(data []byte, groupIdx, hookIdx int) ([]byte, error) {
	hookPath := fmt.Sprintf("%s.%d.hooks.%d", sessionStartPath, groupIdx, hookIdx)
	out, err := sjson.DeleteBytes(data, hookPath)
	if err != nil {
		return nil, fmt.Errorf("claudecode: removing shim hook: %w", err)
	}

	groupPath := fmt.Sprintf("%s.%d", sessionStartPath, groupIdx)
	if len(gjson.GetBytes(out, groupPath+".hooks").Array()) == 0 {
		out, err = sjson.DeleteBytes(out, groupPath)
		if err != nil {
			return nil, fmt.Errorf("claudecode: removing emptied SessionStart group: %w", err)
		}
	}

	if len(gjson.GetBytes(out, sessionStartPath).Array()) == 0 {
		out, err = sjson.DeleteBytes(out, sessionStartPath)
		if err != nil {
			return nil, fmt.Errorf("claudecode: removing emptied hooks.SessionStart: %w", err)
		}
	}

	if len(gjson.GetBytes(out, "hooks").Map()) == 0 {
		out, err = sjson.DeleteBytes(out, "hooks")
		if err != nil {
			return nil, fmt.Errorf("claudecode: removing emptied hooks: %w", err)
		}
	}

	return out, nil
}

// writeBakOnce copies original to path+".bak", unless a .bak already
// exists there — C4.8's one-time backup: the first splice ever preserves
// the pre-splice file, and nothing after it ever overwrites that copy.
// The .bak is written with path's own permission bits (F4), not a fixed
// 0644: settings.json can carry secrets (API keys, tokens in env blocks),
// so a backup copy must never be more permissive than the file it copies.
func writeBakOnce(path string, original []byte) error {
	bak := path + ".bak"
	switch _, err := os.Stat(bak); {
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return fmt.Errorf("claudecode: checking %q: %w", bak, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("claudecode: checking %q: %w", path, err)
	}
	if err := os.WriteFile(bak, original, info.Mode().Perm()); err != nil {
		return fmt.Errorf("claudecode: writing %q: %w", bak, err)
	}
	return nil
}
