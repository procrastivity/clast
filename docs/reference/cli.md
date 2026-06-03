# CLI reference

> Reference doc. New to clast? Start with
> [`explanation/what-is-clast.md`](../explanation/what-is-clast.md) and
> [`getting-started/first-snapshot.md`](../getting-started/first-snapshot.md).
> This doc specs the `clast` CLI in detail: arguments, output formats, JSON
> schemas, exit codes, error handling.

The CLI is a single dispatcher binary (`bin/clast`) with subcommands dispatched to handlers in `lib/clast/clast-subcommands/<name>.bash`. All subcommands are LLM-free. Every command supports `--help` and `--json` where output is structured.

---

## Global flags

| Flag | Purpose |
|---|---|
| `--help`, `-h` | Print usage for this command and exit 0. |
| `--version` | Print version and exit 0. Top-level only (`clast --version`). |
| `--json` | Machine-readable JSON output. Supported on all read commands. |
| `--verbose`, `-v` | Extra diagnostic output to stderr. |
| `--quiet`, `-q` | Suppress informational output to stdout (errors still go to stderr). |
| `--journal-dir PATH` | Override `~/.claude/journal/`. Mainly for testing. Env: `CLAST_JOURNAL_DIR`. |
| `--projects-dir PATH` | Override `~/.claude/projects/`. Mainly for testing. Env: `CLAST_PROJECTS_DIR`. |

---

## Date parsing

Anywhere a `DATE` is accepted, the CLI accepts:

- ISO date: `2026-05-30`
- Relative keywords: `today`, `yesterday`, `last-week`
- Relative offsets: `-1d`, `-3d`, `-1w`

All resolved to local-time dates using the `day_cutoff` offset (default `04:00`). The flag `--day yesterday` at 02:00 on May 31 means "the bucket starting at 04:00 on May 29 and ending at 04:00 on May 30" — i.e. it does the right thing for night-owl invocations.

---

## `clast snapshot`

Capture new transcripts from `~/.claude/projects/` into the journal.

### Synopsis

```
clast snapshot [--dry-run] [--since TIMESTAMP] [--include-segment SEG]
```

### Behavior

1. Read `.manifest.jsonl`, build in-memory set of `(session_id, source_mtime)` already captured.
2. Walk `~/.claude/projects/*/*.jsonl`. For each file: if `(session_id, mtime)` not in manifest, mark for capture.
3. For each new/modified file:
   - Read first line of JSONL to extract session start time (for day-bucket placement).
   - Copy to `~/.claude/journal/transcripts/<day_bucket>/<segment>/<session-uuid>.jsonl`.
   - Append a line to `.manifest.jsonl`.
4. Print summary unless `--quiet`.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--dry-run` | off | Don't copy or write manifest; just print what would be done. |
| `--since TIMESTAMP` | none | Only consider source files with mtime ≥ timestamp. ISO 8601 or relative. |
| `--include-segment SEG` | none | Limit scan to one segment (e.g., `-home-beau-code-xesapps`). Repeatable. Mostly for debugging. |

### Output (default)

Silent if no work was done (correct behavior for cron and hook). Otherwise:

```
Captured 3 sessions across 2 projects (2.4 MB).
  xesapps: 2 sessions
  pi-coding-agent: 1 session
