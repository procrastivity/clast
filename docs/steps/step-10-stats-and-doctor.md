---
step: 10
title: stats-and-doctor
depends_on: [02, 04, 05, 06, 08]
size: medium
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#conventions
  - docs/overview.md#cross-machine-considerations
  - docs/cli-contract.md#date-parsing
  - docs/cli-contract.md#clast-stats
  - docs/cli-contract.md#clast-doctor
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#libclastclast-manifest-libbash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 10: `clast stats` and `clast doctor`

## Context

Step 02 produced `clast-lib.bash` (`clast_journal_dir`, `clast_today`, `clast_parse_date`, `clast_log_*`, `clast_atomic_write`, plus the `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` test hooks). Step 04 produced `clast-manifest-lib.bash` with `clast_manifest_path`, `clast_manifest_lookup`, `clast_manifest_iterate`, and — critically for `--fix` — `clast_manifest_rebuild_from_disk`. Step 05 produced `clast-registry-lib.bash` (`clast_registry_resolve`, `clast_registry_list_json`). Step 06 produced `clast snapshot` and the `multi-project/journal-seed/` fixture (5 manifest lines, 4 transcripts on disk, including one missing-snapshot row `dddddddd-…` referenced by the manifest but absent from `transcripts/2026-05-15/`). Step 08 produced `clast entries` and the `multi-project/entries-seed/entries/` directory format. The `corrupt-manifest/` fixture (step 04) is on disk with one un-parseable middle line plus two valid lines; reuse it for the critical-corruption path.

`bin/clast` already sources every lib at the top, parses global flags (`--json`, `--quiet`, `--verbose`, `--journal-dir`, `--projects-dir`), and dispatches `whereami`, `registry`, `snapshot`, `projects`, `sessions`, `show`, `entries` to real subcommand files. The literal tokens `stats` and `doctor` are still routed through `_clast_stub` (currently labeled "planned for step 12" and "planned for step 13" in the dispatcher map — those labels are out-of-date forecasts that this step corrects). `breadcrumb` is in flight on its own branch (step 09); this step does not depend on it and does not touch `breadcrumbs/` from a write path, but **stats counts breadcrumb files for the day if the directory exists** (a `find ... -name '<day>-*.md' | wc -l` over `$(clast_journal_dir)/breadcrumbs/`). If step 09 has merged before this step lands, the count is non-zero on real journals; if not, it is zero everywhere and the column still renders. Either way: do not import breadcrumb code, do not require the directory to exist.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils (`date -d`, `realpath -m`, `stat -c`, `find -mindepth`) are on PATH.

## Goal

Implement two new subcommand files — `lib/clast/clast-subcommands/stats.bash` and `lib/clast/clast-subcommands/doctor.bash` — wire them into `bin/clast` (replacing the two stubs), and add `test/test-stats.sh` and `test/test-doctor.sh` integration suites. `stats` summarizes journal activity over a date window (default `today`) using only manifest + filesystem stat (no JSONL parsing). `doctor` runs six sanity checks against the journal and reports issues; `--fix` performs only the two safe repairs (rebuild manifest, remove orphan snapshots). No new fixture is required — `multi-project/journal-seed/` already exercises the missing-snapshot case and `corrupt-manifest/` already exercises the critical-corruption case; orphan-snapshot scenarios are built per-test by `cp`'ing a stray file into `transcripts/` after seeding.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — `$(clast_journal_dir)` layout. `transcripts/YYYY-MM-DD/<segment>/<uuid>.jsonl`, `entries/YYYY-MM-DD-HHMM-<slug>-<session-slug>.md`, `breadcrumbs/YYYY-MM-DD-<slug>.md`. Directories may not exist on a fresh journal — every read path must tolerate absence, not error.
- `docs/overview.md#conventions` — exit codes (0 success, 1 general, 2 usage, **4 data integrity**); ISO 8601 UTC with `Z` in JSON; snake_case JSON keys.
- `docs/overview.md#cross-machine-considerations` — the manifest is append-only and may contain multiple lines for the same `session_id` (re-capture case); the **most recent line wins** for dedup. Sessions are unique by `session_id` regardless of how many manifest rows mention them; bytes use the most-recent `source_size` per session_id.
- `docs/cli-contract.md#date-parsing` — `--day` / `--since` / `--until` accept ISO / `today` / `yesterday` / `last-week` / `-Nd` / `-Nw`. `clast_parse_date` from step 02 already does this; do not re-implement.
- `docs/cli-contract.md#clast-stats` — synopsis (`clast stats [--day DATE] [--since DATE] [--until DATE] [--project SLUG]`), default-mode output shape (Window / Projects / Sessions / Messages / Bytes / Curated / Breadcrumbs), and the explicit note **"Stats are derived from manifest + filesystem stat; no JSONL parsing required."** That is a load-bearing constraint — do not `jq` the snapshot bodies. JSON output mirrors the same keys as the default-mode labels (snake_case).
- `docs/cli-contract.md#clast-doctor` — synopsis (`clast doctor [--fix]`), the six checks (manifest validity, registry validity, orphan snapshots, missing snapshots, day-bucket consistency, day-cutoff sanity), the safe-fix list (manifest rebuild, orphan removal), and exit codes (0 / 1 / 4).
- `docs/cli-contract.md#error-handling-conventions` — stderr-vs-stdout split, `{"error":"...","code":N}` shape under `--json`, exit codes.
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — one `clast_cmd_<name>` per file; argument parsing lives in the subcommand, not in libs.
- `docs/repo-bootstrap.md#libclastclast-manifest-libbash` — `clast_manifest_rebuild_from_disk` is already implemented; `doctor --fix` calls it directly. Do not re-implement.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions, subprocess-style integration tests.

