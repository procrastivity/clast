---
step: 08
title: entries-subcommand
depends_on: [02, 03, 04, 05, 06, 07]
size: medium
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#conventions
  - docs/overview.md#glossary
  - docs/cli-contract.md#date-parsing
  - docs/cli-contract.md#clast-entries
  - docs/cli-contract.md#entry-frontmatter
  - docs/cli-contract.md#manifest-line
  - docs/cli-contract.md#error-handling-conventions
  - docs/repo-bootstrap.md#libclastclast-subcommandsnamebash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 08: `clast entries` (list / read / write)

## Context

Steps 02–07 produced the read half of the CLI and every lib `entries` needs. `lib/clast/clast-lib.bash` exposes `clast_journal_dir`, `clast_parse_date` (ISO / `today` / `yesterday` / `-Nd` / `-Nw`), `clast_today`, `clast_log_*`, `clast_atomic_write`, and the `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` test hooks. `lib/clast/clast-manifest-lib.bash` exposes `clast_manifest_lookup` and `clast_manifest_iterate` against the seven-field manifest schema (`session_id`, `source`, `snapshot`, `captured_at`, `source_mtime`, `source_size`, `day_bucket`). `lib/clast/clast-registry-lib.bash` exposes `clast_registry_resolve` (path-or-segment → slug) and `clast_registry_list_json`. `lib/clast/clast-decode-lib.bash` exposes `clast_decode_segment` / `clast_encode_path`. `bin/clast` already sources all four libs at the top, parses global flags (`--json`, `--quiet`, `--verbose`, `--journal-dir`, `--projects-dir`), and dispatches `whereami`, `registry`, `snapshot`, `projects`, `sessions`, `show` to real subcommand files. The literal token `entries` is still routed through `_clast_stub` (currently labeled "planned for step 10" in the dispatcher map — that label is an out-of-date forecast that this step corrects).

After step 07, the `multi-project/journal-seed/` fixture supplies a populated manifest, captured snapshot JSONLs, and a `projects.json` registry. This step adds the curated-entry layer on top: a writer that composes the documented Markdown-with-YAML-frontmatter file from a session_id + body, a list view over `entries/`, and a `read` convenience. None of the read commands from step 07 change; only the `curated` probe in `sessions.bash` / `show.bash` will start finding hits naturally once entries exist.

**Run `direnv allow` (or `nix develop`) before starting** so `jq`, `shellcheck`, and GNU coreutils (`date -d`, `realpath -m`, `stat -c`) are on PATH.

## Goal

Implement one new subcommand file — `lib/clast/clast-subcommands/entries.bash` — that dispatches `clast entries` (default: list), `clast entries read <path>`, and `clast entries write …`; wire it into `bin/clast` (replacing the `entries` stub); extend the `multi-project/` fixture with a small `entries-seed/` subdirectory and a fixture helper to copy it; and add a `test/test-entries.sh` integration suite covering frontmatter generation, slug-collision suffixing, list filtering, and the read path.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — entries live at `$(clast_journal_dir)/entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md`; the directory may not exist yet on a fresh journal. Frontmatter convention is YAML between `---` fences; body is free-form Markdown.
- `docs/overview.md#conventions` — ISO 8601 UTC with `Z` for any timestamp in JSON; snake_case JSON keys; exit codes (0 success, 1 general error, 2 usage error). `machine: <hostname>` is recorded for cross-machine origin visibility.
- `docs/overview.md#glossary` — `session_id`, `segment`, `day_bucket`, `snapshot_path`, `session_slug`, `project_slug` are spelled exactly as documented; do not rename in JSON output.
- `docs/cli-contract.md#date-parsing` — `--day` / `--since` / `--until` accept ISO / `today` / `yesterday` / `last-week` / `-Nd` / `-Nw`. `clast_parse_date` from step 02 already does this; do not re-implement.
- `docs/cli-contract.md#clast-entries` — exact synopsis, list output (`date time project slug tags` column header), JSON schema (`path`, `date`, `time`, `day_bucket`, `project`, `session_id`, `session_slug`, `branch`, `tags`, `title`), write synopsis (`--session`, `--slug`, `--tags`, `--title`, `--body-from FILE` / `--body-stdin`), and write behavior (manifest lookup → registry resolve → compose frontmatter → atomic write → suffix collisions).
- `docs/cli-contract.md#entry-frontmatter` — the canonical frontmatter shape: `date`, `time`, `day_bucket`, `project`, `project_path`, `project_remote`, `branch`, `author`, `tags`, `session_id`, `session_slug`, `snapshot_path`, `machine`. Tags are a YAML inline-array. `snapshot_path` is the journal-relative path from the manifest, not absolute.
- `docs/cli-contract.md#manifest-line` — only the seven manifest fields are trusted without re-reading the snapshot; `branch` is NOT one of them and remains best-effort (step 07 added the same TODO).
- `docs/cli-contract.md#error-handling-conventions` — stderr-vs-stdout split, `{"error":"...","code":N}` shape under `--json`, exit codes (2 = missing required flag / usage, 1 = unknown session / empty body / write failure).
- `docs/repo-bootstrap.md#libclastclast-subcommandsnamebash` — one `clast_cmd_<name>` per file; argument parsing lives in the subcommand, not in libs.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions, subprocess-style integration tests.

