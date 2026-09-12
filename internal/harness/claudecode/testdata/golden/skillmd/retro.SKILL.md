---
name: retro
description: "Render a day-by-project retrospective of captured sessions, curated entries, and breadcrumbs. Use when the user says retro, asks for a look back at a day or period, or wants a retrospective across every project active in it."
---

# clast retro

Generated from clast v0.0.0-test (schema 1) — never hand-edit; re-run `clast install claude-code` after upgrading.

Reach for retro when the user wants to look back over a day or a period
across every project that was active in it — a retrospective, a
look-back, or a "what did I do on `<day>`" question — as opposed to
resuming one project (brief) or curating fresh sessions (wake). It
renders a document; it never writes anything back into the journal.

Read `clast plumbing asset flows/retro.md` and follow it exactly as
written. The flow names the one plumbing call this shape needs, defaults
the day to yesterday when none is named, and describes how to summarize
each curated session and fold the result into the rendered document. Do
not reconstruct the flow from memory or invent a different default
window — the asset is the one place this shape is defined.


## Verbs

| Verb | Usage | Purpose |
|---|---|---|
| `clast plumbing capture` |  | capture everything new or grown, silently |
| `clast plumbing whereami` |  | report the registered project/clone/worktree/branch for the cwd |
| `clast plumbing projects` |  | list every project clast knows about |
| `clast plumbing clones` | `[project]` | list registered clones, scoped to one project or every project |
| `clast plumbing sessions` |  | list sessions, filtered and sorted newest-first |
| `clast plumbing show` | `<session>` | show one session's facts, curation, and entry — or its transcript |
| `clast plumbing breadcrumbs` |  | list a day's breadcrumbs, cross-machine |
| `clast plumbing stats` |  | count sessions by state, harness, project, and day |
| `clast plumbing curate` | `<session>` | write a complete entry.md for a session, valid from every state |
| `clast plumbing dismiss` | `<session>` | mark a session deliberately excluded, with a reason |
| `clast plumbing undismiss` | `<session>` | return a dismissed session to captured |
| `clast plumbing asset` | `<path>` | print one asset's resolved content — override -> shipped -> embedded |
| `clast plumbing wake` |  | the wake shape's deterministic working set (V8/V20) |
| `clast plumbing brief` | `[<project>]` | the brief shape's gathered material (V8/V20) |
| `clast plumbing retro` | `[<day>]` | the retro shape's day→project document (V8/V20) |
