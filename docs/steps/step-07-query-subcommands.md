---
step: 07
title: query-subcommands
depends_on: [02, 03, 04, 05, 06]
size: medium
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#glossary
  - docs/overview.md#conventions
  - docs/cli-contract.md#date-parsing
  - docs/cli-contract.md#clast-projects
  - docs/cli-contract.md#clast-sessions
  - docs/cli-contract.md#clast-show
  - docs/cli-contract.md#manifest-line
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 07: `clast projects`, `clast sessions`, `clast show`

## Context

Steps 02–06 produced everything this step reads from. `lib/clast/clast-lib.bash` provides `clast_journal_dir`, `clast_today`, `clast_parse_date` (with `today`/`yesterday`/`-Nd`/ISO/`-Nw` forms), `clast_log_*`, `clast_atomic_write`, plus the `CLAST_DAY_CUTOFF` / `CLAST_NOW_EPOCH` test hooks. `lib/clast/clast-manifest-lib.bash` exposes `clast_manifest_iterate`, `clast_manifest_lookup`, `clast_manifest_has_capture`, and (since step 04) writes the seven-field manifest line schema from `cli-contract.md#manifest-line`. `lib/clast/clast-registry-lib.bash` exposes `clast_registry_list_json` and `clast_registry_resolve`. `bin/clast` already parses global flags (`--json`, `--quiet`, `--verbose`, `--journal-dir`, `--projects-dir`), exports `CLAST_JSON`/`CLAST_QUIET`/`CLAST_VERBOSE`, sources the three libs, and dispatches `whereami`, `registry`, and `snapshot` to real subcommand files. The literal tokens `projects`, `sessions`, and `show` are still routed through `_clast_stub` (currently labeled steps 07, 08, 09 in the dispatcher map — those labels are an out-of-date forecast that this step corrects).

After step 06 the journal contains real data: `.manifest.jsonl` rows for each captured session plus the captured JSONL files under `transcripts/<day_bucket>/<segment>/<uuid>.jsonl`. This step adds the read half of the CLI — a user can list which projects had activity, list sessions in a window, and dump one session's metadata — without any new capture logic. The manifest is the single source of truth for what was captured; per-session counts and timestamps that aren't in the manifest are derived by streaming the captured JSONL on demand.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils (`date -d`, `realpath -m`, `stat -c`) are on PATH.

## Goal

Implement three new subcommand files — `lib/clast/clast-subcommands/projects.bash`, `sessions.bash`, `show.bash` — each exporting a single `clast_cmd_<name>` entry point, wire them into `bin/clast` (replacing the three stubs), extend the `multi-project/` fixture with a pre-populated journal that exercises the read paths, and add a `test/test-query.sh` integration suite covering every documented flag combination.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — the manifest at `$(clast_journal_dir)/.manifest.jsonl` is the authoritative index; snapshots live at `$(clast_journal_dir)/transcripts/<day_bucket>/<segment>/<uuid>.jsonl`. `entries/` is read by step 08 — DO NOT pre-implement `curated` true detection beyond a "file exists at the expected glob" probe (see task 6).
- `docs/overview.md#glossary` — output strings and JSON keys use these terms verbatim (`session_id`, `segment`, `day_bucket`, `snapshot_path`, `msg_count_approx`).
- `docs/overview.md#conventions` — ISO 8601 UTC with `Z`; snake_case JSON keys; exit codes (0 success, 2 usage error, 4 data integrity).
- `docs/cli-contract.md#date-parsing` — `--day`, `--since`, `--until` accept ISO date, `today` / `yesterday` / `last-week`, and relative offsets `-Nd` / `-Nw`. `clast_parse_date` from step 02 already does this; do not re-implement.
- `docs/cli-contract.md#clast-projects` — exact flag set, default (column) output, `--json` schema (`slug`, `path`, `segment`, `remote`, `session_count`, `msg_count_approx`, `last_active`, `registered`).
- `docs/cli-contract.md#clast-sessions` — flags, default (column) output, `--json` schema (`session_id`, `project`, `segment`, `branch`, `start`, `end`, `msg_count_approx`, `snapshot_path`, `day_bucket`, `curated`).
- `docs/cli-contract.md#clast-show` — positional `<session-id>`, `--full` / `--turns N`, default key/value output, `--json` schema.
- `docs/cli-contract.md#manifest-line` — the seven manifest fields (`session_id`, `source`, `snapshot`, `captured_at`, `source_mtime`, `source_size`, `day_bucket`) are the only fields you can trust without re-reading the snapshot.
- `docs/cli-contract.md#error-handling-conventions` — stderr-vs-stdout split, `{"error":"...","code":N}` shape under `--json`.
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — one `clast_cmd_<name>` per file; argument parsing lives in the subcommand, not in libs.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions, subprocess-style integration tests.

