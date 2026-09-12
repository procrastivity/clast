# brief — flow

Synthesize a briefing for one project so the user can resume work
without re-explaining context: recent curated entries, today's
breadcrumbs, and today's sessions, gathered and read back. This flow
writes nothing, in either form.

## §1 — Gather the material

Run `clast plumbing brief --json`, optionally naming a project:
`clast plumbing brief <project> --json`, where `<project>` is a registry
locator (id or slug); passed, it is resolved and used directly. Omitted,
the project defaults from the current working directory — resolving the
cwd's own registered clone is the verb's own behavior, not this flow's:
an unregistered cwd or one outside any git repository surfaces as a
refusal from the verb itself, not a flow decision. The payload carries,
for the resolved project: recent curated entries grouped by workspace
(current workspace hoisted first, capped 3 per group / 8 total), today's
breadcrumbs, today's sessions, and `empty`.

## §2 — Stop on an empty payload

**Decision:** when the payload carries `empty: true`, there is no
material to synthesize from — report the empty-state guidance (no
curated entries, breadcrumbs, or sessions for this project) and STOP.
Make no LLM call, in either form.

## §3 — Synthesize the working brief

Otherwise, resolve the brief prompt pair through `plumbing asset`:
`clast plumbing asset prompts/brief-system.md` and
`clast plumbing asset prompts/brief-user.md`. Fill the user template's
placeholders from the payload: the project slug, the current workspace
label, the grouped entries (each with its title, tags, and body), the
breadcrumbs, and the sessions. Call the LLM (verb form) or synthesize
directly (skill form) to produce the briefing the prompt template
structures.

## §4 — Present the briefing

Present the synthesized briefing as this flow's output. Nothing from
this flow is written back through any plumbing verb — brief is read-only
in both forms; a user who wants to act on what the briefing surfaces
does so through `wake`, `curate`, or `breadcrumb` separately.

*Non-normative (verb form):* the briefing is printed to stdout as the
verb's own output.

*Non-normative (skill form):* the briefing is presented inline in the
conversation, and the agent may follow it with its own suggested next
action.
