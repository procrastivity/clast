---
step: 22
title: registry labels and explicit-slug honoring
depends_on: [05, 10]
size: medium
references:
  - docs/overview.md
  - docs/reference/cli.md#clast-plumbing-registry
  - docs/reference/cli.md#registry-line-in-projectsjson
  - docs/reference/cli.md#clast-plumbing-doctor
  - docs/explanation/data-model.md
  - lib/clast/clast-registry-lib.bash
  - lib/clast/clast-subcommands/registry.bash
  - lib/clast/clast-subcommands/doctor.bash
---

# Step 22: registry `label` field + honor explicit `--slug`

> **Numbering note.** `step-21`'s prose forward-references a "step 22"
> for batching registry candidate lookups. That work already landed in
> `_clast_registry_lookup_paths` (see `clast-registry-lib.bash`). This
> step reuses the next free file number; renumber the series if the
> reviewer prefers to keep "22" reserved for the (completed) perf work.

## Context

The registry (`projects.json`, JSONL) maps directory paths to a logical
project `slug`. Today, `clast_registry_add` (`clast-registry-lib.bash`)
has two surprising behaviors discovered while debugging a real journal:

1. **Explicit `--slug` is silently discarded** when the directory's git
   remote matches an already-registered entry. `registry add . --slug
   performance-xesapps` inside a second clone of an already-registered
   repo throws away `performance-xesapps` and stamps the existing
   `dev-xesapps` slug, with no warning. The user only finds out later
   when every clone collapses into one briefing.

2. **Sibling paths are rolled into `aliases`** on every remote match, so
   N clones of one repo produce N lines that all share a slug *and* carry
   overlapping alias path-lists. `clast doctor` then reports this exact
   shape as two issues — `duplicate slug` and `alias collision: <slug> and
   <slug> share alias <path>` — even though `data-model.md` explicitly
   says "Multiple lines may share a `slug` if `path` differs." So `doctor`
   contradicts the documented contract.

The user works in several distinct clones of one repo
(`~/Workspaces/{dev,performance,review,control}/xesapps`) — not git
worktrees — and wants them grouped under one slug (`xesapps`) while still
being **distinguishable per directory**. Their layout encodes the purpose
in the parent directory name, so the parent dir is a natural label.

This step makes the registry model support that, and reconciles `doctor`
with it. It does **not** touch entry writing or the briefing (steps 23
and 24).

## Goal

Add a per-directory `label` to registry lines, stop silently discarding an
explicit `--slug`, stop the harmful alias roll-up, and align `doctor` with
the documented "shared slug across differing paths is legal" contract.

## References

Read before starting:

- `docs/reference/cli.md#clast-plumbing-registry` — current `registry add`
  contract (note step 4 describes the alias-rollup behavior being changed).
- `docs/reference/cli.md#registry-line-in-projectsjson` — on-disk schema.
- `docs/reference/cli.md#clast-plumbing-doctor` — `registry_validity` check.
- `docs/explanation/data-model.md` — the Project/Slug/Registry rows.
- `clast-registry-lib.bash` — `clast_registry_add`, `clast_registry_resolve`,
  `_clast_registry_lookup_path(s)`, `clast_registry_match_remote`.
- `clast-subcommands/registry.bash` — `add`/`list` arg parsing + display.
- `clast-subcommands/doctor.bash` — `registry_validity` duplicate-slug and
  alias-collision logic (~lines 120-200).

## Model

A registry line gains one optional field, `label`:

```json
{
  "path": "/home/bsimensen/Workspaces/performance/xesapps",
  "slug": "xesapps",
  "label": "performance",
  "remote": "git@gitlab.xes-mad.com:xes/it/bizapps/symfony/xesapps.git",
  "first_seen": "2026-06-10",
  "aliases": []
}
```

- `slug` — logical project identity. **May be shared** across lines whose
  `path` differs (worktree / multi-clone). This is now first-class, not an
  accident.