## Tasks

1. **Write `lib/clast/clast-subcommands/projects.bash`.** Standard subcommand preamble: `# shellcheck shell=bash`, with `# shellcheck source=lib/clast/clast-lib.bash` / `clast-manifest-lib.bash` / `clast-registry-lib.bash` / `clast-decode-lib.bash` directives at the top. **Do not** add a double-source guard at the subcommand layer. Define exactly one public function `clast_cmd_projects`. Internal helpers use `_clast_projects_` prefix and live below the entry function.

2. **Parse `clast projects` arguments inside `clast_cmd_projects`.** Accept the four documented flags plus `-h|--help`:
   - `--day DATE` / `--day=DATE` → set `day_filter` (resolved to ISO via `clast_parse_date`); mutually exclusive with `--since` / `--until` (exit 2 if combined).
   - `--since DATE` / `--since=DATE` → set `since_date` after `clast_parse_date`.
   - `--until DATE` / `--until=DATE` → set `until_date` after `clast_parse_date`. If neither `--day` nor `--since`/`--until` is given, default to `--day today` (matches `cli-contract.md#clast-projects`'s "today" default).
   - `--unregistered` → set `unregistered_only=1`.
   - `-h|--help` → print synopsis + flag summary to stdout, exit 0.
   - Unknown flag / extra positional → exit 2 with `clast: projects: unknown flag '<arg>'` on stderr (`{"error":"...","code":2}` under `--json`).
   - **Do not** re-parse global flags; read `CLAST_JSON` / `CLAST_QUIET` directly.

3. **Stream manifest rows for the requested window.** Build a jq select-body string that filters on `day_bucket`:
   - `--day D` → `.day_bucket == "D"`.
   - `--since S` / `--until U` (either or both) → `.day_bucket >= "S"` and/or `.day_bucket <= "U"` joined by `and`. Lexicographic string compare is correct because `day_bucket` is always `YYYY-MM-DD`.

   Call `clast_manifest_iterate "$filter"` and stream each line. A missing manifest must produce empty result sets (NOT an error); `clast_manifest_iterate` already returns 0 with no output. Aggregate per-segment in a bash associative array keyed by `segment` (derived from the manifest line's `snapshot` path: the middle path component after `transcripts/<day_bucket>/`). For each segment, track `session_count` (distinct `session_id` count — use a per-segment `declare -A seen` sub-key) and `last_active` (max `source_mtime` ISO string; lex compare works because all timestamps are `Z`-suffixed UTC).

4. **Compute `msg_count_approx` per project.** Sum `wc -l` over each distinct snapshot file that contributed to the bucket. To avoid double-counting re-snapshots of the same session, use only the **most-recent** manifest line per `session_id` within the window — same "most recent line wins" rule that `clast_manifest_lookup` uses. Read the file via `wc -l < "$snap_abs_path"`; an unreadable / missing snapshot is a partial-failure condition: warn to stderr with `clast_log_warn` (visible with `--verbose`), treat its contribution as 0, and continue. The summary's exit code stays 0 unless a `--json` consumer specifically needs to know — keep that for a future doctor step, not here.

5. **Resolve project metadata via the registry.** For each segment, call `clast_registry_resolve "$segment"` (the lib already handles "path-or-segment" input). On hit, populate `slug`, `path`, `remote`, `registered=true`. On miss, populate `slug=null` (`(unregistered)` in the human view), `path=clast_decode_segment "$segment"` if exactly one candidate matches, otherwise null; `remote=null`; `registered=false`. Apply `--unregistered` filtering AFTER resolution: when set, drop rows where `registered=true`.

6. **Emit `clast projects` output.**
   - **Default mode**: a single header line `slug              path                              sessions  msgs   last_active` followed by one row per project, sorted by `(-session_count, slug)`. Use `printf` with fixed-width fields matching `cli-contract.md#clast-projects`'s example (column widths 17 / 33 / right-aligned 9 / right-aligned 5 / 11). `last_active` is the `HH:MM` portion of the latest `source_mtime` IF the window is a single day (`--day` or `--since == --until`); otherwise emit the full ISO date `YYYY-MM-DD`. `(unregistered)` slug renders literally; missing path renders as empty string padded to width.
   - **`--json` mode**: emit a JSON array via `jq -n --argjson rows '...'` (build the array up front to keep one jq invocation rather than per-row); always print, even when empty (`[]`). `last_active` in JSON is always the full ISO 8601 timestamp.
   - **`--quiet`** suppresses the stdout body but NOT stderr; `--json` is unaffected by `--quiet`.

7. **Write `lib/clast/clast-subcommands/sessions.bash`.** Same conventions as task 1 (`# shellcheck shell=bash`, source directives for lib / manifest / registry / decode, no double-source guard, single `clast_cmd_sessions` public entry, `_clast_sessions_` helpers below).

8. **Parse `clast sessions` arguments inside `clast_cmd_sessions`.** Same window flags as projects (`--day`, `--since`, `--until`, default `--day today`, mutually-exclusive rules), plus:
   - `--project SLUG` / `--project=SLUG` → set `project_filter` (a slug, NOT a segment; resolve to segments via `clast_registry_list_json` so multiple registry rows sharing a slug all match).
   - `-h|--help` / unknown flag → as in task 2.

9. **Stream + transform manifest rows for `clast sessions`.** Reuse the window-to-jq-filter helper from task 3 (factor it into a small `_clast_query_window_filter` if both subcommands grow to need it; otherwise inline both — do NOT create a new shared lib file). For each row pick the most-recent manifest line per `session_id` within the window. Then for each surviving line:
   - `session_id`, `segment`, `snapshot_path`, `day_bucket` come straight from the manifest.
   - `project` (slug) via `clast_registry_resolve "$segment"`; fall back to the segment string if unresolved.
   - `start`: first-line `.timestamp` of the snapshot JSONL (`head -n1 ... | jq -r '.timestamp // empty'`). If absent, fall back to `source_mtime`.
   - `end`: last-line `.timestamp` (`tail -n1 ... | jq -r '.timestamp // empty'`). If absent, fall back to `source_mtime`.
   - `branch`: scan the same snapshot via `jq -r '.cwd // .git_branch // empty'` etc. — defer the exact field name to whatever the source JSONL surfaces; if no field is reliably present in the captured JSONL today, leave `branch` as `null` (JSON) / empty (column view) and add a `# TODO(step-10): branch field is best-effort; revisit when stats command lands` comment. **Do not** invent a parser for arbitrary embedded metadata.
   - `msg_count_approx`: `wc -l < "$snap_abs_path"`.
   - `curated`: probe for `$(clast_journal_dir)/entries/*-<session_slug>*.md` or any file whose frontmatter `session_id` matches. Implement the cheap path: `grep -l "session_id: $session_id" "$(clast_journal_dir)/entries/"*.md 2>/dev/null` and treat any hit as `curated: true`. If `entries/` does not yet exist, `curated: false` always. (Step 08 owns the writer; this step only reads.)

10. **Apply `--project` filter.** Build the set of acceptable segments by streaming `clast_registry_list_json | jq -r --arg slug "$project_filter" '.[] | select(.slug == $slug) | .path' | xargs -I{} clast_encode_path {}` (use the decode-lib's `clast_encode_path`). Drop manifest rows whose segment is not in that set. If the slug doesn't resolve to any registered project, exit 0 with empty output (NOT an error — analogous to "no sessions found", per `cli-contract.md`).

11. **Emit `clast sessions` output.**
   - **Default mode**: header `session_id                            project           branch                    start  end    msgs` then one row per session, sorted by `start` ascending. Column widths from `cli-contract.md#clast-sessions`. `start` / `end` are `HH:MM` when both fall on the same day_bucket, else `YYYY-MM-DD HH:MM`.
   - **`--json` mode**: array of objects matching the documented schema; always print, even when empty.
   - `--quiet` / `--json` interaction same as task 6.

12. **Write `lib/clast/clast-subcommands/show.bash`.** Same conventions (preamble, source directives, single public entry, `_clast_show_` helpers).

13. **Parse `clast show` arguments inside `clast_cmd_show`.** Accept:
    - One positional `<session-id>` (required; exit 2 if missing or if more than one positional). Validate against the UUID regex used in step 06; reject with exit 2 otherwise.
    - `--full` (no value) → set `include_turns=1`.
    - `--turns N` / `--turns=N` → set `turn_count=N`; default 5. Validate as a positive integer; otherwise exit 2.
    - `-h|--help` → exit 0 with usage.
    - Unknown flag → exit 2.

14. **Resolve `clast show` data sources.** Look up the manifest line via `clast_manifest_lookup "$session_id"`. If missing, exit 1 with `clast: show: session '<id>' not found in manifest` (or `{"error":"...","code":1}` under `--json`). From the manifest line, take `snapshot`, `day_bucket`, `source_mtime`, `source_size`. Compute the absolute snapshot path as `$(clast_journal_dir)/<snapshot>`. If the snapshot file is missing on disk, exit 1 with `clast: show: snapshot file missing on disk (run 'clast doctor')` — that's the orphan-manifest path documented in step 06's invariants.

15. **Compute show fields.** Same derivations as in task 9 (segment via registry resolve, branch best-effort, msg_count via `wc -l`, start/end via head/tail). Add two `clast show`-only fields:
    - `first_prompt`: the first `.message.content` (or whatever the source's first user message field is) — defer the exact JSON path to whatever the captured JSONL exposes today; if uncertain, scan with `jq -r 'select(.role == "user" or .message.role == "user") | (.message.content // .content // empty) | if type == "array" then map(.text? // "") | join(" ") else . end' "$snap" | head -n1`. Truncate to 120 chars + `…` if longer. If no user message is found, omit the field (JSON `null`).
    - `last_prompt`: same logic, `tail -n1` of the user-message stream.
    - `duration`: `end_epoch - start_epoch` formatted as `Xh Ym` / `Xm Ys` / `Xs` (pick whichever is non-zero). Skip from JSON if either end is missing.

16. **Emit `clast show` output.**
    - **Default mode**: key/value lines matching `cli-contract.md#clast-show`'s exact example (column-aligned with a colon, two-space indent for nested values). With `--full`, append:
      ```
      ## First N turns
      <plain text>
      ## Last N turns
      <plain text>
      ```
      where `N` = `turn_count`. "Turns" here means user-or-assistant messages with non-empty text content, in JSONL order. Same `jq` extraction as `first_prompt`, but pulling both user and assistant roles and limiting to `head -n $((turn_count*2))` / `tail -n $((turn_count*2))` (one user + one assistant per turn, hence ×2). No tool-call output, no embedded JSON — strip to plain text.
    - **`--json` mode**: a single object with the same fields. `--full` adds `first_turns: [...]` and `last_turns: [...]` arrays of `{role, text}` objects. Always print.
    - **`--quiet`**: suppresses the default key/value body but NOT stderr / `--json`.

17. **Wire the three subcommands into the dispatcher.** In `bin/clast`, replace the three `_clast_stub` cases for `projects`, `sessions`, `show` with real `source ...; clast_cmd_<name> "$@" ;;` branches. Each branch must ALSO source `clast-manifest-lib.bash` (currently sourced inline only by `snapshot)` — promote that to a top-level `source` next to the other libs, so all four subcommands that need the manifest don't repeat the line. Leave the other stubs (`entries`, `breadcrumb`, `stats`, `doctor`) untouched; only the three this step owns are wired.

18. **Extend the `multi-project/` fixture with a pre-populated journal.** Step 06 already added `test/fixtures/multi-project/projects-tree/` (the source side). Now add the captured side under `test/fixtures/multi-project/journal-seed/` so query tests don't have to run `clast snapshot` first:
    - `journal-seed/.manifest.jsonl` — seven-field rows pointing at the snapshots below. Include at least: two `xesapps` sessions on `2026-05-29` and `2026-05-30`, one `-tmp-proj-scratch` session on `2026-05-30`, and one historic `xesapps` session on `2026-05-22` (to exercise `--since` / `--until` window filtering).
    - `journal-seed/transcripts/2026-05-22/-tmp-proj-xesapps/<uuid>.jsonl` and the matching `2026-05-29` / `2026-05-30` directories — small JSONL files (5–20 lines each) whose first line includes `.timestamp` and whose body has at least one user message and one assistant message so `first_prompt` / `last_prompt` / `--full` can be exercised.
    - `journal-seed/projects.json` — copy the registry from step 06's fixture (or symlink-via-helper if `make_fixture_journal_seed` already supports it; otherwise inline a small `projects.json`).
    - One snapshot file deliberately referenced by the manifest but ABSENT on disk, to exercise task 14's orphan-manifest exit-1 path. Pick a session_id and manifest entry that no other test row depends on.

19. **Add `make_fixture_journal_seed_from <name>/<subpath>` to `test/helpers.sh`** (mirroring step 06's additive `make_fixture_projects_tree_from`). It copies the fixture's `journal-seed/` subtree into `$CLAST_JOURNAL_DIR`. Keep all existing helper signatures intact. If the existing `setup_test_journal` already accepts an optional seed-path argument, extend that one instead of adding a parallel function — pick whichever change is strictly additive.

20. **Write `test/test-query.sh`.** Subprocess-style suite modeled on `test/test-snapshot.sh`. `cd` to repo root, `source test/helpers.sh`, set `_CLAST_TEST_NAME=test-query`. Each scenario calls `setup_test_journal`, seeds via the new helper from task 19, runs `bin/clast projects|sessions|show ...`, asserts on stdout/stderr/exit code, then `teardown_test_journal`. Cover at minimum:
    - **`clast projects` defaults to today**: with `CLAST_NOW_EPOCH` frozen to `2026-05-30T12:00:00Z`, no flags, lists exactly the projects with `day_bucket=2026-05-30` activity (two projects in the seed).
    - **`clast projects --day 2026-05-29`**: returns only the xesapps row, with `session_count=1` and `last_active` rendering as `HH:MM`.
    - **`clast projects --since 2026-05-22 --until 2026-05-30`**: returns all three projects across the window; `last_active` renders as `YYYY-MM-DD HH:MM` (multi-day window).
    - **`clast projects --day 2026-05-30 --json`**: stdout is valid JSON, array of length 2, each object has the eight documented fields, `last_active` is full ISO 8601 with `Z`.
    - **`clast projects --unregistered`**: returns only the `-tmp-proj-scratch` row (no registry hit).
    - **`clast projects --day` combined with `--since`**: exits 2, stderr mentions mutual exclusion.
    - **Empty manifest**: no manifest file at all → exit 0, default mode prints just the header row (or nothing — pick one and document it in task 6; the test asserts whatever you picked), `--json` prints `[]`.
    - **`clast sessions` default**: today filter, returns the two sessions on `2026-05-30`, sorted by `start` ascending.
    - **`clast sessions --project xesapps --since 2026-05-22 --until 2026-05-30`**: returns the three xesapps sessions across the window; the scratch session is excluded.
    - **`clast sessions --project unknown-slug`**: exit 0, empty output / `[]` under `--json`.
    - **`clast sessions --json`**: every documented field is present per row; `curated` is `false` (entries/ does not exist yet); `branch` may be `null` (acknowledged TODO).
    - **`clast show <known-session-id>`**: exit 0, default output includes `session_id:`, `project:`, `snapshot:`, `first_prompt:`, `last_prompt:` lines that match the fixture content.
    - **`clast show <known-session-id> --full --turns 1`**: appends `## First 1 turns` and `## Last 1 turns` sections.
    - **`clast show <known-session-id> --json`**: stdout is valid JSON object with documented fields; with `--full`, includes `first_turns` / `last_turns` arrays of `{role,text}`.
    - **`clast show <unknown-uuid>`**: exit 1, stderr says "not found in manifest".
    - **`clast show <orphan-session-id>` (manifest line exists, file missing)**: exit 1, stderr mentions "run 'clast doctor'".
    - **`clast show not-a-uuid`**: exit 2, stderr mentions UUID format.
    - **`--help` and unknown flag** for each of the three subcommands: exit 0 / exit 2 respectively, with the subcommand name in the message.

21. **Wire `test/test-query.sh` into `test/test-clast.sh`.** Append it to the `suites` array after `test/test-snapshot.sh`. New order: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot → query.

22. **Update README.md** with a small "Read your sessions" block (or extend the existing usage section). Two-line example each for `clast projects`, `clast sessions`, `clast show <uuid>`. Do not document `--day` / `--since` / `--until` details in the README — link to `docs/cli-contract.md#clast-projects` etc.

23. **Confirm `make lint` and `make test` pass.** Each new subcommand file needs explicit `# shellcheck source=...` directives for every lib it calls into (`clast-lib.bash`, `clast-manifest-lib.bash`, `clast-registry-lib.bash`, `clast-decode-lib.bash`). `make test` must run all nine suites and exit 0.

## Acceptance criteria

- `lib/clast/clast-subcommands/projects.bash`, `sessions.bash`, and `show.bash` exist, each exports exactly one `clast_cmd_<name>` public function, and each passes `shellcheck`.
- `bin/clast` routes `projects`, `sessions`, and `show` to real subcommand files; the corresponding `_clast_stub` entries are gone. Other stubs (`entries`, `breadcrumb`, `stats`, `doctor`) are untouched.
- `bin/clast` sources `clast-manifest-lib.bash` once at the top of the file (no longer only inside the `snapshot)` case).
- `clast projects` defaults to a one-day window of `today` and lists each segment with activity in that window. `--day` / `--since` / `--until` resolve through `clast_parse_date` (ISO, `today`, `yesterday`, `-Nd`, `-Nw`).
- `clast projects --json` emits an array of objects matching `cli-contract.md#clast-projects` (eight documented fields, snake_case keys, ISO 8601 `last_active`).
- `clast projects --unregistered` returns only projects whose segment does not resolve via the registry.
- `clast sessions` defaults to today and lists sessions sorted by `start` ascending. `--project SLUG` filters by registry slug; unknown slug returns empty output, not an error.
- `clast sessions --json` emits the ten-field schema from `cli-contract.md#clast-sessions` per row, including `curated: false` when `entries/` is absent.
- `clast show <uuid>` exits 0 with the documented key/value block; `--full [--turns N]` appends `## First N turns` and `## Last N turns` text blocks; `--json` produces the same data as an object with extra `first_turns` / `last_turns` arrays under `--full`.
- `clast show <unknown-uuid>` exits 1 with a "not found in manifest" message; `clast show <orphan-uuid>` exits 1 with a "snapshot file missing" message; `clast show not-a-uuid` exits 2.
- Every read command tolerates a missing manifest as the empty case (exit 0, no output / `[]`), never as an error.
- `--quiet` suppresses default human stdout for all three subcommands; `--json` ignores `--quiet`.
- `--day` combined with `--since` or `--until` exits 2 with a mutual-exclusion message on stderr.
- `test/fixtures/multi-project/journal-seed/` exists with a manifest covering at least four sessions across three day buckets, matching transcript JSONLs, a `projects.json` registry, and one deliberate orphan manifest line.
- `test/test-query.sh` covers every scenario listed in task 20 and exits 0.
- `make test` runs nine suites (lib, decode, dispatcher, whereami, manifest, registry, registry-cmd, snapshot, query) and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement `clast entries` (list / read / write).** That is step 08. The `curated` field on `clast sessions` / `clast show` is a best-effort probe (`grep -l "session_id: $id" entries/*.md`); the writer side is step 08's job.
- **Do not implement `clast breadcrumb`.** Step 09 owns it.
- **Do not implement `clast stats` or `clast doctor`.** Step 10 owns both. In particular, do not add an orphan-detection sweep; tasks 4 and 14 surface single-row partial failures, nothing more.
- **Do not modify `clast-lib.bash`, `clast-decode-lib.bash`, `clast-manifest-lib.bash`, or `clast-registry-lib.bash`.** If a required helper is missing, stop and ask rather than expanding scope. The only allowed touches outside the three new subcommand files are `bin/clast` (task 17), `test/helpers.sh` (task 19, additive only), `test/fixtures/multi-project/` (task 18, additive only), `test/test-clast.sh` (task 21), `test/test-query.sh` (new), and `README.md` (task 22).
- **Do not invent a richer `branch` parser.** If the captured JSONL does not expose branch in a documented field, leave `branch` as `null` and TODO-flag it for step 10. Re-engineering Claude Code's transcript schema is not in this step.
- **Do not add pagination, sorting flags, or `--limit` to `projects` / `sessions`.** Default sort orders are fixed (see tasks 6 and 11). Future ergonomics live in v1.1.
- **Do not implement `--json` schema validation.** Construct via `jq -n` so output is well-formed by construction; do not add a JSON Schema file or runtime validator.
- **Do not modify the existing `simple/`, `empty/`, or `corrupt-manifest/` fixtures.** Only `multi-project/` gains a `journal-seed/` subdirectory.
- **Do not change the dispatcher's global-flag parsing** (the `--journal-dir` / `--projects-dir` / `--json` / `--quiet` / `--verbose` handling). Task 17 is a surgical replace of three case branches plus one promotion of an existing `source` line.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke against the multi-project seed
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
cp -r test/fixtures/multi-project/journal-seed/. "$CLAST_JOURNAL_DIR/"

# projects
bin/clast projects --day 2026-05-30
bin/clast projects --since 2026-05-22 --until 2026-05-30 --json | jq

# sessions
bin/clast sessions --day 2026-05-30
bin/clast sessions --project xesapps --since 2026-05-22 --until 2026-05-30 --json | jq

# show (pick a real session_id from the manifest)
sid=$(jq -r '.session_id' < "$CLAST_JOURNAL_DIR/.manifest.jsonl" | head -n1)
bin/clast show "$sid"
bin/clast show "$sid" --full --turns 2
bin/clast show "$sid" --json | jq

# Negative paths
bin/clast show 00000000-0000-0000-0000-000000000000 ; echo "exit=$?"  # 1
bin/clast show not-a-uuid                              ; echo "exit=$?"  # 2
bin/clast projects --day today --since 2026-05-01      ; echo "exit=$?"  # 2

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR
```

## Notes for the implementer

- **The manifest is the index; the snapshot files are the data.** Default to deriving everything you can from the manifest (cheap: a single `jq` pass over a small JSONL) and only open snapshot files when you need per-message data (`msg_count_approx`, `first_prompt`, turns). One pass per snapshot, never two.
- **"Most recent line wins" applies window-locally.** For `clast projects` / `clast sessions`, when a session has been captured multiple times, use the latest manifest line within the window — not the latest globally. This matters when a session was first captured on day N and re-captured on day N+1 outside the window.
- **Day-bucket comparison is lexicographic.** Because `day_bucket` is always `YYYY-MM-DD`, `<=` / `>=` on strings is correct and matches numeric date order. Do not pull `date -d` into the inner loop.
- **`clast_parse_date` already exists.** Do not re-implement `today` / `yesterday` / `-Nd` parsing. If `clast_parse_date` does not currently accept `last-week`, stop and ask before expanding scope — it's a lib change, not a subcommand change.
- **`curated` is a probe, not a join.** A simple `grep -l "session_id: $id" entries/*.md` is enough; step 08 will eventually own a proper lookup. Document the cheap path inline so a future reader doesn't think it's the canonical implementation.
- **`branch` may genuinely be unknowable today.** If the captured JSONL doesn't surface a branch field, leave it null and TODO-flag it. Better to ship a correct three-field schema than to hallucinate a fourth.
- **Subcommand tests run `bin/clast` as a subprocess** (mirroring `test/test-snapshot.sh`). Set `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` in the environment of those invocations to drive `--day today` deterministically.
- **Per-test isolation.** Always go through `setup_test_journal` + the new seed helper from task 19; never write to a real `$HOME/.claude/journal/` from a test.
- **Conventional commit suggestion**: `feat(query): implement clast projects, sessions, show`. If the fixture helper change in task 19 is large enough to read separately, a follow-up `test(fixtures): add multi-project journal-seed` is fine; one squashed commit on merge is also fine.
