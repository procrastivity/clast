---
step: 09
title: breadcrumb-subcommand
depends_on: [02, 03, 05]
size: small
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#conventions
  - docs/overview.md#glossary
  - docs/cli-contract.md#date-parsing
  - docs/cli-contract.md#clast-breadcrumb
  - docs/cli-contract.md#breadcrumb-file
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 09: `clast breadcrumb` (write / read / list)

## Context

Steps 02–05 produced every lib `breadcrumb` needs. `lib/clast/clast-lib.bash` exposes `clast_journal_dir`, `clast_today`, `clast_parse_date` (ISO / `today` / `yesterday` / `-Nd` / `-Nw`), `clast_log_*`, `clast_atomic_write`, and the `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` test hooks. `lib/clast/clast-registry-lib.bash` exposes `clast_registry_resolve` (path-or-segment → slug) and `clast_registry_list_json`. `bin/clast` already sources every lib at the top, parses global flags (`--json`, `--quiet`, `--verbose`, `--journal-dir`, `--projects-dir`), and dispatches `whereami`, `registry`, `snapshot`, `projects`, `sessions`, `show` to real subcommand files. The literal token `breadcrumb` is still routed through `_clast_stub` (currently labeled "planned for step 11" in the dispatcher map — that label is an out-of-date forecast that this step corrects). Step 08 (`entries`) is in flight on its own branch; this step does not depend on it and does not touch `entries/`.

A breadcrumb is a one-line in-flight hint written mid-session. It lives at `$(clast_journal_dir)/breadcrumbs/YYYY-MM-DD-<slug>.md` (or `$(clast_journal_dir)/breadcrumbs/YYYY-MM-DD-_global.md` when unscoped — the slug component is the literal string `_global`, with a leading underscore that prevents collision with a real project slugged `global`) and the file is a tiny YAML-frontmatter Markdown document whose body is an append-only `- HH:MM — <TEXT>` log. `/wakeup` reads the day's breadcrumbs for a project; `/day-wakeup` lists them across projects. The CLI surface this step ships is the substrate those skills will call into; no skill work happens here.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils (`date -d`, `realpath -m`, `stat -c`) are on PATH.

## Goal

Implement one new subcommand file — `lib/clast/clast-subcommands/breadcrumb.bash` — that dispatches `clast breadcrumb <TEXT>` (write, default), `clast breadcrumb --read`, and `clast breadcrumb --list`; wire it into `bin/clast` (replacing the `breadcrumb` stub); and add a `test/test-breadcrumb.sh` integration suite covering scoped + global writes, first-write frontmatter creation, append behavior, the read path, the list output (default + `--json`), and every documented error path. No new fixture is required: breadcrumb does not read the manifest or the snapshot tree, and project resolution is exercised via the existing `multi-project/projects-tree/` + `multi-project/journal-seed/projects.json` already shipped in step 06 / step 07.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — breadcrumbs live at `$(clast_journal_dir)/breadcrumbs/YYYY-MM-DD-<slug>.md`; the directory may not exist yet on a fresh journal. `_global.md` is the literal filename suffix for unscoped breadcrumbs (note the leading underscore — chosen so a project literally slugged `global` would not collide).
- `docs/overview.md#conventions` — ISO 8601 UTC with `Z` for any timestamp in JSON; snake_case JSON keys; exit codes (0 success, 1 general error, 2 usage error).
- `docs/overview.md#glossary` — `breadcrumb` is defined as "a one-line in-flight hint left mid-session" and lives at the path above; do not invent alternative storage.
- `docs/cli-contract.md#date-parsing` — `--date` (write) and `--day` (read/list) accept ISO / `today` / `yesterday` / `last-week` / `-Nd` / `-Nw`. `clast_parse_date` from step 02 already does this; do not re-implement.
- `docs/cli-contract.md#clast-breadcrumb` — the three synopses, write resolution order (`--project` → `--global` → registry-resolve `pwd`), the `- HH:MM — <TEXT>` append line shape, "create file with frontmatter if it doesn't exist," "silent on success unless `--verbose`," and the `--list` JSON shape `{project, path, line_count}`.
- `docs/cli-contract.md#breadcrumb-file` — the canonical file shape: a YAML frontmatter block (`date`, `project`) between `---` fences, a blank line, then one `- HH:MM — TEXT` line per breadcrumb. `project` in the frontmatter is the slug for scoped files and the literal string `_global` for the unscoped file.
- `docs/cli-contract.md#error-handling-conventions` — stderr-vs-stdout split, `{"error":"...","code":N}` shape under `--json`, exit codes (2 = missing required arg / usage / mutually-exclusive flags, 1 = unresolved project with neither `--project` nor `--global`, write failure).
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — one `clast_cmd_<name>` per file; argument parsing lives in the subcommand, not in libs.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions, subprocess-style integration tests.

