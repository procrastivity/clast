---
name: day-wakeup
description: 'Generate curated journal entries from yesterday''s Claude Code sessions across all projects. Use when the user says "/day-wakeup", "day wakeup", "morning briefing", "catch me up on yesterday", "what did I work on yesterday", "review my day", "process yesterday''s sessions", or otherwise signals they want to curate prior work across projects at the start of a new day. Runs `clast snapshot` to ensure fresh data, then walks through each uncurated session from yesterday and proposes a draft entry the user can accept, edit, or skip. Prompts for promotion of decisions, common-issues, and workflows per accepted session. This is the once-per-day curation flow; for per-project briefings use /wakeup; for mid-session pivots use session-brief.'
---

# Day Wakeup

Process yesterday's Claude Code sessions across all projects. For each session, generate a draft journal entry and walk the user through accepting/editing/skipping it.

## Why this exists

Curation at end-of-session has high friction (the user wants to stop, not summarize). Curation at start-of-next-day has lower friction (fresh eyes, easier to decide what's worth keeping). `/day-wakeup` is that start-of-next-day flow.

The transcripts themselves are captured automatically by the SessionStart hook + cron — the user never has to remember to log anything. What `/day-wakeup` does is **curate the captured transcripts into durable entries the user controls**, and prompt for promotion of decisions, common-issues, and workflows along the way.

## Step 1: Ensure fresh data

Run `clast snapshot` (idempotent; silent on no-op). This guarantees we're not missing anything that ran since the last hook fire.

```bash
clast snapshot
```

Errors here are non-fatal — proceed even if it fails, just warn the user.

## Step 2: Enumerate uncurated sessions from yesterday

```bash
clast sessions --day yesterday --json
```

Filter to sessions with `curated: false`. If everything from yesterday is already curated, print "Nothing to curate from yesterday — already done." and stop.

Group sessions by project for presentation. Order: most recent project first, sessions chronological within each project.

## Step 3: For each session, generate a draft

For each session in the list:

1. Read session details:
   ```bash
   clast show <session-id> --full --turns 8 --json
   ```
   This returns metadata + first 8 and last 8 turns of the transcript (text only, no tool calls — kept compact).

2. Read breadcrumbs for this project from yesterday:
   ```bash
   clast breadcrumb --read --project <slug> --day yesterday
   ```

3. Generate a draft entry using the **draft generation prompt** (see below).

4. Display the draft to the user inside a fenced markdown code block, with a brief preamble: "Here's a draft for the X session in <project> at HH:MM:".

5. Present the **promotion question** (see below) via AskUserQuestion.

6. Handle the response:
   - **Accept** (any combination of accept-flavored options): pipe the draft to `clast entries write` via stdin.
   - **Edit**: prompt the user for what to change, regenerate the draft incorporating their feedback, loop.
   - **Skip**: do not write.
   - **Stop here**: end the entire `/day-wakeup` flow, leaving remaining sessions uncurated (user can resume tomorrow).

## Step 4: Final summary

After all sessions are processed (or user stopped early), print a summary:

```
Day wakeup complete.
Curated: 3 sessions across 2 projects.
Skipped: 1 session.
Remaining uncurated: 0.

Promoted:
  Decisions: 1
  Common-issues: 0
  Workflows: 1

Run `/wakeup <project>` to start working on a specific project today.
```

## Draft generation prompt

When generating each draft, use this prompt internally (with the placeholders filled in):

````markdown
You are drafting a journal entry for a Claude Code session that the user just reviewed. The entry will be written to the user's journal and may be read days or weeks later to refresh context on what was happening.

Session metadata:
- Project: {project}
- Branch: {branch}
- Start: {start}
- End: {end}
- Approximate messages: {msg_count}

First 8 turns of the session:
{first_turns}

Last 8 turns of the session:
{last_turns}

Breadcrumbs the user left during this session's day:
{breadcrumbs}

Draft a journal entry in this exact markdown structure. Omit any section that has no content (don't write "N/A"):

```
# Session: <short human-readable title>

## Goal
One sentence describing what this session was trying to accomplish.

## What shipped
- Bullet list of what actually got done (files written, features built, fixes landed). Extract from the transcript.

## Issues + fixes
- **Issue:** what broke. **Fix:** what resolved it.

## Dead ends touched
- **Tried:** approach. (You can usually see this in the transcript as "tried X then switched to Y".)
  - Note: if you can't tell *why* an approach was abandoned from the transcript, leave that for the user to fill in. Don't speculate.

## Open threads
- Anything still unfinished or deferred. Use the breadcrumbs and the last turns of the session as signal.

## Notes
- Anything else useful for the next session in this project.
```

Be concise. Prefer bullets over paragraphs. Use the user's terminology (project-specific names, file paths). Don't invent details. If you're uncertain about something, omit it rather than guess.

Suggest tags after the entry, separated by a blank line, prefixed with "Suggested tags:". The user will confirm.
````

## AskUserQuestion: promotion options per session

After showing the draft, present:

- **question**: "What would you like to do with this draft?"
- **header**: "Session draft"
- **multiSelect**: true
- **options**:
  - `Accept`
  - `Accept + promote decision` — also write a decision file
  - `Accept + promote common-issue` — also write a common-issue file
  - `Accept + promote workflow` — also write a workflow file
  - `Edit` — user wants to revise; will prompt for changes
  - `Skip` — do not write this entry
  - `Stop here` — end /day-wakeup entirely, leave remaining sessions uncurated

If `Skip` and `Stop here` are both selected, treat as `Stop here`. If `Edit` is selected alongside any accept option, treat as `Edit` first (the user wants to revise before accepting).

When a promote option is selected, prompt the user for the title and content of that promoted item before writing.

## Editing handler

If the user selects `Edit`:

1. Ask "What should change?"
2. Take their answer as a feedback note.
3. Regenerate the draft, including their feedback in the prompt as "Revisions requested by user: <feedback>".
4. Show the new draft.
5. Present the same promotion options again. Loop until the user stops editing.

## Writing the entry

When the user accepts:

1. Extract the suggested tags from the draft (the user may have edited them).
2. Pipe the entry body (without the suggested-tags trailer) to `clast entries write`:

```bash
clast entries write \
  --session <session-id> \
  --slug <session-slug> \
  --tags <tag1>,<tag2>,<tag3> \
  --title "<title>" \
  --body-stdin
```

(Sending the markdown body via stdin, ending with EOF.)

3. If the write succeeds, append a one-line confirmation to the running summary.

For promoted items (decisions, common-issues, workflows): currently these are tracked inside the entry's body for v1. **TODO for v1.1: separate `clast decisions write` / `clast common-issues write` / `clast workflows write` subcommands and a directory structure to match.** Note this in the user-facing summary so the user knows they're folded into the entry for now.

<!-- step-12 addition: v1 promotion section convention --> When folding promoted items into the accepted entry body, append `## Decision`, `## Common issue`, or `## Workflow` (h2, singular, capitalized), each followed by the prompted-for title (h3) and body before invoking `clast entries write`.

## Edge cases

- **No sessions from yesterday**: print "No sessions from yesterday." and stop.
- **All sessions already curated**: print "All sessions from yesterday already curated." and stop.
- **`clast snapshot` fails**: warn the user, then attempt to proceed with whatever's already in the manifest.
- **`clast show` fails for a specific session**: skip that session, note it in the final summary, continue with the rest.
- **User says "do them all without prompting"**: not a v1 feature. Each session gets its own AskUserQuestion. The friction is intentional — it's where curation happens.