## Tasks

1. **Write `lib/clast/clast-subcommands/entries.bash`.** Standard subcommand preamble: `# shellcheck shell=bash`, with `# shellcheck source=lib/clast/clast-lib.bash` / `clast-manifest-lib.bash` / `clast-registry-lib.bash` / `clast-decode-lib.bash` directives at the top. **Do not** add a double-source guard at the subcommand layer. Define exactly one public function `clast_cmd_entries`. Internal helpers use `_clast_entries_` prefix and live below the entry function.

2. **Inside `clast_cmd_entries`, split on the first positional.** Peek at `${1:-}`: if it is `read`, shift and dispatch to `_clast_entries_read`; if it is `write`, shift and dispatch to `_clast_entries_write`; if it is `-h` / `--help` (or `list -h` / `list --help`), print combined usage and exit 0; if it is `list` or absent or starts with `--`, treat the remainder as flags for the list view and dispatch to `_clast_entries_list`. Any other positional is an unknown subcommand: exit 2 with `clast: entries: unknown subcommand '<arg>'` on stderr (`{"error":"...","code":2}` under `--json`).

3. **Implement `_clast_entries_list` argument parsing.** Accept the documented flags plus `-h|--help`:
   - `--day DATE` / `--day=DATE` → set `day_filter` (resolved to ISO via `clast_parse_date`); mutually exclusive with `--since` / `--until` (exit 2 if combined).
   - `--since DATE` / `--since=DATE` → set `since_date` after `clast_parse_date`.
   - `--until DATE` / `--until=DATE` → set `until_date` after `clast_parse_date`. Unlike `projects` / `sessions`, **list has no implicit default window** — when no date flag is given, return every entry in `entries/`. This matches `cli-contract.md#clast-entries`'s synopsis (all four window flags are bracketed).
   - `--project SLUG` / `--project=SLUG` → set `project_filter`.
   - `--tag TAG` / `--tag=TAG` → append to a `tag_filters` array; repeatable; match semantics is "entry frontmatter `tags` array contains ALL listed tags" (intersection, not union — matches `--tag foo --tag bar` reads naturally as "tagged foo AND bar").
   - `--limit N` / `--limit=N` → cap the result list; default unlimited. Validate as a positive integer; otherwise exit 2.
   - `-h|--help` → print synopsis + flag summary to stdout, exit 0.
   - Unknown flag → exit 2 with `clast: entries: unknown flag '<arg>'`.
   - **Do not** re-parse global flags; read `CLAST_JSON` / `CLAST_QUIET` directly.

