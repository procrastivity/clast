---
step: 04
title: manifest-lib
depends_on: [02]
size: small
references:
  - docs/overview.md#filesystem-reference
  - docs/overview.md#conventions
  - docs/overview.md#cross-machine-considerations
  - docs/cli-contract.md#manifest-line
  - docs/cli-contract.md#exit-codes
  - docs/repo-bootstrap.md#libclastclast-manifest-libbash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 04: `clast-manifest-lib.bash`

## Context

Step 02 wrote the two foundational libraries (`clast-lib.bash`, `clast-decode-lib.bash`) and the test harness (`test/helpers.sh`). Step 03 added the `bin/clast` dispatcher and the first subcommand (`whereami`). Tests and lint are green at HEAD; `make test` runs `test-lib.sh`, `test-decode.sh`, `test-dispatcher.sh`, `test-whereami.sh`.

There is no manifest library yet. `whereami` currently reports `last_snapshot` as `null` / `—` with a `# TODO(step-04)` marker — that field stays as a stub until the `snapshot` subcommand lands (step 06). This step writes the library that the snapshot, doctor, and (eventually) `whereami` paths will all share.

The manifest is an append-only JSONL log at `~/.claude/journal/.manifest.jsonl`. One line per capture event. "Most recent line wins" for any given `session_id`. It is the source of truth for "what has been captured" and is the integrity target that `clast doctor` checks (step 10).

**Run `direnv allow` (or `nix develop`) before starting** so `jq` and `shellcheck` from the dev shell are on PATH.

## Goal

Implement `lib/clast/clast-manifest-lib.bash` with the five functions documented in `repo-bootstrap.md#libclastclast-manifest-libbash` — append, lookup, has-capture, iterate, rebuild-from-disk — plus a focused test file driven by a new `corrupt-manifest/` fixture. No subcommand wiring; the lib is consumed by future steps.

## References

Read before starting:

- `docs/overview.md#filesystem-reference` — where the manifest lives (`~/.claude/journal/.manifest.jsonl`) and what else lives alongside it.
- `docs/overview.md#conventions` — JSONL semantics (append-only, lose at most the last partial line), JSON keys are snake_case, ISO 8601 UTC timestamps with `Z`, exit codes (4 = data integrity issue).
- `docs/overview.md#cross-machine-considerations` — "concurrent appends from two machines merge cleanly with `sort -u` on session_id+captured_at." The lib's behavior must be consistent with that.
- `docs/cli-contract.md#manifest-line` — the exact field set: `session_id`, `source`, `snapshot`, `captured_at`, `source_mtime`, `source_size`, `day_bucket`. All required. "Most recent line wins" semantics for `clast_manifest_lookup`.
- `docs/repo-bootstrap.md#libclastclast-manifest-libbash` — the five function signatures this step implements.
- `docs/repo-bootstrap.md#test-strategy` — fixture conventions for tests; `corrupt-manifest/` lives at `test/fixtures/corrupt-manifest/`.

## Tasks