## Tasks

### A. `clast stats`

1. **Write `lib/clast/clast-subcommands/stats.bash`.** Standard subcommand preamble: `# shellcheck shell=bash`, with `# shellcheck source=lib/clast/clast-lib.bash` / `clast-manifest-lib.bash` / `clast-registry-lib.bash` / `clast-decode-lib.bash` directives at the top. **Do not** add a double-source guard at the subcommand layer. Define exactly one public function `clast_cmd_stats`. Internal helpers use `_clast_stats_` prefix and live below the entry function.

2. **Parse `stats` flags.** Accept:
   - `--day DATE` / `--day=DATE` → set `window_start = window_end = clast_parse_date DATE`. Mutually exclusive with `--since` / `--until` (exit 2 with `clast: stats: --day cannot be combined with --since or --until`).
   - `--since DATE` / `--since=DATE` → set `window_start`. Default when only `--until` is given: the literal string `1970-01-01` (i.e. "since forever" — the manifest cannot contain dates before this).
   - `--until DATE` / `--until=DATE` → set `window_end`. Default when only `--since` is given: `clast_today`.
   - Neither `--day`, `--since`, nor `--until` → `window_start = window_end = clast_today` (i.e. "today only"). Render this as `Window: <date> (today)` in default mode and as `"window": {"start": "<date>", "end": "<date>", "label": "today"}` in JSON.
   - `--project SLUG` / `--project=SLUG` → set `project_filter`. The slug is matched against the registry-resolved slug for each manifest line's `source` path. **A `--project SLUG` that does not exist in the registry exits 1** with `clast: stats: unknown project slug '<slug>'` (and the `{"error":"...","code":1}` shape under `--json`). Rationale: with no registered slug there is no way to filter; silently returning zero counts would hide the typo.
   - `-h|--help` → exit 0 with usage.
   - Unknown flag or any positional argument → exit 2 with `clast: stats: unknown flag '<arg>'` (or `... unexpected positional '<arg>'`).
   Validate that each resolved date matches `^[0-9]{4}-[0-9]{2}-[0-9]{2}$` after `clast_parse_date`; otherwise exit 2 with `clast: stats: invalid date '<input>'`. Validate `window_start <= window_end` (string compare is fine for ISO `YYYY-MM-DD`); otherwise exit 2 with `clast: stats: --since must be <= --until`.

3. **Build the in-window session set.** Stream manifest lines via `clast_manifest_iterate '.day_bucket >= "<window_start>" and .day_bucket <= "<window_end>"'`. Reduce to **one row per `session_id`** keeping the row with the lexicographically largest `captured_at` (ISO 8601 strings sort the same as their epoch values, so a plain string `max` is correct — do not parse back to epoch). Use `jq -s` over the iterate output: `group_by(.session_id) | map(max_by(.captured_at))`. If `--project SLUG` was given, drop any row whose `source` does not resolve (via `clast_registry_resolve`) to `<slug>`. **`source` may be `null`** (a manifest line emitted by `clast_manifest_rebuild_from_disk`, where `source` is unrecoverable) — treat null as "does not match any project filter" and drop the row when `--project` is given; counts include it otherwise (it still represents a real session).

4. **Compute the six tallies.**
   - `projects`: unique non-null `source`-derived slugs in the reduced set. Use `clast_registry_resolve "$source"` on each row; null `source` is dropped from the project tally (it does not contribute to a slug count). Sort + uniq the resulting slug list; the cardinality is `projects`.
   - `sessions`: cardinality of the reduced set itself (one per unique `session_id`).
   - `bytes`: `sum(.source_size)` over the reduced set. Render as a human-readable string in default mode using a three-decimal-significant-figure helper (`_clast_stats_human_bytes`): `1024` → `1.0 KB`, `1234567` → `1.2 MB`, etc. Use base-2 (KiB-style math) with base-10 labels (`KB` / `MB` / `GB`) to match the snapshot summary output from step 06 — consistency over pedantry. The JSON `bytes` field is the raw integer, **not** the human string; emit the human string as `bytes_human` alongside.
   - `messages`: sum of `wc -l` over the on-disk snapshot file for each reduced-set row whose `snapshot` file actually exists on disk. Missing snapshot files contribute 0 (do NOT error — that is doctor's job to flag, not stats'). Render as `Messages: <n> (approx)` in default mode; JSON key is `messages_approx`. **This is the only check that touches snapshot bodies, and it is `wc -l` only — no JSONL parsing.** The "(approx)" qualifier is load-bearing: one JSONL line is roughly one turn but not exactly — `wc -l` is a cheap-and-close-enough proxy.
   - `curated`: count of `$(clast_journal_dir)/entries/*.md` files whose `<YYYY-MM-DD>` filename prefix is within `[window_start, window_end]`. Use `find "$(clast_journal_dir)/entries" -maxdepth 1 -type f -name '*.md' 2>/dev/null` then filter the date prefix with a `case`/`[[ ]]` glob — do not `cat` any file. Render as `Curated: <n> of <sessions> sessions (<pct>%)` in default mode where `<pct>` is `100 * curated / sessions` rounded to the nearest integer (or `0%` when `sessions == 0`); JSON keys are `curated`, `curated_pct` (integer 0–100). The entries-to-sessions match is **not** verified here — that is "curated count" in the loose sense (entry files dated within the window). A stricter session-level join is a doctor-time concern, not a stats one.
   - `breadcrumbs`: count of `$(clast_journal_dir)/breadcrumbs/*.md` files whose date prefix is within the window, plus a parallel count of unique slug suffixes (the `<slug>` between the date and `.md`). Render as `Breadcrumbs: <n> across <m> projects` in default mode; JSON keys are `breadcrumbs`, `breadcrumb_projects`. The literal slug component `_global` counts as one of the `<m>` projects (it is a distinct slug, not a special case for stats — only `--list` in step 09 renders it as `(global)`). Missing `breadcrumbs/` directory → both counts are 0; do not error.

