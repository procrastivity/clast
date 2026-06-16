# Conventions

## File formats

- **JSONL**: append-only, one JSON object per line, no trailing comma issues,
  robust to crashes (lose at most the last partial line). Used for manifest,
  registry, transcripts.
- **Markdown with YAML frontmatter**: entries and breadcrumbs. Human-readable,
  greppable, git-friendly.
- **TOML**: user config (one file). See [`reference/config.md`](../reference/config.md).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (including idempotent no-ops) |
| 1 | General error |
| 2 | Invalid arguments / usage error |
| 3 | Missing dependency or environment problem |
| 4 | Data integrity issue (manifest corrupted, etc.) |

## Date handling

- All dates in user-facing CLI output: `YYYY-MM-DD` (ISO 8601 short form).
- All timestamps in JSON output: ISO 8601 UTC with `Z` suffix
  (e.g., `2026-05-30T14:30:55Z`).
- Day bucketing: local-time date adjusted by `day_cutoff` (default `04:00`).
  A session starting at 01:30 local on May 31 with default cutoff →
  `day_bucket: 2026-05-30`.

See [`reference/cli.md#date-parsing`](../reference/cli.md#date-parsing) for the
full date-input grammar (`today`, `yesterday`, `-1d`, etc.).

## Naming

- Subcommands: lowercase, single word (or hyphen-joined). `clast-plumbing snapshot`, not
  `clast Snapshot` or `clast take-snapshot`.
- Flags: kebab-case long flags (`--day`, `--project`); single-letter shorts only
  where unambiguous and well-known (`-v` for verbose, `-h` for help).
- JSON keys: snake_case (`session_id`, `day_bucket`, `project_remote`).

## Error handling

- Errors go to stderr; structured output (including `--json`) goes to stdout.
- JSON errors: `{"error": "<message>", "code": <exit-code>}` on stdout when
  `--json` is set; non-zero exit.
- Human errors: `clast: <subcommand>: <message>` on stderr; non-zero exit.
- Never write partial output to a destination file. Compose in memory or a
  temp file, then atomically move into place.

## Idempotence

`clast-plumbing snapshot` is idempotent and silent on no-op — safe to run from cron, a
`SessionStart` hook, or by hand as often as you like. Re-running it never
duplicates a manifest entry or overwrites an existing snapshot.
