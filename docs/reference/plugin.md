# `clast` — Skill Prompts

> Reference doc. Read [What is clast?](../explanation/what-is-clast.md) first. This doc spec's the Claude Code plugin: the two skills, their `SKILL.md` content, their internal LLM prompt templates, and the `AskUserQuestion` option sets.

Two skills total: `wake` and `brief`. Plus a `SessionStart` hook script.

The core principle: skills are **thin LLM layers over the CLI**. Every skill follows the same shape: gather data via `clast-plumbing` subcommands, do the LLM work (drafting, synthesis, prompting), then write back via `clast-plumbing` subcommands. Skills never read or write the journal directly.

---

## SKILL.md format reminder

A `SKILL.md` file has YAML frontmatter (name + description) and a body that becomes the in-context instructions to Claude when the skill triggers. The description is what Claude's auto-trigger heuristic matches against — it must include the trigger phrases users will say.

---

## Skill 1: `wake`

**Location:** `skills/wake/SKILL.md`

**Purpose:** The primary curation point. Snapshot fresh transcripts, iterate yesterday's sessions, generate a draft entry per session, prompt the user to accept/edit/promote, write accepted entries via `clast-plumbing entries write`.

`wake` is the primary curation point: it snapshots fresh transcripts, then walks through yesterday's uncurated sessions one at a time, generating a draft journal entry for each. The user accepts, edits, or skips each draft via `AskUserQuestion`, optionally promoting decisions, common-issues, or workflows along the way, and accepted entries are written via `clast-plumbing entries write`.

Full step-by-step implementation: [skills/wake/SKILL.md](../../skills/wake/SKILL.md).

### Draft generation prompt — design notes (not for the SKILL.md itself)

A few intentional choices in the wake draft-generation prompt worth flagging:

- **"Don't speculate about why an approach was abandoned"** — this is the WHY-of-dead-ends gap I flagged repeatedly. The skill explicitly invites the user to fill it in rather than letting the LLM guess.
- **"Use the user's terminology"** — Beau works in a heavy proper-noun world (`xesapps`, `xcind`, `vw_Consumer_Fields_All`, `Wyvern`, `Xciton`). The LLM should preserve these as-is, not rephrase them.
- **"Suggested tags"** — kept as a trailer, separated from the body, so the body that gets written is clean.

---

## Skill 2: `brief`

**Location:** `skills/brief/SKILL.md`

**Purpose:** Per-project briefing synthesized from recent entries + today's breadcrumbs. Fast, read-only. Used when starting work in a specific repo today.

`brief` produces a per-project briefing synthesized from recent curated entries plus today's breadcrumbs. It's fast and read-only — used when starting work in a specific repo today, resolving the project from the current working directory (or an optional slug argument) and never writing anything back.

Full step-by-step implementation: [skills/brief/SKILL.md](../../skills/brief/SKILL.md).

### Synthesis prompt — internal

The shared templates for this briefing live alongside the wake prompts in `lib/clast/prompts/` so the plugin skill and the porcelain [`clast brief`](../guides/run-without-claude-code.md) script stay in sync:

- **System prompt:** [`lib/clast/prompts/brief-system.md`](../../lib/clast/prompts/brief-system.md)
- **User prompt template:** [`lib/clast/prompts/brief-user.md`](../../lib/clast/prompts/brief-user.md)

The inline form below documents the same intent:

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
# Fired on Claude Code SessionStart. Backgrounds `clast-plumbing snapshot` so it doesn't
# block session start. Silent if clast isn't installed — the plugin can still
# load cleanly even if the CLI isn't on PATH.
#
# Idempotent. Safe to run repeatedly.

if command -v clast-plumbing >/dev/null 2>&1; then
  (clast-plumbing snapshot >/dev/null 2>&1 &)
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
5 * * * * /usr/local/bin/clast-plumbing snapshot >/dev/null 2>&1
```

For systemd-timer users: `examples/cron/systemd-timer.sample` should provide a `.service` + `.timer` pair with the same behavior.
