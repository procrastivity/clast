# clast — documentation

The docs are split four ways, modeled on the
[Diátaxis](https://diataxis.fr/) framework:

- **[Explanation](./explanation/)** — what clast is and why it's shaped this way.
- **[Getting started](./getting-started/)** — install and a guided tour.
- **[Guides](./guides/)** — task-oriented how-tos.
- **[Reference](./reference/)** — exhaustive CLI, plugin, and packaging specs.

## New to clast?

1. [What is clast?](./explanation/what-is-clast.md)
2. [Install](./getting-started/install.md)
3. [First snapshot](./getting-started/first-snapshot.md)

## Common tasks

- [Curate an entry by hand](./guides/curate-an-entry.md)
- [Use breadcrumbs](./guides/use-breadcrumbs.md)
- [Automate capture with cron](./guides/automate-with-cron.md) or
  [systemd](./guides/automate-with-systemd.md)
- [Repair the journal](./guides/repair-the-journal.md)
- [Query recipes (`jq` over `--json`)](./guides/query-recipes.md)
- [Run curation without Claude Code](./guides/run-without-claude-code.md) — the `clast wake` / `clast brief` porcelain subcommands
- [Morning briefing walkthrough](../examples/workflows/morning-briefing.md) (in `examples/` because it ships with the npm tarball)

## Reference

- [CLI](./reference/cli.md) — every subcommand, flag, and JSON schema.
- [Plugin](./reference/plugin.md) — `SessionStart` hook, `/day-wakeup`, `/wakeup`.
- [Entry frontmatter](./reference/entry-frontmatter.md) — field-by-field.
- [Config](./reference/config.md) — env vars today, TOML in v1.x.
- [Repo bootstrap](./reference/repo-bootstrap.md) — directory layout, packaging, CI.
- [Releasing](./reference/releasing.md) — tag-driven release runbook.

## Background

- [Architecture](./explanation/architecture.md) — how capture, curation, and surfacing fit together.
- [Data model](./explanation/data-model.md) — sessions, entries, breadcrumbs, snapshots.
- [Conventions](./explanation/conventions.md) — naming, dates, exit codes, file formats.

## Historical / contributor docs

- [`build-steps.md`](./build-steps.md) — meta-doc for the historical
  self-executing step prompts.
- [`steps/`](./steps/) — the step-by-step build prompts that produced v1.
  These reference the pre-restructure doc paths (`docs/overview.md`,
  `docs/cli-contract.md`, etc.) and are preserved as-is for historical
  accuracy.