## Tasks

1. **Write `lib/clast/clast-subcommands/breadcrumb.bash`.** Standard subcommand preamble: `# shellcheck shell=bash`, with `# shellcheck source=lib/clast/clast-lib.bash` / `clast-registry-lib.bash` / `clast-decode-lib.bash` directives at the top. **Do not** add a double-source guard at the subcommand layer. **Do not** source `clast-manifest-lib.bash` — breadcrumb does not read the manifest. Define exactly one public function `clast_cmd_breadcrumb`. Internal helpers use `_clast_breadcrumb_` prefix and live below the entry function.

2. **Inside `clast_cmd_breadcrumb`, pre-scan argv for mode.** Walk a copy of `"$@"` once (do NOT shift the real argv) and check whether `--read` or `--list` appears as a standalone token before `--`. Exactly one of `{write, --read, --list}` may apply: if `--read` and `--list` both appear, exit 2 with `clast: breadcrumb: --read and --list are mutually exclusive`. Otherwise dispatch:
   - `--list` present → strip the `--list` token from argv and call `_clast_breadcrumb_list "$@"`.
   - `--read` present → strip the `--read` token from argv and call `_clast_breadcrumb_read "$@"`.
   - Neither present → call `_clast_breadcrumb_write "$@"` (the default write path).
   - `-h|--help` at any position prints the combined three-mode usage and exits 0.

3. **Implement `_clast_breadcrumb_write` argument parsing.** Accept:
   - `--project SLUG` / `--project=SLUG` → set `project_filter` to a slug string. Mutually exclusive with `--global` (exit 2 with `clast: breadcrumb: --project and --global are mutually exclusive`).
   - `--global` → set `scope_global=1`. Mutually exclusive with `--project`.
   - `--date DATE` / `--date=DATE` → resolve via `clast_parse_date`; if unset, default to `clast_today` (which honors `CLAST_DAY_CUTOFF`). Validate that the resolved value matches `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`; otherwise exit 2.
   - `-h|--help` → exit 0 with usage.
   - Unknown flag → exit 2 with `clast: breadcrumb: unknown flag '<arg>'`.
   - One or more positional words make up the `<TEXT>` argument. Join surviving positionals (after flag parsing) with a single space. Reject embedded newlines or carriage returns inside any positional (exit 2 with `clast: breadcrumb: text must be a single line`). An empty `<TEXT>` (no positionals, or all whitespace after trim) → exit 2 with `clast: breadcrumb: missing required argument <TEXT>`.

4. **Resolve the project for write.** Priority order (matches `cli-contract.md#clast-breadcrumb`):
   1. If `--project SLUG` was given, use that slug verbatim. Do not require the slug to exist in the registry (a user may write a breadcrumb for a slug they intend to register later); emit a `clast_log_warn "slug '<slug>' not in registry"` (which writes unconditionally to stderr — `clast_log_warn` is not gated on `CLAST_VERBOSE`) so the user sees the typo case, but proceed with the write.
   2. Else if `--global` was given, set `slug=_global` and skip registry resolution.
   3. Else attempt `clast_registry_resolve "$PWD"`. On hit, use the resolved slug. On miss, exit 1 with `clast: breadcrumb: pwd does not resolve to a registered project (pass --project SLUG or --global)`. Under `--json`, emit `{"error":"...","code":1}` on stdout. Do NOT prompt — the CLI is non-interactive. **Note**: `docs/cli-contract.md#clast-breadcrumb` currently says "prompt to register or accept `--global`" — that wording is stale (it predates the CLI / skill split) and describes the future `/breadcrumb` skill's behavior, not the CLI's. This step's exit-1-with-hint behavior is the authoritative one; a follow-up docs pass should reword the contract section to match (out of scope here — do not edit `cli-contract.md` as part of this step).

