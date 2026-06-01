# clast

Capture, curate, and surface Claude Code session history across all your projects.

> 🚧 Pre-release. APIs may change before v1.0.

## What it does

- **CLI** (`clast`) — snapshot, browse, and query your Claude Code session JSONL history.
- **Plugin** — installs skills (`/day-wakeup`, `/wakeup`) that surface recent session context.
- **SessionStart hook** — quietly snapshots active sessions in the background each time Claude Code starts.

## Capture your sessions

```sh
clast snapshot                       # copy any new sessions into the journal
clast snapshot --dry-run --json | jq # preview what would be captured
```

`clast snapshot` is idempotent and silent on no-op, safe to run from cron or
a SessionStart hook. See [`docs/cli-contract.md#clast-snapshot`](./docs/cli-contract.md#clast-snapshot)
for the full flag reference.

## Read your sessions

```sh
clast projects                        # which projects had activity today
clast sessions --since -7d            # sessions captured in the last week
clast show <session-uuid> --full      # metadata + first/last turns
```

Window flags (`--day`, `--since`, `--until`) accept ISO dates and
relative keywords. See
[`docs/cli-contract.md#clast-projects`](./docs/cli-contract.md#clast-projects),
[`docs/cli-contract.md#clast-sessions`](./docs/cli-contract.md#clast-sessions),
and [`docs/cli-contract.md#clast-show`](./docs/cli-contract.md#clast-show)
for the full flag and output schemas.

## Curate an entry

```sh
clast entries                                            # list curated entries
clast entries read 2026-05-30-1430-xesapps-foo.md        # cat a single entry
printf 'Notes...\n' | clast entries write \
  --session <session-uuid> --slug short-slug --body-stdin # write a new entry
```

`clast entries write` looks up the session in the manifest, composes the
documented frontmatter from the captured snapshot + registry, and writes
`entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md` atomically. See
[`docs/cli-contract.md#entry-frontmatter`](./docs/cli-contract.md#entry-frontmatter)
for the full frontmatter schema and
[`docs/cli-contract.md#clast-entries`](./docs/cli-contract.md#clast-entries)
for the flag reference.

## Inspect and audit the journal

```sh
clast stats                          # one-line activity summary for today
clast stats --since -7d              # rollup over the last week

clast doctor                         # check manifest, registry, snapshots
clast doctor --fix                   # rebuild a broken manifest, prune orphans
```

See [`docs/cli-contract.md#clast-stats`](./docs/cli-contract.md#clast-stats)
and [`docs/cli-contract.md#clast-doctor`](./docs/cli-contract.md#clast-doctor)
for the contract reference, and `clast stats --help` / `clast doctor --help`
for the current set of flags.

## Install as a Claude Code plugin

For a local checkout, install the plugin with:

```sh
claude plugin install <path-to-clast-checkout>
```

(The marketplace install flow is wired up in a future step.) Today the plugin
ships a single `SessionStart` hook: every time a Claude Code session starts it
backgrounds `clast snapshot`, so your journal stays current with zero manual
effort. The hook is best-effort and silent — if the `clast` CLI isn't on your
`PATH` yet, sessions still start cleanly. See
[`docs/skill-prompts.md#hook-sessionstart`](./docs/skill-prompts.md#hook-sessionstart)
for the hook's design rationale.

### `/day-wakeup`

At the start of each day, run `/day-wakeup` inside any Claude Code session to curate
yesterday's sessions into durable journal entries. The skill walks through each uncurated
session, proposes a draft, and prompts you to accept, edit, or skip. See
[`docs/skill-prompts.md#skill-1-day-wakeup`](./docs/skill-prompts.md#skill-1-day-wakeup).

### `/wakeup`

When starting work on a specific project, run `/wakeup` (or `/wakeup <slug>` from
anywhere) to get a per-project read-only briefing synthesized from recent curated entries,
today's breadcrumbs, and any sessions already started today. `/wakeup` never writes — it
only reads. See
[`docs/skill-prompts.md#skill-2-wakeup`](./docs/skill-prompts.md#skill-2-wakeup).

## Development

**With Nix (recommended).** Run `direnv allow` (or `nix develop`) at the repo root. The dev shell provides `bash`, `jq`, `git`, `shellcheck`, and `pre-commit` — everything `clast` needs at runtime plus the dev tooling.

**Without Nix.** Install `bash 5+`, `jq`, `git`, and `shellcheck` via your package manager, then run `make deps-check` to verify they're on PATH.

## Documentation

- [`docs/overview.md`](./docs/overview.md) — project overview and design.
- [`docs/cli-contract.md`](./docs/cli-contract.md) — CLI reference.
- [`docs/skill-prompts.md`](./docs/skill-prompts.md) — plugin reference.
- [`docs/repo-bootstrap.md`](./docs/repo-bootstrap.md) — repo layout and packaging.

## License

MIT — see [LICENSE](./LICENSE).
