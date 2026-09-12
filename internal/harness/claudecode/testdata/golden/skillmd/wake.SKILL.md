---
name: wake
description: Curate captured Claude Code sessions into durable journal entries — the daily catch-up flow. Use when the user says wake, asks for a morning briefing, wants to catch up on recent or yesterday's sessions, or asks to curate/review what happened across projects since they last looked.
---

# clast wake

Generated from clast v0.0.0-test (schema 1) — never hand-edit; re-run `clast install claude-code` after upgrading.

Reach for wake when the user wants recent Claude Code sessions turned into
durable journal entries: catching up on yesterday's (or older) work,
closing out a backlog of uncurated sessions, or explicitly asking to
curate. It is the write side of the journal — a session becomes an entry
only by passing through here.

Read `clast plumbing asset flows/wake.md` and follow it exactly as
written. The flow names, in order, every plumbing call this shape needs —
how to find the working set, read a session's context, draft an entry,
and disposition it (accept, edit, dismiss, or skip) — including its auto
mode and how promoted sections fold into an entry. Do not reconstruct the
flow from memory or from what a prior run did: the asset is the one place
it is defined, and following a stale recollection instead of the live
asset is exactly the drift this skill exists to avoid.


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
