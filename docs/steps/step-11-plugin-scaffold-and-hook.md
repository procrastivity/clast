---
step: 11
title: plugin-scaffold-and-hook
depends_on: [01, 03, 06]
size: small
references:
  - docs/overview.md#plugin-surface-cheatsheet
  - docs/overview.md#claude-code-plugin-+-skills
  - docs/cli-contract.md#clast-snapshot
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#plugin-files
  - docs/repo-bootstrap.md#filesystem-layout
  - docs/repo-bootstrap.md#test-strategy
---

# Step 11: Plugin scaffold and SessionStart hook

## Context

Step 01 created the `.claude-plugin/` and `hooks/` directories as empty placeholders (each with a `.gitkeep` and nothing else). Step 03 produced the `bin/clast` dispatcher that the hook will invoke, and step 06 produced `clast snapshot` — the actual command the hook backgrounds. Steps 12 and 13 (the `/day-wakeup` and `/wakeup` skills) will populate `.claude-plugin/skills/` later; this step does not write any skill content and does not touch `.claude-plugin/skills/.gitkeep` except to leave it in place so the directory stays committed until step 12 lands its first skill.

The hook surface is described in `docs/overview.md#plugin-surface-cheatsheet`: a `SessionStart` hook that "backgrounds `clast snapshot` on every session start" so capture happens unattended on every Claude Code launch. The hook is the *only* automatic capture surface in v1 (alongside the user-managed cron sample that step 18 will document); everything else is user-initiated.

The plugin manifest shape is in `docs/repo-bootstrap.md#plugin-files`: `plugin.json` carries only metadata (name is the one strictly-required field per the direnv-session-loader reference) and `hooks/hooks.json` declares one entry that points at `${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh`. `${CLAUDE_PLUGIN_ROOT}` is the env var Claude Code sets to the installed plugin root at hook invocation time; the script itself derives its own root from `$0` for portability across direct invocation and via-Claude invocation.

**Run `direnv allow` (or `nix develop`) before starting** so `jq` and `shellcheck` are on PATH.

## Goal

Ship three files that turn the existing empty placeholder directories into a real Claude Code plugin:

1. `.claude-plugin/plugin.json` — the plugin manifest.
2. `hooks/hooks.json` — declares one `SessionStart` hook.
3. `hooks/snapshot.sh` — a short bash script that backgrounds `clast snapshot` and returns immediately (committed with mode `0755`).

Plus a small `test/test-plugin-hook.sh` integration suite that lints the script, validates both JSON files, and asserts the hook exits 0 promptly and the backgrounded `clast snapshot` eventually runs against a synthetic journal.

No subcommand code, no library changes, no fixture additions. `bin/clast` is read but not modified.

## References

Read before starting:

- `docs/overview.md#plugin-surface-cheatsheet` — the one-liner contract for the `SessionStart` hook ("Backgrounds `clast snapshot` on every session start. Free auto-capture."). Treat that sentence as the spec: it must background, it must not block, and it must run on *every* session start with no other gating.
- `docs/overview.md#claude-code-plugin-+-skills` — confirms the hook is the auto-capture surface; the cron / systemd-timer examples in `examples/cron/` are a *parallel* automation path, not a replacement.
- `docs/cli-contract.md#clast-snapshot` — `clast snapshot` is silent when no work was done, idempotent, and explicitly "cron-/hook-safe". The hook may run while another `clast snapshot` is mid-flight (cron + hook can collide); rely on `clast snapshot`'s own concurrency guarantees rather than adding a second lock here.
- `docs/cli-contract.md#error-handling-conventions` — exit codes that `clast snapshot` itself emits. The hook does not surface them: stderr/stdout from the backgrounded `clast` are redirected to `/dev/null`, and the hook always exits 0 from the foreground.
- `docs/repo-bootstrap.md#plugin-files` — canonical JSON shapes for `plugin.json` and `hooks/hooks.json`. Match them.
- `docs/repo-bootstrap.md#filesystem-layout` — the on-disk layout this step is *completing*: `.claude-plugin/plugin.json`, `hooks/hooks.json`, `hooks/snapshot.sh`. The skills tree under `.claude-plugin/skills/` stays empty (with its existing `.gitkeep`) until step 12.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions, subprocess-style integration tests, shellcheck-clean.

## Tasks

### A. Plugin manifest