5. **Compose the breadcrumb file path and the append line.** Path: `$(clast_journal_dir)/breadcrumbs/<resolved_date>-<slug>.md`. For the global scope, the literal filename is `<resolved_date>-_global.md` (single leading underscore on the slug component, matching `docs/overview.md#filesystem-reference`). Create the parent directory with `mkdir -p` (no error if it exists). The append line is `- HH:MM — <TEXT>` where:
   - `HH:MM` is the local time derived from the public `CLAST_NOW_EPOCH` test hook, with a `date +%s` fallback when unset: `local epoch="${CLAST_NOW_EPOCH:-$(date +%s)}"; date -d "@$epoch" +%H:%M`. Do NOT call `_clast_now_epoch` from the subcommand — it is an internal `_`-prefixed helper inside `clast-lib.bash` and this step does not modify libs. Do NOT use `date +%H:%M` directly without the env hook either — that would defeat the test-determinism path. (If a public `clast_now_epoch` accessor lands later, swap to it; for now the env-hook pattern is the minimum-coupling option.)
   - `—` is the literal U+2014 EM DASH, surrounded by single ASCII spaces on each side. Use a `$'—'`-equivalent bash literal (the bytes `\xe2\x80\x94` written directly in the source file) — `printf '—'` is not portable across bash builds.
   - `<TEXT>` is the cleaned positional text from task 3, untouched (no shell quoting transformation).

6. **First-write vs append.** If the file does not exist:
   - Compose the full file in memory: the YAML frontmatter block (exactly two keys, in this order: `date: <resolved_date>` then `project: <slug>`; for the global scope the value is the literal string `_global` with no quotes), the closing `---`, a blank line, then the single new `- HH:MM — TEXT` line.
   - Write via `clast_atomic_write` (compose into a sibling tempfile, fsync, atomic rename — same helper steps 04–08 use).
   If the file exists:
   - Append the single new `- HH:MM — TEXT` line. Use a plain `printf '%s\n' "$line" >> "$path"` — appends are race-safe enough for a single user's breadcrumb log; do not invent a lock. Do NOT touch the existing frontmatter even if it disagrees with `--date` (a date mismatch means the file was carried forward across midnight; that is the user's choice). If the file is missing a trailing newline (someone hand-edited it), prepend a `\n` to the appended bytes so the new entry starts on its own line.
   Write is silent on success unless `--verbose`; with `--verbose`, log `clast: breadcrumb: wrote <relative-path> (<n> lines)` to stderr. Under `--json`, write mode emits `{"path":"<absolute-path>","slug":"<slug>","date":"<date>","line_count":<n>}` on stdout (always — `--json` overrides `--quiet`).

7. **Implement `_clast_breadcrumb_read` argument parsing.** Accept:
   - `--project SLUG` / `--project=SLUG` (mutually exclusive with `--global`, same as write).
   - `--global` (mutually exclusive with `--project`).
   - `--day DATE` / `--day=DATE` → resolve via `clast_parse_date`; default `clast_today`.
   - `-h|--help` → exit 0 with usage.
   - Unknown flag or any positional argument → exit 2.
   Same project-resolution logic as task 4 (fall back to registry-resolve `pwd`; unresolved → exit 1). Once resolved, compute the file path identically to task 5. If the file exists, stream it to stdout via `cat --` and exit 0. If it does NOT exist, exit 0 with empty output (a missing breadcrumb file is the empty case, not an error — same convention as missing manifest in step 07). Under `--json`, emit `{"path":"<path>","exists":<bool>,"content":"<file body or empty string>"}` so a skill caller can distinguish "no file" from "empty file." `--quiet` suppresses the default-mode body but never the `--json` body or stderr.

8. **Implement `_clast_breadcrumb_list` argument parsing.** Accept:
   - `--day DATE` / `--day=DATE` → resolve via `clast_parse_date`; default `clast_today`.
   - `-h|--help` → exit 0 with usage.
   - Unknown flag or any positional → exit 2. `--project` / `--global` are NOT accepted by list (the whole point of list is "all breadcrumbs for the day, across projects"); rejecting them with the unknown-flag path is fine, no special message required.

