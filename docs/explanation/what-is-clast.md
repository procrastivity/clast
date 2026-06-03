# What is clast?

`clast` captures, curates, and surfaces Claude Code session history across all
your projects. It ships two artifacts from one repo:

1. **A CLI** (`clast`) that does the deterministic, LLM-free work — capturing
   transcripts, listing projects and sessions, writing curated entries, and
   managing a project registry.
2. **A Claude Code plugin** that ships skills (`/day-wakeup`, `/wakeup`)
   wrapping the CLI with LLM curation, plus a `SessionStart` hook that
   auto-invokes `clast snapshot` so capture happens unattended.

## Why this exists

Claude Code writes session transcripts to `~/.claude/projects/<encoded>/<uuid>.jsonl`
and rotates them away eventually. Without a durable copy, "what was I working on
last Thursday" requires re-reading whatever made it into a chat log, a commit
message, or your memory.

`clast` keeps a sync-friendly journal alongside Claude Code's own store —
append-only manifest, snapshotted transcripts, human-edited entries, lightweight
breadcrumbs — and gives you a CLI and two plugin skills to read it back.

## Two principles drive the whole design

1. **The CLI never calls an LLM.** All summarization happens in skills. If a
   future feature needs LLM work inside the CLI, it gets a separate
   explicitly-named subcommand.
2. **Capture is automatic; curation is deferred.** Snapshots run unattended (via
   `SessionStart` hook and/or cron). Curation runs when you want it (typically
   at start-of-day via `/day-wakeup`).

## What this design explicitly does not address

- **Anthropic's built-in Session Memory** (Pro/Max API; not on Bedrock/Vertex/
  Foundry or self-hosted vLLM). Opaque, automatic, not user-curated. `clast`
  is the user-controlled durable layer; the two coexist without overlap.
- **Web/desktop Claude history.** Different stores, different machines,
  marginal value for the daily-briefing use case.
- **Semantic search across snapshots.** Out of scope for v1. The storage layout
  doesn't preclude it — a future `clast search` would be a natural extension.

## Next steps

- [Architecture](./architecture.md) — how capture, curation, and surfacing fit together.
- [Data model](./data-model.md) — sessions, entries, breadcrumbs, snapshots.
- [Conventions](./conventions.md) — naming, dates, exit codes, file formats.
- [Getting started → first snapshot](../getting-started/first-snapshot.md) — try it now.
