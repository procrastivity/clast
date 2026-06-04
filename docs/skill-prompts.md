# `clast` — Skill Prompts

> Reference doc. Read [`overview.md`](./overview.md) first. This doc spec's the Claude Code plugin: the three skills, their `SKILL.md` content, their internal LLM prompt templates, and the `AskUserQuestion` option sets.

Three skills total: `day-wakeup`, `wakeup`, and (optional, deferred to v1.1) `breadcrumb`. Plus a `SessionStart` hook script.

The core principle: skills are **thin LLM layers over the CLI**. Every skill follows the same shape: gather data via `clast` subcommands, do the LLM work (drafting, synthesis, prompting), then write back via `clast` subcommands. Skills never read or write the journal directly.

---

## SKILL.md format reminder

A `SKILL.md` file has YAML frontmatter (name + description) and a body that becomes the in-context instructions to Claude when the skill triggers. The description is what Claude's auto-trigger heuristic matches against — it must include the trigger phrases users will say.

---

## Skill 1: `day-wakeup`

**Location:** `.claude-plugin/skills/day-wakeup/SKILL.md`

**Purpose:** The primary curation point. Snapshot fresh transcripts, iterate yesterday's sessions, generate a draft entry per session, prompt the user to accept/edit/promote, write accepted entries via `clast entries write`.

### `SKILL.md` content

```markdown
---
name: day-wakeup
description: Generate curated journal entries from yesterday's Claude Code sessions across all projects. Use when the user says "/day-wakeup", "day wakeup", "morning briefing", "catch me up on yesterday", "what did I work on yesterday", "review my day", "process yesterday's sessions", or otherwise signals they want to curate prior work across projects at the start of a new day. Runs `clast snapshot` to ensure fresh data, then walks through each uncurated session from yesterday and proposes a draft entry the user can accept, edit, or skip. Prompts for promotion of decisions, common-issues, and workflows per accepted session. This is the once-per-day curation flow; for per-project briefings use /wakeup; for mid-session pivots use session-brief.
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

## Step 2: Enumerate uncurated sessions

```bash
clast sessions --since -30d --json
```

Filter to sessions with `curated: false`. If none remain, print "Nothing to curate — all sessions are curated or dismissed." and stop.

If uncurated sessions span more than one day (e.g., after a weekend or break), present a triage prompt so the user can choose: process all, yesterday only, choose how many days back, or dismiss older sessions and process the rest. If only one day has uncurated sessions, skip triage and process directly.

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

The prompt templates live in `lib/clast/prompts/` so they are shared between the plugin skill and the standalone `clast-wake` script:

- **System prompt:** [`lib/clast/prompts/day-wakeup-draft-system.md`](../lib/clast/prompts/day-wakeup-draft-system.md)
- **User prompt template:** [`lib/clast/prompts/day-wakeup-draft-user.md`](../lib/clast/prompts/day-wakeup-draft-user.md)

The user prompt template uses `{{placeholder}}` syntax: `{{project}}`, `{{branch}}`, `{{start}}`, `{{end}}`, `{{msg_count}}`, `{{first_turns}}`, `{{last_turns}}`, `{{breadcrumbs}}`.

When generating each draft, read those files, substitute the placeholders with session data, and use them as the system and user messages respectively.

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

## Edge cases

- **No sessions from yesterday**: print "No sessions from yesterday." and stop.
- **All sessions already curated**: print "All sessions from yesterday already curated." and stop.
- **`clast snapshot` fails**: warn the user, then attempt to proceed with whatever's already in the manifest.
- **`clast show` fails for a specific session**: skip that session, note it in the final summary, continue with the rest.
- **User says "do them all without prompting"**: not a v1 feature. Each session gets its own AskUserQuestion. The friction is intentional — it's where curation happens.
```

### Draft generation prompt — design notes (not for the SKILL.md itself)

A few intentional choices in the prompt template above worth flagging:

- **"Don't speculate about why an approach was abandoned"** — this is the WHY-of-dead-ends gap I flagged repeatedly. The skill explicitly invites the user to fill it in rather than letting the LLM guess.
- **"Use the user's terminology"** — Beau works in a heavy proper-noun world (`xesapps`, `xcind`, `vw_Consumer_Fields_All`, `Wyvern`, `Xciton`). The LLM should preserve these as-is, not rephrase them.
- **"Suggested tags"** — kept as a trailer, separated from the body, so the body that gets written is clean.