9. **Implement `_clast_breadcrumb_list` discovery + output.** Source set is `$(clast_journal_dir)/breadcrumbs/<resolved_day>-*.md`. If the directory does not exist, the result set is empty (exit 0, default mode prints just the header, `--json` prints `[]`). For each matching file, derive:
   - `path`: absolute filesystem path on disk.
   - `project`: the filename suffix between `<resolved_day>-` and `.md`. For the literal slug component `_global`, the JSON `project` field is the string `_global` (unchanged); the default-mode column renders it as `(global)` for readability. Do NOT re-read the file's frontmatter for this — the filename is authoritative and cheaper.
   - `line_count`: count of body lines that match `^- ` (use `grep -c '^- ' "$file" || true` so a zero-count file does not exit non-zero under `pipefail`). This counts breadcrumb entries, not raw file lines — a five-line frontmatter plus two entries returns `2`.
   Sort by `project` ascending (`_global` sorts wherever its leading underscore lands under default `LC_ALL=C` — that is fine; do not special-case it).
   - **Default mode**: a single header line `project           path                                              breadcrumbs` (project width 17, path width 50, breadcrumbs right-aligned width 11) followed by one row per file. Empty result set still prints the header. Use `printf` with fixed-width fields.
   - **`--json` mode**: emit a JSON array via `jq -n --argjson rows '...'` (build the array up front, one jq invocation). Always print, even when empty (`[]`). Per-row shape: `{"project":"<slug or _global>","path":"<absolute path>","line_count":<int>}`.
   - **`--quiet`** suppresses the default-mode body but NOT stderr; `--json` is unaffected by `--quiet`.

10. **Wire the subcommand into the dispatcher.** In `bin/clast`, replace the single `_clast_stub` case for `breadcrumb` with `source ...; clast_cmd_breadcrumb "$@" ;;` (mirrors the pattern used by `whereami` / `snapshot` / `projects` / `sessions` / `show` / `registry`). Leave every other stub (`entries`, `stats`, `doctor`) untouched — `entries` belongs to step 08, the others to step 10. The `_clast_stub` helper itself stays; only the `breadcrumb)` case branch changes.