5. **Render the output.**
   - **Default mode** (and unaffected by `--verbose`; `--verbose` only adds stderr breadcrumbs about what was scanned): a fixed-shape block. Use `printf` with right-padded labels (label column width 12). Always print, even with zero counts:
     ```
     Window:      2026-05-30 (today)
     Projects:    2
     Sessions:    3
     Messages:    47 (approx)
     Bytes:       2.4 MB
     Curated:     1 of 3 sessions (33%)
     Breadcrumbs: 5 across 2 projects
     ```
     For multi-day windows, the first line becomes `Window: <start>..<end>` (no `(today)` suffix). For `--since X` with implicit `--until today` the suffix is `(through today)`. For `--day yesterday` the suffix is `(yesterday)`; everything else is unsuffixed.
   - **`--json` mode**: emit one JSON object via a single `jq -n` invocation. Keys in this order: `window` (object with `start`, `end`, `label`), `projects`, `sessions`, `messages_approx`, `bytes`, `bytes_human`, `curated`, `curated_pct`, `breadcrumbs`, `breadcrumb_projects`. Always print, even with zero counts (do not emit `null` for any field — zero is the empty value).
   - **`--quiet`** suppresses the default-mode body but never the `--json` body or stderr. There is no useful "stats on stderr" mode; treat `--quiet` as a stdout-suppressor only.

6. **Exit code for `stats`.** Always 0 on a successful read (including all-zero counts). Exit 1 only on infrastructure errors (manifest unreadable due to a non-corruption permission issue, journal directory unwritable for the cache subdir we never actually write — i.e. essentially never under normal use). Exit 2 on usage errors per task 2. Do NOT exit 4 on a corrupt manifest — stats is read-only and best-effort; rows that `clast_manifest_iterate` silently skips via `fromjson?` simply do not contribute. Doctor exists specifically to flag corruption; stats stays out of that lane.

### B. `clast doctor`

7. **Write `lib/clast/clast-subcommands/doctor.bash`.** Standard subcommand preamble (same shellcheck-source directives as task 1 — `clast-lib.bash`, `clast-manifest-lib.bash`, `clast-registry-lib.bash`, `clast-decode-lib.bash`). Define exactly one public function `clast_cmd_doctor`. Internal helpers use `_clast_doctor_` prefix.