```

### Output (`--json`)

```json
{
  "captured": [
    {
      "session_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "source": "/home/beau/.claude/projects/-home-beau-code-xesapps/a1b2c3....jsonl",
      "snapshot": "transcripts/2026-05-30/-home-beau-code-xesapps/a1b2c3....jsonl",
      "bytes": 1234567,
      "day_bucket": "2026-05-30"
    }
  ],
  "skipped": 47,
  "errors": []
}
```

### Exit codes

- 0: success (including no-op)
- 1: partial failure (some files captured, some errored); errors in JSON output's `errors` field
- 4: manifest corruption detected; halts before any writes

---

## `clast projects`

List projects with activity in a window.

### Synopsis

```
clast projects [--day DATE] [--since DATE] [--until DATE] [--unregistered]
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--day DATE` | `today` | Single-day window. Mutually exclusive with `--since`/`--until`. |
| `--since DATE` | none | Start of range (inclusive). |
| `--until DATE` | `today` | End of range (inclusive). |
| `--unregistered` | off | Include only projects whose path/segment is not in the registry. |

### Output (default)

```
slug              path                              sessions  msgs   last_active
xesapps           /home/beau/code/xesapps                  3   147   14:30
pi-coding-agent   /home/beau/code/pi-coding-agent          1    23   09:15
weftlo            /home/beau/code/weftlo                   2    61   16:22
(unregistered)    /home/beau/scratch/foo                   1    12   11:04
```

### Output (`--json`)

```json
[
  {
    "slug": "xesapps",
    "path": "/home/beau/code/xesapps",
    "segment": "-home-beau-code-xesapps",
    "remote": "git@gitlab.xes-inc.com:xes/xesapps.git",
    "session_count": 3,
    "msg_count_approx": 147,
    "last_active": "2026-05-30T14:30:55Z",
    "registered": true
  }
]
```

`msg_count_approx` is `wc -l` on the JSONL minus dedup heuristics; it's an upper bound. Documented as approximate.

---

## `clast sessions`

List sessions in a window, optionally filtered by project.

### Synopsis

```
clast sessions [--day DATE] [--since DATE] [--until DATE] [--project SLUG]
```

### Output (default)

```
session_id                            project           branch                    start  end    msgs
a1b2c3d4-e5f6-7890-abcd-ef1234567890  xesapps           feature/canonical-field   09:15  11:48   47
b2c3d4e5-f6a7-8901-bcde-f12345678901  xesapps           main                      13:22  14:30   31
c3d4e5f6-a7b8-9012-cdef-123456789012  pi-coding-agent   autopatchelf-bun          15:00  16:22   23
```

### Output (`--json`)

```json
[
  {
    "session_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "project": "xesapps",
    "segment": "-home-beau-code-xesapps",
    "branch": "feature/canonical-field",
    "start": "2026-05-30T09:15:23Z",
    "end": "2026-05-30T11:48:07Z",
    "msg_count_approx": 47,
    "snapshot_path": "transcripts/2026-05-30/-home-beau-code-xesapps/a1b2c3....jsonl",
    "day_bucket": "2026-05-30",
    "curated": false
  }
]
```

`curated: true` if there's a corresponding entry in `entries/` (joined on `session_id` via entry frontmatter). The plugin's `/day-wakeup` uses this field to iterate only uncurated sessions.

---

## `clast show`

Dump session metadata.

### Synopsis

```
clast show <session-id> [--full] [--turns N]
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--full` | off | Include first/last N turns (text only, no tool calls). |
| `--turns N` | 5 | Number of turns at each end when `--full` is set. |

### Output (default)

```
session_id:       a1b2c3d4-e5f6-7890-abcd-ef1234567890
project:          xesapps
segment:          -home-beau-code-xesapps
branch:           feature/canonical-field
start:            2026-05-30 09:15:23
end:              2026-05-30 11:48:07
duration:         2h 32m
msg_count:        47 (approx)
snapshot:         ~/.claude/journal/transcripts/2026-05-30/-home-beau-code-xesapps/a1b2c3....jsonl
curated:          no
first_prompt:     "Let's investigate why vw_Consumer_Fields_All is slow…"
last_prompt:      "Can we benchmark the new query plan?"
```

With `--full`, appends `## First 5 turns` and `## Last 5 turns` sections in plain text.

`--json` produces the same data as a JSON object.

---

## `clast entries`

List or read curated journal entries.

### Synopsis

```
clast entries [--day DATE] [--since DATE] [--until DATE] [--project SLUG] [--tag TAG] [--limit N]
clast entries read <entry-path>
clast entries write [...]
```

### `clast entries` (list)

Default output:

```
date        time   project          slug                         tags
2026-05-30  14:30  xesapps          vw-consumer-fields-explain   mysql,optimization
2026-05-30  09:15  pi-coding-agent  autopatchelf-bun-binary      nix,packaging
```

JSON output:

```json
[
  {
    "path": "/home/beau/.claude/journal/entries/2026-05-30-1430-xesapps-vw-consumer-fields-explain.md",
    "date": "2026-05-30",
    "time": "14:30",
    "day_bucket": "2026-05-30",
    "project": "xesapps",
    "session_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "session_slug": "vw-consumer-fields-explain",
    "branch": "feature/canonical-field",
    "tags": ["mysql", "optimization"],
    "title": "vw_Consumer_Fields_All EXPLAIN walkthrough"
  }
]
```

### `clast entries read <entry-path>`

Cat the entry file to stdout. Convenience over `cat` so the skill doesn't need to know the file layout. Accepts either an absolute path or just the filename (resolves under `entries/`).

