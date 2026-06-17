# Configuration

> **Status:** v1.0 reads only the environment-variable equivalents. The TOML
> loader is planned for v1.x. Uncommenting values in `config.toml` today is a
> no-op; treat the file as documentation for the planned config surface.

## Search path (planned)

```
~/.config/clast/config.toml
```

`$XDG_CONFIG_HOME/clast/config.toml` if `$XDG_CONFIG_HOME` is set.

## Schema

Each key has an env-var equivalent that works **today** (v1.0). Set the env vars
in your shell profile to get the same effect as the planned TOML keys.

### `[paths]`

| TOML key | Env var | Default | Meaning |
|---|---|---|---|
| `projects_dir` | `CLAST_PROJECTS_DIR` | `~/.claude/projects` | Where clast reads JSONL transcripts from. |
| `journal_dir` | `CLAST_JOURNAL_DIR` | `~/.claude/journal` | Where clast writes the journal (manifest, snapshots, entries, breadcrumbs). |

### `[time]`

| TOML key | Env var | Default | Meaning |
|---|---|---|---|
| `day_cutoff` | `CLAST_DAY_CUTOFF` | `04:00` | Sessions started before this local time on date D count as belonging to date D-1. Used by `clast-plumbing stats`, `clast-plumbing sessions`, and the `/wake` window. |

### `[logging]`

| TOML key | Env var | Default | Meaning |
|---|---|---|---|
| `quiet` | `CLAST_QUIET` | `false` | Suppress informational output (same as the global `--quiet` flag). |

### `[llm]`

Only used by the porcelain [`clast wake` and `clast brief`](../guides/run-without-claude-code.md) subcommands, which call an OpenAI-compatible chat-completions endpoint directly. The Claude Code plugin skills do **not** read these — they run inside Claude Code and need no API key. There is no TOML equivalent; these are env-only.

| Env var | Example | Meaning |
|---|---|---|
| `CLAST_LLM_BASE_URL` | `https://api.openai.com/v1` | Base URL of the OpenAI-compatible API. |
| `CLAST_LLM_API_KEY` | `sk-…` | API key sent as the bearer token. |
| `CLAST_LLM_MODEL` | `gpt-4o` | Model name passed in the request. |

All three must be set for `clast wake`/`clast brief` to run.

## Sample

A heavily-commented sample lives at
[`examples/config/config.toml.sample`](../../examples/config/config.toml.sample).

## Precedence

Once the TOML loader exists, the intended precedence will be (highest first):

1. Command-line flags (`--journal-dir`, `--quiet`, etc.)
2. Environment variables (`CLAST_*`)
3. `~/.config/clast/config.toml`
4. Built-in defaults

Today, with no loader, the chain collapses to flags → env → defaults.
