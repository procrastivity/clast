---
step: 11
title: plugin-scaffold-and-hook
depends_on: [03]
size: small
references:
  - docs/overview.md#plugin-surface-cheatsheet
  - docs/repo-bootstrap.md#plugin-files
  - docs/repo-bootstrap.md#directory-tree
  - docs/skill-prompts.md#hook-sessionstart
  - docs/skill-prompts.md#why-no-sessionend-hook
---

# Step 11: Claude Code plugin scaffold + `SessionStart` snapshot hook

## Context

Step 03 wired `bin/clast` as a single-dispatcher binary and shipped `clast whereami`; steps 04–10 layered in every CLI subcommand the plugin will eventually call (`snapshot`, `projects`, `sessions`, `show`, `entries`, `registry`, `stats`, `doctor`; `breadcrumb` is in flight on its own branch but irrelevant here — this step does not call it). The repo already has empty `.claude-plugin/`, `.claude-plugin/skills/`, and `hooks/` directories kept alive only by `.gitkeep` markers (see `git ls-files .claude-plugin hooks`); `package.json` already lists both directories in its `files` array so whatever lands here ships via npm without further config; and `flake.nix`'s dev shell provides `bash`, `jq`, and `shellcheck`. No plugin manifest, no hook manifest, and no hook script exist yet — every Claude Code plugin trigger and every `SessionStart` event currently goes nowhere.

This step is the **plugin substrate**: the three files Claude Code's plugin loader needs to recognize this repo as a plugin and to fire `clast snapshot` automatically on every session start. It ships zero skill content (skills come in steps 12 and 13) and zero CLI changes — `bin/clast`, `lib/clast/`, and every existing test stays untouched. The hook is intentionally near-trivial (one `command -v` guard, one backgrounded `clast snapshot`, then `exit 0`) so the plugin loads cleanly even on a machine where the `clast` CLI isn't installed: the user can adopt the plugin first and the CLI second without seeing errors at session start.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and a sane `bash` are on PATH for the validation steps.

## Goal

Create `.claude-plugin/plugin.json`, `hooks/hooks.json`, and `hooks/snapshot.sh`; mark the hook executable in git; remove the now-obsolete `.gitkeep` placeholders under `.claude-plugin/` and `hooks/` that this step displaces; and verify both JSON files parse and the shell script passes `shellcheck`. No code changes outside those four directories (plus `README.md` for a usage paragraph and `test/test-clast.sh` for one new linter assertion).

## References

Read before starting:

- `docs/overview.md#plugin-surface-cheatsheet` — the three-skill + one-hook surface; this step ships the hook only.
- `docs/repo-bootstrap.md#plugin-files` — canonical content for `plugin.json` and `hooks/hooks.json`. Use those JSON bodies verbatim except where this step calls out a deviation.
- `docs/repo-bootstrap.md#directory-tree` — `.claude-plugin/`, `.claude-plugin/skills/<name>/SKILL.md`, `hooks/hooks.json`, `hooks/snapshot.sh` are the canonical paths; do not invent alternatives.
- `docs/skill-prompts.md#hook-sessionstart` — `snapshot.sh` body, why it backgrounds, why it's silent when the CLI is missing, and why `chmod +x` matters (plugin loader respects the executable bit).
- `docs/skill-prompts.md#why-no-sessionend-hook` — explicitly only one hook in v1; do NOT add a `Stop` / `SessionEnd` entry to `hooks.json`.

## Tasks

1. **Write `.claude-plugin/plugin.json`** with the exact body shown in `docs/repo-bootstrap.md#plugin-files`. Fields, in this order: `name` (`"clast"`), `version` (`"0.1.0"` — keep in sync with `package.json`'s `version` field; this step does not bump either), `description` (the one-line tagline from the bootstrap doc, byte-identical to `package.json`'s `description`), `homepage` (`"https://github.com/procrastivity/clast"`), `author` (object with `name` and `url`), `license` (`"MIT"`). Two-space indent, trailing newline. The file must be valid JSON (`jq . .claude-plugin/plugin.json` succeeds).