### `clast entries write`

```
clast entries write \
  --session SESSION_ID \
  --slug SESSION_SLUG \
  [--tags TAG,TAG,...] \
  [--title TITLE] \
  --body-from FILE | --body-stdin
```

Behavior:

1. Look up session in manifest by `session_id`. Error if unknown.
2. Resolve project info from the session's snapshot (segment → registry → slug + remote).
3. Compose frontmatter (date, time, day_bucket, project, project_path, project_remote, branch, author, tags, session_id, session_slug, snapshot_path, machine).
4. Read body from `--body-from FILE` or stdin (`--body-stdin`).
5. Write to `entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md`.
6. Refuse to overwrite existing file with same name (suffix `-2`, `-3`).

Output:

```
Wrote entries/2026-05-30-1430-xesapps-vw-consumer-fields-explain.md
```

JSON output: `{"path": "..."}`.

Exit codes:
- 2: missing required flag
- 1: session unknown, body empty, write failed

---

## `clast breadcrumb`

Append a one-line in-flight hint.

### Synopsis

```
clast breadcrumb [--project SLUG | --global] [--date DATE] <TEXT>
clast breadcrumb --read [--project SLUG | --global] [--day DATE]
clast breadcrumb --list [--day DATE]
```

### Write mode (default)

1. Resolve project from `--project` or `pwd` (via registry).
2. If `pwd` doesn't resolve and `--project` not given and `--global` not given, prompt to register or accept `--global`.
3. Append `- HH:MM — <TEXT>` to `breadcrumbs/YYYY-MM-DD-<slug>.md` (or `-_global.md`).
4. Create the file with frontmatter if it doesn't exist.

Output: silent on success unless `--verbose`.

### `--read` mode

Cat the breadcrumb file for the given project + day. Used by `/wakeup` and `/day-wakeup` to read in-flight hints.

### `--list` mode

List all breadcrumb files for a day. JSON output: array of `{project, path, line_count}`.

---

## `clast registry`

Manage `projects.json`.

### `clast registry list [--json]`

```
slug              path                              remote                                      aliases
xesapps           /home/beau/code/xesapps           git@gitlab.xes-inc.com:xes/xesapps.git      /mnt/c/code/xesapps
pi-coding-agent   /home/beau/code/pi-coding-agent   https://github.com/.../pi-coding-agent.git  (none)
```

### `clast registry add <path> [--slug NAME] [--remote URL]`

1. Resolve `path` to absolute, canonicalize.
2. Run `git -C <path> remote get-url origin` if `--remote` not given.
3. If `--slug` not given, prompt with default = repo dirname.
4. If `remote` matches an existing entry, add `path` to that entry's `aliases` instead of creating a new entry.
5. Else create a new registry entry.

Output: confirmation line, or JSON with `--json`.

### `clast registry resolve <path-or-segment>`

Given a filesystem path or a Claude Code encoded segment, return the registered slug.

```
$ clast registry resolve /home/beau/code/xesapps
xesapps

$ clast registry resolve -home-beau-code-xesapps
xesapps

$ clast registry resolve /tmp/unknown
(error: not registered)
```

Exit codes:
- 0: resolved
- 1: not registered

JSON output: `{"slug": "..."}` on success, `{"error": "..."}` on failure.

### `clast registry remove <slug>`

Remove a slug from the registry. **Does not delete entries or transcripts.** Just unregisters the path mapping.

---

## `clast stats`

Token/duration/session-count stats.

### Synopsis

```
clast stats [--day DATE] [--since DATE] [--until DATE] [--project SLUG]
```

### Output

```
Window:    2026-05-30 (today)
Projects:  3
Sessions:  6
Messages:  234 (approx)
Bytes:     14.2 MB (snapshot total)
Curated:   2 of 6 sessions (33%)
Breadcrumbs: 5 across 2 projects
```

JSON output mirrors the structure as keys.

Stats are derived from manifest + filesystem stat; no JSONL parsing required.

---

## `clast doctor`

Sanity-check the journal.

### Synopsis

```
clast doctor [--fix]
```

### Checks performed

1. **Manifest validity**: every line is parseable JSON with required fields.
2. **Registry validity**: every line is parseable JSON; no duplicate slugs; aliases don't conflict across entries.
3. **Orphan snapshots**: files in `transcripts/` not referenced by manifest.
4. **Missing snapshots**: entries reference snapshot paths that don't exist on disk.
5. **Day-bucket consistency**: snapshot file location matches its manifest `day_bucket`.
6. **Day cutoff sanity**: warn if entries were written across a day boundary in ways the cutoff may have miscategorized.

