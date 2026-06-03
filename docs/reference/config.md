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
| `day_cutoff` | `CLAST_DAY_CUTOFF` | `04:00` | Sessions started before this local time on date D count as belonging to date D-1. Used by `clast stats`, `clast sessions`, and the `/day-wakeup` window. |

### `[logging]`

| TOML key | Env var | Default | Meaning |
|---|---|---|---|
| `quiet` | `CLAST_QUIET` | `false` | Suppress informational output (same as the global `--quiet` flag). |

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