2. **Write `hooks/hooks.json`** with the exact body shown in `docs/repo-bootstrap.md#plugin-files` / `docs/skill-prompts.md#hooks-json`. One `hooks` array containing exactly one entry: `event: "SessionStart"`, `command: "${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh"`. Two-space indent, trailing newline. The `${CLAUDE_PLUGIN_ROOT}` token is literal — do NOT expand it at write time; Claude Code substitutes it at hook-fire time. Do NOT add any other event (`Stop`, `SessionEnd`, `UserPromptSubmit`, …) — `docs/skill-prompts.md#why-no-sessionend-hook` explicitly carves those out as non-goals.

3. **Write `hooks/snapshot.sh`** with the body shown in `docs/skill-prompts.md#hook-sessionstart`. Required shape:
   - `#!/usr/bin/env bash` shebang on line 1.
   - A short header comment block (purpose, idempotency, "silent if `clast` is not on PATH").
   - `# shellcheck shell=bash` directive (the file has no `.bash` extension, so shellcheck needs the explicit shell hint to lint it cleanly).
   - **Do NOT** add `set -euo pipefail`. The hook must be best-effort and must never propagate a non-zero exit to Claude Code (which would surface as a startup error to the user). A failing `clast snapshot` is not a session-start failure.
   - Guard: `if command -v clast >/dev/null 2>&1; then (clast snapshot >/dev/null 2>&1 &) fi`. The double parenthesization (`(... &)`) double-forks the snapshot so it survives the shell exiting and never blocks session start; `>/dev/null 2>&1` discards stdout and stderr so Claude Code's session-start UI never shows noise from the background snapshot.
   - Final `exit 0` so the hook is unambiguously a success even if the `command -v` branch was taken and `clast snapshot` later failed asynchronously.

4. **Make `hooks/snapshot.sh` executable in git.** After writing the file: `chmod +x hooks/snapshot.sh` AND `git update-index --chmod=+x hooks/snapshot.sh` (the latter is the one that survives across clones — the on-disk mode alone is not enough on a fresh checkout). Verify with `git ls-files --stage hooks/snapshot.sh` — the leading mode must be `100755`, not `100644`. The plugin loader keys off the executable bit; a `100644` hook silently no-ops.

5. **Remove the now-obsolete `.gitkeep` placeholders** that this step displaces:
   - `.claude-plugin/.gitkeep` — displaced by `.claude-plugin/plugin.json`.
   - `hooks/.gitkeep` — displaced by `hooks/hooks.json` and `hooks/snapshot.sh`.
   - **Leave `.claude-plugin/skills/.gitkeep` ALONE.** The `skills/` directory stays empty in this step — steps 12 (`day-wakeup`) and 13 (`wakeup`) populate it. Removing the `.gitkeep` here would either leave an unkept empty directory (git drops it) or force step 12 to recreate the directory tree, which is silly. The `.gitkeep` goes away naturally when step 12 lands `skills/day-wakeup/SKILL.md`.

6. **Add a lint assertion to `test/test-clast.sh`** that runs `shellcheck` on `hooks/snapshot.sh` and `jq .` on both JSON files. Locate the existing `shellcheck` or lint block in the suite runner (if there is one — `test/test-clast.sh` today is primarily a suite multiplexer; if no global lint hook exists, add a small `_assert_plugin_assets` helper at the top of the file that runs before the suite loop and exits non-zero with a clear message on any failure). The three assertions:
   - `jq -e . .claude-plugin/plugin.json >/dev/null` (parses, top-level is an object).
   - `jq -e '.hooks | type == "array" and length == 1' hooks/hooks.json >/dev/null` (parses AND has exactly one hook entry — guards against accidental drift if someone later adds a `Stop` event).
   - `shellcheck --shell=bash hooks/snapshot.sh` (passes with no warnings).
   These assertions live in `test-clast.sh` not in a dedicated `test-plugin.sh` because the plugin layer has no behavioral surface to integration-test — there's nothing to invoke and nothing to assert beyond "files exist and are well-formed."

