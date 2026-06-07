# Install the plugin

The Claude Code plugin adds two skills (`/day-wakeup`, `/wakeup`) and a
`SessionStart` hook that auto-runs `clast snapshot` in the background each
time a session starts.

The plugin is optional — the CLI works fine without it. Install it when you
want LLM-assisted curation and automatic capture. Prefer to stay in a plain
terminal? The [`clast-wake` / `clast-brief`](../guides/run-without-claude-code.md)
scripts give you the same curation and briefing without Claude Code.

## From a local checkout

```sh
claude plugin install <path-to-clast-checkout>
```

## From a global npm install

```sh
npm install -g @procrastivity/clast
claude plugin install $(npm root -g)/@procrastivity/clast
```

## What you get

| Surface | Purpose |
|---|---|
| `SessionStart` hook | Backgrounds `clast snapshot` on every session start. Free auto-capture. |
| `/day-wakeup` | Once-per-day cross-project curation. Walks each uncurated session through a draft you can accept, edit, or skip. |
| `/wakeup [project]` | Per-project briefing synthesized from recent entries + today's breadcrumbs. Read-only. |

See [`reference/plugin.md`](../reference/plugin.md) for the full prompts and
option sets.

## After installation

In a Claude Code session, try `/wakeup` from inside a registered project, or
`/day-wakeup` first thing in the morning.

If `clast` is not on `PATH` yet, the `SessionStart` hook is silent and
best-effort — sessions still start cleanly. Add `clast` to your PATH and the
hook will start capturing automatically on the next session start.
