---
step: 24
title: migrate existing registry + journal to the shared-slug/label model
depends_on: [22, 23]
size: small
references:
  - docs/overview.md
  - docs/reference/cli.md#registry-line-in-projectsjson
  - docs/reference/entry-frontmatter.md
  - docs/guides/repair-the-journal.md
  - contrib/release
---

# Step 24: migrate an existing journal to the shared-slug + label model

## Context

Steps 22–23 establish the model: one slug may span multiple directories,
each line carries a `label`, the alias roll-up is gone, and entries are
stamped with the correct per-directory `label` and `project_path`. But that
is all forward-looking. A real journal predating these steps has:

- Registry lines for a multi-clone project that were force-stamped with the
  auto-adopted slug (e.g. `dev-xesapps`) instead of the intended slug
  (`xesapps`), each carrying a stale `aliases` roll-up and no `label`.
- Curated entries with `project: dev-xesapps`, no `label`, and (per the
  step-23 bug) possibly the wrong `project_path`.

This step provides a **one-shot, opt-in migration** to bring an existing
journal onto the new model, plus backups and a dry-run. It is a `contrib/`
script, not a core verb — it is a maintenance operation, not part of the
day-to-day CLI surface.

## Goal

Provide `contrib/migrate-slug.sh <old-slug> <new-slug>` that rewrites
registry lines and matching entry frontmatter onto the shared-slug + label
model, safely and reversibly.

## References

- `docs/reference/cli.md#registry-line-in-projectsjson` — target line shape.
- `docs/reference/entry-frontmatter.md` — target frontmatter (incl. `label`).
- `docs/guides/repair-the-journal.md` — where to document the migration.
- `contrib/release`, `contrib/check-version-sync.sh` — house style for
  contrib scripts (`set -euo pipefail`, `--help`, dry-run conventions).

## Tasks

1. **`contrib/migrate-slug.sh`:**
   - Usage: `migrate-slug.sh [--journal-dir DIR] [--dry-run] [--yes]
     <old-slug> <new-slug>`.
   - **Registry pass** (`projects.json`): for every line with
     `slug == <old-slug>`, set `slug = <new-slug>`, set
     `label = slugify(basename(dirname(path)))` if `label` is absent/empty,
     and reset `aliases` to `[]` (drop stale roll-ups). Whole-file rewrite
     via the same atomic-write approach `clast_registry_remove` uses; never
     touch other lines.
   - **Entries pass**: for every entry whose frontmatter `project ==
     <old-slug>`, set `project = <new-slug>`; if `label` is
     absent, backfill `label = slugify(basename(dirname(project_path)))`
     using the entry's own `project_path`. Only rewrite the frontmatter
     block; leave the body untouched.
   - **Backups**: before writing, copy `projects.json` and every entry
     about to change into `<journal>/.migrations/<timestamp>-<old>-to-<new>/`
     (timestamp passed in / derived without `date` if run under the
     workflow constraints; a plain `date +%s` is fine in a contrib script).
     Print the backup location.
   - **Dry-run**: `--dry-run` prints the planned per-file changes (counts +
     a sample diff) and writes nothing, including no backups.
   - **Confirmation**: without `--yes`, summarize (N registry lines, M
     entries, backup path) and prompt before applying.
   - Be idempotent: re-running when nothing matches `<old-slug>` is a no-op
     that exits 0 with a clear message.
2. **Validate after migrate:** the script ends by running
   `clast-plumbing doctor` (if on PATH) and surfacing its exit status, so
   the user immediately sees a clean registry.
3. **Docs:** add a "Renaming / merging a project slug" section to
   `docs/guides/repair-the-journal.md` showing the
   `dev-xesapps → xesapps` example, the backup location, and how to roll
   back (restore from `.migrations/`).
4. **Test:** `test/test-migrate-slug.sh` against a synthetic journal
   fixture: builds a registry with 4 shared-remote lines (old slug, stale
   aliases, no labels) + a few entries, runs the migration, asserts new
   slug + derived labels + empty aliases on registry lines, and `project`
   rewritten + `label` backfilled on entries. Assert `--dry-run` changes
   nothing and that a backup directory was created on a real run.

## Acceptance criteria

- `contrib/migrate-slug.sh --dry-run dev-xesapps xesapps` lists the four
  registry lines and the affected entries and writes nothing.
- A real run rewrites those lines to `slug: xesapps`, `aliases: []`, and
  labels `dev`/`performance`/`review`/`control`; rewrites matching entries'
  `project` to `xesapps` and backfills `label` from each entry's
  `project_path`; and leaves a restorable backup under `.migrations/`.
- `clast-plumbing doctor` reports a clean registry afterward.
- Re-running is a no-op.
- `test/test-migrate-slug.sh`, `make test`, and `make lint` pass.

## Out of scope

- A first-class `registry rename` CLI verb (could come later; the contrib
  script covers the need without growing the porcelain surface).
- Merging entries across *different* remotes, or splitting one slug into
  several — this script only renames a slug and backfills labels.
- Touching breadcrumbs (keyed by slug in their filename). If breadcrumb
  rename is wanted, note it as a follow-up; do not silently rename files
  this script wasn't asked to.

## Verification

```bash
test/test-migrate-slug.sh
shellcheck contrib/migrate-slug.sh

# Dry-run against the real journal (writes nothing)
contrib/migrate-slug.sh --dry-run dev-xesapps xesapps

# Real run, then confirm
contrib/migrate-slug.sh dev-xesapps xesapps
clast-plumbing registry list
clast-plumbing doctor
```

## Notes for the implementer

- Frontmatter rewriting must be surgical: operate only between the first
  two `---` fences, preserve key order and the body verbatim. Reuse the
  `_clast_entries_read_frontmatter` awk pattern as a model; do not reserialize
  via a YAML library (none is a dependency).
- The breadcrumb caveat matters: breadcrumb files embed the slug in their
  filename (`YYYY-MM-DD-<slug>.md`). This migration intentionally does not
  rename them — call that out in the guide so users aren't surprised.
- This depends on step 22 (so `doctor` accepts the post-migration shape)
  and step 23 (so the `label` field is defined). Do not run it before both
  land.