7. **Update `package.json`'s `lint` script** to include `hooks/snapshot.sh`. Current value: `"shellcheck bin/clast lib/clast/**/*.bash test/*.sh"`. New value: `"shellcheck bin/clast lib/clast/**/*.bash test/*.sh hooks/snapshot.sh"`. Also update `Makefile`'s `lint` target the same way so `make lint` and `npm run lint` agree.

8. **Update `README.md`** with a short "Plugin install" paragraph (3–6 lines). Two pieces: (a) the install path — `claude plugin install <path-to-clast-checkout>` for local installs, with a TODO note that the marketplace flow is wired up in a later step (step 17 / step 18; do NOT name a specific step, just say "future step"); (b) what the plugin gives you today — auto-snapshot on every `SessionStart` so the journal stays current with zero manual effort. Do NOT describe `/day-wakeup` or `/wakeup` — those skills do not exist yet and will get their own README paragraphs when steps 12 and 13 land. Link to `docs/skill-prompts.md#hook-sessionstart` for the hook's design rationale.

9. **Confirm `make lint` and `make test` pass.** `make lint` must lint the new `hooks/snapshot.sh`. `make test` must run the new plugin-asset assertions (task 6) plus every existing suite, and exit 0. If a previously-passing test starts asserting on the contents of `.gitkeep` (unlikely but possible if any suite enumerates the directory), update the suite to match the new file set; do NOT keep the `.gitkeep` to preserve test stability.

## Acceptance criteria