- `label` — per-directory human distinguisher. Resolution never keys off
  it; it exists so downstream consumers (step 23's brief) can segment a
  multi-directory project. Optional: a single-directory project needs none.
- `aliases` — reserved for genuine alternate paths of the *same*
  directory (e.g. WSL vs macOS mount of one checkout). **No longer
  auto-populated** by the remote-match roll-up.

### `registry add` resolution rules (new)

Let `matched` = slug of the first existing line whose `remote` equals this
directory's remote (via `clast_registry_match_remote`).

| `--slug` given? | `matched`        | Result                                                                 |
|-----------------|------------------|------------------------------------------------------------------------|
| yes (`S`)       | none             | use `S`                                                                |
| yes (`S`)       | equals `S`       | use `S` (intentional grouping — silent)                                |
| yes (`S`)       | differs (`M`)    | use `S` (distinct project); **warn**: remote already registered as `M` |
| no              | none             | `slug = basename(path)`                                                |
| no              | some (`M`)       | `slug = M`; **info notice** that the directory was grouped under `M`   |

Both the warn and the info notice go to stderr; neither is silent. Never
overwrite an explicit `--slug`.

### `label` defaulting

- `--label X` → `label = X`.
- else `label = basename(dirname(canonical_path))` (e.g.
  `~/Workspaces/performance/xesapps` → `performance`).

Always derive a label. It is only ever *surfaced* (step 23) when a project
spans more than one directory, so a noise value like `code` for a
single-clone `~/code/clast` is harmless and never displayed.

Validate `--label` like a slug-ish token: `^[a-z0-9][a-z0-9-]{0,31}$` after
lowercasing; reject otherwise with exit 2. Auto-derived labels are
slugified the same way (lowercase, non-`[a-z0-9-]` → `-`, collapse, trim).

## Tasks

1. **`clast-registry-lib.bash` — `clast_registry_add`:**
   - Parse a new `--label VALUE` / `--label=VALUE` flag.
   - Replace the remote-match block with the resolution table above. Keep
     an explicit `--slug` whenever one was passed; only adopt `matched`
     when `--slug` was absent.
   - Emit the warn / info notice via `clast_log_warn` / `clast_log_info`
     (stderr), gated so `--quiet` suppresses the info notice but not the
     warn (match existing logging conventions).
   - Compute and slugify `label` (flag or parent-dir default); validate.
   - Stop building `aliases_json` from the sibling roll-up; write
     `aliases: []` for new lines. (Leave the field present — `doctor` and
     the schema still expect it.)
   - Add `label` to the emitted JSON line (omit the key only if empty,
     consistent with how empty `remote` is dropped).
2. **`clast-subcommands/registry.bash`:**
   - `add`: pass `--label` through (add to the `--slug|--remote` arm).
   - `list`: add a `label` column to both the table and the column header;
     include `label` in the per-row rendering.
   - Update `add`'s non-JSON confirmation to mention the label when set.
3. **`clast-subcommands/doctor.bash` — `registry_validity`:**
   - Remove plain `duplicate slug` from issues. Replace with: flag only
     when the **same `path`** appears on two lines (real duplicate), or
     when one `path` maps to two **different** slugs (genuine conflict).
   - Gate the alias-collision checks on `slug` *difference*: two lines that
     share a slug sharing an alias is benign; only cross-slug alias
     overlap (or an alias equal to a *different* line's slug) is an issue.
   - Keep `has("aliases")` in the line-validity check (still required);
     `label` is optional and must not be required.
4. **Docs:**
   - `cli.md#registry-line-in-projectsjson`: add `label`; note `aliases` is
     no longer auto-rolled.
   - `cli.md#clast-plumbing-registry`: rewrite `registry add` steps to the
     new resolution table; document `--label`; document the warn/notice.
   - `cli.md#clast-plumbing-doctor`: update the registry check description
     (shared slugs across differing paths are valid).
   - `data-model.md`: add a **Label** glossary row; tweak the Registry row.
   - `entry-frontmatter.md`: leave untouched (step 23 adds the entry field).
5. **Tests** (`test/test-registry.sh`, `test/test-registry-cmd.sh`,
   `test/test-doctor.sh`):
   - explicit `--slug` honored even when remote matches; warn emitted.
   - no `--slug` + remote match → adopts matched slug; notice emitted.
   - `--label` honored; absent `--label` → parent-dir default; invalid
     `--label` → exit 2.
   - new lines have `aliases: []` (no roll-up).
   - `list` shows the label column.
   - `doctor`: two paths sharing a slug → **no** issue; same path twice, or
     one path with two slugs → issue.

## Acceptance criteria

- `registry add <dir> --slug foo` where `<dir>`'s remote already maps to
  `bar` writes a line with `slug:foo` and prints a stderr warning naming
  `bar`. Exit 0.
- `registry add <dir>` (no slug) where the remote maps to `bar` writes
  `slug:bar`, prints an info notice, and writes `aliases: []`.
- `registry add ~/x/performance/xesapps` (no `--label`) yields
  `label:performance`; `--label perf` yields `label:perf`; `--label 'Bad
  Label'` exits 2.
- `registry list` includes a `label` column.
- `doctor` reports **no** `registry_validity` issue for a registry with
  four lines sharing slug `xesapps` on four distinct paths with
  `aliases: []`.
- `doctor` still reports an issue when one `path` appears with two
  different slugs.
- `make test` and `make lint` pass.

## Out of scope

- Stamping `label` onto entries or changing entry frontmatter — step 23.
- Segmenting the briefing by label/branch — step 23.
- Migrating the user's existing `dev-xesapps` registry lines or journal
  entries — step 24.
- A `registry rename`/`relabel` verb — step 24 handles migration via a
  contrib script; a first-class verb is out of scope here.
- Walking up to a git root when resolving a subdirectory path (unchanged).

## Verification

```bash
# Unit tests
test/test-registry.sh
test/test-registry-cmd.sh
test/test-doctor.sh

# Lint
shellcheck bin/clast-plumbing \
  lib/clast/clast-registry-lib.bash \
  lib/clast/clast-subcommands/registry.bash \
  lib/clast/clast-subcommands/doctor.bash

# Manual: explicit slug survives a remote match (isolated journal)
J=$(mktemp -d)
CLAST_JOURNAL_DIR=$J bin/clast-plumbing registry add /tmp/a --slug shared --remote git@host:repo.git
CLAST_JOURNAL_DIR=$J bin/clast-plumbing registry add /tmp/b --slug distinct --remote git@host:repo.git  # expect warning
CLAST_JOURNAL_DIR=$J bin/clast-plumbing registry list
CLAST_JOURNAL_DIR=$J bin/clast-plumbing doctor   # expect no duplicate-slug/alias issue
```

## Notes for the implementer

- `clast_registry_match_remote` already exists and returns the first
  slug for a remote — reuse it instead of re-querying with `jq`.
- The registry is append-only JSONL; `add` only ever appends. Do **not**
  attempt to backfill labels onto pre-existing lines here — that retroactive
  rewrite is migration work (step 24). Always-deriving the label on new
  lines is what keeps `add` append-only.
- `data-model.md` already documents shared slugs as legal; the `doctor`
  change is bringing the implementation in line with the existing
  contract, not introducing a new policy. Frame the test/commit message
  that way (`fix(doctor): …`, `feat(registry): …`).
- Watch `set -euo pipefail`: the `matched` lookup can legitimately return
  empty — guard with `|| true` patterns already used in the file.
