---
step: 06
title: snapshot-subcommand
depends_on: [02, 03, 04]
size: medium
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#glossary
  - docs/overview.md#conventions
  - docs/cli-contract.md#clast-snapshot
  - docs/cli-contract.md#manifest-line
  - docs/cli-contract.md#exit-codes
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 06: `clast snapshot`

## Context

Steps 02–05 built every dependency this step needs. `lib/clast/clast-lib.bash` provides `clast_projects_dir`, `clast_journal_dir`, `clast_today` (with `CLAST_DAY_CUTOFF` + `CLAST_NOW_EPOCH` test hooks), `clast_log_*`, and `clast_atomic_write`. `lib/clast/clast-decode-lib.bash` round-trips segments and paths. `lib/clast/clast-manifest-lib.bash` exposes `clast_manifest_append`, `clast_manifest_has_capture`, `clast_manifest_lookup`, and `clast_manifest_iterate`. `bin/clast` already parses global flags, exports `CLAST_JSON`/`CLAST_QUIET`/`CLAST_VERBOSE`, sources the three libs at the top, and dispatches `whereami` and `registry` to real subcommand files. The literal token `snapshot` is currently routed to `_clast_stub snapshot 06`, which exits 2 with "planned for step 06"; replacing that stub is part of this step.

No snapshot writer or transcript walker exists yet. After this step, the capture half of `clast` works end-to-end: `clast snapshot` against a populated `~/.claude/projects/` (or a fixture) produces `transcripts/<day>/<segment>/<uuid>.jsonl` copies plus a manifest line per captured session, and re-running is a no-op.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils (`date -d`, `realpath -m`, `stat -c`) are on PATH.

## Goal

Implement `lib/clast/clast-subcommands/snapshot.bash` with the `clast_cmd_snapshot` entry function documented in `cli-contract.md#clast-snapshot`, wire it into the dispatcher (replace the stub), extend the `multi-project/` fixture with synthetic project transcripts, and cover both happy-path and edge cases with a new `test/test-snapshot.sh` integration suite.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — `~/.claude/projects/<segment>/<uuid>.jsonl` is the source; `~/.claude/journal/transcripts/<day_bucket>/<segment>/<uuid>.jsonl` is the destination; `.manifest.jsonl` sits alongside the `transcripts/` directory inside `$(clast_journal_dir)`.
- `docs/overview.md#glossary` — terminology for **snapshot**, **segment**, **day bucket**, **manifest**, **session**. Output strings and JSON keys must use these terms verbatim.
- `docs/overview.md#conventions` — local-time dates adjusted by `CLAST_DAY_CUTOFF` (default `04:00`); ISO 8601 timestamps with `Z`; snake_case JSON keys; exit codes (0 success, 1 partial failure, 2 usage error, 4 data integrity).
- `docs/cli-contract.md#clast-snapshot` — exact `--dry-run` / `--since TIMESTAMP` / `--include-segment SEG` semantics; the four-step behavior (read manifest → walk projects → for each new file: read first line, copy, append manifest → summarize); silent-on-no-op convention for cron/hook compatibility; the `--json` schema (`captured[]`, `skipped`, `errors`); exit codes (0 / 1 / 4).
- `docs/cli-contract.md#manifest-line` — the seven-field schema `clast_manifest_append` writes; this step uses the writer rather than restating fields.
- `docs/cli-contract.md#exit-codes` — top-level exit code table.
- `docs/cli-contract.md#error-handling-conventions` — stderr-vs-stdout split; structured errors land in `errors[]` under `--json`, never abort the whole run.
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — subcommand file convention (single `clast_cmd_snapshot` entry; argument parsing lives there, not in any lib).
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions; `simple/`, `multi-project/`, `empty/` are the three fixtures this step exercises.

## Tasks

1. **Write `lib/clast/clast-subcommands/snapshot.bash`.** Standard subcommand preamble: `# shellcheck shell=bash`, top-of-file `# shellcheck source=lib/clast/clast-lib.bash` / `clast-manifest-lib.bash` / `clast-decode-lib.bash` directives so `shellcheck` resolves cross-file calls. **Do not** add a double-source guard at the subcommand layer (subcommands are sourced once per dispatch); guards are a lib convention. Define exactly one public function `clast_cmd_snapshot`. Argument parsing, walking, copying, manifest writes, and output formatting all live in this file or in small `_clast_snapshot_*` helpers below `clast_cmd_snapshot`.

