# wake — flow

Curate the working set of captured sessions (plus any stale curated ones)
into durable journal entries. For each session in scope, draft an entry
from its transcript and the previous day's breadcrumbs, then let the
user (or, in auto mode, a deterministic length guard) decide whether it
is written, dismissed, or left for later.

## §1 — Gather the working set

Run `clast plumbing wake --json`. The payload is the working set: every
session in state `captured`, plus every `curated` session that is stale
(its transcript grew since curation), project-grouped then chronological
within each group. A curated-and-fresh or dismissed session never
appears here. If the working set is empty, report that there is nothing
to curate and stop.

## §2 — Read each session's context

For each session in the working set, in the order `plumbing wake`
returned it:

1. Run `clast plumbing show <session> --transcript --max-turn-chars 2000`
   to read the session's transcript, capped so it fits a prompt budget.
2. Run `clast plumbing breadcrumbs --day yesterday --project <slug>` for
   the session's frozen project (its `project.slug`), to read the notes
   the user left the day before. A projectless session (no `project` on
   the working-set row) has no project to scope breadcrumbs to — skip
   this call for it and pass no breadcrumbs into §3.

## §3 — Draft an entry

Resolve the wake-draft prompt pair through `plumbing asset`:
`clast plumbing asset prompts/wake-draft-system.md` and
`clast plumbing asset prompts/wake-draft-user.md`. Fill the user
template's placeholders from the session's facts (project, branch,
start, end, message count), the §2 transcript, and the §2 breadcrumbs,
then call the LLM (verb form) or apply agent judgment directly (skill
form) to produce a draft entry body — title, tags, and markdown body —
in the `entry.md` shape `plumbing curate` accepts.

## §4 — Disposition the draft

**Decision:** the draft is accepted, edited, dismissed, or skipped.

- **Accept** — write the draft as-is (after any promotions from §5)
  through `clast plumbing curate <session>`, piping the complete
  `entry.md` document (frontmatter included) via stdin, or via `--file`.
- **Edit** — take the requested changes as feedback, regenerate the
  draft (§3) incorporating them, and return to this decision.
- **Dismiss** — run `clast plumbing dismiss <session> --reason <text>`
  and move to the next session. Nothing is written.
- **Skip** — write nothing and move to the next session; the session
  stays in whatever state it was in (uncurated, or stale-curated),
  available to curate later.

## §5 — Promote sections

**Decision:** before accepting, optionally promote a decision, a common
issue, or a workflow surfaced by the session into the entry body. For
each promoted item, fold it into the draft as its own section —
`## Decision`, `## Common issue`, or `## Workflow` — followed by the
item's title (`###`) and body, before the entry is written in §4. A
skipped or dismissed draft promotes nothing.

## §6 — Stale sessions are offered, never revoked

A stale session (already curated, transcript grown since) walks through
§2–§5 exactly like a fresh `captured` one. Accepting its draft in §4
calls the same `clast plumbing curate <session>`, which re-curates in
place — this is re-curation, not revocation: a stale session that is
skipped or left alone keeps its existing entry untouched, it does not
lose it.

## §7 — Close the run

Once every session in the working set has been accepted, dismissed, or
skipped, report a summary: sessions curated, dismissed, and skipped,
across however many projects were touched, plus counts of anything
promoted.

*Non-normative (verb form):* the recorded-time header on each draft, the
edit/accept/dismiss/skip retry menu's exact shape, and how draft
generation time is reported are this form's own presentation.

*Non-normative (skill form):* presenting the §4/§5 decisions as an
interactive multi-select question, and the exact wording of the
per-draft preamble, are this form's own presentation.

## Auto mode

Auto mode processes the entire working set from §1 without pausing for
§4/§5 review:

- Skip §5 entirely — promotion requires a human choice, so nothing is
  promoted in auto mode.
- For each drafted entry (§3), before writing: take the draft body
  without its suggested-tags trailer, trim surrounding whitespace, and
  compare its length against the `wake.auto_min_chars` config key
  (shipped default 60). Below that threshold, **skip** the draft —
  do not write it and do not dismiss it, so it stays in the working set
  for a later interactive pass.
- At or above the threshold, accept the draft automatically through
  `clast plumbing curate <session>` (§4's Accept path), with no retry
  loop — a draft that fails to generate is skipped, not retried, since
  there is no reviewer to ask.
- Close with the same §7 summary, plus a count of drafts skipped for
  being under the length threshold.

*Non-normative (verb form):* auto mode is entered with the `--auto` flag
on the verb form's top-level `wake` command; unattended/cron invocation
uses this path.

*Non-normative (skill form):* auto mode is entered only when the user
explicitly asks to curate without review (e.g. "do them all", "don't ask
me") — the per-session decision in §4 stays the default otherwise.
