# Use breadcrumbs

Breadcrumbs are append-only one-line notes for `/wakeup` and `/day-wakeup` to
surface later. Use them when you want to capture an in-flight thought without
breaking flow.

## Write

```sh
clast breadcrumb --project xesapps 'check migration before deploy'
clast breadcrumb --global 'remember to bump the cache version'
```

If you omit both `--project` and `--global`, clast tries to resolve the project
from `pwd` via the registry. If that fails it asks you which to use.

## Read

```sh
clast breadcrumb --read --project xesapps          # today's, for one project
clast breadcrumb --read --global                   # today's, global
clast breadcrumb --read --project xesapps --day yesterday
clast breadcrumb --list                            # all breadcrumb files for today
```

## Project vs global

| Kind | When to use |
|---|---|
| `--project SLUG` (or auto-resolved) | The note is about a specific repo. `/wakeup <slug>` will surface it the next time you start working there. |
| `--global` | The note is cross-cutting — a reminder, a meta-task, a "next time I sit down at the computer" thought. Surfaced by `/day-wakeup`. |

## On disk

Breadcrumbs live in `~/.claude/journal/breadcrumbs/YYYY-MM-DD-<slug>.md`
(or `YYYY-MM-DD-_global.md`) as Markdown files with YAML frontmatter and an
append-only bullet list:

```markdown
---
date: 2026-05-30
project: xesapps
---

- 14:23 — check the migration before next deploy
- 16:07 — figure out why the EXPLAIN plan differs in CI
```

Edit them by hand whenever you want — clast only ever appends.

## See also

- [`reference/cli.md#clast-breadcrumb`](../reference/cli.md#clast-breadcrumb)
  — full command reference.