- `.claude-plugin/plugin.json` exists, parses as JSON, and matches the field set described in `docs/repo-bootstrap.md#plugin-files` (`name`, `version`, `description`, `homepage`, `author`, `license`).
- `.claude-plugin/plugin.json`'s `version` field equals `package.json`'s `version` field byte-for-byte.
- `hooks/hooks.json` exists, parses as JSON, has a top-level `hooks` array of length exactly 1, and that entry has `event: "SessionStart"` and `command: "${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh"` (the `${…}` token unexpanded).
- `hooks/snapshot.sh` exists, is marked executable in git (`git ls-files --stage hooks/snapshot.sh` shows mode `100755`), and passes `shellcheck --shell=bash`.
- `hooks/snapshot.sh` does NOT use `set -euo pipefail` (a snapshot failure must not propagate to Claude Code).
- `hooks/snapshot.sh` is a no-op (`exit 0` with no stderr) when `clast` is not on `PATH`.
- `.claude-plugin/.gitkeep` and `hooks/.gitkeep` are removed; `.claude-plugin/skills/.gitkeep` is retained.
- `test/test-clast.sh` runs three new assertions on the plugin assets (`jq` parse + hook-count + `shellcheck`) and exits non-zero if any fails.
- `package.json`'s `lint` script and `Makefile`'s `lint` target both include `hooks/snapshot.sh`.
- `README.md` has a short plugin-install paragraph; no mention of skills (which don't exist yet).
- `make lint` and `make test` both exit 0.

## Out of scope

- **Do not write any `SKILL.md`.** `day-wakeup` is step 12; `wakeup` is step 13. The `skills/` directory stays empty (its `.gitkeep` stays put) so the plugin loads as "hook-only" today.
- **Do not add a `Stop` / `SessionEnd` / `UserPromptSubmit` hook.** `docs/skill-prompts.md#why-no-sessionend-hook` is explicit: one hook in v1. Multiple hooks are a future step at most.
- **Do not invoke `clast snapshot` from `bin/clast` differently** (no new env-var contract for hook callers, no `--from-hook` flag). The hook calls `clast snapshot` with no flags — exactly the cron path. If the hook needs to differ from cron later, that's a future spec.
- **Do not add an `examples/cron/crontab.sample`** in this step. The cron sample lives at the bottom of `skill-prompts.md` and ships as part of step 18 (`docs-and-examples`). Putting it here would drag in `examples/cron/` directory scaffolding for no near-term gain.
- **Do not add a `Dockerfile` or any container scaffolding.** `docs/repo-bootstrap.md#open-decisions-specific-to-bootstrap` row 7 explicitly skips Docker in v1.
- **Do not change `bin/clast`, any file under `lib/clast/`, or any existing test suite.** The only allowed edits outside the three new files are `test/test-clast.sh` (task 6), `package.json` (task 7), `Makefile` (task 7), and `README.md` (task 8).
- **Do not run a real Claude Code session against the new plugin as part of acceptance.** The plugin loader is a moving target across CC versions; we assert on the static contract (JSON shape + executable bit + shellcheck) and leave live-loader verification to manual smoke at install time.
- **Do not bump `package.json`'s `version`.** The `0.1.0` it ships today is correct; version bumps happen at release-prep (step 16) and tagging (step 19).
- **Do not modify `.gitattributes`** to force eol=lf on the hook. The repo-level `.gitattributes` already covers shell scripts; adding a hook-specific rule is noise.

## Verification

```bash
# Lint
make lint

# Tests
make test

# JSON parses
jq . .claude-plugin/plugin.json
jq . hooks/hooks.json

# Hook count is exactly 1, event is SessionStart
jq -e '.hooks | length == 1 and (.[0].event == "SessionStart")' hooks/hooks.json

# Hook is executable in the index (must show 100755, NOT 100644)
git ls-files --stage hooks/snapshot.sh

# Hook is a clean no-op when clast is not on PATH
env -i PATH=/usr/bin:/bin bash hooks/snapshot.sh ; echo "exit=$?"   # exit=0, no output

# Hook backgrounds and exits fast when clast IS on PATH (snapshot runs async)
PATH="$PWD/bin:$PATH" time bash hooks/snapshot.sh                   # real time well under 1s
```

## Notes for the implementer

- **The plugin loader respects the executable bit.** This is the single most common silent-failure mode: a `100644` `snapshot.sh` looks correct on disk but the loader skips it. `git update-index --chmod=+x` (task 4) is non-optional — verify with `git ls-files --stage` before committing.
- **`${CLAUDE_PLUGIN_ROOT}` is a literal in the JSON.** It's a template token Claude Code expands at hook-fire time. Do NOT pre-expand it to the user's local checkout path — that would break installs.
- **Why no `set -euo pipefail` in the hook.** The hook must never propagate failure to Claude Code's session start. `set -e` would abort on the first failed `command -v` (well, that one returns 0/1 cleanly, but future edits might add commands that exit non-zero benignly), and `set -u` would error on optional env vars. Keep the hook permissive; it has exactly one job and a clear `exit 0` at the end.
- **The double-fork `(clast snapshot &)`** is what makes the hook safe to run from a UI-blocking session-start context. A single `&` would leave the snapshot as a job of the parent shell; the parenthesized subshell + background makes the snapshot a child of init, so the hook process can exit immediately without waiting on it.
- **Plugin manifest version coupling.** `.claude-plugin/plugin.json`'s `version` and `package.json`'s `version` must stay in lockstep — a future release-prep step (16) will bump both together. Hard-coupling them now in the test suite is overkill; the byte-identical check in acceptance is reminder enough.
- **`.gitkeep` removal asymmetry is intentional.** `.claude-plugin/.gitkeep` and `hooks/.gitkeep` go away (their directories now have real content); `.claude-plugin/skills/.gitkeep` stays (its directory is still empty until step 12). Don't be tempted to remove all three uniformly.
- **Conventional commit suggestion**: `feat(plugin): scaffold .claude-plugin manifest + SessionStart snapshot hook`. One commit is fine; the README touch is small enough to fold in.