8. **Parse `doctor` flags.** Accept:
   - `--fix` → set `fix_mode=1`.
   - `--yes` / `-y` → set `assume_yes=1`. With `--fix`, the orphan-removal step skips the interactive confirmation. Without `--fix`, `--yes` is accepted but has no effect (do not error — it's a future-compat no-op).
   - `-h|--help` → exit 0 with usage.
   - Unknown flag or any positional → exit 2 with `clast: doctor: unknown flag '<arg>'`.
   Doctor takes no date window; it checks the journal as a whole.

9. **Implement the six checks as separate helper functions, each returning a status code that the entry function aggregates.** Each helper writes its findings to a shared accumulator (a bash array of jq-built JSON objects, one per finding) and returns one of three states: `ok` / `warn` / `critical`. Findings shape: `{"check":"<id>","severity":"ok|warn|critical","message":"<one-line>","items":[<list of paths or ids>]}`. The aggregator records every finding regardless of severity (an `ok` finding produces a `✓` line in default mode).

   - **9a. `_clast_doctor_check_manifest_validity`**: read `clast_manifest_path` line by line. For each line: `jq -e 'has("session_id") and has("source") and has("snapshot") and has("captured_at") and has("source_mtime") and has("source_size") and has("day_bucket")' <<< "$line"`. Count valid vs invalid. If any line is **un-parseable as JSON** (jq exits with anything other than 0/1, e.g. an exit-3-style parse error), classify as `critical` — that is the "manifest unparseable" case that flips the overall exit code to 4. Missing-field lines are `warn` (parseable JSON but schema mismatch). Missing manifest file is `ok` with the message `manifest: no manifest yet (0 entries)`. Per-line errors capture the 1-based line number, not the content (the content can be megabytes per line in adversarial inputs).

   - **9b. `_clast_doctor_check_registry_validity`**: read `$(clast_journal_dir)/projects.json` line by line. Same parse-vs-schema split as 9a. Required keys: `path`, `slug`, `first_seen`, `aliases`. Then: any duplicate slug → `warn` (the lookup uses most-recent-line semantics, so a dup is recoverable but suspicious). Any alias that appears as another entry's slug or as another entry's alias → `warn` with both entry slugs listed in `items`. Missing registry file is `ok` with `registry: no registry yet (0 entries)`.

   - **9c. `_clast_doctor_check_orphan_snapshots`**: walk `$(clast_journal_dir)/transcripts/*/*/*.jsonl` (3-deep, matching the structure step 06 writes). For each file, derive `(day_bucket, segment, session_id)` from the path and check whether the manifest contains *any* line with that `session_id` (use `clast_manifest_iterate '.session_id == "<sid>"'` and test for a non-empty result — do NOT require an exact path match; a re-capture re-uses the same snapshot path, and a per-machine alternate path is fine as long as the session_id is known). Orphans are files with no matching session_id in the manifest. Severity: `warn`. Missing `transcripts/` directory is `ok` with zero orphans.

   - **9d. `_clast_doctor_check_missing_snapshots`**: iterate **deduped** manifest rows (most-recent line per `session_id` — same `group_by | max_by(.captured_at)` reduce as stats task 3). For each row, test whether the `snapshot` file exists on disk relative to `$(clast_journal_dir)`. Missing files are `warn`. The `multi-project/journal-seed/` fixture has exactly one such row (`dddddddd-…` references `transcripts/2026-05-15/...` which is not on disk) — assert against that in the test suite.

   - **9e. `_clast_doctor_check_day_bucket_consistency`**: for each deduped manifest row, parse the day component out of `snapshot` (the segment immediately after `transcripts/`) and compare to the row's `day_bucket` field. Mismatch is `warn`. Rows whose `snapshot` does not start with `transcripts/<day>/` (e.g. a relocated journal) are skipped — that is a stylistic choice the user can make, not a corruption to flag.

   - **9f. `_clast_doctor_check_day_cutoff_sanity`**: compute the manifest's `captured_at` distribution by hour-of-day (local time, honoring `CLAST_DAY_CUTOFF` when set; otherwise the literal default `04:00`). If more than 5% of captures occurred within `±30 minutes` of the cutoff hour, emit a `warn` finding pointing the user at `~/.config/clast/config.toml`'s `day_cutoff`. Otherwise `ok`. Empty manifest → `ok`. This check is informational; never escalates to `critical` and never blocks `--fix`.

10. **Aggregate findings and decide the exit code.**
    - If any finding is `critical` → exit 4 after printing the report. **Do not run `--fix` repairs in critical state** unless the critical finding is specifically `manifest_validity` AND `--fix` was passed — in that one case, call `clast_manifest_rebuild_from_disk`, re-run the checks once, and exit on the post-rebuild aggregate (which should be `ok` if the rebuild succeeded). One rebuild per invocation; do not loop.
    - Else if any finding is `warn` → exit 1.
    - Else → exit 0.

11. **Implement `--fix`.** Only two repairs are safe:
    - **Manifest rebuild** (covered in task 10 — runs only when `manifest_validity` is `critical`).
    - **Orphan-snapshot removal**. If `_clast_doctor_check_orphan_snapshots` listed N > 0 orphans AND `--fix` was passed: under `--yes`, `rm -f` each orphan and log `removed N orphan snapshot(s)` via `clast_log_info`. Without `--yes`, print the orphan list to stdout, prompt `Remove these N file(s)? [y/N] ` on stdout (read a single line from `/dev/tty` — if `/dev/tty` is unavailable, abort with `clast: doctor: --fix needs --yes when stdin is not a TTY` to exit 2; do NOT read from `$STDIN`, which a hook or pipe could supply). Accept `y` or `Y` only; anything else is "no, keep them" and proceeds without removal. After removal, re-run the orphan check once for the post-fix report (so the default-mode output reflects the now-zero state).
    - Missing-snapshot rows are **not** auto-removable. Rationale: the file might be on a sibling machine that hasn't synced yet (per `docs/overview.md#cross-machine-considerations`). The user can `clast doctor --fix` after sync, or manually inspect.
    - Day-bucket and day-cutoff findings are **never** auto-fixed.

12. **Render the doctor output.**
    - **Default mode**: one line per finding, prefixed `✓ ` (ok), `! ` (warn), or `✗ ` (critical). Use the literal bytes `\xe2\x9c\x93`, `\xe2\x9c\x97`, and ASCII `!`. Each finding line is `<prefix> <check>: <message>`. For warn/critical findings with `items`, indent each item two spaces under the finding. Example:
      ```
      ✓ Manifest: 247 entries, all valid
      ✓ Registry: 8 projects, no duplicates
      ! Orphan snapshots: 3
        transcripts/2026-04-15/-old-path/abc.jsonl
        transcripts/2026-04-15/-old-path/def.jsonl
        transcripts/2026-04-15/-old-path/ghi.jsonl
      ✓ Missing snapshots: none
      ✓ Day-bucket consistency: ok
      ✓ Day-cutoff sanity: ok

      Run `clast doctor --fix` to clean up orphans.
      ```
      The trailing "Run ..." hint appears only when there is at least one auto-fixable finding (orphans, or critical manifest in the rebuild path) and `--fix` was NOT passed. When `--fix` IS passed, replace the hint with a summary of what was fixed (`Fixed: removed 3 orphan snapshot(s)`).
    - **`--json` mode**: a single object `{"findings":[<finding objects>], "exit_code":<int>, "fixed":[<list of fix-summary strings>]}`. The `findings` array is in the canonical order: manifest, registry, orphans, missing, day_bucket, day_cutoff (regardless of severity). `fixed` is empty unless `--fix` performed work. Always print.
    - **`--quiet`** suppresses the default-mode body but never the `--json` body, stderr, or interactive prompts. With `--quiet --fix` and no `--yes`, the prompt still appears — `--quiet` is a stdout filter, not an interaction killer.

### C. Dispatcher + tests + docs

13. **Wire the subcommands into the dispatcher.** In `bin/clast`, replace the two `_clast_stub` cases for `stats` and `doctor` with the source-then-dispatch pattern used by `whereami` / `snapshot` / `projects` / `sessions` / `show` / `registry` / `entries`. Leave the `breadcrumb` stub untouched — step 09 owns that. The `_clast_stub` helper itself stays (it's a no-op once no stubs remain in the case map, but removing it would expand scope; the dead-code cleanup belongs to whichever step ships the last remaining stub).

14. **Write `test/test-stats.sh`.** Subprocess-style suite modeled on `test/test-query.sh` and `test/test-entries.sh`. `cd` to repo root, `source test/helpers.sh`, set `_CLAST_TEST_NAME=test-stats`, set `CLAST_BIN="$PWD/bin/clast"` (mirror the existing suites — every invocation goes through `"$CLAST_BIN"`), and `export TZ=UTC` at the top. Each scenario calls `setup_test_journal`, seeds via `make_fixture_journal_seed_from multi-project/journal-seed`, optionally seeds the registry via `make_fixture_projects_tree_from multi-project/projects-tree` (only when `--project` filtering is exercised — the registry already lives in the journal-seed, but the projects tree is needed for `clast_registry_resolve` to match a `source` path), exports `CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T12:00:00Z' +%s)` so `clast_today` returns `2026-05-30`, runs `"$CLAST_BIN" stats …`, asserts on stdout / stderr / exit code, then `teardown_test_journal`. Cover at minimum:
    - **Default day, no flags**: `"$CLAST_BIN" stats` against the multi-project seed prints `Window: 2026-05-30 (today)`, `Projects: 2`, `Sessions: 2` (the two `2026-05-30` rows: `33333333` and `22222222`), nonzero `Bytes`, `Curated: 0 of 2 sessions (0%)` (entries-seed not loaded), `Breadcrumbs: 0 across 0 projects`. Exit 0.
    - **`--json` default day**: `"$CLAST_BIN" --json stats` returns a valid JSON object with the documented keys and the same numeric values; `jq -e '.window.label == "today"'` succeeds.
    - **`--day yesterday`**: against the seed (where the most recent `2026-05-29` row is `11111111`), `Window: 2026-05-29 (yesterday)`, `Sessions: 1`, `Projects: 1`.
    - **`--day` arbitrary**: `--day 2026-05-22` → `Sessions: 1`, `Projects: 1` (the `aaaaaaaa` row).
    - **`--since/--until` multi-day**: `--since 2026-05-15 --until 2026-05-30` covers every seed row → `Sessions: 5` (one per unique `session_id` in the seed, including the `dddddddd` row whose snapshot file is missing on disk). `Window: 2026-05-15..2026-05-30 (through today)` — but since `--until 2026-05-30` is explicit, the `(through today)` suffix is NOT added; only `--since X` without `--until` gets that. Plain `--since 2026-05-15..2026-05-30` (no `--until`) is the case for the suffix; `--since 2026-05-15 --until 2026-05-30` is bare.
    - **`--since` only**: `--since 2026-05-29` → window end defaults to `2026-05-30` (today); `Window: 2026-05-29..2026-05-30 (through today)`.
    - **`--day` + `--since` together**: exit 2.
    - **`--since > --until`**: `--since 2026-05-30 --until 2026-05-29` exits 2 with the `--since must be <=` message.
    - **`--project xesapps`**: against the seed (registry has `xesapps` mapped to `/tmp/proj-xesapps`), `--since 2026-05-15 --until 2026-05-30 --project xesapps` filters out the `scratch` row → `Sessions: 4`, `Projects: 1`.
    - **`--project unknown-slug`**: exit 1 with the unknown-slug message; under `--json`, the `{"error":...,"code":1}` shape.
    - **Empty result set**: `--day 2026-01-01` (no manifest rows that day) → exit 0, `Sessions: 0`, `Projects: 0`, `Bytes: 0 B`, `Curated: 0 of 0 sessions (0%)`.
    - **Missing manifest entirely**: do NOT seed; `"$CLAST_BIN" stats` → exit 0, all-zero counts.
    - **`bytes_human` rendering**: a hand-crafted manifest with a single 1572864-byte row produces `Bytes: 1.5 MB` in default mode and `"bytes": 1572864, "bytes_human": "1.5 MB"` in JSON.
    - **Curated count**: `make_fixture_entries_seed_from multi-project/entries-seed` then `stats --since 2026-05-15 --until 2026-05-30 --json` — the `curated` count equals the number of `.md` files in the entries-seed whose filename prefix falls in window; `curated_pct` matches.
    - **Breadcrumb count**: hand-create `$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md` and `2026-05-30-_global.md` (one-line frontmatter is fine; this suite does NOT call `clast breadcrumb` since step 09 may not be merged), then `stats --json` → `breadcrumbs: 2, breadcrumb_projects: 2`. Files with the wrong date prefix do NOT count.
    - **`--help`**: exit 0 with usage. **Unknown flag**: exit 2.

15. **Write `test/test-doctor.sh`.** Same skeleton as test-stats.sh. Cover at minimum:
    - **All-clean journal**: seed `multi-project/journal-seed` minus the `dddddddd` line (use `grep -v dddddddd $(seed)/.manifest.jsonl > $CLAST_JOURNAL_DIR/.manifest.jsonl` and `cp -R seed/transcripts ...`); `"$CLAST_BIN" doctor` exits 0 with all `✓` lines.
    - **Missing snapshot detection**: full `multi-project/journal-seed` (which includes the `dddddddd` orphan-in-manifest row) → exit 1; default-mode output includes a `!` line for `Missing snapshots: 1` and lists `transcripts/2026-05-15/-tmp-proj-xesapps/dddddddd-…`. `--json` form includes a finding with `check: "missing_snapshots"`, `severity: "warn"`, and the path in `items`.
    - **Orphan-snapshot detection**: seed plus a stray file at `$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl` whose `session_id` is not in the manifest. `doctor` exits 1; finding includes the orphan path.
    - **Orphan removal under `--fix --yes`**: same setup, `"$CLAST_BIN" doctor --fix --yes` → exits 0 (after the rebuild path is skipped because manifest is not critical; orphans are removed); the stray file is gone; the post-fix re-check reports `Missing snapshots: 1` (still, because the seed has the `dddddddd` warn — orphan removal does not address missing-snapshot rows) → exit code is 1 since `dddddddd` warn survives. Test must assert exit 1 here, not 0. (If you want the all-clean case post-fix, you have to also patch the manifest — but doctor does not do that.)
    - **Orphan-removal interactive abort**: same setup, `printf 'n\n' | "$CLAST_BIN" doctor --fix </dev/tty` is impossible to drive in a non-tty test; use the dedicated abort path instead: `"$CLAST_BIN" doctor --fix </dev/null` (no `--yes`, no tty) exits 2 with the `needs --yes when stdin is not a TTY` message and removes nothing.
    - **Critical manifest with `--fix`**: seed `corrupt-manifest/` directly into `$CLAST_JOURNAL_DIR` (`make_fixture_journal_tree corrupt-manifest`). `"$CLAST_BIN" doctor` (no fix) → exit 4, default-mode shows a `✗ Manifest:` line. `"$CLAST_BIN" doctor --fix` → exit 0 after one rebuild; the post-rebuild manifest contains 2 lines (the two valid sessions from the fixture) and is `jq` -parseable line-by-line. The rebuild emits `manifest rebuilt: 2 line(s)` to stderr via `clast_manifest_rebuild_from_disk`.
    - **Critical manifest without `--fix`**: same seed, plain `doctor` → exit 4 without modifying the manifest (assert mtime / content unchanged).
    - **Registry duplicate slug**: hand-craft a `projects.json` with two lines both having `"slug":"xesapps"`. `doctor` exits 1; finding includes the duplicate slug.
    - **Registry alias collision**: hand-craft two lines where one entry's `aliases` contains the other entry's `slug`. `doctor` exits 1.
    - **Day-bucket mismatch**: hand-craft a manifest line where `snapshot` is `transcripts/2026-05-22/...` but `day_bucket` is `2026-05-30`. `doctor` exits 1 with the day-bucket finding.
    - **Day-cutoff warning**: hand-craft a manifest where ≥6 of 10 lines have `captured_at` within 30 minutes of `04:00` UTC (pin `TZ=UTC` so the local hour math is deterministic). `doctor` exits 1 with the day-cutoff finding.
    - **Day-cutoff no-warning**: a uniform-distribution manifest (lines spread across hours) → ok line, exit 0 (assuming no other warns).
    - **`--json` shape**: `"$CLAST_BIN" --json doctor` against the all-clean journal → a single object with `findings` (length 6, all `severity: "ok"`), `exit_code: 0`, `fixed: []`.
    - **`--help`**: exit 0 with usage. **Unknown flag**: exit 2. **Positional arg**: exit 2.

16. **Wire `test/test-stats.sh` and `test/test-doctor.sh` into `test/test-clast.sh`.** Append after `test/test-entries.sh` (and after `test/test-breadcrumb.sh` if step 09 has merged first — alphabetize within the post-entries block: breadcrumb → doctor → stats). New order assuming step 09 has merged: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot → query → entries → breadcrumb → doctor → stats. If step 09 has NOT merged when this step executes: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot → query → entries → doctor → stats. Either way, doctor goes before stats (no real dependency reason — alphabetical).

17. **Update README.md** with a small "Inspect and audit the journal" block (or extend the existing usage section). Two-line example each for `stats` (default + `--since`), `doctor` (default + `--fix`). Do not document the full check list in README — link to `docs/cli-contract.md#clast-doctor`.

18. **Confirm `make lint` and `make test` pass.** Each new subcommand file needs explicit `# shellcheck source=...` directives for every lib it sources. `make test` must run every suite (existing + the two new suites) and exit 0.

## Acceptance criteria

### `clast stats`

- `lib/clast/clast-subcommands/stats.bash` exists, exports exactly one `clast_cmd_stats` public function, and passes `shellcheck`.
- `bin/clast` routes `stats` to the real subcommand file; the `_clast_stub` entry for `stats` is gone.
- `clast stats` with no flags reports `Window: <today> (today)` and counts derived from `today`'s manifest rows only.
- `clast stats --day yesterday`, `--day 2026-05-22`, and other `clast_parse_date` inputs resolve correctly.
- `clast stats --since X --until Y` reports the union of day-buckets in `[X, Y]`; `--since` alone defaults `--until` to today; `--until` alone defaults `--since` to `1970-01-01`.
- `clast stats --day` is mutually exclusive with `--since` / `--until` (exit 2).
- `clast stats --since > --until` exits 2.
- `clast stats --project <known-slug>` filters by registry-resolved slug; `--project <unknown>` exits 1.
- `clast stats` derives `sessions` from unique `session_id`s using the most-recent-line per session for `source_size`. Multiple manifest rows for the same `session_id` count as one session.
- `messages_approx` is the sum of `wc -l` over the on-disk snapshot files in the reduced set; missing files contribute 0 (no error).
- `curated` counts `entries/*.md` files whose date prefix is in window. `breadcrumbs` counts `breadcrumbs/*.md` files whose date prefix is in window; `breadcrumb_projects` is the count of unique slug suffixes.
- `--json` emits a single object with `window`, `projects`, `sessions`, `messages_approx`, `bytes`, `bytes_human`, `curated`, `curated_pct`, `breadcrumbs`, `breadcrumb_projects` (snake_case, in that key order).
- `--quiet` suppresses default-mode stdout; `--json` is unaffected.
- `clast stats` exits 0 on read success (including zero counts) and exits 2 only on usage errors. **Never exits 4** even against a partially corrupt manifest (corruption is doctor's job).

### `clast doctor`

- `lib/clast/clast-subcommands/doctor.bash` exists, exports exactly one `clast_cmd_doctor` public function, and passes `shellcheck`.
- `bin/clast` routes `doctor` to the real subcommand file; the `_clast_stub` entry for `doctor` is gone.
- `clast doctor` runs all six checks (manifest validity, registry validity, orphan snapshots, missing snapshots, day-bucket consistency, day-cutoff sanity) and reports each as `✓ ok` / `! warn` / `✗ critical`.
- All checks tolerate missing files/directories (empty journal → all `ok`).
- An un-parseable manifest line produces a `critical` manifest_validity finding and exits 4.
- A missing required field on an otherwise-parseable manifest line produces a `warn` and exits 1.
- Duplicate slugs, alias collisions, orphan snapshots, missing snapshots, day-bucket mismatches, and day-cutoff anomalies all surface as `warn` findings and exit 1.
- `clast doctor --fix` removes orphan snapshots (after `--yes` or interactive `y`) and rebuilds the manifest from disk when manifest_validity is critical. Missing snapshots and day-bucket / day-cutoff findings are NEVER auto-fixed.
- `clast doctor --fix </dev/null` without `--yes` exits 2 with a clear message rather than reading from a non-tty stdin.
- `clast doctor --fix` re-runs the affected checks once after a fix; the second-pass aggregate drives the final exit code.
- `--json` emits `{"findings":[...],"exit_code":<int>,"fixed":[...]}` with findings in canonical order; `fixed` is empty unless work was done.
- `--quiet` suppresses default-mode stdout; `--json` and interactive prompts are unaffected.

### Wiring

- `test/test-stats.sh` covers every scenario listed in task 14 and exits 0.
- `test/test-doctor.sh` covers every scenario listed in task 15 and exits 0.
- `test/test-clast.sh` invokes both new suites and exits 0.
- `make lint` exits 0.
- `make test` exits 0.

## Out of scope

- **Do not implement `clast breadcrumb`.** That is step 09, in flight on a sibling branch. Stats counts breadcrumb files via filesystem stat only; it does not import or call any breadcrumb code, and a missing `breadcrumbs/` directory is the zero case, not an error.
- **Do not parse JSONL transcript bodies in stats.** `messages_approx` is `wc -l`. The contract is explicit: "Stats are derived from manifest + filesystem stat; no JSONL parsing required." A future "exact" message count belongs to v1.1 with its own caching story.
- **Do not auto-fix missing snapshots, day-bucket mismatches, or day-cutoff anomalies in doctor.** The cross-machine sync story (`docs/overview.md#cross-machine-considerations`) makes "missing on this machine" potentially "present on the laptop" — auto-removal would destroy data. Day-bucket / day-cutoff issues require user judgment.
- **Do not modify `clast-lib.bash`, `clast-decode-lib.bash`, `clast-manifest-lib.bash`, or `clast-registry-lib.bash`.** Every helper this step needs already exists. If you find yourself reaching for a missing helper, stop and ask rather than expanding scope. The only allowed touches outside the two new subcommand files are `bin/clast` (task 13), `test/test-clast.sh` (task 16), `test/test-stats.sh` (new), `test/test-doctor.sh` (new), and `README.md` (task 17).
- **Do not add new fixtures.** `multi-project/journal-seed/` already exercises the missing-snapshot case; `corrupt-manifest/` already exercises the critical-corruption case; orphan-snapshot and registry-collision scenarios are built per-test inside the test script (`cp` a stray file, hand-write a `projects.json`). Adding fixtures for one-off corruption shapes inflates the repo without value.
- **Do not add `--since-bucket` / `--until-bucket` / other window aliases to stats.** The `--day` / `--since` / `--until` triple from `cli-contract.md#clast-stats` is canonical. Reject anything else with exit 2.
- **Do not add a `clast doctor --strict` mode that escalates warns to criticals.** The severity ladder is fixed; tooling that wants strict-mode CI can grep the JSON `findings[].severity` field.
- **Do not change the dispatcher's global-flag parsing.** Task 13 is a surgical replace of two case branches.
- **Do not remove the `_clast_stub` helper.** It still serves `breadcrumb` until step 09 lands; cleanup belongs to whichever step ships the last stub-removal.
- **Do not implement a `clast doctor` JSON-Schema validator for manifest / registry lines.** The required-fields check is enough for v1; a full schema is overkill given how few fields exist.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke — stats
export TZ=UTC
export CLAST_JOURNAL_DIR="$(mktemp -d)"
cp -R test/fixtures/multi-project/journal-seed/. "$CLAST_JOURNAL_DIR/"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
export CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T12:00:00Z' +%s)

bin/clast stats
bin/clast stats --day yesterday
bin/clast stats --since 2026-05-15 --until 2026-05-30
bin/clast --json stats --since 2026-05-15 --until 2026-05-30 | jq
bin/clast stats --project xesapps --since 2026-05-15 --until 2026-05-30
bin/clast stats --project not-a-real-slug                              ; echo "exit=$?"  # 1
bin/clast stats --day 2026-05-30 --since 2026-05-29                    ; echo "exit=$?"  # 2

# Manual smoke — doctor
bin/clast doctor                                                        ; echo "exit=$?"  # 1 (dddddddd missing)
bin/clast --json doctor | jq

# Orphan-removal flow
mkdir -p "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan"
: > "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
bin/clast doctor                                                        ; echo "exit=$?"  # 1 (orphan + dddddddd)
bin/clast doctor --fix --yes                                            ; echo "exit=$?"  # 1 (orphan gone, dddddddd warn survives)

# Critical-corruption flow
rm -rf "$CLAST_JOURNAL_DIR"
export CLAST_JOURNAL_DIR="$(mktemp -d)"
cp -R test/fixtures/corrupt-manifest/. "$CLAST_JOURNAL_DIR/"
bin/clast doctor                                                        ; echo "exit=$?"  # 4
bin/clast doctor --fix                                                  ; echo "exit=$?"  # 0 (rebuilt + clean)
wc -l "$CLAST_JOURNAL_DIR/.manifest.jsonl"                              # 2

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_NOW_EPOCH TZ
```

## Notes for the implementer

- **Stats is read-only and best-effort; doctor is the truth-teller.** Resist any urge to make stats double as a corruption flagger. If a manifest line is malformed, stats silently skips it (via `fromjson?`) and the counts are correspondingly lower. Doctor exists specifically to flag the malformed line; that division is the contract.
- **Most-recent-line semantics matter for re-captures.** A session that grew across re-snapshots has multiple manifest rows. `sessions` is one per `session_id`; `bytes` uses the most recent `source_size`; `messages_approx` is `wc -l` of the snapshot file at that most-recent row's path. Two re-snapshots of the same session do NOT inflate any count.
- **`source` can be `null`.** `clast_manifest_rebuild_from_disk` emits null `source` (and 0 `source_size`) because those values are unrecoverable from a snapshot alone. Stats must treat null `source` as "no project filter match" but still count the session; bytes contribution is 0 for those rows.
- **`wc -l` over snapshot files is the one place stats touches disk per-row.** Cache nothing in this step — a v1.1 perf step can add a `cache/messages.json` later. For now, a `for snapshot; do wc -l < "$snapshot" ; done | paste -sd+ | bc` (or equivalent jq-driven loop) is fine.
- **Doctor's six-check shape is a forward-compatible substrate.** A future check (e.g. "stale entries pointing at removed sessions") slots in as a 7th helper without restructuring the aggregator. Keep helpers pure (no I/O outside their scope; no side effects until `--fix` opts in).
- **`/dev/tty` for interactive confirmation, not stdin.** A user piping `yes | clast doctor --fix` should not bypass the confirmation — the prompt is intentional friction against scripted destructive operations. `--yes` is the documented escape hatch.
- **Day-cutoff sanity is informational only.** Never escalate it; never gate `--fix` on it. The heuristic (5%, ±30min) is a v1 placeholder — expect a tuning pass once real users hit it.
- **`✓` `!` `✗` are literal UTF-8 bytes**, same convention as step 09's `—` (U+2014). Write them directly in the source file; do not `printf '%s' "\xe2\x9c\x93"` or rely on locale.
- **Conventional commit suggestion**: `feat(stats,doctor): implement clast stats and clast doctor`. One commit is fine; if the doctor scope grows, splitting into two commits (`feat(stats): ...` then `feat(doctor): ...`) on the same branch is also fine. The README touch can ride either commit.
