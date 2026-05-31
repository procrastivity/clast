---
step: 05
title: registry-lib-and-subcommand
depends_on: [02, 03, 04]
size: medium
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#glossary
  - docs/overview.md#conventions
  - docs/cli-contract.md#clast-registry
  - docs/cli-contract.md#registry-line-in-projectsjson
  - docs/cli-contract.md#exit-codes
  - docs/repo-bootstrap.md#libclastclast-registry-libbash
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 05: `clast-registry-lib.bash` + `clast registry`

## Context

Steps 02–04 built the foundation. `lib/clast/clast-lib.bash` provides path/log/jq/atomic-write helpers, `lib/clast/clast-decode-lib.bash` round-trips between filesystem paths and Claude Code's dash-encoded segments, `lib/clast/clast-manifest-lib.bash` reads/writes the snapshot manifest, and `bin/clast` dispatches to subcommands sourced from `lib/clast/clast-subcommands/`. The dispatcher already routes the literal token `registry` to a `_clast_stub` that exits 2 with "planned for step 05"; replacing that stub is part of this step. `whereami` carries a `# TODO(step-05)` marker on the `registered`/`slug` fields that says "consult the registry lib"; lighting that up is also in scope here, because the lib lookups exist after this step ships and leaving the stub would be a regression to fix in the next step that touches `whereami`.

There is no registry library and no `projects.json` reader/writer yet. After this step, all five remaining query/curation subcommands (`projects`, `sessions`, `show`, `entries`, `breadcrumb`) will have a single source of truth for "what slug does this path belong to."

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils are on PATH.

## Goal

Implement `lib/clast/clast-registry-lib.bash` with the four functions documented in `repo-bootstrap.md#libclastclast-registry-libbash` (plus the writers `add` and `remove` need), build `lib/clast/clast-subcommands/registry.bash` for the `list | add | resolve | remove` surface in `cli-contract.md#clast-registry`, wire it into the dispatcher, populate `whereami`'s `registered`/`slug` fields from the new lib, and cover both layers with tests against a new `multi-project/` registry fixture.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — registry lives at `$(clast_journal_dir)/projects.json` (JSONL despite the name); same parent dir as the manifest.
- `docs/overview.md#glossary` — terminology for **slug**, **segment**, **registry**, **project**. The lib's vocabulary must match these terms exactly (no `name`, no `identifier`).
- `docs/overview.md#conventions` — JSONL semantics, snake_case keys, ISO 8601 date for `first_seen` (no `Z` suffix — `first_seen` is a date, not a timestamp), exit codes (2 = usage error).
- `docs/cli-contract.md#clast-registry` — exact `list/add/resolve/remove` UX: columnar human output for `list`, the five-step resolution logic for `add` (canonicalize → `git remote` → slug default → match-existing-remote → new entry), the path-or-segment input contract for `resolve`, and the "unregister only, never delete data" guarantee for `remove`.
- `docs/cli-contract.md#registry-line-in-projectsjson` — the on-disk schema: `path` and `slug` required; `remote`, `first_seen`, `aliases` optional. Multiple lines may share a `slug` if `path` differs.
- `docs/cli-contract.md#exit-codes` — `resolve` returns 0 on hit, 1 on miss; `add`/`remove` return 2 on bad arguments and 1 on registry write failure.
- `docs/repo-bootstrap.md#libclastclast-registry-libbash` — the four function names this step must export.
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — subcommand file convention (`clast_cmd_registry` is the single entrypoint; argument parsing lives there, not in the lib).
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions; `multi-project/` is the named fixture this step introduces.

## Tasks

1. **Write `lib/clast/clast-registry-lib.bash`.** Standard preamble: `# shellcheck shell=bash`, double-source guard with `_CLAST_REGISTRY_LIB_SOURCED` (mirror `clast-manifest-lib.bash`). Assume `clast-lib.bash` is already sourced (the dispatcher does this); `clast-decode-lib.bash` is also already sourced, so `clast_encode_path` is available for the segment-derivation path.

2. **Implement `clast_registry_path`.** Returns `"$(clast_journal_dir)/projects.json"`. Every other function routes through it so `CLAST_JOURNAL_DIR` redirects the registry to a test tmpdir without further surgery.

