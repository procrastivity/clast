---
name: wake
description: 'Generate curated journal entries from yesterday''s Claude Code sessions across all projects. Use when the user says "/wake", "wake", "morning briefing", "catch me up on yesterday", "what did I work on yesterday", "review my day", "process yesterday''s sessions", or otherwise signals they want to curate prior work across projects at the start of a new day. Runs `clast-plumbing snapshot` to ensure fresh data, then walks through each uncurated session from yesterday and proposes a draft entry the user can accept, edit, or skip. Prompts for promotion of decisions, common-issues, and workflows per accepted session. Supports an opt-in auto mode when the user explicitly asks to curate everything without review ("do them all", "don''t ask me") — the equivalent of `clast wake --auto`. This is the once-per-day curation flow; for per-project briefings use /brief; for mid-session pivots use session-brief.'
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

Query all recent sessions and filter to uncurated. The scan window defaults to 14 days and is configurable via `CLAST_WAKE_SINCE`:

```bash
$CLAST_BIN --json sessions --since "${CLAST_WAKE_SINCE:--14d}"
```

Filter to sessions with `curated: false` or `stale: true` (stale sessions were curated but their transcript was updated since). If none remain, print "Nothing to curate — all sessions are curated or dismissed." and stop.

### Step 2a: Auto-dismiss no-op sessions (deterministic, no LLM)

Each session row carries a deterministic `substantive` flag (computed by
`clast-plumbing` from the transcript, cached at snapshot time). A session is
`substantive: false` when **Claude never replied** — an empty session, one that
contains only slash commands like `/clear`, `/model`, `/config`, or one abandoned
before any response. These are worthless to curate. (A session driven by a *custom*
slash command still has assistant replies, so it stays `substantive: true`.)

Before generating any drafts, auto-dismiss every uncurated session with
`substantive == false` (skip this if the user set `CLAST_WAKE_AUTODISMISS_NOOP=0`):

```bash
$CLAST_BIN sessions dismiss <session-id> --reason "auto: no substantive content (empty / slash-command-only)"
```

Do **not** call the LLM for these. Dismissal is reversible via `clast undismiss <id>`.
Remove them from the working set, keep a count, and report it in the final summary
(e.g. "Auto-dismissed 5 no-op session(s)."). If nothing substantive remains after this,
print "Nothing to curate — all remaining sessions were empty or slash-command-only." and stop.

### Triage when multiple days have uncurated sessions