---

## Skill 2: `wakeup`

**Location:** `.claude-plugin/skills/wakeup/SKILL.md`

**Purpose:** Per-project briefing synthesized from recent entries + today's breadcrumbs. Fast, read-only. Used when starting work in a specific repo today.

### `SKILL.md` content

```markdown
---
name: wakeup
description: Synthesize a briefing for the current project (or a named one) so the user can resume work without re-explaining context. Use when the user says "/wakeup", "wakeup", "wake up", "catch me up", "where was I", "what was I working on", "load last session", "resume", or otherwise signals they want prior context for the project they're about to work on. Optionally accepts a project slug like "/wakeup xesapps". Reads recent curated entries and today's breadcrumbs from `~/.claude/journal/` and produces a 2–5k-token briefing. This is the per-project read flow; for cross-project daily curation use /day-wakeup; for mid-session pivots use session-brief.
---

# Wakeup

Synthesize a briefing for the current (or named) project so the user can resume without re-explaining context.

## Why this exists

`/day-wakeup` curates yesterday's work into entries. `/wakeup` reads those entries back when starting work in a specific repo. The two are complementary: one writes, one reads.

## Step 1: Resolve the project

If the user passed a slug as an argument (`/wakeup xesapps`), use it directly. Otherwise resolve from current working directory:

```bash
clast registry resolve "$(pwd)"
```

If `pwd` doesn't resolve and no slug was given: print "Not in a registered project. Run `clast registry add .` first, or invoke as `/wakeup <slug>`." and stop.

## Step 2: Gather data

In parallel:

```bash
# Recent curated entries for this project (newest first)
clast entries --project <slug> --limit 5 --json

# Today's breadcrumbs for this project
clast breadcrumb --read --project <slug> --day today

# Today's session activity (if any — user might have started already)
clast sessions --day today --project <slug> --json
```

For each entry returned, also read the body if it'll fit (file sizes are typically 1–5KB each):

```bash
clast entries read <entry-path>
```

## Step 3: Synthesize the briefing

Using the **synthesis prompt** (see below), produce a briefing of 2–5k tokens. Structure:

```
## Wakeup briefing — <project>

**Active thread:** <one-line from most recent entry's "Open threads" section, or "None">

**Last session:** <date> on branch `<branch>`: <one-line goal>
- Work done: <2-3 bullets condensed from most recent entry>
- Open threads: <bullets, if any>
- Dead ends to avoid: <bullets, if any>

**Recent sessions:** (up to 5)
- <date> [<branch>] <slug>: <one-line goal>

**Today's breadcrumbs:** (if any)
- HH:MM — <text>

**Today's sessions:** (if user has already worked today)
- HH:MM start: <branch>, <msg-count> messages

**Suggested next step:** <derived from active thread + breadcrumbs>
```

End with one of:

- "Resume? Active thread: '<thread>'. Suggested next step: <step>."
- "No active thread. Last session ended cleanly. What are you working on today?"

## Step 4: Don't write anything

Wakeup is read-only. Never invoke `clast entries write` or `clast breadcrumb` from this skill.

## Edge cases

- **No entries for project**: print "No curated entries for `<slug>` yet. Run `/day-wakeup` to process recent sessions, or run `clast sessions --project <slug>` to see what's available." and stop.
- **Slug resolves but no entries and no sessions**: print "Project `<slug>` registered but has no journal activity yet."
- **Today's session count > 5**: summarize ("worked 12 sessions today, most recent 16:22 on branch `loop-guard-ngram`") rather than listing all.
```

### Synthesis prompt — internal

```
You are synthesizing a project briefing for the user. They are about to start work on the `{slug}` project and want a tight summary of where they left off.

Recent curated entries (newest first):
{entries_json_with_bodies}

Today's breadcrumbs for this project:
{breadcrumbs_today}

Today's session activity for this project:
{sessions_today}

Produce a briefing using this structure (omit any section that has no content):

[same structure as in SKILL.md step 3]

Be concise. Use the user's terminology. Don't repeat content across sections. The total briefing should be 2–5k tokens — if you're approaching that, summarize rather than list verbatim.

For the "Suggested next step": prefer the most recent entry's "Open threads" content, then the most recent breadcrumb, then a synthesis of the recent work. If nothing concrete, say "No active thread."
```

