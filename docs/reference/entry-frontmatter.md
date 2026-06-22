# Entry frontmatter

Every curated entry is a Markdown file with a YAML frontmatter block written by
`clast-plumbing entries write`. The body that follows is free-form Markdown.

## Frontmatter schema

```yaml
---
date: 2026-05-30
time: 14:30
day_bucket: 2026-05-30
project: xesapps
project_path: /home/beau/code/xesapps
label: dev
project_remote: git@gitlab.xes-inc.com:xes/xesapps.git
branch: feature/canonical-field
author: beau
tags: [mysql, optimization, eav]
session_id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
session_slug: vw-consumer-fields-explain
snapshot_path: transcripts/2026-05-30/-home-beau-code-xesapps/a1b2c3....jsonl
machine: beau-wsl2
curated_source_mtime: 2026-05-30T14:30:30Z
---
```

| Field | Source | Notes |
|---|---|---|
| `date` | session start, local time | `YYYY-MM-DD` |
| `time` | session start, local time | `HH:MM` |
| `day_bucket` | adjusted by `day_cutoff` | may differ from `date` for late-night sessions |
| `project` | resolved via registry from session's segment | the slug |
| `project_path` | registry line for the session's directory | absolute path |
| `label` | registry line for the session's directory | per-directory distinguisher when a slug spans multiple clones/worktrees; `null` when the line has none |
| `project_remote` | registry, may be omitted | `git remote get-url origin` at registration time |
| `branch` | session metadata at start | best effort |
| `author` | `$USER` at write time | |
| `tags` | passed via `--tags` | comma-split into a list |
| `session_id` | required write arg | manifest lookup key |
| `session_slug` | required write arg | short kebab-case identifier |
| `snapshot_path` | manifest | relative to the journal root |
| `machine` | `hostname` at write time | useful when syncing journals across machines |
| `curated_source_mtime` | manifest `source_mtime` at write time | drives stale detection — `clast-plumbing sessions` flags `stale: true` when the transcript is modified after this; legacy entries without it are treated as not stale |

## Body conventions

Conventional structure (none of this is enforced by the CLI):

```markdown
# Session: <title>

## Goal
One sentence describing what this session was trying to accomplish.

## What shipped
- Bullet list of what actually got done.

## Issues + fixes
- **Issue:** what broke. **Fix:** what resolved it.

## Dead ends touched
- **Tried:** approach. (Why abandoned, if known.)

## Open threads
- Anything still unfinished or deferred.

## Notes
- Anything else useful for the next session in this project.
```

Filenames are written as
`entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md`. `clast-plumbing entries write`
refuses to overwrite an existing file with the same name and falls back to a
`-2`, `-3`, … suffix.

See [`reference/cli.md#clast-entries`](./cli.md#clast-entries) for the write
command flags and exit codes.