11. **Write `test/test-breadcrumb.sh`.** Subprocess-style suite modeled on `test/test-snapshot.sh` and `test/test-query.sh`. `cd` to repo root, `source test/helpers.sh`, set `_CLAST_TEST_NAME=test-breadcrumb`, set `CLAST_BIN="$PWD/bin/clast"` (mirror the existing suites — every invocation goes through `"$CLAST_BIN"`, never `bin/clast` directly), and `export TZ=UTC` at the top of the file (matches `test/test-snapshot.sh:90` — `HH:MM` is derived from `CLAST_NOW_EPOCH` formatted in local time, so without `TZ=UTC` the timestamp assertions flake on any developer/CI host whose local zone is not UTC). Each scenario calls `setup_test_journal`, optionally seeds the registry via `make_fixture_projects_tree_from multi-project/projects-tree` and a stub `projects.json` (or `make_fixture_journal_seed_from multi-project/journal-seed` if the `projects.json` already exists there is what you want — re-use, do not duplicate), exports `CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T14:23:00Z' +%s)` for deterministic `HH:MM` (always `-u` so the epoch math is timezone-free even before `TZ=UTC` takes effect on the CLI side), runs `"$CLAST_BIN" breadcrumb …`, asserts on stdout / stderr / exit code / file bytes, then `teardown_test_journal`. Cover at minimum:
    - **First write, scoped via `--project`**: `"$CLAST_BIN" breadcrumb --project xesapps 'check migration before deploy'` exits 0, silent stdout, file `breadcrumbs/2026-05-30-xesapps.md` exists with a 4-line frontmatter (`---` / `date: 2026-05-30` / `project: xesapps` / `---`), one blank line, then `- 14:23 — check migration before deploy`.
    - **Append, scoped**: a second invocation with the same `--project` and a new text (advance `CLAST_NOW_EPOCH` by 1h44m so the second timestamp is `16:07`) leaves the frontmatter untouched and appends `- 16:07 — figure out why EXPLAIN differs in CI` on its own line. File ends in `\n`. Total of two `- ` lines in the body.
    - **First write, `--global`**: file is `breadcrumbs/2026-05-30-_global.md`; frontmatter has `project: _global`.
    - **First write, resolved from `pwd`**: `pushd "$CLAST_PROJECTS_DIR/-home-beau-code-xesapps"` (or whatever segment the fixture exposes) with the registry pointing at it, then `"$CLAST_BIN" breadcrumb 'no flag'` resolves to the `xesapps` slug.
    - **Unresolved pwd, no `--project`, no `--global`**: exit 1, stderr mentions `--project SLUG or --global`. Default mode AND `--json` form (`"$CLAST_BIN" --json breadcrumb 'x'`) both assert.
    - **`--project` and `--global` together**: exit 2, stderr mentions mutual exclusion.
    - **Empty text**: `"$CLAST_BIN" breadcrumb --global ''` (or no positional) exits 2 with the missing-`<TEXT>` message.
    - **Multi-word text**: `"$CLAST_BIN" breadcrumb --global remember to bump the cache version` joins positionals with single spaces into one breadcrumb line.
    - **Text with embedded newline**: `"$CLAST_BIN" breadcrumb --global $'line1\nline2'` exits 2 with the single-line message; no file is created.
    - **`--date` override**: `"$CLAST_BIN" breadcrumb --global --date 2026-05-22 'historic note'` writes `breadcrumbs/2026-05-22-_global.md`, not the `today` file. Invalid `--date foo` exits 2.
    - **`--verbose` write**: `"$CLAST_BIN" --verbose breadcrumb --global 'x'`; stderr includes `wrote breadcrumbs/2026-05-30-_global.md (1 lines)` (or whatever the chosen literal wording is — assert on the path and line count substrings, not on exact prose).
    - **`--json` write**: `"$CLAST_BIN" --json breadcrumb --global 'x'`; stdout is a valid JSON object with `path`, `slug`, `date`, `line_count`; `line_count` reflects the post-write total.
    - **`--read` of an existing file**: `"$CLAST_BIN" breadcrumb --read --project xesapps --day 2026-05-30` cats the file to stdout. With the JSON form `"$CLAST_BIN" --json breadcrumb --read --project xesapps --day 2026-05-30`, `exists` is `true` and `content` equals the file body byte-for-byte. (Global `--json` precedes the subcommand name — the dispatcher only parses global flags before the subcommand. Breadcrumb specifically reads `CLAST_JSON` from env and does NOT accept a subcommand-level `--json`; some other subcommands like `whereami` do re-parse `--json` locally for ergonomics, but breadcrumb intentionally does not — keep the global-flag-first invocation in every test.)
    - **`--read` of a missing file**: exit 0, empty stdout (default mode); `"$CLAST_BIN" --json breadcrumb --read ...` returns `exists: false`, `content: ""`.
    - **`--list` empty**: no `breadcrumbs/` directory at all → exit 0, default mode prints just the header row, `"$CLAST_BIN" --json breadcrumb --list` prints `[]`.
    - **`--list` with two files**: after writing one xesapps file (2 entries) and one global file (1 entry) on `2026-05-30`, `"$CLAST_BIN" --json breadcrumb --list --day 2026-05-30` returns an array of length 2 with the correct `line_count` per row; default mode renders `_global` as `(global)` and `xesapps` as `xesapps`, line counts in the right column.
    - **`--list` ignores other days**: writing one file on `2026-05-22` and one on `2026-05-30`, then `"$CLAST_BIN" breadcrumb --list --day 2026-05-30` returns only the `2026-05-30` row.
    - **`--read` and `--list` mutually exclusive**: `"$CLAST_BIN" breadcrumb --read --list` exits 2.
    - **`--help` and unknown subcommand-level flag**: `"$CLAST_BIN" breadcrumb --help` exits 0 with usage; `"$CLAST_BIN" breadcrumb --bogus foo` exits 2.

12. **Wire `test/test-breadcrumb.sh` into `test/test-clast.sh`.** Append it to the `suites` array after `test/test-query.sh` (and after `test/test-entries.sh` if that suite has already landed via step 08 — the relative order between entries and breadcrumb does not matter; alphabetize by suite name to keep the list stable). New order (assuming step 08 has merged first): lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot → query → entries → breadcrumb. If step 08 has not yet merged when this step executes, append after `query` and leave the entries-suite insertion for whoever merges last (a one-line conflict either way).

13. **Update README.md** with a small "Leave a breadcrumb" block (or extend the existing usage section). Two-line example each for the scoped, global, and read forms. Do not document `--date` / `--day` resolution details in the README — link to `docs/cli-contract.md#clast-breadcrumb`.