3. **Implement `clast_registry_list_json`.** Prints the registry as a single JSON array on stdout:
   - If the file does not exist, print `[]` and return 0 (an empty registry is not an error).
   - Use `jq -cs 'map(select(.path != null))' "$(clast_registry_path)"` to slurp the JSONL file into an array, silently dropping any malformed line via `jq -cR 'fromjson?'` (same trick as `clast_manifest_iterate`). A partial last line from a crashed write must not poison the read.
   - Output is compact JSON (`-c`), one array per call. Callers that want pretty output pipe through `jq .` themselves.

4. **Implement `clast_registry_resolve <path-or-segment>`.** Prints the matching `slug` on stdout (no trailing whitespace beyond the newline `printf` adds) and returns 0; returns 1 silently if no match. Resolution order, documented inline with a one-line comment per branch:
   - If the input starts with `-` (a segment), decode it to a path with `clast_decode_segment` first, then resolve as a path. If decoding is ambiguous and yields multiple candidates, prefer the one that exists on the registry; if none match, return 1.
   - If the input is a path, canonicalize with `realpath -m` (the `-m` allows non-existent paths — `whereami` resolves against `$PWD` of a non-git directory all the time) before comparing.
   - Match the canonicalized path against each line's `.path`. If no hit, also scan each line's `.aliases[]`. First match wins.
   - The lookup MUST tolerate malformed registry lines (skip them via `fromjson?`); a corrupt registry is a `doctor` problem, not a `resolve` problem.

