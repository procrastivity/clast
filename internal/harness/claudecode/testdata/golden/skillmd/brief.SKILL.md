---
name: brief
description: Synthesize a working brief for the current or a named project from recent curated entries, today's breadcrumbs, and today's sessions. Use when the user says brief, asks what they were working on, wants to resume a project, or needs context restored before starting work.
---

# clast brief

Generated from clast v0.0.0-test (schema 1) — never hand-edit; re-run `clast install claude-code` after upgrading.

Reach for brief when the user is about to resume work on a project and
wants prior context restored without re-reading old sessions themselves —
starting a work session, switching back to a repo after time away, or
asking "where was I". It is the read side of the journal: it never writes
anything.

Read `clast plumbing asset flows/brief.md` and follow it exactly as
written. The flow names the one plumbing call this shape needs, how to
recognize an empty result and stop without synthesizing anything, and how
to turn a non-empty payload into a briefing. Do not reconstruct the flow
from memory. A user who wants to act on what the briefing surfaces —
curating a session, jotting a note, catching up on more than today — is
pointed at wake, curate, or breadcrumb instead; brief does not do their
work for them.


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