2. **Parse arguments inside `clast_cmd_snapshot`.** Accept the three documented flags plus `-h|--help`:
   - `--dry-run` (no value) → set `dry_run=1`.
   - `--since TIMESTAMP` and `--since=TIMESTAMP` → set `since_epoch` after parsing the value. Accept ISO 8601 (`2026-05-30`, `2026-05-30T14:30:55Z`) or any string GNU `date -d` understands (relative offsets like `-1d` are already covered by `date -d`). Reject unparseable input with `clast_log_error` + exit 2.
   - `--include-segment SEG` and `--include-segment=SEG` → push into an array `include_segments`. Repeatable. Validate that the value starts with `-` (segments always do); reject otherwise with exit 2 and a clear message.
   - `-h|--help` → print a usage block (synopsis + flag table summary) to stdout and exit 0.
   - Any other arg → exit 2 with "snapshot: unknown flag '<arg>'" on stderr.
   - **Do not** re-parse the global flags (`--json`, `--quiet`, `--journal-dir`, `--projects-dir`); the dispatcher already handled them via env. Read `CLAST_JSON` / `CLAST_QUIET` directly.

3. **Read the manifest precondition.** Before walking anything, run `clast_manifest_iterate 'true' >/dev/null 2>&1 || exit_code=$?`. If the iterate call exits non-zero with the file present, that's a manifest-corruption signal — print `clast: snapshot: manifest is corrupt; refusing to write` to stderr (or `{"error":"manifest is corrupt","code":4}` to stdout if `CLAST_JSON=1`) and exit 4 without writing anything. A *missing* manifest is fine and must NOT exit 4: `clast_manifest_iterate` already returns 0 on missing file. The four-exit-code path exists so cron loops don't quietly capture into a broken journal.

4. **Walk `$(clast_projects_dir)/*/*.jsonl`.** Use `find "$(clast_projects_dir)" -mindepth 2 -maxdepth 2 -type f -name '*.jsonl' -print0 | sort -z` to enumerate candidate sources in a stable order (segments alphabetical, sessions alphabetical within). Skip silently if the projects dir does not exist: that is the documented `empty/` case and must exit 0 with no output / `{"captured":[],"skipped":0,"errors":[]}` JSON.
   - Apply `--include-segment` filtering before any per-file work: derive `segment="$(basename "$(dirname "$source")")"` and skip the file if `include_segments` is non-empty and `segment` is not in it.
   - Apply `--since` filtering next: read source mtime via `stat -c %Y "$source"` (GNU) and skip if `mtime_epoch < since_epoch`. Skipped files count toward `skipped`, not `captured`.