1. **Delete `.claude-plugin/.gitkeep`** — once `plugin.json` exists the directory is committed via real content and the placeholder is dead weight. Do *not* delete `.claude-plugin/skills/.gitkeep`; that directory stays empty until step 12.

2. **Write `.claude-plugin/plugin.json`.** Copy the exact shape from `docs/repo-bootstrap.md#plugin-files`:

   ```json
   {
     "name": "clast",
     "version": "0.1.0",
     "description": "Capture, curate, and surface Claude Code session history across all your projects.",
     "homepage": "https://github.com/procrastivity/clast",
     "author": {
       "name": "Beau",
       "url": "https://github.com/procrastivity"
     },
     "license": "MIT"
   }
   ```

   Keep `version` at `0.1.0` — the npm `package.json` and the plugin manifest move together; the step that bumps them (step 19) will handle both. Two-space indent, trailing newline. Must be valid per `jq -e . < .claude-plugin/plugin.json`.

### B. Hook declaration

3. **Delete `hooks/.gitkeep`** — replaced by real files in this step.

4. **Write `hooks/hooks.json`.** Copy the exact shape from `docs/repo-bootstrap.md#plugin-files`:

   ```json
   {
     "hooks": [
       {
         "event": "SessionStart",
         "command": "${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh"
       }
     ]
   }
   ```

   Two-space indent, trailing newline. The literal `${CLAUDE_PLUGIN_ROOT}` token stays unexpanded in the JSON — Claude Code substitutes it at hook-invocation time. Must be valid per `jq -e . < hooks/hooks.json`.

### C. Hook script

