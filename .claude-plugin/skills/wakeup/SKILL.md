---
name: wakeup
description: |
  Synthesize a briefing for the current project (or a named one) so the user can resume work without re-explaining context. Use when the user says "/wakeup", "wakeup", "wake up", "catch me up", "where was I", "what was I working on", "load last session", "resume", or otherwise signals they want prior context for the project they're about to work on. Optionally accepts a project slug like "/wakeup xesapps". Reads recent curated entries and today's breadcrumbs from `~/.claude/journal/` and produces a 2–5k-token briefing. This is the per-project read flow; for cross-project daily curation use /day-wakeup; for mid-session pivots use session-brief.
---

# Wakeup

Synthesize a briefing for the current (or named) project so the user can resume without re-explaining context.

## Why this exists

`/day-wakeup` curates yesterday's work into entries. `/wakeup` reads those entries back when starting work in a specific repo. The two are complementary: one writes, one reads.

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

## Step 1: Resolve the project

If the user passed a slug as an argument (`/wakeup xesapps`), use it directly. Otherwise resolve from current working directory:

```bash
$CLAST_BIN registry resolve "$(pwd)"
```

If `pwd` doesn't resolve and no slug was given: print "Not in a registered project. Run `clast-plumbing registry add .` first, or invoke as `/wakeup <slug>`." and stop.

## Step 2: Gather data

In parallel:

```bash
# Recent curated entries for this project (newest first)
$CLAST_BIN --json entries --project <slug> --limit 5

# Today's breadcrumbs for this project
$CLAST_BIN breadcrumb --read --project <slug> --day today

# Today's session activity (if any — user might have started already)
$CLAST_BIN --json sessions --day today --project <slug>
```

For each entry returned, also read the body if it'll fit (file sizes are typically 1–5KB each):

```bash
$CLAST_BIN entries read <entry-path>
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

Wakeup is read-only. Never invoke write-form subcommands (`entries write`, `breadcrumb '<text>'`, `snapshot`) from this skill.

## Edge cases

- **No entries for project**: print "No curated entries for `<slug>` yet. Run `/day-wakeup` to process recent sessions, or run `clast-plumbing sessions --project <slug>` to see what's available." and stop.
- **Slug resolves but no entries and no sessions**: print "Project `<slug>` registered but has no journal activity yet."
- **Today's session count > 5**: summarize ("worked 12 sessions today, most recent 16:22 on branch `loop-guard-ngram`") rather than listing all.

## Synthesis prompt

<!-- step-13 addition: inlined synthesis prompt with explicit structure re-statement -->

```
You are synthesizing a project briefing for the user. They are about to start work on the `{slug}` project and want a tight summary of where they left off.

Recent curated entries (newest first):
{entries_json_with_bodies}

Today's breadcrumbs for this project:
{breadcrumbs_today}

Today's session activity for this project:
{sessions_today}

Produce a briefing using this structure (omit any section that has no content):

- Active thread: one-line from most recent entry's "Open threads", or "None"
- Last session: date, branch, one-line goal, work done (2-3 bullets), open threads, dead ends to avoid
- Recent sessions: up to 5 entries with date, branch, slug, one-line goal
- Today's breadcrumbs: timestamped list if any
- Today's sessions: list if user has already worked today
- Suggested next step: derived from active thread + breadcrumbs

Be concise. Use the user's terminology. Don't repeat content across sections. The total briefing should be 2–5k tokens — if you're approaching that, summarize rather than list verbatim.

For the "Suggested next step": prefer the most recent entry's "Open threads" content, then the most recent breadcrumb, then a synthesis of the recent work. If nothing concrete, say "No active thread."
```
