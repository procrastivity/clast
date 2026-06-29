# Run curation without Claude Code

The [`/wake` and `/brief` plugin skills](../reference/plugin.md) run *inside*
Claude Code. If you want the same LLM-assisted curation and briefing from a plain
terminal — a cron job, a remote box, or just a shell without Claude Code — use the
porcelain subcommands that ship with the CLI:

- **`clast wake`** — interactive day curation. The standalone equivalent of `/wake`.
- **`clast brief`** — a project briefing. The standalone equivalent of `/brief`.
- **`clast retro`** — a model-condensed work retrospective grouped by work day → project.

Both call an OpenAI-compatible chat-completions endpoint directly (via `curl`) and
reuse the same prompt templates as the plugin skills, so the output stays in sync.

`clast` is the user-facing porcelain. The deterministic core it sits on top of —
`whereami`, `snapshot`, `sessions`, `entries`, … — lives in a separate
`clast-plumbing` binary. You normally won't invoke `clast-plumbing` directly; it's
what `clast wake` / `clast brief` and the plugin skills call under the hood.

## Requirements

- `clast`, `clast-plumbing`, `curl`, and `jq` on `PATH`.
- An interactive terminal for `clast wake` (it reads choices from the tty).
- An OpenAI-compatible LLM endpoint, configured through three env vars:

  ```bash
  export CLAST_LLM_BASE_URL="https://api.openai.com/v1"
  export CLAST_LLM_API_KEY="sk-..."
  export CLAST_LLM_MODEL="gpt-4o"
  ```

  A local model works too — point `CLAST_LLM_BASE_URL` at ollama/vllm/etc. and set
  `CLAST_LLM_API_KEY` to any non-empty placeholder:

  ```bash
  export CLAST_LLM_BASE_URL="http://localhost:11434/v1"
  export CLAST_LLM_API_KEY="unused"
  export CLAST_LLM_MODEL="llama3"
  ```

See [`reference/config.md`](../reference/config.md#llm) for the full env-var table.

## Availability

The [`install.sh`](../getting-started/install.md) script installs both `clast`
(the porcelain) and `clast-plumbing` (the core) into `$PREFIX/bin`. From a
checkout, run them directly as `bin/clast` and `bin/clast-plumbing`. The npm
package ships both under its `bin/` directory.

## `clast wake` — curate the day

```bash
clast wake
```

1. **Triage.** It enumerates uncurated sessions from the last 30 days
   (`clast-plumbing sessions --since -30d`). When they span more than one day it
   offers to process everything, just yesterday, the last *N* days, or to
   [dismiss](./curate-an-entry.md) everything older and process the rest. Older
   sessions are dismissed via `clast-plumbing sessions dismiss` so they won't
   resurface.
2. **Draft per session.** For each remaining (uncurated or
   [stale](../reference/cli.md#clast-sessions)) session it gathers context, fills the
   shared draft prompt, and asks the LLM for an entry draft.
3. **Decide.** After each draft it prompts:
   `[a] Accept  [e] Edit  [d] Dismiss  [s] Skip  [q] Stop here`. Accepting writes the
   entry with `clast-plumbing entries write`; dismissing records a
   `clast-plumbing sessions dismiss`.

## `clast brief` — brief a project

```bash
clast brief [<project-slug>]
```

With no argument it resolves the project from the current directory via
`clast-plumbing registry resolve`. (If the directory isn't a registered project it
tells you to run `clast-plumbing registry add .` or pass a slug.) It then reads
recent curated entries, today's breadcrumbs, and today's sessions for that project
and prints a synthesized briefing. It writes nothing — it's read-only, same as
`/brief`.

## `clast retro` — condense a work retrospective

```bash
clast retro [--from DATE] [--to DATE] [--window work-days|file-dates] [--refresh] [--json]
```

Builds the deterministic day→project structure with `clast-plumbing retro --json
--bodies`, then asks the LLM to condense each session's body into a few retro
bullets. The grouping, work-day bucketing, provenance notes, and friendly project
names are all deterministic — only the per-session prose is model-written. With no
`--from`/`--to` it covers the whole corpus.

Summaries are **cached per session** under `<journal>/.retro-summaries/`, keyed by
a content fingerprint of the session body, so re-runs are free and re-rendering
costs nothing. A session re-summarizes only when its content changes or you pass
`--refresh`. `--json` emits the manifest with a `summary` per session instead of
the rendered report.

For a model-free version (raw entry bodies instead of condensed bullets), use
[`clast-plumbing retro`](../reference/cli.md#clast-plumbing-retro) directly.

## Customizing the prompts

Both subcommands read their prompt templates from `lib/clast/prompts/` — the same
files the plugin skills use:

- `wake-draft-system.md` / `wake-draft-user.md` (used by `clast wake`)
- `brief-system.md` / `brief-user.md` (used by `clast brief`)
- `retro-summary-system.md` / `retro-summary-user.md` (used by `clast retro`)

Edit those files to change tone or structure once for both the porcelain and the
plugin. If the files are missing the porcelain falls back to built-in inline prompts.

## Automating it

`clast brief` and `clast retro` are non-interactive and safe to run from a login
shell, a shell prompt, or cron (`clast retro`'s per-session cache keeps repeat runs
cheap). `clast wake` is interactive and expects a tty — don't run it from cron.
To capture sessions on a schedule, automate `clast-plumbing snapshot` instead (see
[automate with cron](./automate-with-cron.md) or [systemd](./automate-with-systemd.md)),
then run `clast wake` by hand when you want to curate.
