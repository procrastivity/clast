# Repair the journal

`clast-plumbing doctor` sanity-checks the journal and reports issues. `clast-plumbing doctor
--fix` applies the safe, non-destructive fixes for you.

## Run a check

```sh
clast-plumbing doctor
```

Sample output:

```
✓ Manifest: 247 entries, all valid
✓ Registry: 8 projects, no duplicates
✗ Orphan snapshots: 3 (see below)
  transcripts/2026-04-15/-old-path/abc.jsonl
  transcripts/2026-04-15/-old-path/def.jsonl
  transcripts/2026-04-15/-old-path/ghi.jsonl
✓ Missing snapshots: none
✓ Day-bucket consistency: ok

Run `clast-plumbing doctor --fix` to clean up orphans.
```

## Exit codes

- `0` — all checks passed.
- `1` — issues found, none fixed.
- `4` — critical corruption (e.g., manifest unparseable). No writes were
  attempted. See "Critical corruption" below.

## What `--fix` will do

```sh
clast-plumbing doctor --fix
```

Only the safe fixes are applied without prompting:

- Rebuild the manifest from `transcripts/` contents if the manifest itself is
  corrupted.
- Remove orphan snapshots (files in `transcripts/` not referenced by the
  manifest). Lists them first; respects `--yes` to skip confirmation.

Destructive operations (removing entries, rewriting frontmatter) always
require explicit user action — they're never auto-applied.

## Critical corruption (exit 4)

If the manifest is unparseable, `clast-plumbing doctor` halts before touching anything
else. To recover:

```sh
# Inspect the offending lines
jq . ~/.claude/journal/.manifest.jsonl >/dev/null   # shows the parse error and line

# If the file is salvageable, edit the bad line out by hand, then re-check
clast-plumbing doctor

# If it's not salvageable, rebuild it from the snapshot contents
clast-plumbing doctor --fix
```

`--fix` regenerates the manifest deterministically from the files actually
present under `transcripts/`. You won't lose snapshots, only the manifest's
record of when/from-where each was captured.

## Renaming / merging a project slug

If several directories of one repo were registered under an auto-adopted slug
(e.g. four clones all became `dev-xesapps`) and you'd rather they shared a
deliberate slug with per-directory labels, use the one-shot migration:

```sh
# Preview — writes nothing, no backups
contrib/migrate-slug.sh --dry-run dev-xesapps xesapps

# Apply (prompts unless --yes); pass --journal-dir for a non-default journal
contrib/migrate-slug.sh dev-xesapps xesapps
```

It rewrites every registry line with `slug: dev-xesapps` to `slug: xesapps`,
backfills `label` from each path's parent directory (`dev`, `performance`, …)
when absent, and clears stale `aliases` roll-ups. It then rewrites matching
curated entries' `project:` and backfills their `label:` from each entry's own
`project_path`. Bodies are never touched; non-matching and malformed registry
lines are preserved verbatim. The run ends by invoking `clast-plumbing doctor`
so you immediately see a clean registry.

Before writing, every file about to change is copied to
`~/.claude/journal/.migrations/<timestamp>-<old>-to-<new>/`. **To roll back**,
restore `projects.json` and the `entries/` files from that directory:

```sh
cp -p ~/.claude/journal/.migrations/<ts>-dev-xesapps-to-xesapps/projects.json \
      ~/.claude/journal/projects.json
cp -p ~/.claude/journal/.migrations/<ts>-dev-xesapps-to-xesapps/entries/*.md \
      ~/.claude/journal/entries/
```

Re-running with the old slug after a successful migration is a safe no-op.

> **Breadcrumbs are not renamed.** Breadcrumb files embed the slug in their
> filename (`breadcrumbs/YYYY-MM-DD-<slug>.md`); this migration leaves them as
> they are. Rename them by hand if you rely on old breadcrumbs under the new
> slug.

## See also

- [`reference/cli.md#clast-doctor`](../reference/cli.md#clast-doctor) — full
  flag and exit-code reference.