5. **Write `hooks/snapshot.sh`.** Shebang `#!/usr/bin/env bash`, `set -eu` (not `pipefail` — the hook deliberately swallows backgrounded failures; `pipefail` adds nothing). One-line top-of-file comment explaining purpose. Body:

   - Resolve a `clast` binary in this order, picking the first that exists and is executable:
     1. `${CLAUDE_PLUGIN_ROOT:-}/bin/clast` if `CLAUDE_PLUGIN_ROOT` is set (the plugin-bundled binary path when invoked via Claude Code's npm or nix install).
     2. A sibling-derived path: `"$(cd "$(dirname "$0")/.." && pwd)/bin/clast"` (this is what makes the hook work for an in-repo `claude` invocation during dev, and for any install layout that ships `bin/` next to `hooks/`).
     3. `clast` resolved through PATH via `command -v clast`.
   - If none of the three resolves, the hook exits 0 silently. **Do not** print to stderr — a SessionStart hook that prints turns into UI noise on every session, and a missing `clast` is a user-config problem, not a runtime error. Logging requirements belong to a future `/doctor`-style command, not here.
   - Otherwise, launch `"$clast_bin" snapshot </dev/null >/dev/null 2>&1 &` and `disown` the background job, then `exit 0`. The `</dev/null` redirect is required so the backgrounded process does not inherit the hook's stdin (some shells will SIGTTIN it otherwise on session detach).
   - The foreground portion of the script must return in well under one second on a populated journal (no synchronous I/O beyond stat-ing up to three paths). Add a brief comment noting that constraint so a future editor does not casually insert blocking work.

6. **Mark the script executable.** `chmod +x hooks/snapshot.sh`, and verify it stays mode 0755 in the git index (`git ls-files -s hooks/snapshot.sh` should report `100755` after `git add`).

7. **Shellcheck the script.** It must pass `shellcheck hooks/snapshot.sh` clean under the project's existing rules (no new disables added in this step). The script is intentionally tiny — if shellcheck fires, fix the code, not the directives.

### D. Tests

8. **Write `test/test-plugin-hook.sh`.** Standard subprocess-style integration test (mirror the preamble of `test/test-dispatcher.sh` and `test/test-whereami.sh`). Cover:

   - **JSON validity.** `jq -e . < .claude-plugin/plugin.json` succeeds; the `.name` field is the string `"clast"`. `jq -e . < hooks/hooks.json` succeeds; `.hooks[0].event` is `"SessionStart"`; `.hooks[0].command` ends with `"/hooks/snapshot.sh"` and contains the literal substring `${CLAUDE_PLUGIN_ROOT}`.
   - **Hook is executable.** `[ -x hooks/snapshot.sh ]` is true.
   - **Foreground return is fast and clean.** Invoke `hooks/snapshot.sh` with `CLAUDE_PLUGIN_ROOT` unset, with `PATH` set so that `clast` is *not* found, and assert the script exits 0 and emits zero bytes on both stdout and stderr (`./hooks/snapshot.sh >out 2>err; [ ! -s out ]; [ ! -s err ]`).
   - **Hook backgrounds `clast snapshot` when found via PATH.** Build a tmp dir with a stub `clast` that does `#!/usr/bin/env bash\necho "$@" >> "$MARKER"`; prepend it to `PATH`; invoke `hooks/snapshot.sh`; assert the foreground exits 0; then poll the marker file for up to 2 seconds with a small sleep loop and assert it contains the line `snapshot`. The poll budget must be a fixed bound, not an unbounded wait — the test exits 1 with a clear message if the marker doesn't appear in time.
   - **Hook prefers `${CLAUDE_PLUGIN_ROOT}/bin/clast` over PATH.** Build *two* stub `clast` binaries: one under `$tmp/plugin-root/bin/clast` writing `from-root` to a marker, one earlier on `PATH` writing `from-path`. Set `CLAUDE_PLUGIN_ROOT=$tmp/plugin-root`. Invoke the hook. After the poll budget, the marker contains `from-root` and not `from-path`.
   - **Hook tolerates a non-existent `${CLAUDE_PLUGIN_ROOT}`.** Set `CLAUDE_PLUGIN_ROOT` to a path that does not exist; with `clast` also not on PATH; assert exit 0 and silent output (this is the "fresh install before npm link" path).

   Every test runs in a per-test `mktemp -d` and cleans up via a trap.

9. **Wire the new suite into `test/test-clast.sh`.** Append a call to `test/test-plugin-hook.sh` alongside the other suite invocations, in the order it appears in the test file (alphabetical-ish; doctor comes after dispatcher, so plugin-hook can go at the end of the list).

### E. Wiring

10. **Update `Makefile` `lint` target if and only if it does not already glob `hooks/*.sh`.** Read the existing target first; if `shellcheck` already runs over `hooks/` via a wildcard, change nothing. If not, add `hooks/snapshot.sh` to the explicit file list. Do *not* refactor the lint target shape in this step.

11. **README touch.** In the existing "Install" or "Plugin" section (whichever exists in `README.md` at this point — read first), add one short paragraph noting that installing the plugin enables auto-capture via a `SessionStart` hook and pointing at `hooks/snapshot.sh`. No new top-level section, no marketing copy. If the README has no install / plugin section yet, skip this task — step 18 ("docs-and-examples") owns the proper README rewrite, and a placeholder here just creates merge churn.

## Acceptance criteria

### `.claude-plugin/plugin.json`

- File exists, parses with `jq -e`, has `.name == "clast"` and `.version == "0.1.0"`.
- Matches the canonical shape from `docs/repo-bootstrap.md#plugin-files` exactly (no extra fields invented in this step).

### `hooks/hooks.json`

- File exists, parses with `jq -e`, has `.hooks` as a one-element array with `event: "SessionStart"` and `command` ending in `/hooks/snapshot.sh`.
- The literal token `${CLAUDE_PLUGIN_ROOT}` appears in the command string.

### `hooks/snapshot.sh`

- Committed with mode `0755` (git index entry `100755`).
- `shellcheck hooks/snapshot.sh` exits 0 with no new directives added.
- Resolves a `clast` binary in the documented order (plugin-root → sibling → PATH) and is silent on a not-found.
- Foreground exit is 0 in every case; backgrounded `clast snapshot` runs detached.

### Tests

- `test/test-plugin-hook.sh` covers every scenario listed in task 8 and exits 0.
- `test/test-clast.sh` invokes the new suite and still exits 0.
- `make lint` exits 0.
- `make test` exits 0.

## Out of scope

- **No skill files.** `.claude-plugin/skills/day-wakeup/SKILL.md` and `wakeup/SKILL.md` are steps 12 and 13. Do not create skill directories or stubs in this step; step 12 owns the first one.
- **No install / packaging changes.** `package.json`'s `files:` array already includes `.claude-plugin/` and `hooks/` (verify, but do not modify). `install.sh`, `flake.nix` packaging, and the marketplace manifest belong to steps 14 / 15 / 19.
- **No locking, no PID file, no log file in the hook.** Backgrounded `clast snapshot` handles its own concurrency; logging belongs to a v1.1 observability story. Adding a log here means committing to a path, a rotation policy, and a permission model — all of which belong to a dedicated step, not this one.
- **No additional hook events.** Only `SessionStart`. `Stop`, `PreToolUse`, etc. are not in the v1 plan. If a future step adds a second hook, it appends to `hooks.json`; it does not get folded into this step.
- **No `clast snapshot --hook` flag or other CLI changes.** The hook calls `clast snapshot` with no flags. If the hook needs to differentiate its invocations (e.g. for stats), that's a `--source=hook` flag added in a later step against the snapshot subcommand, not here.
- **No fixture additions.** The hook test builds tmp stubs inline. Fixtures are for real `clast` behavior, not for hook scaffolding.
- **No changes to `bin/clast`** or any `lib/clast/*.bash` file. If a missing helper seems needed, stop and ask rather than expanding scope.
- **No `.gitkeep` removal under `.claude-plugin/skills/`.** That directory stays empty until step 12 lands the first skill.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke — JSON shapes
jq . .claude-plugin/plugin.json
jq . hooks/hooks.json

# Manual smoke — hook foreground is silent and fast on a not-found
env -i PATH=/usr/bin:/bin bash -c './hooks/snapshot.sh' ; echo "exit=$?"   # 0, no output

# Manual smoke — hook backgrounds clast snapshot
tmp=$(mktemp -d)
cat >"$tmp/clast" <<'EOF'
#!/usr/bin/env bash
echo "$@" >> "$tmp/marker"
EOF
chmod +x "$tmp/clast"
PATH="$tmp:$PATH" tmp="$tmp" ./hooks/snapshot.sh ; echo "exit=$?"           # 0
sleep 1 ; cat "$tmp/marker"                                                  # snapshot

# Manual smoke — CLAUDE_PLUGIN_ROOT wins over PATH
mkdir -p "$tmp/plugin-root/bin"
cat >"$tmp/plugin-root/bin/clast" <<'EOF'
#!/usr/bin/env bash
echo "from-root" >> "$tmp/marker2"
EOF
chmod +x "$tmp/plugin-root/bin/clast"
PATH="$tmp:$PATH" tmp="$tmp" CLAUDE_PLUGIN_ROOT="$tmp/plugin-root" ./hooks/snapshot.sh
sleep 1 ; cat "$tmp/marker2"                                                 # from-root

rm -rf "$tmp"
```

## Notes for the implementer

- **The hook is plumbing, not policy.** Every line beyond resolve-binary / launch-detached / exit-0 is suspect. If a piece of logic feels clever, it belongs in `clast snapshot`, not in the hook. The hook is the part Claude Code calls; the CLI is the part that knows things.
- **Silence is correct.** A `SessionStart` hook prints on every session start across every project the plugin is installed in. Anything the hook writes to stderr surfaces in the user's UI. The not-found path is a config issue, not a runtime error — keep it silent here and let `clast doctor` (step 10, already shipped) be the place where users find out their install is broken.
- **`disown` matters.** Without it, on some shells the backgrounded `clast snapshot` becomes a zombie if the parent shell exits before it does. `&` alone is not enough.
- **`</dev/null` matters.** A subset of Claude Code hook invocations attach a controlling tty to stdin; without redirecting stdin the backgrounded process can SIGTTIN on shells that detach it. Redirect explicitly.
- **`set -e` without `pipefail`.** This is one of the rare scripts where pipefail would be actively wrong: the foreground is meant to swallow failures from the backgrounded command. Don't add it.
- **Resolution order is "plugin first, then sibling, then PATH" by intent.** The plugin-bundled binary is the one Claude Code installed alongside the hook and is the version that matches what the hook expects. PATH is the dev / system-install fallback. Reversing the order means a stale system `clast` shadows a fresh plugin install — exactly the wrong failure mode.
- **The sibling-derived path is for in-repo development.** When Beau runs `claude` from a checkout with no install, `$0` resolves to `./hooks/snapshot.sh` and `..` resolves to the repo root — so `bin/clast` is one directory up. Keep that lookup; it's how the hook works during step 12+ skill development.
- **Test poll budgets are upper bounds, not sleeps.** A 2-second `until [ -f "$marker" ]; do sleep 0.1; done` loop with a timeout is the shape; a fixed `sleep 2 ; test ...` is the anti-shape (slower on success, less informative on failure).
- **Conventional commit suggestion**: `feat(plugin): scaffold plugin manifest and SessionStart snapshot hook`. One commit. The README touch (if it happens at all per task 11) rides this commit.