1. **Write `lib/clast/clast-manifest-lib.bash`.** Standard preamble: `# shellcheck shell=bash`, double-source guard (mirror `clast-lib.bash`'s `_CLAST_LIB_SOURCED` pattern with `_CLAST_MANIFEST_LIB_SOURCED`). Assume `clast-lib.bash` has already been sourced — the dispatcher will source it first — so `clast_journal_dir`, `clast_log_*`, `clast_atomic_write`, and `jq` are available.

2. **Implement `clast_manifest_path`** (private-ish helper, but exported because tests use it): returns `"$(clast_journal_dir)/.manifest.jsonl"`. Every other function in this file routes through it so a single env-var override (`CLAST_JOURNAL_DIR`) redirects the manifest to a test tmpdir.

3. **Implement `clast_manifest_append <session-id> <source-path> <snapshot-path> <source-mtime> <source-size> <day-bucket>`.** Per `cli-contract.md#manifest-line`:
   - Build the JSON line with `jq -c -n --arg ... --argjson source_size $size` so numeric fields stay numeric and string fields are properly escaped.
   - `captured_at` is the current UTC time in ISO 8601 with `Z` suffix. Use `date -u +%Y-%m-%dT%H:%M:%SZ`, but honor `CLAST_NOW_EPOCH` (the test-only freeze hook from `clast-lib.bash`) so tests can pin timestamps: `date -u -d "@${CLAST_NOW_EPOCH:-$(date +%s)}" +%Y-%m-%dT%H:%M:%SZ`.
   - Ensure the journal dir exists (`mkdir -p "$(clast_journal_dir)"`) before appending.
   - Append with `>>`. Per `overview.md#cross-machine-considerations`, JSONL appends are crash-safe at the line boundary — a single `>>` write of one line is the right primitive. **Do not use `clast_atomic_write`** here: that helper replaces a whole file, which would clobber concurrent appends.
   - Validate argument count (exit 2 on wrong arity) and reject empty fields with a clear error to stderr.
   - On success print nothing; return 0. Callers that want the appended line can read it back with `clast_manifest_lookup`.

4. **Implement `clast_manifest_lookup <session-id>`.** Print the most recent manifest line matching `session_id`, or return 1 if not found:
   - If the manifest file does not exist, return 1 silently (an empty journal is not an error).
   - "Most recent" = latest line in file order. Manifest is append-only, so file order is time order. Use `tac` (available in coreutils via the dev shell) piped through `jq -c --arg sid "$1" 'select(.session_id == $sid)' | head -n1`, or equivalent. Document the choice with a one-line comment so future-you understands why.
   - Print the matched JSON line verbatim to stdout (the caller pipes it through `jq -r '.snapshot'` or similar).
   - **Skip malformed lines silently** (use `jq -c '. as $x | try (...) catch empty'` or `jq -cR 'fromjson? | select(...)'`). A partial last line from a crashed write should not poison reads. `clast doctor` (step 10) is where corruption gets surfaced; lookup just degrades gracefully.

5. **Implement `clast_manifest_has_capture <session-id> <source-mtime>`.** Exit 0 if a line exists for this `(session_id, source_mtime)` pair; exit 1 otherwise. This is the fast-path predicate `clast snapshot` (step 06) calls per source file to decide whether to skip. Implementation note: this is `clast_manifest_lookup` with an extra `.source_mtime == $mtime` filter — feel free to share a private helper, but the public surface is two separate functions.

6. **Implement `clast_manifest_iterate <jq-filter>`.** Stream every line that matches the supplied jq filter to stdout. `<jq-filter>` is a `select(...)` expression body without the `select()` wrapper — e.g., `'.day_bucket == "2026-05-30"'`. Implementation: `jq -cR 'fromjson? | select('"$1"')' "$(clast_manifest_path)"`. Skip malformed lines (same `fromjson?` trick). If the manifest does not exist, print nothing and return 0.

7. **Implement `clast_manifest_rebuild_from_disk`.** For `clast doctor --fix` (step 10). Walk `$(clast_journal_dir)/transcripts/*/*/*.jsonl`, derive one manifest line per snapshot file from path + `stat`, and write the rebuilt manifest atomically:
   - Compose the new content in a temp file in the same directory (`mktemp -p "$(clast_journal_dir)"` or `.manifest.jsonl.rebuild.$$`).
   - Sort by `captured_at` ascending so the file stays time-ordered; use the snapshot file's mtime as a proxy for `captured_at` when rebuilding from disk (the original `captured_at` is lost when the manifest is gone).
   - Atomic rename onto `.manifest.jsonl` via `clast_atomic_write` or a `mv -f`.
   - `source` and `source_size` cannot be recovered when rebuilding from snapshots alone — populate them as `null` and `0` respectively, and add a one-line code comment explaining why. The schema requires the fields; rebuild produces best-effort lines that round-trip through lookup.
   - Print a single info line via `clast_log_info` summarizing the rebuild (count of lines written).
   - Exit 0 on success; non-zero on write failure.

8. **Create the `corrupt-manifest/` fixture.** Layout:
   - `test/fixtures/corrupt-manifest/.manifest.jsonl` — a hand-written file containing:
     - A valid line.
     - A second valid line for the same `session_id` with a later `captured_at` (proves "most recent wins").
     - A malformed line (truncated JSON: `{"session_id": "broken", "source":`).
     - A non-JSON garbage line (`this is not json`).
     - A third valid line for a different `session_id`.
   - Optionally include a tiny `transcripts/<day>/<segment>/<uuid>.jsonl` tree (one file is enough) so `clast_manifest_rebuild_from_disk` has something to walk. Each file's content can be a one-line stub — the rebuild reads file metadata, not content.

9. **Add a `make_fixture_journal_tree <name>` helper to `test/helpers.sh`** mirroring `make_fixture_projects_tree`, but copying into `$CLAST_JOURNAL_DIR` instead of `$CLAST_PROJECTS_DIR`. Use it from `test-manifest.sh` to populate the journal with the corrupt fixture before each lookup/iterate scenario.

10. **Write `test/test-manifest.sh`** following the shape of `test/test-lib.sh`. Sourcing pattern: `cd` to repo root, `source test/helpers.sh`, `source lib/clast/clast-lib.bash`, `source lib/clast/clast-manifest-lib.bash`. Cover at minimum:
    - **append round-trip**: against a fresh `setup_test_journal`, call `clast_manifest_append` with known fields, then `clast_manifest_lookup` returns the same JSON with the same field values.
    - **append arity check**: calling with too few args returns 2 and writes to stderr.
    - **lookup missing manifest**: returns 1 silently when the file does not exist.
    - **lookup most-recent-wins**: against the `corrupt-manifest/` fixture, looking up the session with two lines returns the one with the later `captured_at`.
    - **lookup skips malformed lines**: against the fixture, lookup of a known-good `session_id` still works despite garbage lines in the file.
    - **has-capture true / false**: returns 0 for an exact `(session_id, source_mtime)` match, 1 otherwise.
    - **iterate filters**: `clast_manifest_iterate '.day_bucket == "<x>"'` against the fixture prints only matching valid lines.
    - **iterate skips malformed lines**: count of matches matches the count of *valid* lines, not the file's line count.
    - **rebuild produces a parseable manifest**: after `clast_manifest_rebuild_from_disk` against a fixture with snapshot files but no `.manifest.jsonl`, the rebuilt file's every line is valid JSON and `clast_manifest_iterate '.'` yields one entry per snapshot file.
    - **rebuild is atomic**: simulate a write failure (e.g., make the journal dir read-only briefly) and confirm the original manifest is untouched. Skip this assertion if it proves portability-flaky on macOS; the atomic-write helper from step 02 has its own coverage.

11. **Wire `test/test-manifest.sh` into `test/test-clast.sh`.** Order: lib → decode → dispatcher → whereami → manifest. The manifest tests don't depend on the dispatcher, but running them last keeps the "build-up" reading order intuitive.

12. **Confirm `make lint` passes.** Add `# shellcheck source=lib/clast/clast-lib.bash` directives where appropriate so the dependency on `clast_journal_dir` / `clast_log_*` resolves cleanly.

## Acceptance criteria

- `lib/clast/clast-manifest-lib.bash` exists, sources cleanly under `set -euo pipefail`, and is idempotent against double-sourcing.
- `clast_manifest_append` writes a single JSON line per call with all seven required fields (`session_id`, `source`, `snapshot`, `captured_at`, `source_mtime`, `source_size`, `day_bucket`); numeric `source_size` survives the round-trip (`jq -r '.source_size | type'` returns `"number"`).
- `clast_manifest_lookup` returns the most recent line for a `session_id` and tolerates malformed lines in the file.
- `clast_manifest_has_capture` exits 0/1 per the documented predicate.
- `clast_manifest_iterate` streams only valid lines matching the supplied filter; non-JSON lines are silently skipped.
- `clast_manifest_rebuild_from_disk` writes a parseable manifest atomically (no half-written file on failure) and is callable against an empty `transcripts/` tree (zero output lines, exit 0).
- `test/fixtures/corrupt-manifest/` exists with a hand-crafted `.manifest.jsonl` containing both valid and malformed lines, plus at least one snapshot file under `transcripts/`.
- `test/test-manifest.sh` covers each scenario listed in task 10 and exits 0.
- `make test` runs all five test files (`lib`, `decode`, `dispatcher`, `whereami`, `manifest`) and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not wire the manifest lib into the dispatcher or any subcommand.** No subcommand is updated by this step. `whereami`'s `last_snapshot` field stays a `# TODO(step-04)` stub until step 06 ships `clast snapshot`. Repurposing the TODO comment to `# TODO(step-06)` is a fine drive-by; actually populating the field is not.
- **Do not implement `clast snapshot`.** Capture is step 06. This step only provides the *lib* it will call.
- **Do not implement `clast doctor` checks or `--fix`.** `clast_manifest_rebuild_from_disk` is the rebuild *primitive*; the `doctor` subcommand that calls it ships in step 10.
- **Do not add concurrent-writer locking.** Append-only JSONL with single-line writes is the documented concurrency model (`overview.md#cross-machine-considerations`). No `flock`, no PID files.
- **Do not invent additional manifest fields** (e.g., `machine`, `captured_by`) beyond the seven listed in `cli-contract.md#manifest-line`. Cross-machine `machine` belongs in entry frontmatter, not the manifest.
- **Do not extend `clast_atomic_write`** from step 02. If a different write primitive is needed for the rebuild path (e.g., `mktemp` + `mv`), inline it or add a private helper; do not modify the public step-02 surface.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke
source lib/clast/clast-lib.bash
source lib/clast/clast-manifest-lib.bash

export CLAST_JOURNAL_DIR="$(mktemp -d)"
clast_manifest_append \
  "00000000-0000-0000-0000-000000000001" \
  "/tmp/source.jsonl" \
  "transcripts/2026-05-30/-tmp/00000000-0000-0000-0000-000000000001.jsonl" \
  "2026-05-30T12:00:00Z" \
  "1234" \
  "2026-05-30"

cat "$CLAST_JOURNAL_DIR/.manifest.jsonl" | jq .
clast_manifest_lookup "00000000-0000-0000-0000-000000000001" | jq .
clast_manifest_has_capture "00000000-0000-0000-0000-000000000001" "2026-05-30T12:00:00Z" && echo "has=yes"
clast_manifest_iterate '.day_bucket == "2026-05-30"'

# Fixture-driven lookup
cp -R test/fixtures/corrupt-manifest/. "$CLAST_JOURNAL_DIR"/
clast_manifest_lookup "<session-id-with-two-lines-in-the-fixture>" | jq -r '.captured_at'
# → the later of the two timestamps

rm -rf "$CLAST_JOURNAL_DIR"
```

## Notes for the implementer

- **`jq` is the only parser.** Don't hand-roll JSON with `printf` — even for the append path. `jq -c -n --arg / --argjson` keeps numeric and string types clean and is the same convention `whereami` uses.
- **`tac` portability.** GNU `tac` is in the nix dev shell (coreutils). If you ever need to support a no-`tac` environment, `awk '{a[NR]=$0} END{for(i=NR;i>0;i--) print a[i]}'` works — but don't preemptively add the fallback. The dev shell is the supported environment per `AGENTS.md`.
- **Malformed-line handling.** `jq -cR 'fromjson?'` returns nothing for unparseable input rather than erroring — that's the trick. Pair it with `select(...)` to get filter-with-skip-on-garbage in one pipe.
- **Most-recent-wins semantics live in the lib, not the callers.** Don't push the "find the latest captured_at" logic to `clast snapshot` or `clast doctor` — they should call `clast_manifest_lookup` once and trust the return.
- **Rebuild's lossy fields.** `source` (the original `~/.claude/projects/...` path) cannot be reconstructed from the snapshot file alone — the snapshot path encodes the *segment*, but the decoder is ambiguous (step 02 covered why). Writing `null` for `source` and `0` for `source_size` is the honest answer; `clast doctor` can flag the resulting "best-effort" lines if it wants stricter integrity.
- **Test isolation.** Always use `setup_test_journal` (now paired with `make_fixture_journal_tree`) so the test tmpdir is per-test. Never write to the real `~/.claude/journal/` from a test. The harness already handles cleanup via `teardown_test_journal`.
- **Conventional commit suggestion**: `feat(manifest): add clast-manifest-lib.bash with append/lookup/iterate/rebuild`. One commit per step keeps `depends_on` history readable.