---

## Skill 3: `breadcrumb` (optional, v1.1)

**Recommendation:** defer to v1.1. The bare `clast breadcrumb "<text>"` command is already trivial. A skill wrapper adds value mostly when context inference is needed.

If shipped:

### `SKILL.md` content

```markdown
---
name: breadcrumb
description: Leave a quick in-flight note for tomorrow's day-wakeup to surface. Use when the user says "/breadcrumb", "leave a breadcrumb", "note for tomorrow", "remind me", "make a note", or otherwise signals they want to capture a one-line hint without breaking flow. The hint is appended to today's breadcrumb file for the current project. Different from /handoff (which doesn't exist in clast) and from session-brief (which generates a copy-to-clipboard brief for /clear pivots).
---

# Breadcrumb

Append a one-line hint to today's breadcrumb file for the current project.

## Step 1: Resolve project

```bash
clast registry resolve "$(pwd)"
```

If unresolved, ask the user: "Which project should this breadcrumb attach to?" Show registered slugs as options via AskUserQuestion, plus a "Global (no project)" option.

## Step 2: Extract the text

Take everything after the skill trigger as the breadcrumb text. If the user invoked it with no body, ask: "What would you like to note?"

## Step 3: Write

```bash
clast breadcrumb --project <slug> "<text>"
# or for global:
clast breadcrumb --global "<text>"
```

## Step 4: Confirm

Print: "Breadcrumb recorded for `<slug>` at HH:MM."

That's it. Don't summarize, don't ask follow-up questions, don't suggest next steps. This is meant to be lightweight — the user is mid-flow.
```

---

## Hook: `SessionStart`

**Location:** `hooks/snapshot.sh`

**Manifest:** `hooks/hooks.json`

### `hooks.json`

```json
{
  "hooks": [
    {
      "event": "SessionStart",
      "command": "${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh"
    }
  ]
}
```

### `snapshot.sh`

```bash
#!/usr/bin/env bash
# hooks/snapshot.sh
#
# Fired on Claude Code SessionStart. Backgrounds `clast snapshot` so it doesn't
# block session start. Silent if clast isn't installed — the plugin can still
# load cleanly even if the CLI isn't on PATH.
#
# Idempotent. Safe to run repeatedly.

if command -v clast >/dev/null 2>&1; then
  (clast snapshot >/dev/null 2>&1 &)
fi
exit 0
```

Make sure this is `chmod +x` in the repo. The plugin loader respects the executable bit.

---

## Why no `SessionEnd` hook

Claude Code does have a `Stop` event, but adding a snapshot there is redundant:

- The next `SessionStart` will catch anything missed.
- Claude Code's auto-deletion grace period is much longer than the gap between sessions in normal use.
- Adding another hook increases the surface area for breakage during CC updates.

If a future use case emerges (e.g., capturing token usage stats at session end), a `SessionEnd` hook can be added then.

---

## Cron sample

Lives in `examples/cron/crontab.sample`:

```cron
# Every hour at :05, capture any new transcripts.
# Idempotent and silent on no-op, so safe to run frequently.
5 * * * * /usr/local/bin/clast snapshot >/dev/null 2>&1
```

For systemd-timer users: `examples/cron/systemd-timer.sample` should provide a `.service` + `.timer` pair with the same behavior.

---

## Open questions about skills

Pulled forward from the plan doc; resolve when implementing:

1. **Should `/day-wakeup` accept a `--day` argument** (e.g., `/day-wakeup last-week` to process the whole week)? Recommendation: yes, but lower priority. Default stays "yesterday".
2. **Should the draft generation prompt be exposed for user override** (e.g., a config file with their preferred entry template)? Recommendation: defer to v1.1. Iterate on the default first.
3. **What happens if `AskUserQuestion` is interrupted partway through `/day-wakeup`** (user kills Claude)? Recommendation: nothing — accepted entries are already written, skipped ones can be revisited tomorrow.
4. **Should `/wakeup` print to a file** for easy copy-paste, or just to chat? Recommendation: chat only. Add a `clast briefing --project <slug>` CLI command in v1.1 if file output is wanted.
