---
name: retro
description: 'Condense a work retrospective (grouped by work day → project → session) from recent Claude Code sessions and render it, or emit the enriched manifest as JSON. Use when the user says "/retro", "retro", "work retrospective", "what did I get done this week", "summarize my week", or "condense my sessions". Runs `clast-plumbing retro --json --bodies` for the deterministic structure, then asks the model to condense each session body into a summary via the shared retro-summary prompt templates. This is a read-and-render report with no approval step — unlike `/wake`, there is no draft to accept, edit, or skip; the output is printed directly.'
---

# Retro

Condense a work retrospective — grouped by actual work day → project → session — from recent Claude Code sessions, and render it (or emit it as JSON).

## Why this exists

Structure comes from the deterministic core (`clast-plumbing retro --json --bodies`); the model's only job is condensing each session's body into a few retro bullets. This mirrors what `clast retro` does at the CLI, but drives the condensation through this Claude Code session instead of an LLM API call. There is no write path and no per-session decision to make — it's a report, not a curation flow.

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

## Step 1: Determine the window

Support the same flags as `clast retro`, parsed from the user's request (natural language or literal flags):

- `--from DATE` — start of the window (inclusive).
- `--to DATE` — end of the window (inclusive).
- `--all` — cover the whole corpus (overrides the 7-day default below).
- `--window WHICH` — `work-days` (default) or `file-dates`. Passed straight through to `clast-plumbing retro`.
- `--refresh` — this skill has no cache of its own to invalidate (see Step 3's note on caching); treat `--refresh` as a no-op if the user passes it, since every invocation of this skill already does a fresh model call per session.
- `--json` — emit the enriched manifest instead of rendering (see Step 4).

`DATE` accepts whatever forms `clast-plumbing retro` itself accepts for `--from`/`--to` — ISO (`YYYY-MM-DD`), `today`, `yesterday`, `last-week`, `-Nd`, `-Nw` — this skill just passes the flag value through; don't invent additional forms.

**Default window:** if the user gives no `--from`, `--to`, or `--all`, default to the last 7 days (`--from -7d`), same as the CLI. Call this out to the user if they ask for an unbounded retro without `--all` — an unbounded run means one model call per session across the whole corpus, which can be slow and costly.

## Step 2: Build the work-day manifest

```bash
$CLAST_BIN --json retro --bodies --window <window> [--from <from>] [--to <to>]
```

This returns a deterministic manifest shaped `days[].projects[].sessions[]`, where each session carries at least `session_id`, `work_day`, `body`, `project_path`, `title`, and `interrupted`. The structure itself is deterministic and requires no interpretation — only the prose condensation in Step 3 is the model's job.

## Step 3: Condense each session

For each session in the manifest:

- **If `.body` is non-empty:** read the two shared prompt templates —
  `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/retro-summary-system.md` (system
  prompt) and `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/retro-summary-user.md`
  (user prompt template) — and substitute the user template's placeholders
  with this session's data:

  | Placeholder | Filled with |
  |---|---|
  | `{{project}}` | the session's `project_path` |
  | `{{work_day}}` | the session's `work_day` |
  | `{{session_id}}` | the session's `session_id` (or its fold key if the session has no id) |
  | `{{body}}` | the session's `body` |

  Send the substituted user prompt together with the (unmodified) system
  prompt to the model, and use its response as that session's summary. The
  system prompt fully specifies what the response should contain — follow
  it as written; do not restate or paraphrase its instructions here.

- **If `.body` is empty or absent:** use the literal string
  `(no body to summarize)` as the session's summary and skip the model call
  for that session entirely — do not invoke the model on empty input.

**Caching is out of scope for this skill.** The CLI caches summaries per
session under `<journal>/.retro-summaries/`, content-fingerprinted so
re-runs are free unless a session changed; this skill does not need to read,
write, or reverse-engineer that cache to behave correctly, since each
invocation already does a fresh model call per session.

## Step 4: Render vs `--json`

**Default (no `--json`):** render the retrospective grouped by day →
project → session: a header line naming the resolved window, then one
block per day, containing one block per project, containing one line per
session (showing at least the session's title, a short identifier, and
whether it was interrupted) followed by its condensed summary from Step 3.
For example, illustrating the shape only (not a literal spec to match
byte-for-byte):

```
Retro: 2026-07-05 -> 2026-07-12 (work-days)

== 2026-07-08 ==

[clast]
  * Fix retro caching  (a1b2c3d4)
  <condensed summary for this session>
```

If a day has no sessions, note that plainly rather than omitting the day.

**`--json`:** skip rendering entirely and emit the enriched manifest from
Step 2 with each session's `.summary` field populated from Step 3, and
`.body` removed from every session (the raw body was only needed as
summarizer input).

## Edge cases

- **No sessions in the resolved window:** say so plainly (e.g. "no sessions in range") and stop; don't render empty day/project headers.
- **`clast-plumbing retro` fails:** surface its error message to the user and stop — there's no partial manifest to fall back to.
- **A session has no `session_id`:** use its fold key (e.g. its first journal entry path) in place of `{{session_id}}` and as the short identifier shown when rendering.