14. **Confirm `make lint` and `make test` pass.** The new subcommand file needs explicit `# shellcheck source=...` directives for every lib it sources (`clast-lib.bash`, `clast-registry-lib.bash`, `clast-decode-lib.bash`). `make test` must run every suite (existing + the new `test-breadcrumb.sh`) and exit 0.

## Acceptance criteria

- `lib/clast/clast-subcommands/breadcrumb.bash` exists, exports exactly one `clast_cmd_breadcrumb` public function, and passes `shellcheck`.
- `bin/clast` routes `breadcrumb` to the real subcommand file; the `_clast_stub` entry for `breadcrumb` is gone. Other stubs (`entries`, `stats`, `doctor`) are untouched.
- `clast breadcrumb <TEXT>` resolves the project from `--project`, `--global`, or registry-resolve `pwd` (in that priority order). Unresolved pwd without `--project` / `--global` exits 1 with a message that names both fallbacks.
- `clast breadcrumb --project xesapps 'hint'` creates `breadcrumbs/YYYY-MM-DD-xesapps.md` with frontmatter `date: YYYY-MM-DD` / `project: xesapps` on first write, and appends `- HH:MM — hint` on every subsequent write.
- `clast breadcrumb --global 'hint'` writes to `breadcrumbs/YYYY-MM-DD-_global.md` with `project: _global` in the frontmatter.
- `clast breadcrumb --date 2026-05-22 --global 'hint'` writes to the `2026-05-22-_global.md` file instead of the `today` file.
- `clast breadcrumb <TEXT>` is silent on stdout on success unless `--verbose`; `--json` always prints a JSON object with `path`, `slug`, `date`, `line_count`.
- `clast breadcrumb` rejects empty `<TEXT>` (exit 2), embedded newlines in `<TEXT>` (exit 2), mutually-exclusive `--project` + `--global` (exit 2), and invalid `--date` (exit 2).
- `clast breadcrumb --read [--project|--global] [--day DATE]` cats the breadcrumb file to stdout; a missing file is exit 0 with empty output (default mode) or `{exists: false, content: ""}` (`--json`).
- `clast breadcrumb --list [--day DATE]` lists every breadcrumb file for the day with `{project, path, line_count}` per row; `--json` returns an array (`[]` when empty); default mode renders `_global` as `(global)`.
- `clast breadcrumb --read --list` exits 2 with a mutual-exclusion message.
- Missing `breadcrumbs/` directory is tolerated as the empty case by `--list` (exit 0); the write path creates the directory on demand.
- `test/test-breadcrumb.sh` covers every scenario listed in task 11 and exits 0.
- `make test` runs every suite including `test-breadcrumb.sh` and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement the `/breadcrumb` skill.** The skill layer (the LLM-facing prompt that calls `clast breadcrumb`) belongs to step 13. The CLI is non-interactive: when `pwd` does not resolve and neither `--project` nor `--global` is given, exit 1 with a hint — do NOT prompt to register, do NOT spawn an editor.
- **Do not modify `clast-lib.bash`, `clast-decode-lib.bash`, `clast-manifest-lib.bash`, or `clast-registry-lib.bash`.** If a required helper is missing, stop and ask rather than expanding scope. The only allowed touches outside the new subcommand file are `bin/clast` (task 10), `test/test-clast.sh` (task 12), `test/test-breadcrumb.sh` (new), and `README.md` (task 13).
- **Do not read or write `entries/`.** That directory is step 08's territory. Breadcrumb's `curated` relationship to entries (if any) is a `/wakeup`-time synthesis, not a CLI-time join.
- **Do not read the manifest, the snapshot tree, or `projects/` source files.** Breadcrumb is a pure write-side feature against the registry; it does not need any capture state. Do NOT source `clast-manifest-lib.bash`.
- **Do not add `--list --json --since/--until` windowing.** The contract is single-day list; multi-day aggregation is a future ergonomic that lives in v1.1. Reject anything that is not `--day` with exit 2.
- **Do not add a global breadcrumb-line lock or rotation.** Append is a single-`printf` write; multi-writer races are an acknowledged "v1.1 if it ever bites" risk, not a problem worth solving now.
- **Do not invent a richer frontmatter.** The two-key (`date`, `project`) shape from `docs/cli-contract.md#breadcrumb-file` is canonical. No `author`, no `machine`, no `tags` — those are entry concepts.
- **Do not change the dispatcher's global-flag parsing** (the `--journal-dir` / `--projects-dir` / `--json` / `--quiet` / `--verbose` handling). Task 10 is a surgical replace of one case branch.
- **Do not add a `breadcrumbs-seed/` fixture.** The write tests build their own files; the list / read tests build on top of those writes. Adding a fixture for a step whose primary tests are "did this write produce these bytes" creates indirection without value.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke
export TZ=UTC                    # the append-line HH:MM is formatted in local time; pin to UTC so the smoke output matches the comments below regardless of your shell's zone
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
cp test/fixtures/multi-project/journal-seed/projects.json "$CLAST_JOURNAL_DIR/projects.json"
export CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T14:23:00Z' +%s)

