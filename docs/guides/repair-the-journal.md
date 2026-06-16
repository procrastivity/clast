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

## See also

- [`reference/cli.md#clast-doctor`](../reference/cli.md#clast-doctor) — full
  flag and exit-code reference.
