---
step: 21
title: manifest-metadata-cache
depends_on: [04, 06, 07, 10]
size: medium
references:
  - docs/reference/cli.md#manifest-line
  - docs/reference/cli.md#clast-plumbing-snapshot
  - docs/reference/cli.md#clast-plumbing-sessions
  - docs/reference/cli.md#clast-plumbing-projects
  - docs/reference/cli.md#clast-plumbing-show
  - docs/reference/cli.md#clast-plumbing-stats
---

# Step 21: cache per-session metadata in the manifest

## Context

`clast sessions`, `projects`, `stats`, and `show` each re-derive a
session's message count and start/end timestamps by reading the snapshot
transcript file at query time: `wc -l` for the line count and
`head -n1` / `tail -n1` piped to `jq` for the first/last `.timestamp`.

A prior performance pass (PR #29, `perf(sessions)`) removed the
per-manifest-line and per-session `grep` forks from `clast sessions`,
taking a 30-day window from ~7 min to ~1.5 min on a large real journal.
The query still opens and reads every referenced transcript file to
recompute the same three values — pure recomputation, since the values
never change for a given capture.

> **Correction (post-profiling).** This step was originally scoped on the
> assumption that those transcript reads were the dominant remaining cost.
> A syscall trace of the ~1.5-min run disproved that: ~98% of `exec`s are
> `clast_registry_resolve` forking one `jq` per decoded candidate path for
> unregistered/deep segments (~16.6k `jq` total), not the file reads. The
> real headline win lives in **step 22** (batch registry candidate
> lookups). This step is still worth doing — it removes O(bytes) work from
> four read paths, is a clean correctness/design improvement, and lets the
> reads stay cheap once step 22 makes resolution cheap — but it is a
> *prerequisite/cleanup*, not the fix for the slow `clast wake` startup.

The snapshot writer already reads the transcript at capture time: it
reads the first line for `first_ts` (to compute `day_bucket`) and copies
the whole file. So the three values can be computed **once, at write
time**, and stored in the manifest line. Every reader then reads them
from the manifest (O(manifest)) instead of from disk (O(bytes)).

The manifest is append-only with "most recent line wins" per
`session_id`, and re-captures are deduped on `(session_id, source_mtime)`
(`snapshot.bash` `clast_manifest_has_capture`). A session that grows gets
a fresh line on the next snapshot, so cached values stay correct per
capture with no invalidation logic.

## Goal

Compute and persist `msg_count`, `first_ts`, and `last_ts` in each
manifest line at snapshot time, and have `sessions`/`projects`/`stats`/
`show` consume the cached values, falling back to reading the transcript
only for legacy lines that predate the cache.

## References

- `docs/reference/cli.md#manifest-line` — canonical manifest line schema
  (currently 7 required fields). This step adds three fields.
- `lib/clast/clast-manifest-lib.bash:30` — `clast_manifest_append`, the
  single writer of manifest lines (6-arg signature + jq builder at `:57`).
- `lib/clast/clast-manifest-lib.bash:152` — `clast_manifest_rebuild_from_disk`,
  doctor `--fix`'s reconstruction path; emits `source:null, source_size:0`
  for fields it cannot recover from the snapshot copy.
- `lib/clast/clast-subcommands/snapshot.bash:152-216` — the capture loop:
  already computes `first_ts` at `:155`, `source_size` at `:153`, calls
  `clast_manifest_append` at `:202`.
- `lib/clast/clast-subcommands/sessions.bash:271-285` — consumer (the
  `wc -l` / `head|jq` / `tail|jq` block + `start_ts`/`end_ts` fallback).
- `lib/clast/clast-subcommands/show.bash:98-101` — consumer (same three
  reads). Note `:110-111` also extract `first_prompt`/`last_prompt` text;
  those stay (show reads the full transcript regardless).
- `lib/clast/clast-subcommands/stats.bash:249` — consumer (`wc -l` only).
- `lib/clast/clast-subcommands/projects.bash:150` — consumer (`wc -l` only).
- `lib/clast/clast-subcommands/doctor.bash:84-92` — manifest schema check;
  uses `has(...)` on the 7 existing fields and ignores extras (so new
  optional fields do not break validation).
- `test/test-manifest.sh:30-34,60-61,128-129` — schema/value assertions to
  extend.

## Tasks

1. **Extend the manifest schema doc.** In `docs/reference/cli.md` under
   "### Manifest line", add `msg_count` (integer), `first_ts` (ISO 8601,
   nullable), and `last_ts` (ISO 8601, nullable) to the example and field
   list. Document them as **optional** (lines written before this step
   omit them) and describe the reader fallback. Adjust the "All fields
   required" sentence to distinguish the 7 always-present fields from the
   3 cache fields.

2. **Decide and document the `msg_count` definition.** `wc -l` counts
   newlines; `awk 'END{print NR}'` counts records (differs by one when the
   last line lacks a trailing newline). Pick **one** definition and use it
   identically in the writer and in every reader's fallback so cached and
   uncached results are equal. Record the choice in a code comment at the
   writer. (The field is `*_approx` in output, so exactness is not
   contractual, but writer/reader consistency is required for tests.)

3. **Compute the three values at snapshot time.** In `snapshot.bash`,
   replace the standalone `head -n1 ... first_ts` read (`:155`) with a
   single pass that yields line count + first line + last line (e.g. one
   `awk`), then extract `first_ts`/`last_ts` from those two lines with a
   single `jq`. Preserve the existing `day_bucket` computation from
   `first_ts`. Reuse the `dest` copy if cheaper than re-reading `source`.
   Keep the malformed-first-line fallback to `mtime`-based `day_bucket`.

4. **Extend `clast_manifest_append`.** Grow the signature from 6 to 9 args
   (append `msg_count`, `first_ts`, `last_ts`). Emit them in the jq line
   builder (`:57`). `msg_count` is `--argjson` (integer, default 0);
   `first_ts`/`last_ts` are `--arg` and serialized as JSON `null` when
   empty (mirror the existing `branch`/`remote` null-coalescing idiom).
   Validate `msg_count` is a non-negative integer, like `source_size`.
   Update the arg-count guard and its error message.

5. **Update the one other caller of the append builder.**
   `clast_manifest_rebuild_from_disk` (`:152`) reconstructs lines from the
   snapshot copy, which still contains the transcript lines — so it **can**
   populate all three fields accurately (unlike `source_size`, which it
   sets to 0). Compute them there too so `doctor --fix` backfills the
   cache rather than emitting legacy-shaped lines.

6. **Switch the four readers to prefer the cache.** In each of
   `sessions.bash`, `show.bash`, `stats.bash`, `projects.bash`: read
   `msg_count` / `first_ts` / `last_ts` from the manifest line (already in
   scope as the parsed line / TSV), and **only** fall back to the
   `wc -l` / `head|jq` / `tail|jq` reads when the cached field is absent or
   null (legacy line, or snapshot file present but field missing). The
   existing `start_ts="${first_ts:-$mtime}"` / `end_ts="${last_ts:-$mtime}"`
   fallbacks stay as the final default.

7. **Extend tests.**
   - `test/test-manifest.sh`: assert the three new fields are present,
     correctly typed (`msg_count` numeric, `first_ts`/`last_ts` string or
     null), and that "most recent line wins" returns the newer line's
     cached values. Assert `rebuild_from_disk` populates them.
   - Add a **legacy-line fallback** case: hand-write a manifest line
     without the cache fields, point a snapshot file at it, and assert
     `clast --json sessions` still returns the correct `msg_count_approx` /
     `start` / `end` (proving the file-read fallback path).
   - `test/test-query.sh` and `test/test-entries.sh` must stay green
     unchanged: the cached path must produce **byte-identical** output to
     the file-read path for the existing fixtures (which are freshly
     snapshotted, so they exercise the cached path).

8. **Confirm gates.** `make lint`, `make test`, and any version/sync
   checks exit 0. `shellcheck -x` passes on all modified `.bash` files.

## Acceptance criteria

- New snapshots write `msg_count`, `first_ts`, `last_ts` into each
  manifest line; `doctor --fix` rebuild populates them too.
- `clast --json sessions`, `projects`, `stats`, and `show` return the same
  `msg_count_approx` / `start` / `end` / counts as before for a
  freshly-snapshotted journal (verified by diff against pre-change output).
- A manifest line lacking the cache fields still yields correct output via
  the file-read fallback (covered by a test).
- For a journal whose sessions all carry cached metadata, none of the four
  readers opens a transcript file to compute counts/timestamps — verifiable
  by `strace`/`ltrace` count, or by removing a snapshot **file** (leaving
  its manifest line) and confirming `sessions`/`stats` still report the
  cached count rather than 0.
- `docs/reference/cli.md#manifest-line` documents the three fields as
  optional with the reader-fallback behavior.
- `make lint`, `make test`, `shellcheck -x` exit 0.

## Out of scope

- **`show`'s `first_prompt`/`last_prompt` extraction** (`show.bash:110-111`).
  Those parse user-message text, not metadata, and `show` reads the full
  transcript anyway. Leave them reading the file.
- **A manifest schema version marker.** Use field-presence detection for
  the fallback, not an explicit `v`/`schema` field, unless a concrete need
  emerges.
- **Backfilling existing manifests outside `doctor --fix`.** No standalone
  migration command; old lines upgrade naturally on the next re-capture or
  via `doctor --fix`.
- **Removing the file-read fallback.** It must remain for legacy lines and
  for snapshots written by older `clast` versions on a shared journal.
- **Re-deriving `day_bucket` semantics or the dedup key.** Unchanged.

## Verification

```bash
# Gates
make lint
make test
shellcheck -x lib/clast/clast-manifest-lib.bash \
  lib/clast/clast-subcommands/snapshot.bash \
  lib/clast/clast-subcommands/sessions.bash \
  lib/clast/clast-subcommands/show.bash \
  lib/clast/clast-subcommands/stats.bash \
  lib/clast/clast-subcommands/projects.bash

# New snapshots carry the cache fields
JD=$(mktemp -d)
CLAST_PROJECTS_DIR=test/fixtures/simple CLAST_JOURNAL_DIR=$JD bin/clast snapshot
jq -e 'has("msg_count") and has("first_ts") and has("last_ts")' \
  "$JD/.manifest.jsonl" >/dev/null && echo "cache fields present"

# Cache is used, not the file: drop a snapshot FILE but keep its manifest
# line; sessions must still report the cached msg_count (not 0).
sid=$(jq -r '.session_id' < <(head -n1 "$JD/.manifest.jsonl"))
snap=$(jq -r '.snapshot' < <(head -n1 "$JD/.manifest.jsonl"))
rm -f "$JD/$snap"
CLAST_JOURNAL_DIR=$JD bin/clast --json sessions --since -3650d \
  | jq -e --arg s "$sid" '.[] | select(.session_id==$s) | .msg_count_approx > 0' \
  && echo "served from cache, not file"

# Output parity: freshly snapshotted journal must match pre-change output
# (run on the branch point vs HEAD; expect identical as a set).
```

## Notes for the implementer

- **Why caching is correct without invalidation.** Manifest lines are
  immutable and append-only; a capture's `(session_id, source_mtime)` is
  its identity. When a session grows, mtime bumps and snapshot writes a
  *new* line with fresh cached values. "Most recent line wins" then serves
  the latest. There is no stale-cache window.
- **Writer cost is amortized.** The extra `awk`+`jq` runs only on
  newly-captured/changed sessions (deduped by mtime), in a batch
  (hook/cron) context, on a file the writer already copies. It is not on
  any interactive read path.
- **`msg_count` definition (task 2) is the subtle one.** If the writer and
  the reader fallback disagree by one (newline vs record count), a journal
  with mixed cached/legacy lines will produce inconsistent counts and the
  parity test will flip. Pick one and assert it both ways.
- **Null handling.** A transcript whose first/last line lacks `.timestamp`
  must store JSON `null`, and readers must treat `null` the same as
  "absent" → fall back to `mtime`. Don't store the literal string
  `"null"`.
- **Don't regress PR #29.** `sessions.bash` now parses the manifest in a
  single `jq` reduce emitting TSV; thread the three new fields through that
  TSV rather than re-parsing the line per session.
- **Conventional commit suggestion**: `perf(snapshot): cache msg_count and
  timestamps in the manifest`.
