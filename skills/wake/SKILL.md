---
name: wake
description: 'Generate curated journal entries from yesterday''s Claude Code sessions across all projects. Use when the user says "/wake", "wake", "morning briefing", "catch me up on yesterday", "what did I work on yesterday", "review my day", "process yesterday''s sessions", or otherwise signals they want to curate prior work across projects at the start of a new day. Runs `clast-plumbing snapshot` to ensure fresh data, then walks through each uncurated session from yesterday and proposes a draft entry the user can accept, edit, or skip. Prompts for promotion of decisions, common-issues, and workflows per accepted session. This is the once-per-day curation flow; for per-project briefings use /brief; for mid-session pivots use session-brief.'
---

# Wake

Process yesterday's Claude Code sessions across all projects. For each session, generate a draft journal entry and walk the user through accepting/editing/skipping it.

## Why this exists

Curation at end-of-session has high friction (the user wants to stop, not summarize). Curation at start-of-next-day has lower friction (fresh eyes, easier to decide what's worth keeping). `/wake` is that start-of-next-day flow.

The transcripts themselves are captured automatically by the SessionStart hook + cron — the user never has to remember to log anything. What `/wake` does is **curate the captured transcripts into durable entries the user controls**, and prompt for promotion of decisions, common-issues, and workflows along the way.

## Step 0: Resolve the clast-plumbing binary

This skill calls the deterministic core (`clast-plumbing`), not the
LLM-aware porcelain (`clast`). Determine the binary to use once at the
start and reuse the result for all commands in this skill:

```bash
if [[ -n "${CLAUDE_PLUGIN_ROOT:-}" && -x "$CLAUDE_PLUGIN_ROOT/bin/clast-plumbing" ]]; then
  CLAST_BIN="$CLAUDE_PLUGIN_ROOT/bin/clast-plumbing"
elif command -v clast-plumbing >/dev/null 2>&1; then
  CLAST_BIN="clast-plumbing"
else
  _pdir="$(find ~/.claude -maxdepth 5 -name plugin.json -path '*/clast/.claude-plugin/*' -print -quit 2>/dev/null)"
  if [[ -n "$_pdir" ]]; then
    CLAST_BIN="$(cd "$(dirname "$_pdir")/../.." && pwd)/bin/clast-plumbing"
  fi
fi
```

If `CLAST_BIN` is still empty, tell the user: "clast-plumbing CLI not found.
Install it with `npm i -g @procrastivity/clast` or see the README for other
options."

Use `$CLAST_BIN` in place of bare `clast-plumbing` for all commands in this skill.

## Step 1: Ensure fresh data

Run `$CLAST_BIN snapshot` (idempotent; silent on no-op). This guarantees we're not missing anything that ran since the last hook fire.

```bash
$CLAST_BIN snapshot
```

Errors here are non-fatal — proceed even if it fails, just warn the user.

## Step 2: Enumerate uncurated sessions

Query all recent sessions (last 30 days) and filter to uncurated:

```bash
$CLAST_BIN --json sessions --since -30d
```

Filter to sessions with `curated: false` or `stale: true` (stale sessions were curated but their transcript was updated since). If none remain, print "Nothing to curate — all sessions are curated or dismissed." and stop.

### Triage when multiple days have uncurated sessions

If uncurated sessions span more than one day (e.g., after a weekend or break), present a triage step before processing. Show a per-day breakdown:

```
Found 45 uncurated sessions across 5 days (2026-05-26 to 2026-05-30).

    2026-05-26  8 session(s)
    2026-05-27  12 session(s)
    2026-05-28  15 session(s)
    2026-05-29  7 session(s)
    2026-05-30  3 session(s)
```

Then present via AskUserQuestion:

- **question**: "How would you like to handle these?"
- **header**: "Backlog"
- **options**:
  - `Process all` — curate everything
  - `Yesterday only` — only process yesterday's sessions
  - `Choose days back` — prompt for a number, process only that many days back
  - `Dismiss older, process recent` — prompt for how many days to keep, dismiss the rest via `$CLAST_BIN sessions dismiss`, then process what remains

If only one day has uncurated sessions, skip triage and process directly.

### Ordering

Group sessions by project for presentation. Order: most recent project first, sessions chronological within each project.

## Step 3: For each session, generate a draft

For each session in the list:

1. Read session details:
   ```bash
   $CLAST_BIN --json show <session-id> --full --turns 8
   ```
   This returns metadata + first 8 and last 8 turns of the transcript (text only, no tool calls — kept compact).

2. Read breadcrumbs for this project from yesterday:
   ```bash
   $CLAST_BIN breadcrumb --read --project <slug> --day yesterday
   ```

3. Generate a draft entry using the **draft generation prompt** (see below).

4. Display the draft to the user inside a fenced markdown code block, with a brief preamble: "Here's a draft for the X session in <project> at HH:MM:".

5. Present the **promotion question** (see below) via AskUserQuestion.

6. Handle the response:
   - **Accept** (any combination of accept-flavored options): pipe the draft to `clast-plumbing entries write` via stdin.
   - **Edit**: prompt the user for what to change, regenerate the draft incorporating their feedback, loop.
   - **Skip**: do not write.
   - **Stop here**: end the entire `/wake` flow, leaving remaining sessions uncurated (user can resume tomorrow).

## Step 4: Final summary

After all sessions are processed (or user stopped early), print a summary:

```
Wake complete.
Curated: 3 sessions across 2 projects.
Skipped: 1 session.
Remaining uncurated: 0.

Promoted:
  Decisions: 1
  Common-issues: 0
  Workflows: 1

Run `/brief <project>` to start working on a specific project today.
```

## Draft generation prompt

The prompt templates are installed alongside the plugin under `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/`:

- **System prompt:** `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/wake-draft-system.md`
- **User prompt template:** `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/wake-draft-user.md` (uses `{{placeholder}}` syntax)

When generating each draft, read those files and substitute the placeholders with session data. The user prompt template uses these placeholders: `{{project}}`, `{{branch}}`, `{{start}}`, `{{end}}`, `{{msg_count}}`, `{{first_turns}}`, `{{last_turns}}`, `{{breadcrumbs}}`.

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
  - `Stop here` — end /wake entirely, leave remaining sessions uncurated

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
2. Pipe the entry body (without the suggested-tags trailer) to `clast-plumbing entries write`:

```bash
$CLAST_BIN entries write \
  --session <session-id> \
  --slug <session-slug> \
  --tags <tag1>,<tag2>,<tag3> \
  --title "<title>" \
  --body-stdin
```

(Sending the markdown body via stdin, ending with EOF.)

3. If the write succeeds, append a one-line confirmation to the running summary.

For promoted items (decisions, common-issues, workflows): currently these are tracked inside the entry's body for v1. **TODO for v1.1: separate `clast-plumbing decisions write` / `clast-plumbing common-issues write` / `clast-plumbing workflows write` subcommands and a directory structure to match.** Note this in the user-facing summary so the user knows they're folded into the entry for now.

<!-- step-12 addition: v1 promotion section convention --> When folding promoted items into the accepted entry body, append `## Decision`, `## Common issue`, or `## Workflow` (h2, singular, capitalized), each followed by the prompted-for title (h3) and body before invoking `clast-plumbing entries write`.

## Edge cases

- **No uncurated sessions**: print "Nothing to curate — all sessions are curated or dismissed." and stop.
- **Multi-day backlog**: present the triage step (Step 2) so the user can choose scope before processing.
- **`clast-plumbing snapshot` fails**: warn the user, then attempt to proceed with whatever's already in the manifest.
- **`clast-plumbing show` fails for a specific session**: skip that session, note it in the final summary, continue with the rest.
- **User says "do them all without prompting"**: not a v1 feature. Each session gets its own AskUserQuestion. The friction is intentional — it's where curation happens.