4. **Implement `_clast_entries_list` discovery + filtering.** The source set is `$(clast_journal_dir)/entries/*.md`. If the directory does not exist, the result set is empty — exit 0, default mode prints just the header row (matches `projects` / `sessions` behavior from step 07), `--json` prints `[]`. For each file:
   - Parse its frontmatter by reading lines between the leading `---` and the next `---`. Use a small awk one-liner factored into a `_clast_entries_read_frontmatter <path>` helper that prints `key=value` pairs (one per line) for the keys this step needs: `date`, `time`, `day_bucket`, `project`, `session_id`, `session_slug`, `branch`, `tags`, `title`. Tags arrive as a YAML inline array (`[a, b, c]`); strip brackets and split on `,` then trim whitespace. **Do not** add a full YAML parser; the writer in task 8 controls the file shape, and the format is fixed.
   - Apply window filter on `day_bucket`: same lex-compare logic as step 07's projects/sessions (`--day D` → `==`, `--since` / `--until` → `>=` / `<=`).
   - Apply `--project SLUG` filter on the parsed `project` field.
   - Apply `--tag T` filters: every tag in `tag_filters` must appear in the parsed tags list.
   - Sort by `date` descending then `time` descending (most recent entry first).
   - Apply `--limit N` after sort.

5. **Emit `clast entries` list output.**
   - **Default mode**: a single header line `date        time   project          slug                         tags` (matching `cli-contract.md#clast-entries`'s example), then one row per surviving entry. Columns: `date` width 11, `time` width 6, `project` width 17, `slug` width 29 (uses the entry's `session_slug`), `tags` joined with `,` (no spaces) and truncated to width 30 with `…`. Use `printf` with fixed-width fields. Missing values render as empty padded to width.
   - **`--json` mode**: emit a JSON array via `jq -n --argjson rows '...'` (build the array up front, one jq invocation); always print, even when empty (`[]`). Per-row shape: `{"path","date","time","day_bucket","project","session_id","session_slug","branch","tags","title"}`. `path` is the absolute filesystem path on disk. `tags` is an array of strings (empty array if no tags). `branch` / `title` may be `null`.
   - **`--quiet`** suppresses stdout body but NOT stderr; `--json` is unaffected by `--quiet`.

6. **Implement `_clast_entries_read`.** Accepts exactly one positional `<entry-path>` (exit 2 if missing or if more than one positional, or on any unknown flag). Resolve the path: if absolute and exists, use it directly; otherwise treat the argument as a filename relative to `$(clast_journal_dir)/entries/`. If the resolved path does not exist or is not a regular file, exit 1 with `clast: entries: read: not found '<arg>'` (or JSON-error under `--json`). On success, stream the file to stdout via `cat --` and exit 0. `--json` mode wraps the file content as `{"path":"...","content":"..."}` so the skill layer can consume either form; `--quiet` is ignored for `read` (this is the explicit "give me the file" path).

7. **Implement `_clast_entries_write` argument parsing.** Accept:
   - `--session SESSION_ID` / `--session=SESSION_ID` → required. Validate against the UUID regex used by step 07's `show` (same regex; copy the literal, do not factor into a lib for one duplicate).
   - `--slug SESSION_SLUG` / `--slug=SESSION_SLUG` → required. Validate as `[a-z0-9][a-z0-9-]{0,63}` (lowercase, digits, hyphens; max 64 chars; no leading hyphen). Exit 2 on invalid.
   - `--tags TAG,TAG,...` / `--tags=...` → optional. Split on `,`, trim whitespace, drop empties. Each surviving tag must match `[a-z0-9][a-z0-9-]{0,31}`; reject the whole flag with exit 2 if any tag fails.
   - `--title TITLE` / `--title=TITLE` → optional free-form string. Reject embedded newlines (exit 2); everything else is allowed and quoted into YAML.
   - `--body-from FILE` / `--body-from=FILE` → read body bytes from `FILE`. Mutually exclusive with `--body-stdin` (exit 2).
   - `--body-stdin` → read body bytes from stdin until EOF. Mutually exclusive with `--body-from`.
   - `-h|--help` → exit 0 with usage. Unknown flag → exit 2.
   - Missing `--session`, missing `--slug`, or missing both body sources → exit 2 with `clast: entries: write: missing required flag '<name>'`.

