# retro — flow

Render a day→project retrospective: for each project active on the
target day (or window), its sessions, its curated entries condensed into
short summaries, and its breadcrumbs. This flow writes nothing — it
renders a document, it does not curate.

## §1 — Gather the window

Run `clast plumbing retro --json`, which defaults `<day>` to yesterday;
optionally name a different day: `clast plumbing retro <day> --json`,
where `<day>` is the V5 day grammar (`YYYY-MM-DD`, `today`, `yesterday`,
`-Nd`). `--since <duration>` widens the single day into a window ending
at `<day>` (never `all` — the window is always anchored to a fixed day).
The payload groups, per project (alphabetical by slug, the no-project
bucket first): that project's sessions with state and title, the entry
body for every session that has one, and the project's breadcrumbs in
the window; a `global_breadcrumbs` list sits outside every group for
breadcrumbs naming no project.

## §2 — Summarize each entry

For every session with an entry body — every row in a group's `entries`
list — produce a short summary using the retro-summary prompt pair,
resolved through `plumbing asset`: `clast plumbing asset
prompts/retro-summary-system.md` and `clast plumbing asset
prompts/retro-summary-user.md`. Fill the user template's placeholders
from that entry: the project, the session's work day, the session id,
and the entry body. Call the LLM (verb form) or produce the summary
directly (skill form).

**Decision:** a session with no entry body (an uncurated or dismissed
session in the window's `sessions` list) is listed by state and title in
§3, but produces no summary here — there is nothing curated to condense.

## §3 — Fold summaries into the document

Attach each §2 summary to its session, within its project's group,
alongside that session's listing from the `sessions` list (state, title)
— so a project's section carries both its plain session activity and,
for the curated ones, a condensed summary in place of the full entry
body. Preserve the payload's own ordering: groups alphabetical by
project slug, global breadcrumbs reported separately from every group.

## §4 — Render the document

Render the day (or day range, when `--since` widened it), each project's
section with its sessions, summaries, and breadcrumbs, and the global
breadcrumbs section, as this flow's output.

*Non-normative (verb form):* summaries are served from a
content-fingerprinted cache under `$XDG_CACHE_HOME/clast/retro/`, keyed
so an unchanged entry is not re-summarized on a later run; `--refresh`
bypasses the cache and recomputes. This cache is the verb form's own
private state (SURFACE V7/V11) — it holds no logic the skill form cannot
reproduce, only a shortcut around repeat LLM calls.

*Non-normative (skill form):* every summary is produced fresh, with no
cache — there is no `$XDG_CACHE_HOME` equivalent to check or bypass in
this form.
