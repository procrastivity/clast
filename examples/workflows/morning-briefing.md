# Morning Briefing Example

This is a realistic walkthrough of one `/day-wakeup` run. Yesterday, `xesapps`
finished a field-normalization fix, `notes` explored a journal template update,
and `infra-tools` checked a flaky shell lint issue.

```bash
# User: /day-wakeup
clast snapshot
clast sessions --day yesterday --json
```

It filters to `curated: false`, groups by project, and shows:

```text
infra-tools  16:05  lint-shellcheck          17 messages
notes        13:40  main                     31 messages
xesapps      09:12  feature/field-normalize  58 messages
```

## Session 1: infra-tools

The skill reads session details and breadcrumbs:

```bash
clast show 33333333-3333-4333-8333-333333333333 --full --turns 8 --json
clast breadcrumb --read --project infra-tools --day yesterday
```

It drafts a short entry:

```markdown
# Session: Shellcheck cleanup pass

## Goal
Understand why the lint job failed on the helper scripts.

## Open threads
- Decide whether to rewrite the helper or add a targeted shellcheck suppression.
```

AskUserQuestion presents `Accept`, `Accept + promote decision`, `Accept + promote
common-issue`, `Accept + promote workflow`, `Edit`, `Skip`, and `Stop here`.

The user chooses `Skip`. The skill writes nothing; the session remains
uncurated and can be revisited in a later `/day-wakeup`.

## Session 2: notes

The skill reads the next transcript:

```bash
clast show 22222222-2222-4222-8222-222222222222 --full --turns 8 --json
clast breadcrumb --read --project notes --day yesterday
```

The user asks for a revision:

```text
User chooses: Edit
Assistant asks: What should change?
User: Make the title about the entry template, not the markdown cleanup.
```

The regenerated draft is accepted:

```markdown
# Session: Entry template cleanup
## Goal
Tighten the note-entry template before using it for project briefings.
## What shipped
- Drafted a shorter `## Open threads` section.
- Removed duplicate wording from the template notes.
```

The skill writes through stdin:

```bash
clast entries write \
  --session 22222222-2222-4222-8222-222222222222 \
  --slug entry-template-cleanup \
  --tags notes,template \
  --title "Entry template cleanup" \
  --body-stdin
```

## Session 3: xesapps

The skill reads the session and breadcrumbs:

```bash
clast show 11111111-1111-4111-8111-111111111111 --full --turns 8 --json
clast breadcrumb --read --project xesapps --day yesterday
```

The user chooses `Accept + promote decision` for this draft:

```markdown
# Session: Field normalization fix
## Goal
Normalize field metadata before it reaches the consumer-field view.
## What shipped
- Added the canonical field-name path.
- Merged the feature branch after the regression check passed.
## Issues + fixes
- **Issue:** The first pass changed display labels instead of canonical names.
  **Fix:** Moved normalization earlier and kept display labels unchanged.
```

The skill asks for the promoted decision title and content. In v1.0, promoted
items are folded into the entry body rather than written to separate decision,
common-issue, or workflow files.

```bash
clast entries write \
  --session 11111111-1111-4111-8111-111111111111 \
  --slug field-normalization-fix \
  --tags xesapps,fields,regression \
  --title "Field normalization fix" \
  --body-stdin
```

Final summary:

```text
Day wakeup complete.
Curated: 2 sessions across 2 projects.
Skipped: 1 session.
Remaining uncurated: 1.
Promoted: 1 decision (folded into the accepted entry for v1.0).
Run `/wakeup <project>` to start working on a specific project today.
```

What this changes on disk:
`entries/2026-05-31-1340-notes-entry-template-cleanup.md` and `entries/2026-05-31-0912-xesapps-field-normalization-fix.md` were written.
`.manifest.jsonl` was already current from `clast snapshot`; curation does not append to it.
The skipped `infra-tools` session stays uncurated and remains eligible for a later `/day-wakeup`.