8. **Implement `_clast_entries_write` composition + atomic write.**
   - Look up the manifest line via `clast_manifest_lookup "$session_id"`. If missing, exit 1 with `clast: entries: write: session '<id>' not found in manifest` (JSON-error under `--json`).
   - From the manifest line take `snapshot`, `day_bucket`, `source_mtime`. The journal-relative `snapshot_path` for the frontmatter is the `snapshot` field verbatim.
   - Derive `segment` from the snapshot path: middle component after `transcripts/<day_bucket>/`. Resolve project via `clast_registry_resolve "$segment"`:
     - On hit: read the matching line from `clast_registry_list_json` (jq filter on `slug`) to recover `project` (slug), `project_path` (the registry `path` field), and `project_remote` (the registry `remote` field, may be null).
     - On miss: `project` = the segment string verbatim, `project_path` = `clast_decode_segment "$segment"` if a single candidate matches, else null; `project_remote` = null. Do NOT fail the write — entries may legitimately be written for unregistered segments. Emit a `clast_log_warn` so `--verbose` users see the fallback.
   - Compose `date` / `time` / `day_bucket`:
     - `date` and `time` come from "now" (`clast_today` + a sibling `_clast_entries_now_hhmm` helper that formats `_clast_now_epoch` as `HH:MM` local). `day_bucket` for the frontmatter and the filename uses `clast_today` so a 02:00 invocation with the default cutoff still buckets under the previous date.
     - `time` is HOUR:MINUTE local — see `cli-contract.md#clast-entries`'s example (`14:30`).
   - Pull `branch` best-effort from the snapshot JSONL (same conservative path as step 07's `sessions.bash` — `jq -r '.cwd // .git_branch // empty'` first line; null if absent). The journal-relative `snapshot_path` is taken verbatim from the manifest's `snapshot` field (the same value step 07 uses). Pull `author` from `${CLAST_AUTHOR:-$USER}`; `machine` from `${CLAST_MACHINE:-$(hostname)}`. These env-var overrides exist so tests are deterministic.
   - Read body: if `--body-from FILE`, read it (exit 1 if unreadable); if `--body-stdin`, slurp stdin. After read, reject an empty body (zero bytes or whitespace-only) with exit 1 `clast: entries: write: body is empty`. Trim a single trailing newline at most, then ensure the body ends with exactly one newline before the EOF.
   - Compose the file: leading `---\n`, the frontmatter keys in the exact order documented in `cli-contract.md#entry-frontmatter` (`date`, `time`, `day_bucket`, `project`, `project_path`, `project_remote`, `branch`, `author`, `tags`, `session_id`, `session_slug`, `snapshot_path`, `machine`), `---\n`, blank line, body. Optional fields (`project_remote`, `branch`, `title`, `tags`) are emitted with explicit `null` / `[]` when missing — do NOT omit the key. Tags render as `tags: [a, b, c]` (single-line YAML inline array; bracket + comma-space). `title` is NOT a documented frontmatter field per `cli-contract.md#entry-frontmatter` — when `--title` is supplied, emit it as the first body line `# Session: <title>\n\n` instead. Strings that contain `:`, `#`, `'`, `"`, leading/trailing whitespace, or any non-printable bytes are wrapped in YAML double quotes with `\` / `"` / `\n` backslash-escaped; bare strings otherwise.
   - Filename: `entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md`. `<project-slug>` is the resolved `project` slug; if unresolved, fall back to the bare segment string (collapsed: leading `-` stripped, internal `--` left intact — easier to keep round-trippable to `clast_decode_segment` than to fight it). Collision handling: if the target file already exists, append `-2`, `-3`, etc. before `.md` until a free name is found. Cap the suffix search at `-99`; beyond that, exit 1 with `clast: entries: write: too many collisions for <basename>` (defensive guard — never expected in real use).
   - Write atomically via `clast_atomic_write "$target" "$composed"`. Create `$(clast_journal_dir)/entries/` with `mkdir -p` first.

9. **Emit `clast entries write` output.**
   - **Default mode**: print `Wrote entries/<basename>` to stdout, exit 0. The path printed is journal-relative (matches `cli-contract.md#clast-entries`'s example), so test assertions are stable across `mktemp` prefixes.
   - **`--json` mode**: print `{"path":"<absolute-path>"}` and exit 0.
   - **`--quiet`** suppresses the default-mode line; `--json` ignores `--quiet`.

10. **Wire the entries subcommand into the dispatcher.** In `bin/clast`, replace the `entries) _clast_stub entries 10 ;;` case with the real source+dispatch branch, mirroring the shape used for `projects` / `sessions` / `show` in step 07. Leave the other stubs (`breadcrumb`, `stats`, `doctor`) untouched; only `entries` is wired in this step. Update the inline step-number labels on the remaining stubs only if step 07 already did — do not invent new step numbers for steps that have not been written yet.

11. **Extend the `multi-project/` fixture with an entries seed and a writer-target seed.** Step 07's `journal-seed/` already supplies a manifest + transcripts + registry. Now add `test/fixtures/multi-project/entries-seed/` with a small `entries/` directory pre-populated for the list-view tests:
    - At least one entry per fixture project: `2026-05-30-1430-xesapps-vw-consumer-fields-explain.md`, `2026-05-29-0915-xesapps-old-thread.md`, `2026-05-30-1100-scratch-quick-note.md` — pick session_ids that match the manifest from step 07's `journal-seed/`.
    - One entry with multiple tags to exercise `--tag` intersection (e.g., the xesapps `2026-05-30` entry gets `[mysql, optimization, eav]`).
    - Body contents short (one or two lines) — the tests will only assert that `clast entries read` round-trips byte-for-byte; the body itself is fixture data and stable.
    - Add `test/fixtures/multi-project/entries-seed/.gitkeep` if no other file would otherwise keep the directory tracked. (The above three files satisfy this; the `.gitkeep` is a defensive note for the implementer if they prefer to start with an empty seed and add files file-by-file.)

12. **Add `make_fixture_entries_seed_from <name>/<subpath>` to `test/helpers.sh`** (mirroring step 06's `make_fixture_projects_tree_from` and step 07's `make_fixture_journal_seed_from`). It copies the fixture's `entries-seed/` subtree into `$CLAST_JOURNAL_DIR` (so an `entries-seed/entries/` subdirectory in the fixture lands at `$CLAST_JOURNAL_DIR/entries/`). Keep all existing helper signatures intact; this must be a strictly additive change.

13. **Write `test/test-entries.sh`.** Subprocess-style suite modeled on `test/test-query.sh`. `cd` to repo root, `source test/helpers.sh`, set `_CLAST_TEST_NAME=test-entries`. Each scenario calls `setup_test_journal`, seeds the journal (manifest + entries) via the helpers from step 07's task 19 and task 12 above, runs `bin/clast entries …`, asserts on stdout/stderr/exit code, then `teardown_test_journal`. Set `CLAST_NOW_EPOCH=$(date -d '2026-05-30T14:30:00Z' +%s)`, `CLAST_AUTHOR=test-user`, and `CLAST_MACHINE=test-host` in the environment of each subprocess so frontmatter is deterministic. Cover at minimum:
    - **`clast entries` (no flags) with three seeded entries**: exit 0, default mode prints header + three rows sorted by date desc / time desc.
    - **`clast entries --json`**: stdout is valid JSON array of length 3; each object has the ten documented fields; `tags` for the xesapps `2026-05-30` row is `["mysql","optimization","eav"]`; `path` is absolute.
    - **`clast entries --day 2026-05-30`**: exit 0, two rows.
    - **`clast entries --since 2026-05-22 --until 2026-05-29`**: exit 0, one row (old-thread only).
    - **`clast entries --project xesapps`**: exit 0, two rows (both xesapps entries).
    - **`clast entries --tag mysql`**: exit 0, one row.
    - **`clast entries --tag mysql --tag optimization`**: exit 0, one row (intersection).
    - **`clast entries --tag mysql --tag does-not-exist`**: exit 0, zero rows.
    - **`clast entries --limit 1`**: exit 0, one row (the most recent).
    - **`clast entries --day` combined with `--since`**: exits 2, stderr mentions mutual exclusion.
    - **`clast entries` against an empty journal** (no `entries/` directory): exit 0, default mode prints just the header, `--json` prints `[]`.
    - **`clast entries read <relative-basename>`**: exit 0, stdout equals fixture file contents byte-for-byte.
    - **`clast entries read /absolute/path/to/entry.md`**: exit 0, same content.
    - **`clast entries read no-such-entry.md`**: exit 1, stderr says "not found".
    - **`clast entries write --session <known-id> --slug new-slug --body-stdin`** with stdin `Hello\n`: exit 0, stdout `Wrote entries/2026-05-30-1430-xesapps-new-slug.md` (path is journal-relative). The written file:
      - has frontmatter in the documented key order,
      - has `author: test-user`, `machine: test-host` (from env),
      - has `project: xesapps`, `project_path` from the registry, `project_remote` from the registry,
      - has `session_id` and `session_slug` matching the flags,
      - has `tags: []` (none supplied),
      - has `snapshot_path` matching the manifest's `snapshot` field for that session,
      - body equals exactly `Hello\n` after the frontmatter and blank line.
    - **`clast entries write --session <known-id> --slug new-slug --tags mysql,perf --title "Long Title" --body-from FILE`**: exit 0, written file has `tags: [mysql, perf]` and body begins with `# Session: Long Title\n\n` followed by the file contents.
    - **Slug-collision suffixing**: run the same `write` command twice with identical `--session` + `--slug`. First call writes `…-new-slug.md`; second call writes `…-new-slug-2.md`; third writes `…-new-slug-3.md`. Assert all three files exist and the manifest+frontmatter is consistent across them.
    - **`clast entries write` missing `--session`**: exit 2, stderr mentions `--session`.
    - **`clast entries write` missing `--slug`**: exit 2, stderr mentions `--slug`.
    - **`clast entries write` with both `--body-from` and `--body-stdin`**: exit 2, stderr mentions mutual exclusion.
    - **`clast entries write` with neither body flag**: exit 2.
    - **`clast entries write --session <unknown-uuid> --slug s --body-stdin`** with stdin `x`: exit 1, stderr "not found in manifest".
    - **`clast entries write --session not-a-uuid …`**: exit 2.
    - **`clast entries write --slug BAD_SLUG …`** (uppercase / underscore): exit 2.
    - **`clast entries write --tags 'mysql, BAD_TAG'`**: exit 2.
    - **`clast entries write --session <known-id> --slug s --body-stdin`** with empty stdin: exit 1, stderr "body is empty".
    - **`clast entries write … --json`** on success: stdout is valid JSON object `{"path":"<abs>"}`.
    - **`clast entries write … --json`** on error: stdout is `{"error":"...","code":N}`; non-zero exit.
    - **`clast entries unknown`**: exit 2, stderr mentions unknown subcommand.
    - **`clast entries --help`** and **`clast entries write --help`**: exit 0.

14. **Confirm the `curated` probe in step 07 still works.** Step 07's `sessions.bash` / `show.bash` look for entries via `grep -l "session_id: $id" "$(clast_journal_dir)/entries/"*.md`. That probe must keep matching files written by this step. Verify by adding a tiny cross-suite assertion in `test/test-entries.sh`: after the first successful `clast entries write`, run `bin/clast sessions --day 2026-05-30 --json` and assert that the matching session's `curated` field is `true`. Do NOT modify `sessions.bash` or `show.bash` in this step.

15. **Wire `test/test-entries.sh` into `test/test-clast.sh`.** Append it to the `suites` array after `test/test-query.sh`. New order: lib → decode → dispatcher → whereami → manifest → registry → registry-cmd → snapshot → query → entries.

16. **Update README.md** with a small "Curate an entry" block (or extend the existing read-flow block). One-line example each for `clast entries`, `clast entries read <name>`, and `clast entries write --session UUID --slug NAME --body-stdin`. Do not document the full frontmatter schema in the README — link to `docs/cli-contract.md#entry-frontmatter`.

17. **Confirm `make lint` and `make test` pass.** The new subcommand file needs explicit `# shellcheck source=...` directives for every lib it calls into. `make test` must run all ten suites and exit 0.

## Acceptance criteria

- `lib/clast/clast-subcommands/entries.bash` exists, exports exactly one `clast_cmd_entries` public function, and passes `shellcheck`.
- `bin/clast` routes `entries` to the real subcommand file; the corresponding `_clast_stub` entry is gone. Other stubs (`breadcrumb`, `stats`, `doctor`) are untouched.
- `clast entries` defaults to "all entries"; `--day` / `--since` / `--until` / `--project` / `--tag` (repeatable, intersection) / `--limit` filter as documented; `--day` is mutually exclusive with `--since` / `--until`.
- `clast entries --json` emits the ten-field schema from `cli-contract.md#clast-entries` per row (`path`, `date`, `time`, `day_bucket`, `project`, `session_id`, `session_slug`, `branch`, `tags`, `title`); always prints, even when empty (`[]`).
- `clast entries read <path-or-basename>` exits 0 streaming the file contents; unknown path exits 1; `--json` wraps as `{"path","content"}`.
- `clast entries write` requires `--session` (UUID), `--slug` (lowercase kebab), and exactly one of `--body-from FILE` / `--body-stdin`; rejects missing flags with exit 2 and unknown sessions / empty bodies with exit 1.
- A successful `clast entries write` composes frontmatter in the documented key order (`cli-contract.md#entry-frontmatter`), populates `project_path` / `project_remote` from the registry (null on miss), resolves `branch` best-effort from the snapshot, derives `date` / `time` / `day_bucket` from `clast_today` + `_clast_now_epoch`, takes `author` from `${CLAST_AUTHOR:-$USER}` and `machine` from `${CLAST_MACHINE:-$(hostname)}`, and writes atomically to `entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md`.
- Slug collisions append `-2`, `-3`, … up to `-99`; beyond that, exit 1 with a clear message.
- `--title TITLE` is emitted as the first body line `# Session: <title>` (NOT as a frontmatter field — `cli-contract.md#entry-frontmatter` does not list it).
- `--quiet` suppresses default-mode stdout for `list` and `write`; `--json` ignores `--quiet`; `read` ignores `--quiet`.
- The entries directory may be absent; `clast entries` still exits 0 with an empty result.
- `test/fixtures/multi-project/entries-seed/entries/` exists with at least three Markdown files matching session_ids from step 07's `journal-seed/.manifest.jsonl`.
- `test/test-entries.sh` covers every scenario listed in task 13 and exits 0. The `curated` cross-check in task 14 confirms step 07's probe finds files written by this step.
- `make test` runs ten suites (lib, decode, dispatcher, whereami, manifest, registry, registry-cmd, snapshot, query, entries) and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement `clast breadcrumb`.** Step 09 owns it. The cross-link with breadcrumbs (and the optional `/breadcrumb` skill wrapper) is not part of this step.
- **Do not implement `clast stats` or `clast doctor`.** Step 10 owns both. In particular, do not add a doctor-style pass that checks every entry's `session_id` against the manifest; that's step 10's job.
- **Do not modify `clast-lib.bash`, `clast-decode-lib.bash`, `clast-manifest-lib.bash`, or `clast-registry-lib.bash`.** If a required helper is missing, stop and ask rather than expanding scope. The only allowed touches outside the new subcommand file are `bin/clast` (task 10), `test/helpers.sh` (task 12, additive only), `test/fixtures/multi-project/` (task 11, additive only), `test/test-clast.sh` (task 15), `test/test-entries.sh` (new), and `README.md` (task 16).
- **Do not modify the existing `simple/`, `empty/`, `corrupt-manifest/`, or `multi-project/journal-seed/` fixtures.** Only `multi-project/entries-seed/` is added.
- **Do not modify `sessions.bash` or `show.bash`** even if the `curated` probe could be made cheaper or richer. That refactor (a real frontmatter-indexed lookup) belongs to a v1.1 polish step, not this one.
- **Do not invent richer body-formatting heuristics.** No "auto-detect first H1 as title", no auto-tag inference from the body, no Markdown sanitization. The body is whatever the caller hands `--body-from` or pipes via `--body-stdin`.
- **Do not implement `clast entries delete` / `--edit` / `--reframe`.** `cli-contract.md` does not document them. v1.1 territory.
- **Do not add a global frontmatter parser or YAML dependency.** The awk-extracted `key=value` helper is enough; the writer controls the format.
- **Do not change the dispatcher's global-flag parsing** (the `--journal-dir` / `--projects-dir` / `--json` / `--quiet` / `--verbose` handling). Task 10 is a surgical replace of one case branch.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke against the multi-project seed
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
export CLAST_AUTHOR=beau CLAST_MACHINE=smoke-host
cp -r test/fixtures/multi-project/journal-seed/. "$CLAST_JOURNAL_DIR/"
cp -r test/fixtures/multi-project/entries-seed/. "$CLAST_JOURNAL_DIR/"

# list
bin/clast entries
bin/clast entries --day 2026-05-30
bin/clast entries --project xesapps --json | jq

# read
first_entry=$(bin/clast entries --json | jq -r '.[0].path')
bin/clast entries read "$first_entry" | head -n 20

# write (pick a real session_id from the manifest)
sid=$(jq -r '.session_id' < "$CLAST_JOURNAL_DIR/.manifest.jsonl" | head -n1)
printf 'Smoke body line.\n' | bin/clast entries write \
  --session "$sid" --slug smoke-test --tags smoke,manual --body-stdin
bin/clast entries --tag smoke

# Negative paths
bin/clast entries read no-such.md            ; echo "exit=$?"   # 1
bin/clast entries write --slug s --body-stdin <<<'x' ; echo "exit=$?"  # 2 (missing --session)
bin/clast entries write --session 00000000-0000-0000-0000-000000000000 \
  --slug s --body-stdin <<<'x'                ; echo "exit=$?"  # 1
bin/clast entries --day today --since 2026-05-01 ; echo "exit=$?"      # 2

# Confirm step 07's curated probe sees the new entry
bin/clast sessions --day 2026-05-30 --json | jq '.[] | {session_id, curated}'

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_AUTHOR CLAST_MACHINE
```

## Notes for the implementer

- **The manifest is required input, not output.** `clast entries write` reads the manifest to anchor an entry to a real captured session; it does NOT append to the manifest. Entries live under `entries/`, manifest stays focused on captures.
- **`day_bucket` for the entry is the writer's "today", not the session's bucket.** If a session captured on 2026-05-29 gets curated on 2026-05-30, the entry's `day_bucket` is `2026-05-30`. This is what `/day-wakeup` will rely on when listing yesterday's entries. (The session itself is unambiguously identified by `session_id` in the frontmatter, so no information is lost.)
- **`--title` is body, not frontmatter.** `cli-contract.md#entry-frontmatter` lists thirteen keys; `title` is not among them. Convention is `# Session: <title>` as the first body line, per `cli-contract.md#entry-frontmatter`'s closing paragraph. The frontmatter and the README should stay aligned with that.
- **YAML quoting is narrow.** Most frontmatter values are slug-shaped (`xesapps`, `mysql`, `2026-05-30`) and need no quoting. The only realistic quoting cases in v1 are `project_path` (when it contains `:` on Windows) and `branch` (when it contains `/`). Use double quotes with `\` / `"` / `\n` escapes — do not pull in `yq`.
- **Tags ARE a YAML inline array.** Emit `tags: [a, b, c]` (one space after each comma). This matches `cli-contract.md#entry-frontmatter`'s example and the awk parser in task 4 expects exactly that shape.
- **Atomic write composes in memory.** `clast_atomic_write` from step 02 takes a path and the full content; build the composed string with `printf` / heredoc and hand it in. Do NOT stream piecewise into the target — that defeats the atomic semantics.
- **Collision suffixing is per-call, not per-session.** Two writes for the same session with different slugs do NOT collide. Two writes for the same session with the same slug DO collide and get `-2`. Same slug across different sessions also collides if the resulting basename matches — by design; `/day-wakeup`'s acceptance loop is the consumer here and will surface the suffix to the user.
- **Per-test isolation.** Always go through `setup_test_journal` + the seed helpers; never write to a real `$HOME/.claude/journal/` from a test.
- **Conventional commit suggestion**: `feat(entries): implement clast entries list/read/write`. If the fixture additions in task 11 read separately, a follow-up `test(fixtures): add multi-project entries-seed` is fine; one squashed commit on merge is also fine.