If uncurated sessions span more than one day (e.g., after a weekend or break), present a triage step before processing. (In [Auto mode](#auto-mode) skip triage entirely and process the whole window.) Show a per-day breakdown:

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
  - `Quit` — end `/wake` now; nothing processed

If `Quit` is selected, stop the entire `/wake` flow immediately — do not process, dismiss, or write anything, and skip the rest of Step 2/Step 3/Step 4 entirely. This differs from the per-session `Stop here` option (see below): `Stop here` is chosen after some sessions have already been processed, so the run ends with a partial summary of what was done; `Quit` at triage ends the run before any session in this backlog has been touched, so zero sessions are processed.

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

   Before using these turns to build the draft prompt, truncate any single turn's text that exceeds ~2000 characters: keep the first 2000 characters and note how many characters were cut (e.g. "… [N more chars truncated]"). This mirrors the CLI's per-turn `turn_cap=2000` and applies independently of the 8-turn count limit above — a single oversized turn (a large pasted blob or tool dump) should not bloat the prompt.

2. Read breadcrumbs for this project from yesterday:
   ```bash
   $CLAST_BIN breadcrumb --read --project <slug> --day yesterday
   ```

3. Generate a draft entry using the **draft generation prompt** (see below).

4. Display the draft to the user inside a fenced markdown code block, with a brief preamble: "Here's a draft for the X session in `<project>` (id: `<session-id>`, recorded: `<date>` `<start>`–`<end>` `<tz>`):".

5. Present the **promotion question** (see below) via AskUserQuestion. (In [Auto mode](#auto-mode) skip this and accept the draft, subject to the length guard.)

6. Handle the response:
   - **Accept** (any combination of accept-flavored options): pipe the draft to `clast-plumbing entries write` via stdin.
   - **Edit**: prompt the user for what to change, regenerate the draft incorporating their feedback, loop.
   - **Skip**: do not write.
   - **Dismiss**: pipe to `$CLAST_BIN sessions dismiss <session-id> --reason "dismissed via wake"`; do not write an entry.
   - **Stop here**: end the entire `/wake` flow, leaving remaining sessions uncurated (user can resume tomorrow).

## Step 4: Final summary

After all sessions are processed (or user stopped early), print a summary:

```
Wake complete.
Curated: 3 sessions across 2 projects.
Auto-dismissed (no-op): 5 sessions.
Dismissed: 1 session.
Skipped: 1 session.
Remaining uncurated: 0.

(In Auto mode, also report: "Skipped (below length threshold): N sessions.")

Promoted:
  Decisions: 1
  Common-issues: 0
  Workflows: 1

Run `/brief <project>` to start working on a specific project today.
```

(Include the `Dismissed:` line only when at least one session was dismissed this run, mirroring the CLI's summary output.)

## Auto mode

By default every session gets its own AskUserQuestion — the friction is the point, it's where
curation happens. But if the user **explicitly** asks to curate everything without reviewing it
("do them all", "don't ask me", "no prompts", "just write them all"), switch to auto mode.

This mirrors `clast wake --auto` in the CLI porcelain, and behaves the same way:

- **Skip the triage step** (Step 2) even when the backlog spans multiple days. The whole scan
  window is processed.
- **Skip the per-session promotion question** (Step 3, item 5). Accept and write every draft via
  the normal `entries write` path in "Writing the entry" below. Nothing is promoted — promotion
  requires a human choice.
- **Length guard.** Before writing, take the draft body *without* the suggested-tags trailer and
  trim surrounding whitespace. If it is shorter than `CLAST_WAKE_AUTO_MIN_CHARS` characters
  (default `60`; `0` disables the guard), **skip** it — do not write it, and do **not** dismiss
  it. It stays uncurated so it can be handled in a later interactive pass. Report it as
  `Draft below threshold (N < M chars) — skipping (stays uncurated).`
- **A draft that fails to generate is skipped**, not retried — there's no reviewer to ask.
- Step 2a no-op auto-dismissal still runs first, unchanged.
- Don't print each draft in full; the write confirmation is the record of what landed.

Everything else is unchanged. Announce the mode up front ("Auto mode: drafts will be accepted
without review.") and report the counts in the Step 4 summary, including how many were skipped
for being under the length threshold.

For unattended/cron curation outside a Claude Code session, the CLI equivalent is
`clast wake --auto`.

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
  - `Skip` — do not write this entry (leaves it uncurated; can be revisited later)
  - `Dismiss` — mark this session dismissed via `clast-plumbing sessions dismiss`; it won't be offered again
  - `Stop here` — end /wake entirely, leave remaining sessions uncurated

If `Skip` and `Stop here` are both selected, treat as `Stop here`. If `Edit` is selected alongside any accept option, treat as `Edit` first (the user wants to revise before accepting). If `Dismiss` is selected alongside any other option, treat as `Dismiss` only — dismissal is a permanent, terminal action for this session, so it overrides Accept/Edit/Skip/Stop here selected in the same response.

When a promote option is selected, prompt the user for the title and content of that promoted item before writing.

**Note:** the promote-to-decision/common-issue/workflow options above are a deliberate skill-only
capability, not CLI lag — the CLI's interactive menu (`_clast_wake_prompt_choice` in `wake.bash`)
never had promote options at all, because the skill *is* the LLM turn and can synthesize a
decision/common-issue/workflow body inline, something a keystroke-driven CLI menu has no analog
for. Because this isn't a CLI flag or `CLAST_*` env var, step-07's BDS-89 parity guard needs to
either add a "skill-only" allowlist category or consciously scope itself to CLI flags/env vars
only — it doesn't fit the existing `cli-only` category the way `undismiss` does.

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
- **All uncurated sessions are no-ops**: after Step 2a auto-dismisses them, nothing substantive remains — print "Nothing to curate — all remaining sessions were empty or slash-command-only." and stop.
- **Multi-day backlog**: present the triage step (Step 2) so the user can choose scope before processing.
- **`clast-plumbing snapshot` fails**: warn the user, then attempt to proceed with whatever's already in the manifest.
- **`clast-plumbing show` fails for a specific session**: skip that session, note it in the final summary, continue with the rest.
- **User says "do them all without prompting"**: switch to [Auto mode](#auto-mode) — accept and write every draft, skipping triage and the per-session question, with the `CLAST_WAKE_AUTO_MIN_CHARS` length guard applied. This is opt-in only: the interactive per-session AskUserQuestion stays the default, because the friction is where curation happens.
