# clast

Capture, curate, and surface Claude Code session history across all your projects.

> 🚧 Pre-release. APIs may change before v1.0.

## What it does

- **CLI** (`clast`) — snapshot, browse, and query your Claude Code session JSONL history.
- **Plugin** — installs skills (`/day-wakeup`, `/wakeup`) that surface recent session context.
- **SessionStart hook** — quietly snapshots active sessions in the background each time Claude Code starts.

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