5. **Implement `clast_registry_match_remote <remote>`.** Prints the slug of the first line whose `.remote` equals `<remote>` and returns 0; returns 1 silently if none match. Empty `<remote>` argument returns 1 (an unset remote does not "match" empty remotes — that path goes through `add`'s "new entry" branch, not the alias-merge branch).

6. **Implement `clast_registry_add <path>` plus its optional flags `--slug NAME` and `--remote URL`.** This is the only writer in the lib. Behavior, per the five-step list in `cli-contract.md#clast-registry-add-path-slug-name-remote-url`:
   1. Canonicalize `<path>` with `realpath -m`. Reject empty or whitespace-only paths (exit 2 with a clear stderr message).
   2. If `--remote` is not given, attempt `git -C "$path" remote get-url origin 2>/dev/null`. An empty result is fine — the entry just won't have a `remote` field. Do not error when `git` is missing or the path is not a repo; the registry must support non-git directories.
   3. If `--slug` is not given, derive a default from `basename "$path"` (no prompting from the lib — interactive prompts live in the subcommand, never in the lib).
   4. If a remote was resolved and `clast_registry_match_remote <remote>` returns a slug, **do not create a new entry**: instead, append a new JSONL line representing the *aliased* state — same `slug` as the matched line, new `path` as an alias, and the original `path` carried forward. To keep the lib's writer dumb and append-only, the convention is: write a fresh line with the new `path`, the existing `slug`, the same `remote`, today's date as `first_seen`, and an `aliases` array containing the previously-known paths for that slug from the current registry (collected by re-scanning with `clast_registry_list_json | jq`). "Most recent line wins" semantics are then deferred to `clast_registry_resolve`, which already prefers any matching line including aliases.
   5. Else (no remote match), append a brand-new line with the given `slug`, the canonical `path`, the resolved `remote` (or absent if empty), today's date as `first_seen` via `clast_today`, and an empty `aliases` array (`[]`, not `null`).
   - Build every JSON line with `jq -c -n --arg path "$path" --arg slug "$slug" ... '{ path: $path, slug: $slug, ... }'`. Conditionally drop fields with `| with_entries(select(.value != null and .value != ""))` so missing optional fields do not litter the file as `"remote": ""`.
   - Append with `>>` — same crash-safe single-line append primitive `clast_manifest_append` uses. Ensure the journal directory exists first (`mkdir -p`). **Do not** use `clast_atomic_write` here for the same reason as the manifest: a whole-file rewrite would clobber concurrent appends from another machine.
   - On success, print the appended JSON line to stdout. Callers (the subcommand) decide whether to show it to the user or format it as a confirmation message.
   - On any write failure return 1 with a clear stderr error.

7. **Implement `clast_registry_remove <slug>`.** Per `cli-contract.md#clast-registry-remove-slug`, "remove" means "unregister, do not delete data":
   - Read the current registry via `clast_registry_list_json`, filter out every entry whose `.slug == <slug>` with `jq -c '.[] | select(.slug != $slug)' --arg slug "$slug"`, and rewrite the file. Because rewrite is a whole-file replace (not an append), use `clast_atomic_write` here — concurrent writers losing each other's adds during a rare `remove` is the documented trade-off, and `remove` is interactive enough that the user can re-run if a race occurs.
   - If the registry does not exist or contains no matching lines, return 1 (nothing to do); exit 0 only when at least one line was removed. The subcommand surfaces the distinction in its UX.
   - Never touch `transcripts/`, `entries/`, `breadcrumbs/`, or `.manifest.jsonl`. The whole point of `remove` is reversibility.

8. **Wire the lib into the dispatcher.** Update `bin/clast` so the top of the file sources `clast-registry-lib.bash` alongside the existing `clast-lib.bash` and `clast-decode-lib.bash` sources (so subcommands beyond `registry` can call `clast_registry_resolve` without re-sourcing — `whereami` is the first consumer in this step). Replace the `registry) _clast_stub registry 05 ;;` line with a real `source "$CLAST_LIB/clast-subcommands/registry.bash"; clast_cmd_registry "$@" ;;` branch, mirroring the existing `whereami)` case. Leave the other stubs alone.

9. **Write `lib/clast/clast-subcommands/registry.bash`.** Single function `clast_cmd_registry`:
   - First positional arg is the operation (`list`, `add`, `resolve`, `remove`). No op → print a usage block and return 2.
   - `list`: accepts `--json`. Default (human) output is the four-column table shown in `cli-contract.md#clast-registry-list-json` — header row, then one row per entry with `slug`, `path`, `remote` (or empty), `aliases` (joined with `,` or `(none)`). Use `printf '%-17s %-33s %-43s %s\n'` style fixed-width columns; readability over perfect alignment is fine. `--json` prints the array from `clast_registry_list_json` unchanged.
   - `add`: parses `<path>` and the two long flags `--slug NAME` and `--remote URL` (both with `--flag value` and `--flag=value` forms, matching the dispatcher's pattern). Calls `clast_registry_add`. On success and absent `--json`, prints `registered <slug> → <path>` (one line). With `--json`, prints the JSON line returned by `clast_registry_add`. No interactive prompt for `--slug` in this step — the doc says "prompt with default = repo dirname"; an interactive prompt is out of scope here. Pass-through the default (`basename`) and document the deferred prompt in a `# TODO(v1.1): interactive --slug prompt` code comment.
   - `resolve`: takes one positional `<path-or-segment>`. On hit, prints the slug (or `{"slug":"..."}` with `--json`) and exits 0. On miss, prints nothing to stdout, writes `clast: error: not registered` to stderr (or `{"error":"not registered"}` to stdout with `--json` — JSON callers parse stdout, not stderr), and exits 1.
   - `remove`: takes one positional `<slug>`. Confirms with `clast_registry_remove`; on success prints `unregistered <slug>` (absent `--json`) or `{"removed":"<slug>"}` (with `--json`) and exits 0. On not-found, exits 1 with a stderr error (or JSON `{"error":"not registered"}`).
   - Honor `CLAST_JSON=1` from the dispatcher's global flag handling as the `--json` default, same as `whereami` does.
   - Per-op `-h|--help` prints the usage for that op and returns 0.

10. **Light up `whereami`'s `registered` / `slug` fields.** In `lib/clast/clast-subcommands/whereami.bash`, replace the `# TODO(step-05)` stub block with a `clast_registry_resolve` call against `$git_root` if set, else `$PWD`. On hit, `registered="yes"` and `slug` is the resolved slug; on miss, `registered="no"` and `slug=""`. Keep the JSON / human output shape unchanged — the values just become real. Update the existing tests that assumed `registered="no"` so they exercise both the registered and unregistered cases against a `multi-project/` journal fixture.

11. **Create the `multi-project/` fixture.** Layout:
    - `test/fixtures/multi-project/projects.json` — a hand-written JSONL file with at least four lines:
      - A canonical entry: `path` = `/home/beau/code/xesapps`, `slug` = `xesapps`, `remote` = `git@gitlab.xes-inc.com:xes/xesapps.git`, `first_seen` = `2026-03-12`, `aliases` = `[]`.
      - An aliased entry for the same slug: `path` = `/Users/beau/code/xesapps`, `slug` = `xesapps`, same `remote`, `first_seen` later, `aliases` = `["/mnt/c/code/xesapps"]`. (This exercises the "match remote on add" path and the alias-resolve path in one row.)
      - A bare entry with no remote: `path` = `/tmp/scratch-no-remote`, `slug` = `scratch`, no `remote` key, `first_seen` set, `aliases` = `[]`.
      - A malformed line (truncated JSON: `{"path": "/oops", "slug":`). Resolve / list must tolerate it.
    - Optionally include a tiny `transcripts/<day>/...` stub if any test wants to verify "registry change doesn't touch transcripts." Not required for the registry tests themselves.

12. **Write `test/test-registry.sh`.** Follow the shape of `test/test-manifest.sh`: `cd` to repo root, `source test/helpers.sh`, `source lib/clast/clast-lib.bash`, `source lib/clast/clast-decode-lib.bash`, `source lib/clast/clast-registry-lib.bash`. Cover at minimum:
    - **path helper**: `clast_registry_path` echoes `$(clast_journal_dir)/projects.json`.
    - **list empty**: against a fresh `setup_test_journal` (no `projects.json`), `clast_registry_list_json` prints `[]` and returns 0.
    - **list against fixture**: against `multi-project/`, `clast_registry_list_json | jq 'length'` returns 3 (the malformed line is dropped silently).
    - **resolve by path (hit)**: against the fixture, resolving `/home/beau/code/xesapps` prints `xesapps` and exits 0.
    - **resolve by alias**: resolving `/mnt/c/code/xesapps` prints `xesapps` and exits 0.
    - **resolve by segment**: resolving `-home-beau-code-xesapps` prints `xesapps` and exits 0.
    - **resolve miss**: resolving `/tmp/unknown` prints nothing and exits 1.
    - **resolve tolerates malformed lines**: a `resolve` of a known-good entry succeeds even though the fixture contains a truncated line.
    - **match_remote hit / miss**: `clast_registry_match_remote git@gitlab.xes-inc.com:xes/xesapps.git` prints `xesapps`; an unknown remote returns 1. An empty-string argument returns 1.
    - **add new entry**: against an empty journal, `clast_registry_add /tmp/proj-x --slug proj-x` writes one line, and a subsequent `clast_registry_resolve /tmp/proj-x` returns `proj-x`. The resulting JSON line round-trips through `jq` with the expected fields.
    - **add aliases an existing remote**: pre-seed the journal with one entry for `slug=foo` + `remote=R`; then `clast_registry_add /tmp/proj-foo-2 --slug ignored --remote R` produces a new line whose `slug` is `foo` (the matched slug, not `ignored`) and whose `path` is the new one. Document the behavior with one assertion that captures *why* the input `--slug` was overridden.
    - **add rejects empty path**: returns 2 and writes to stderr.
    - **add without --remote and without git**: against a non-git tmpdir path, writes a line with no `remote` field at all (`jq -e 'has("remote") | not'` succeeds).
    - **remove by slug**: after seeding two entries with the same slug, `clast_registry_remove foo` rewrites the file with zero matching lines and returns 0; a second call returns 1 (nothing to remove).
    - **remove never touches transcripts**: create a sentinel file under `$CLAST_JOURNAL_DIR/transcripts/` before calling `remove`; assert it still exists afterward.
    - **double-source guard**: re-sourcing `clast-registry-lib.bash` does not error.

13. **Write `test/test-registry-cmd.sh`** — integration test for the subcommand surface, paralleling `test/test-whereami.sh`'s style (invokes `bin/clast` as a subprocess). Cover:
    - `clast registry list` (human) against the fixture: stdout contains a header and a row for each valid entry.
    - `clast registry list --json` returns a valid JSON array of length 3.
    - `clast registry add /tmp/proj-new --slug proj-new` exits 0 and emits `registered proj-new → /tmp/proj-new`.
    - `clast registry add /tmp/proj-new --slug proj-new --json` exits 0 and emits valid JSON containing `.path` and `.slug`.
    - `clast registry resolve /home/beau/code/xesapps` exits 0 with stdout `xesapps`.
    - `clast registry resolve /tmp/nope` exits 1 with stderr containing `not registered`.
    - `clast registry resolve /tmp/nope --json` exits 1 with stdout `{"error":"not registered"}` and empty stderr.
    - `clast registry remove xesapps` against the fixture removes both `xesapps` lines (verify by re-running `list --json | jq 'length'`).
    - `clast registry` with no args prints usage and exits 2.
    - `clast registry bogus-op` exits 2 with a clear stderr error.

14. **Update `test/test-whereami.sh`** to cover the now-real `registered` / `slug` fields:
    - One scenario: tmpdir journal seeded with `multi-project/`, `cd` into a tmpdir whose canonicalized path matches the fixture's first entry, run `clast whereami --json`, assert `registered == "yes"` and `slug == "xesapps"`. Because the fixture paths are absolute (`/home/beau/...`), simulate the match by setting `CLAST_JOURNAL_DIR` to a temp journal containing a one-line `projects.json` whose `path` equals the actual `mktemp` directory — building this inline in the test is fine; do not pollute the fixture with machine-specific paths.
    - One scenario: an unregistered tmpdir reports `registered == "no"`, `slug == ""` / `slug == null` (JSON).
    - Keep all existing scenarios green; the existing "default invocation" scenario must continue to pass against a real `$HOME/.claude/journal/`, so do not assume the test harness's tmpdir is the only registry path in scope. Use a per-test `setup_test_journal` for the new assertions only.

15. **Wire both new test scripts into `test/test-clast.sh`.** Append `test/test-registry.sh` and `test/test-registry-cmd.sh` to the `suites` array, after `test/test-manifest.sh`. Read order remains: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd.

16. **Confirm `make lint` passes.** Add `# shellcheck source=lib/clast/clast-lib.bash` (and `clast-decode-lib.bash`) directives where appropriate so `shellcheck` resolves the cross-file dependencies. The subcommand file should declare `# shellcheck source=lib/clast/clast-registry-lib.bash` at the top so its `clast_registry_*` calls resolve.

## Acceptance criteria

- `lib/clast/clast-registry-lib.bash` exists, sources cleanly under `set -euo pipefail`, and is idempotent against double-sourcing.
- The lib exports exactly these public functions: `clast_registry_path`, `clast_registry_list_json`, `clast_registry_resolve`, `clast_registry_match_remote`, `clast_registry_add`, `clast_registry_remove`. (Private helpers prefixed with `_clast_registry_` are allowed.)
- `clast_registry_list_json` always prints a valid JSON array (`[]` when the file does not exist), and silently skips malformed JSONL lines.
- `clast_registry_resolve` accepts both filesystem paths and dash-encoded segments, walks `path` then `aliases[]`, and returns 1 (silently) on miss.
- `clast_registry_add` writes exactly one JSONL line per call, derives sensible defaults (`basename` for slug, `git remote get-url origin` for remote, `clast_today` for `first_seen`), and merges into an existing slug when the remote matches.
- `clast_registry_remove` rewrites `projects.json` atomically, returns 0 only when ≥ 1 line was removed, and never touches files outside `projects.json`.
- `bin/clast` sources the registry lib at the top alongside the other libs and dispatches `registry` to `clast_cmd_registry` (the `_clast_stub registry 05` line is gone).
- `clast registry list` (human and `--json`), `add`, `resolve`, `remove`, no-arg, and bogus-op surfaces behave per `cli-contract.md#clast-registry` and the tasks above.
- `clast whereami` reports `registered == "yes"` + the resolved slug when the current path is registered, and `registered == "no"` otherwise. Existing whereami tests still pass.
- `test/fixtures/multi-project/projects.json` exists with the four documented lines (three valid + one malformed).
- `test/test-registry.sh` and `test/test-registry-cmd.sh` cover every scenario in tasks 12 and 13 and exit 0.
- `make test` runs all seven test files (`lib`, `decode`, `dispatcher`, `whereami`, `manifest`, `registry`, `registry-cmd`) and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement an interactive `--slug` prompt.** `cli-contract.md` mentions one as a future polish; leave a `# TODO(v1.1)` and default to `basename "$path"` non-interactively. Interactive UX deserves its own step once one of the curation commands needs it.
- **Do not implement `clast registry remove <path>` or `--alias` removal.** Only `remove <slug>` ships in v1 per the contract. Removing individual aliases is a `doctor --fix` candidate, not a `registry` op.
- **Do not implement `clast projects` / `sessions` / `show`** even though those commands are the largest consumers of `clast_registry_resolve`. They land in step 07.
- **Do not modify `clast-manifest-lib.bash` or `clast-decode-lib.bash`.** The registry consumes both but neither needs new surface area. If a missing helper is genuinely required, stop and ask rather than expanding scope.
- **Do not add a config-file reader.** `~/.config/clast/config.toml` (mentioned in `overview.md#config-optional`) is a separate step's problem. `clast registry` reads only env vars (`CLAST_JOURNAL_DIR`) and CLI flags.
- **Do not write any subcommand other than `registry`.** Stubs for `snapshot`, `projects`, `sessions`, `show`, `entries`, `breadcrumb`, `stats`, `doctor` stay as `_clast_stub` calls in the dispatcher.
- **Do not invent additional registry fields** (e.g., `machine`, `notes`, `tags`) beyond the five listed in `cli-contract.md#registry-line-in-projectsjson`.
- **Do not add cross-machine path normalization** beyond `realpath -m`. Mapping `/Users/beau/...` ↔ `/home/beau/...` belongs in `aliases[]`, not in the resolver.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke — empty registry
export CLAST_JOURNAL_DIR="$(mktemp -d)"
bin/clast registry list             # prints header only
bin/clast registry list --json      # prints []

# Add a non-git entry
bin/clast registry add /tmp/proj-x --slug proj-x
bin/clast registry resolve /tmp/proj-x       # → proj-x
bin/clast registry resolve -tmp-proj-x       # → proj-x (segment form)
bin/clast registry resolve /tmp/nope; echo "rc=$?"   # → rc=1

# Add with a remote, then alias a second path under the same remote
bin/clast registry add /tmp/proj-y --slug proj-y --remote git@example.com:o/r.git
bin/clast registry add /tmp/proj-y-mirror --slug ignored --remote git@example.com:o/r.git
bin/clast registry resolve /tmp/proj-y-mirror   # → proj-y (slug overridden)

# Remove
bin/clast registry remove proj-y
bin/clast registry resolve /tmp/proj-y; echo "rc=$?"   # → rc=1
ls "$CLAST_JOURNAL_DIR"/transcripts 2>/dev/null   # → does not exist; remove did not create or delete anything outside projects.json

# whereami integration
mkdir -p /tmp/proj-x && cd /tmp/proj-x && bin/clast whereami --json | jq '{registered, slug}'
# → { "registered": "yes", "slug": "proj-x" }

cd /tmp && bin/clast whereami --json | jq '{registered, slug}'
# → { "registered": "no", "slug": null }

rm -rf "$CLAST_JOURNAL_DIR"
```

## Notes for the implementer

- **`jq` is the only JSON parser.** No `awk`, no `grep` over JSON, no `printf` JSON construction. The append path uses `jq -c -n --arg ...`; the read path uses `jq -cR 'fromjson?'` to tolerate corruption.
- **`realpath -m` matters.** GNU `realpath` with `-m` resolves paths that don't exist on disk (e.g., `whereami` running inside a non-git tmpdir whose parent does exist). BSD `realpath` does not accept `-m`; the dev shell pulls in GNU coreutils so this is fine, but flag it explicitly in a code comment so future-you doesn't try to drop `-m`.
- **Append semantics for `add` mirror the manifest's.** Each `add` is one `>>` of one JSONL line; "most recent line wins" semantics for the aliased path are handled at resolve time. Do NOT rewrite the whole file on `add`; only `remove` does that.
- **Why `remove` uses `clast_atomic_write` despite the manifest's "no whole-file rewrite" rule.** `remove` is the documented exception: it's user-initiated, infrequent, and the alternative (writing a tombstone line and filtering at read time) bloats the file forever. The cross-machine concurrency cost is "if you `remove` from two machines simultaneously, one wins" — acceptable.
- **`whereami` integration is the smallest possible diff.** Don't refactor `clast_cmd_whereami` while you're in there. Replace the two TODO stubs with their real values and update only the tests that assumed `registered="no"`.
- **Per-test fixture isolation.** `test/helpers.sh` already provides `make_fixture_journal_tree`; reuse it to copy `multi-project/` into a tmpdir. Never write to a real `$HOME/.claude/journal/` from a test.
- **Subcommand tests run `bin/clast` as a subprocess** (mirroring `test/test-whereami.sh`); lib tests source the lib directly. Keep the two layers separated — a bug in the dispatcher should fail `test-registry-cmd.sh` but not `test-registry.sh`.
- **Conventional commit suggestion**: `feat(registry): add clast-registry-lib.bash and clast registry subcommand`. If the `whereami` integration is meaningful enough to deserve its own commit (the diff is ~10 lines), a follow-up `feat(whereami): resolve registered/slug from registry` commit is fine; one squashed commit on merge is also fine. The PR title should describe both.