5. **Derive `(session_id, source_mtime, source_size, day_bucket)` for each candidate.**
   - `session_id`: extract from the basename of `$source` (strip `.jsonl`). Validate against a UUID-shaped regex (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`). On mismatch, append a `{file, reason: "non-uuid filename"}` entry to `errors[]` and continue.
   - `source_mtime`: ISO 8601 UTC from the epoch via `date -u -d "@$mtime_epoch" +%Y-%m-%dT%H:%M:%SZ`. Mirrors `clast-manifest-lib.bash`'s `_clast_manifest_now_iso` format.
   - `source_size`: bytes via `stat -c %s "$source"`.
   - `day_bucket`: read the first line of the JSONL via `head -n1 "$source"`, parse `.timestamp` with `jq -r '.timestamp // empty'`. If present, parse it through `date -d "$timestamp" +%s` and apply `CLAST_DAY_CUTOFF` the same way `clast_today` does (factor a `_clast_snapshot_bucket_for_epoch` helper rather than re-implementing). If the first line lacks `.timestamp` (or is malformed), fall back to `mtime_epoch` and emit a `clast_log_warn` line (visible only with `--verbose`); do NOT error out — the source file is still capturable.

6. **Skip if already captured.** Call `clast_manifest_has_capture "$session_id" "$source_mtime"`. On 0 (already captured this exact `(session_id, mtime)` pair), increment `skipped` and continue. On 1, proceed to copy. Document inline why mtime (not ctime / size) is the dedup key: the manifest's "most recent line wins" semantics rely on mtime advancing when a session grows, which Claude Code's writer guarantees.

7. **Copy the source into `transcripts/`.** Compose the destination as `$(clast_journal_dir)/transcripts/$day_bucket/$segment/$session_id.jsonl`. `mkdir -p` the parent dir. Use `clast_atomic_write` — but the helper takes content as a string, not a source path; for a multi-megabyte session that materializes the file into a bash variable, which is unsafe. Instead use the same temp-file-then-rename primitive: `tmp="$(mktemp "$dest.copy.XXXXXX")"; cp "$source" "$tmp" && mv -f "$tmp" "$dest"`. On copy failure, `rm -f "$tmp"`, append `{file, reason}` to `errors[]`, do NOT write a manifest line, and continue. The invariant is: a manifest line implies the destination file exists and is complete.

8. **Append a manifest line.** Call `clast_manifest_append "$session_id" "$source" "transcripts/$day_bucket/$segment/$session_id.jsonl" "$source_mtime" "$source_size" "$day_bucket"`. The third argument is the *relative* snapshot path (relative to `$(clast_journal_dir)`), matching the example in `cli-contract.md#manifest-line`. On non-zero return from `clast_manifest_append`, the destination file is now an orphan; append a `{file, reason: "manifest append failed"}` entry to `errors[]` (the doctor command in step 10 will clean orphans), do NOT count the file as `captured`, and continue.

9. **`--dry-run` short-circuit.** When `dry_run=1`, skip the `cp` and the `clast_manifest_append` in tasks 7–8 and instead print one human-readable line per file that would be captured (`would capture: <segment>/<uuid> → <day_bucket>`) to stderr, *or* still build a `captured[]` entry under `--json` so callers can preview the JSON shape without writing. `skipped` and `errors` accounting stays identical to a real run. No files or manifest writes happen at all.

10. **Print the summary.**
    - **No-op rule (critical for cron / hook):** if `captured` is empty and `errors` is empty, print *nothing* in default mode (regardless of how many files were skipped). The dispatcher exits 0. This is the silent path the SessionStart hook depends on.
    - **Default mode, captures present:**

      ```
      Captured N session(s) across M project(s) (X.Y MB).
        <slug-or-segment>: K session(s)
        ...
      ```

      Resolve `slug` via `clast_registry_resolve "$source_dir"` per captured file (the registry lib is already sourced by the dispatcher). On miss, fall back to the literal segment. Group by slug-or-segment, sort by descending count then alpha. Bytes are summed across the run and formatted with one decimal place in MB (`printf '%.1f'`). If `errors` is non-empty, print a trailing `<E> error(s); see --json for details.` line to stderr.
    - **`--json` mode:** emit the documented schema verbatim. Build it with `jq -n` from accumulated bash arrays so the output is always valid JSON, even with zero captures or all errors. Always print, even on no-op (cron consumers may pipe through `jq`).
    - `--quiet` suppresses the human summary stdout but NOT stderr error lines; `--json` is unaffected by `--quiet` because the JSON IS the output.

11. **Wire the subcommand into the dispatcher.** In `bin/clast`, replace the `snapshot)   _clast_stub snapshot   06 ;;` line with a real `source "$CLAST_LIB/clast-subcommands/snapshot.bash"; clast_cmd_snapshot "$@" ;;` branch, mirroring the `whereami)` and `registry)` cases. Leave the other stubs untouched — they are owned by later steps.

12. **Extend the `multi-project/` fixture for snapshot tests.** Step 05 created `test/fixtures/multi-project/projects.json`; this step adds a synthetic `~/.claude/projects/` subtree alongside it:
    - `test/fixtures/multi-project/projects-tree/-tmp-proj-xesapps/<uuid-1>.jsonl` — first line has `"timestamp":"2026-05-29T14:00:00Z"`, second line is a normal message.
    - `test/fixtures/multi-project/projects-tree/-tmp-proj-xesapps/<uuid-2>.jsonl` — different session of the same project; first-line timestamp `2026-05-30T01:30:00Z` so the day-cutoff math actually bumps it from `2026-05-30` into `2026-05-29` under the default 04:00 cutoff (this is the test for task 5's bucket logic).
    - `test/fixtures/multi-project/projects-tree/-tmp-proj-scratch/<uuid-3>.jsonl` — session for the unregistered `scratch` slug, first-line timestamp `2026-05-30T10:00:00Z`.
    - One file with a non-UUID basename (e.g., `notes.jsonl`) under one of the segment dirs, to exercise task 5's regex-reject error path.
    - Tests will copy `projects-tree/` into `$CLAST_PROJECTS_DIR` via `make_fixture_projects_tree` after pointing it at `multi-project/projects-tree` (see task 14 — `helpers.sh` may need a tiny addition).

13. **Adjust `test/helpers.sh` if needed.** `make_fixture_projects_tree` currently copies `test/fixtures/<name>/.` into `$CLAST_PROJECTS_DIR`. Because `multi-project/` mixes registry + transcript fixtures, add a sibling `make_fixture_projects_tree_from <name>/<subpath>` (or extend the existing function to accept a `name/subpath` form) so a single fixture directory can serve both the registry-lib and snapshot tests without colliding. Keep the existing signature working — any change must be additive.

14. **Write `test/test-snapshot.sh`.** Subprocess-style suite, modeled on `test/test-registry-cmd.sh`: `cd` to repo root, `source test/helpers.sh`, set `_CLAST_TEST_NAME=test-snapshot`. Each scenario calls `setup_test_journal`, populates the projects/journal as needed, invokes `bin/clast snapshot ...`, asserts on stdout/stderr/exit code and on the resulting `$CLAST_JOURNAL_DIR/transcripts/` + `.manifest.jsonl`, then `teardown_test_journal`. Cover at minimum:
    - **empty fixture, no projects dir**: `bin/clast snapshot` against `empty/` exits 0 with empty stdout (no-op rule), no manifest is created, no `transcripts/` dir is created.
    - **empty fixture, `--json`**: same setup, exits 0, stdout is valid JSON with `captured: []`, `skipped: 0`, `errors: []`.
    - **simple fixture, fresh capture**: against `simple/` (two sessions across two segments), exits 0, two files appear under `transcripts/<day>/<segment>/<uuid>.jsonl`, `.manifest.jsonl` has exactly two lines, and the human summary names `2 session(s) across 2 project(s)`.
    - **simple fixture, idempotent re-run**: a second invocation immediately after exits 0, prints nothing (no-op), and the manifest still has two lines (not four).
    - **simple fixture, mtime advances**: `touch -d "+1 minute" "$source"` on one file, re-run, exit 0, manifest grows to three lines (new entry for the bumped mtime), destination JSONL is rewritten with the newer content. Asserts task 6's mtime-keyed dedup.
    - **simple fixture, `--dry-run`**: against a fresh tmp journal, exit 0, NO files created, NO manifest written. `--dry-run --json` still emits `captured[]` of length 2.
    - **multi-project fixture, day-cutoff bucket**: with `CLAST_NOW_EPOCH` and `CLAST_DAY_CUTOFF=04:00`, the `2026-05-30T01:30:00Z` session lands under `transcripts/2026-05-29/...`, not `2026-05-30/...`. This is the assertion that proves task 5 honors the cutoff.
    - **multi-project fixture, `--include-segment`**: passing `--include-segment -tmp-proj-xesapps` captures only the xesapps sessions; the scratch session and the non-UUID file are skipped.
    - **multi-project fixture, `--since`**: passing `--since 2026-05-30T00:00:00Z` skips the `2026-05-29` session and captures the later two only.
    - **multi-project fixture, slug grouping**: the human summary names `xesapps: 2 session(s)` (slug, resolved via registry) and `-tmp-proj-scratch: 1 session(s)` (segment, unresolved). The fixture's `projects.json` from step 05 will need a `xesapps` entry whose `path` matches `/tmp/proj-xesapps` — adjust the fixture or add a per-test seed line if needed; pick whichever keeps the registry fixture's existing tests green.
    - **non-UUID file is reported as error, not silent skip**: the `notes.jsonl` decoy in the fixture appears in `errors[]` under `--json` with `reason` mentioning "uuid"; other files in the same run still capture; exit code is 1 (partial failure).
    - **corrupt manifest aborts before any writes**: pre-seed `$CLAST_JOURNAL_DIR/.manifest.jsonl` with a line `clast_manifest_iterate 'true'` rejects (e.g., a bare `{` with no newline-terminated JSON), run snapshot, exit 4, no files written under `transcripts/`. Note: this depends on `clast_manifest_iterate`'s actual corruption detection behavior — if `fromjson?` silently swallows the bad line and `iterate` exits 0, surface that to the user instead of guessing and treat task 3's exit-4 path as documented-but-currently-unreachable; flag it in the step write-up rather than papering over it.
    - **`--help` exits 0**: `bin/clast snapshot --help` exits 0, stdout contains `snapshot` and one of the flag names.
    - **unknown flag exits 2**: `bin/clast snapshot --no-such-flag` exits 2, stderr mentions the flag.
    - **`--include-segment` rejects non-segment values**: e.g., `--include-segment foo` (no leading dash) exits 2.

15. **Wire `test/test-snapshot.sh` into `test/test-clast.sh`.** Append it to the `suites` array after `test/test-registry-cmd.sh`. New order: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot.

16. **Update README.md with a one-block usage example.** Add a short "Capture your sessions" section (or extend the existing top-level usage section) showing `clast snapshot` and `clast snapshot --dry-run --json | jq` so a reader on `crates.io`/`npm` / GitHub gets an immediate hands-on hook. Keep it tight; full docs live in `docs/cli-contract.md`. Do not document `--since` or `--include-segment` in README (they are debugging flags) — link to `docs/cli-contract.md#clast-snapshot` instead.

17. **Confirm `make lint` and `make test` pass.** Add explicit `# shellcheck source=...` directives in `lib/clast/clast-subcommands/snapshot.bash` for `clast-lib.bash`, `clast-manifest-lib.bash`, `clast-decode-lib.bash`, and `clast-registry-lib.bash` (the last because task 10 calls `clast_registry_resolve`).

## Acceptance criteria

- `lib/clast/clast-subcommands/snapshot.bash` exists, exports `clast_cmd_snapshot` as its only public function, and passes `shellcheck`.
- `bin/clast` routes `snapshot` to `clast_cmd_snapshot` (the `_clast_stub snapshot 06` line is gone). The other stubs are untouched.
- `clast snapshot` against an empty / missing `~/.claude/projects/` exits 0 with no stdout output (the cron/hook silent path).
- `clast snapshot` against the `simple/` fixture creates two files under `transcripts/<day>/<segment>/` and appends two lines to `.manifest.jsonl`. A second invocation is a no-op.
- `clast snapshot --json` against `simple/` emits valid JSON matching `cli-contract.md#clast-snapshot`'s schema (`captured[]`, `skipped`, `errors[]`); `jq -e .captured | length == 2` succeeds.
- `clast snapshot --dry-run` writes neither files nor manifest lines and still reports the would-capture set on stderr (default) or in `captured[]` (`--json`).
- `clast snapshot --include-segment SEG` limits capture to one segment; `--include-segment` is repeatable.
- `clast snapshot --since TIMESTAMP` skips files with `mtime < TIMESTAMP`; ISO 8601 and `date -d`-friendly relative strings both parse.
- A session whose first-line `timestamp` is before the day cutoff lands in the previous day's bucket; one whose first line lacks a `timestamp` falls back to the source mtime and logs a `--verbose` warning.
- A non-UUID `.jsonl` filename in the projects tree is reported in `errors[]` (or stderr in default mode) and produces a partial-failure exit code (1); the other captures in the same run still succeed.
- A pre-existing corrupted `.manifest.jsonl` (per `clast_manifest_iterate`'s rejection criteria) makes `clast snapshot` exit 4 before writing anything. (If `clast_manifest_iterate` does not currently surface corruption, document the gap in the PR description and leave the task-3 code path conditional on a future iterate-returns-error contract — do not invent a corruption detector in this step.)
- `clast snapshot --help` exits 0; an unknown flag exits 2.
- `test/fixtures/multi-project/projects-tree/` exists with the three valid sessions + one non-UUID decoy described in task 12.
- `test/test-snapshot.sh` covers every scenario in task 14 and exits 0.
- `make test` runs all eight suites (lib, decode, dispatcher, whereami, manifest, registry, registry-cmd, snapshot) and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement `clast projects`, `sessions`, or `show`.** Those land in step 07. The snapshot summary uses `clast_registry_resolve` for slug naming, but that lib already exists; no new query surface is added here.
- **Do not implement `clast doctor` or any rebuild / orphan-cleanup logic.** Orphan snapshots (file present, manifest line missing) are a step-10 concern. This step documents the orphan possibility (task 8) and stops there.
- **Do not implement a config-file reader.** `~/.config/clast/config.toml` (`day_cutoff`, default args) is a separate step's problem; the env vars `CLAST_DAY_CUTOFF` and `CLAST_NOW_EPOCH` are the only configuration.
- **Do not modify `clast-lib.bash`, `clast-decode-lib.bash`, `clast-manifest-lib.bash`, or `clast-registry-lib.bash`.** If a missing helper is genuinely required, stop and ask rather than expanding scope. The one allowed touch outside `clast-subcommands/snapshot.bash` is `test/helpers.sh` (task 13) and only additively.
- **Do not parallelize the walk.** A serial loop is fine for v1 even with 1000+ sessions. A performance step lives in v1.1; flagging it now with a `# TODO(v1.1): parallel capture` comment is enough.
- **Do not add cross-machine sync logic** (manifest merging, dedup across hosts). The append-only manifest is by design sync-friendly per `overview.md#cross-machine-considerations`; making it so is out of scope here.
- **Do not modify the existing `simple/` or `empty/` fixtures.** Only `multi-project/` gains a `projects-tree/` subdirectory.
- **Do not implement Windows path handling beyond what step 02's decoder already does.** WSL2 paths work via the existing `clast_decode_segment`; native Windows is future work.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke against the simple fixture
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/simple"

bin/clast snapshot                              # human summary, two captures
ls "$CLAST_JOURNAL_DIR"/transcripts/*/-*        # two JSONL files
wc -l "$CLAST_JOURNAL_DIR/.manifest.jsonl"      # 2

bin/clast snapshot                              # no output — idempotent
bin/clast snapshot --json | jq                  # captured: [], skipped: 2

# Dry run
rm -rf "$CLAST_JOURNAL_DIR"; export CLAST_JOURNAL_DIR="$(mktemp -d)"
bin/clast snapshot --dry-run --json | jq '.captured | length'   # 2
test ! -e "$CLAST_JOURNAL_DIR/.manifest.jsonl" && echo "dry-run wrote nothing"

# Day-cutoff (frozen clock)
export CLAST_NOW_EPOCH=$(date -d "2026-05-30T05:00:00Z" +%s)
export CLAST_DAY_CUTOFF=04:00
bin/clast snapshot --json | jq '.captured[].day_bucket' | sort -u

# Include-segment + since
bin/clast snapshot --include-segment -tmp-clast-test-fixtures-simple-proj-a --json | jq '.captured | length'
bin/clast snapshot --since 2099-01-01 --json | jq '{captured: (.captured|length), skipped}'

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_NOW_EPOCH CLAST_DAY_CUTOFF
```

## Notes for the implementer

- **Subcommand, not lib.** All snapshot logic lives in `lib/clast/clast-subcommands/snapshot.bash`. Do not factor a `clast-snapshot-lib.bash` — the only consumer is the subcommand itself, and `repo-bootstrap.md` does not name one. Internal helpers (`_clast_snapshot_bucket_for_epoch`, `_clast_snapshot_capture_one`, etc.) stay in the subcommand file with the `_clast_snapshot_` prefix.
- **Silent on no-op is load-bearing.** The SessionStart hook (step 11) backgrounds this command on every Claude Code start; a chatty summary on every launch would be noise the user sees on stderr forever. Re-read `cli-contract.md#clast-snapshot`'s "Silent if no work was done" wording before changing this behavior.
- **Day bucket from the *first line*, not file mtime.** A session that runs across midnight stays in the day it started in. The first-line `timestamp` field is authoritative; mtime is the fallback for malformed files only. Test both paths.
- **GNU `date -d` and `realpath -m`.** Dev shell pulls in GNU coreutils so this is fine; BSD `date` is not supported per `overview.md`'s constraints. Flag any `date -d` usage with a one-line comment so a future Windows-native effort knows where to look.
- **Atomic copy, not in-memory.** `clast_atomic_write` writes a content *string*; do NOT slurp a multi-megabyte JSONL into a bash variable. Use the `cp $source $tmp && mv $tmp $dest` pattern explicitly. Same crash-safety story (rename is atomic on the same filesystem), without the memory hazard.
- **Manifest-first vs file-first ordering.** The invariant is: a manifest line implies the snapshot file exists. So: copy first, then append. If the manifest append fails, the file is an orphan but the dedup key (`session_id, mtime`) is still uncaptured; a re-run will overwrite the orphan file and try again. That is the documented self-healing path.
- **`clast_registry_resolve` is cheap.** It's a `jq` scan of `projects.json`. Calling it once per captured file in the summary loop is fine. Do not cache; the registry is small.
- **Subcommand tests run `bin/clast` as a subprocess** (mirroring `test/test-whereami.sh` and `test/test-registry-cmd.sh`). Set `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` in the environment of those subprocess invocations to drive the day-bucket assertions deterministically.
- **Per-test fixture isolation.** Always go through `setup_test_journal` + `make_fixture_projects_tree[_from]`; never write to a real `$HOME/.claude/journal/` from a test.
- **Conventional commit suggestion**: `feat(snapshot): implement clast snapshot subcommand`. If the fixture-helper change in task 13 is large enough to read separately, a follow-up `test(helpers): support subpath fixture copies` is fine; one squashed commit on merge is also fine. The PR title should describe the snapshot work.
