# Architecture

```
   Claude Code session ────writes──► ~/.claude/projects/<encoded>/<uuid>.jsonl
                                                  │
                                                  │ (CC auto-deletes eventually)
                                                  ▼
   cron OR SessionStart hook ──invokes──►  clast snapshot
                                                  │
                                                  ▼
                                  ~/.claude/journal/transcripts/...
                                  ~/.claude/journal/.manifest.jsonl
                                                  │
   /day-wakeup ──invokes──► clast snapshot       │  (durable; survives CC auto-deletion)
                ──invokes──► clast sessions --day yesterday --json
                          │
                          ▼
            iterates sessions → LLM-generated draft → AskUserQuestion
                          │
                          ▼ (on accept)
                ──invokes──► clast entries write
                                                  │
                                                  ▼
                                  ~/.claude/journal/entries/...

   /wakeup [project] ──invokes──► clast entries --project SLUG --json
                     ──synthesizes briefing in chat

   clast breadcrumb TEXT ──appends──► ~/.claude/journal/breadcrumbs/...
```

## What `clast` reads (read-only)

```
~/.claude/projects/<segment>/<uuid>.jsonl        # Claude Code's transcript store
~/.claude/projects/<segment>/sessions-index.json # CC's index, when present
~/.claude/history.jsonl                          # CC's global prompt history
```

## What `clast` writes

```
~/.claude/journal/
├── .manifest.jsonl                  # append-only snapshot log
├── projects.json                    # registry (JSONL despite the name)
├── transcripts/
│   └── YYYY-MM-DD/                  # day_bucket of session start
│       └── <segment>/               # original CC-encoded segment, not slug
│           └── <session-uuid>.jsonl
├── entries/
│   └── YYYY-MM-DD-HHMM-<slug>-<session-slug>.md
├── breadcrumbs/
│   └── YYYY-MM-DD-<slug>.md         # or YYYY-MM-DD-_global.md for unscoped
└── cache/                           # derived data, regenerable
```

## Optional config

```
~/.config/clast/config.toml          # day_cutoff, default args, etc. (planned, see reference/config.md)
```

Env-var overrides for all planned config keys exist today: `CLAST_DAY_CUTOFF=04:00`,
`CLAST_JOURNAL_DIR=...`, `CLAST_PROJECTS_DIR=...`.

## Cross-machine considerations

The journal is sync-friendly by design:

- Per-file artifacts (entries, breadcrumbs) have unique names per session/day.
- Manifest is append-only JSONL — concurrent appends from two machines merge
  cleanly with `sort -u` on `session_id`+`captured_at`.
- Registry is JSONL — line-level merges work without conflict.

Recommended sync mechanism is left to the user (syncthing, private git repo,
iCloud). Hostname is recorded in entry frontmatter as `machine: <hostname>` so
cross-machine origin stays visible.

## Library structure

```
lib/clast/
├── clast-lib.bash               # I/O, JSON, date math, path resolution
├── clast-decode-lib.bash        # segment ↔ path decoder with collision logic
├── clast-manifest-lib.bash      # manifest read/append/lookup/dedupe
├── clast-registry-lib.bash      # registry read/write/resolve, alias handling
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

`bin/clast` is a thin dispatcher. Each subcommand exposes a single entry
function (`clast_cmd_<name>`) that the dispatcher invokes after argument
parsing.

See [`reference/repo-bootstrap.md`](../reference/repo-bootstrap.md) for the
full repo layout, packaging, and CI.