### With `--fix`

Only safe fixes are applied without prompting:
- Rebuild manifest from `transcripts/` contents (if manifest is corrupted).
- Remove orphan snapshots (after listing them, with confirmation if `--yes` not given).

Destructive operations (removing entries, rewriting frontmatter) always require explicit user action; never auto-applied.

### Output

```
✓ Manifest: 247 entries, all valid
✓ Registry: 8 projects, no duplicates
✗ Orphan snapshots: 3 (see below)
  transcripts/2026-04-15/-old-path/abc.jsonl
  transcripts/2026-04-15/-old-path/def.jsonl
  transcripts/2026-04-15/-old-path/ghi.jsonl
✓ Missing snapshots: none
✓ Day-bucket consistency: ok

Run `clast doctor --fix` to clean up orphans.
```

Exit codes:
- 0: all checks passed
- 1: issues found (still reported, none fixed)
- 4: critical corruption (e.g., manifest unparseable)

---

## `clast whereami`

Debug current state. Mostly for users to understand what `clast` sees.

### Output

```
pwd:            /home/beau/code/xesapps/api
git_root:       /home/beau/code/xesapps
registered:     yes
slug:           xesapps
remote:         git@gitlab.xes-inc.com:xes/xesapps.git
last_snapshot:  2026-05-30 14:32:11 (3 min ago)
journal_dir:    /home/beau/.claude/journal
projects_dir:   /home/beau/.claude/projects
day_cutoff:     04:00
machine:        beau-wsl2
```

---

## File format specs

### Manifest line

```json
{
  "session_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "source": "/home/beau/.claude/projects/-home-beau-code-xesapps/a1b2c3....jsonl",
  "snapshot": "transcripts/2026-05-30/-home-beau-code-xesapps/a1b2c3....jsonl",
  "captured_at": "2026-05-30T16:42:11Z",
  "source_mtime": "2026-05-30T16:30:55Z",
  "source_size": 2384721,
  "day_bucket": "2026-05-30"
}
```

All fields required. Lookups use "most recent line wins" semantics for a given `session_id`.

### Registry line (in `projects.json`)

```json
{
  "path": "/home/beau/code/xesapps",
  "slug": "xesapps",
  "remote": "git@gitlab.xes-inc.com:xes/xesapps.git",
  "first_seen": "2026-03-12",
  "aliases": ["/mnt/c/code/xesapps", "/Users/beau/code/xesapps"]
}
```

`path` is required. `slug` is required. Other fields optional. Multiple lines may share a `slug` if `path` differs (slug acts as a logical project identifier).

### Entry frontmatter

See [`entry-frontmatter.md`](./entry-frontmatter.md) for the full field-by-field
schema and the conventional body structure.

### Breadcrumb file

```markdown
---
date: 2026-05-30
project: xesapps
---

- 14:23 — check the migration before next deploy
- 16:07 — figure out why the EXPLAIN plan differs in CI
```

---

## Error handling conventions

- Errors go to stderr; structured output (incl. `--json`) goes to stdout.
- JSON errors: `{"error": "<message>", "code": <exit-code>}` on stdout when `--json` is set; non-zero exit.
- Human errors: `clast: <subcommand>: <message>` on stderr; non-zero exit.
- Never write partial output to a destination file. Compose in memory or temp file, then atomically move into place.

---

## Library structure (for implementers)

```
lib/clast/
├── clast-lib.bash               # Sourced by bin/clast. Common helpers: I/O, JSON, date math, path resolution.
├── clast-decode-lib.bash        # Segment ↔ path decoder with collision resolution.
├── clast-manifest-lib.bash      # Manifest read/append/lookup.
├── clast-registry-lib.bash      # Registry read/write/resolve, alias handling.
└── clast-subcommands/
    ├── snapshot.bash
    ├── projects.bash
    ├── sessions.bash
    ├── show.bash
    ├── entries.bash
    ├── breadcrumb.bash
    ├── registry.bash
    ├── stats.bash
    ├── doctor.bash
    └── whereami.bash
```

Each subcommand exposes a single entry function (e.g., `clast_cmd_snapshot`) that `bin/clast` invokes after argument parsing. Subcommands `source` only the libs they need; the dispatcher decides what to load.