# Write — scoped
bin/clast breadcrumb --project xesapps 'check migration before deploy'
cat "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md"

# Append — scoped (advance the clock)
export CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T16:07:00Z' +%s)
bin/clast breadcrumb --project xesapps 'figure out why EXPLAIN differs in CI'
cat "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md"

# Write — global
bin/clast breadcrumb --global 'remember to bump the cache version'
cat "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md"

# Read  (global --json precedes the subcommand name; the dispatcher only
# parses global flags before the subcommand. Breadcrumb reads CLAST_JSON
# from env and does NOT accept a subcommand-level --json; some other
# subcommands like whereami do re-parse --json locally, but breadcrumb
# intentionally does not.)
bin/clast breadcrumb --read --project xesapps --day 2026-05-30
bin/clast --json breadcrumb --read --global --day 2026-05-30 | jq

# List
bin/clast breadcrumb --list --day 2026-05-30
bin/clast --json breadcrumb --list --day 2026-05-30 | jq

# Negative paths
bin/clast breadcrumb                                                  ; echo "exit=$?"  # 1 (no pwd resolution, no flag)
bin/clast breadcrumb --project xesapps --global 'x'                   ; echo "exit=$?"  # 2
bin/clast breadcrumb --project xesapps ''                             ; echo "exit=$?"  # 2
bin/clast breadcrumb --read --list                                    ; echo "exit=$?"  # 2
bin/clast breadcrumb --global --date not-a-date 'x'                   ; echo "exit=$?"  # 2

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_NOW_EPOCH TZ
```

## Notes for the implementer

- **Breadcrumb is the smallest curation surface in the CLI.** Resist any urge to make it "complete": no editing, no deletion, no in-line tags, no priorities. Append-only is the entire point — a breadcrumb is a sticky-note someone leaves themselves for `/wakeup` to find.
- **The CLI is non-interactive.** The spec's `cli-contract.md` line "prompt to register or accept `--global`" refers to the future `/breadcrumb` skill, which is an LLM wrapper. The CLI must NEVER prompt — exit 1 with a clear message so the skill (or a human in a terminal) can decide what to do.
- **Filename slug component is authoritative for list.** The frontmatter inside the file is for human readers and for any future tool that joins breadcrumbs by content. The list command derives `project` from the filename and never reads the file body — this keeps `--list` cheap (one `stat` + one `grep -c` per file).
- **Single-line text discipline.** Breadcrumbs are explicitly one-line hints. Accept multi-word text (join positionals with spaces), reject embedded newlines. A user who wants a multi-line note should write an entry, not a breadcrumb.
- **`_global` underscore prefix is deliberate.** A project literally slugged `global` would otherwise collide with the unscoped file. Don't strip the underscore in the JSON `project` field — it's load-bearing data; only the default-mode `(global)` rendering is cosmetic.
- **`CLAST_NOW_EPOCH` drives `HH:MM`.** Step 02 established that any "now" timestamp the CLI emits must flow through the epoch hook so tests are deterministic. The append-line timestamp is no exception. A test that depends on `date +%H:%M` directly will flake on the wall clock.
- **First-write composition vs append should look obviously different in the source.** The first-write path goes through `clast_atomic_write` (compose-then-rename). The append path is a single `printf >>`. Don't try to unify them into one branch — the asymmetry is the safety invariant.
- **Conventional commit suggestion**: `feat(breadcrumb): implement clast breadcrumb (write/read/list)`. One commit is fine; if the README touch grows beyond a few lines, a follow-up `docs(readme): add breadcrumb usage` is also fine.
