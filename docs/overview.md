# `clast` — Overview

> **This doc is the shared reference for `clast`.** Every other planning doc points back here for definitions, paths, and conventions. Read this first; skim the others as reference material.

`clast` captures, curates, and surfaces Claude Code session history across all of Beau's projects. It ships as two artifacts from one repo:

1. **A CLI** (`clast`) that does all the deterministic, LLM-free work — capturing transcripts, listing projects/sessions, writing curated entries, managing a project registry.
2. **A Claude Code plugin** that ships skills (`/day-wakeup`, `/wakeup`, `/breadcrumb`) wrapping the CLI with LLM curation, plus a `SessionStart` hook that auto-invokes `clast snapshot` so capture happens unattended.

---

## Architecture

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

   /breadcrumb TEXT  ──invokes──► clast breadcrumb TEXT
                                                  │
                                                  ▼
                                  ~/.claude/journal/breadcrumbs/...
```

**Two principles drive the whole design:**

1. **The CLI never calls an LLM.** All summarization happens in skills. If a future feature needs LLM work in the CLI, it gets a separate explicitly-named subcommand.
2. **Capture is automatic; curation is deferred.** Snapshots run unattended (via hook + cron). Curation runs when Beau wants it (typically at start-of-day via `/day-wakeup`).

---

## Glossary

| Term | Meaning |
|---|---|
| **Session** | A single Claude Code conversation. Identified by a UUID. Lives as one `.jsonl` file in `~/.claude/projects/<segment>/<uuid>.jsonl`. |
| **Project** | A logical workspace, usually one git repo. May appear as multiple paths (worktrees, WSL2 vs macOS). Identified by a stable user-chosen **slug**. |
| **Slug** | Short user-chosen identifier for a project, e.g. `xesapps`, `pi-coding-agent`. Used as the human-friendly key throughout `clast`. |
| **Segment** | Claude Code's dash-encoded path under `~/.claude/projects/`. E.g. `/home/beau/code/xesapps` → `-home-beau-code-xesapps`. Beau's CC 2.1.158 uses dash-substitution; older docs referencing a hash scheme are outdated. |
| **Manifest** | `~/.claude/journal/.manifest.jsonl`. Append-only log of every snapshot event. Source of truth for "what has been captured." |
| **Registry** | `~/.claude/journal/projects.json` (line-oriented JSONL despite the name). Maps paths → slugs + remotes + aliases. |
| **Entry** | A curated journal entry. Markdown with YAML frontmatter. Lives in `~/.claude/journal/entries/`. Written by `/day-wakeup` via `clast entries write`. |
| **Breadcrumb** | A one-line in-flight hint left mid-session. Lives in `~/.claude/journal/breadcrumbs/YYYY-MM-DD-<slug>.md`. |
| **Snapshot** | A durable copy of a session's `.jsonl` placed in `~/.claude/journal/transcripts/`. Decouples curation from Claude Code's auto-deletion. |
| **Day bucket** | The local-time date a session is attributed to, computed using the day-cutoff offset (default `04:00`). A session starting at 1am Saturday is in Friday's bucket. |

---

## Filesystem reference

### What `clast` reads (read-only)

```
~/.claude/projects/<segment>/<uuid>.jsonl        # Claude Code's transcript store
~/.claude/projects/<segment>/sessions-index.json # CC's index, when present (newer projects often lack it)
~/.claude/history.jsonl                          # CC's global prompt history
```

### What `clast` writes

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

### Config (optional)

```
~/.config/clast/config.toml          # day_cutoff, default args, etc.
```

Env-var overrides for all config keys: `CLAST_DAY_CUTOFF=04:00`, etc.

---

## CLI subcommand cheatsheet

Full details in [`cli-contract.md`](./cli-contract.md). One-liner each:

| Command | Purpose |
|---|---|
| `clast snapshot` | Capture new transcripts from `~/.claude/projects/` into the journal. Idempotent. Cron-/hook-safe. |
| `clast projects [--day DATE]` | List projects with activity in a window. "Show me projects I worked on yesterday." |
| `clast sessions [--day DATE] [--project SLUG]` | List sessions in a window. |
| `clast show <session-id> [--full]` | Dump session metadata; with `--full`, first/last 5 turns. |
| `clast entries [--day DATE] [--project SLUG] [--tag TAG]` | List curated journal entries. |
| `clast entries write …` | Write a new entry. Invoked by `/day-wakeup` on accept. |
| `clast breadcrumb [--project SLUG] <TEXT>` | Append one-line in-flight hint. |
| `clast registry list \| add \| resolve` | Manage projects registry. |
| `clast stats [--day DATE]` | Token/duration/session-count stats. |
| `clast doctor` | Sanity-check the journal; offer `--fix` for safe issues. |
| `clast whereami` | Debug: what `clast` thinks about current `pwd` and registry state. |
| `clast --version` | Version. |

---

## Plugin surface cheatsheet

Full details in [`skill-prompts.md`](./skill-prompts.md). One-liner each:

| Skill / Hook | Purpose |
|---|---|
| `SessionStart` hook | Backgrounds `clast snapshot` on every session start. Free auto-capture. |
| `/day-wakeup` | Snapshot + iterate yesterday's sessions + draft + prompt to promote + write entries. The one curation point. |
| `/wakeup [project]` | Per-project briefing synthesized from recent entries + today's breadcrumbs. Fast, read-only. |
| `/breadcrumb` *(optional)* | Thin natural-language wrapper over `clast breadcrumb`. May defer to v1.1. |

---

## Conventions

### File formats

- **JSONL**: append-only, one JSON object per line, no trailing comma issues, robust to crashes (lose at most the last partial line). Used for manifest, registry, transcripts.
- **Markdown with YAML frontmatter**: entries and breadcrumbs. Human-readable, greppable, git-friendly.
- **TOML**: user config (one file). Familiar territory.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (including idempotent no-ops) |
| 1 | General error |
| 2 | Invalid arguments / usage error |
| 3 | Missing dependency or environment problem |
| 4 | Data integrity issue (manifest corrupted, etc.) |

### Date handling

- All dates in user-facing CLI output: `YYYY-MM-DD` (ISO 8601 short form).
- All timestamps in JSON output: ISO 8601 UTC with `Z` suffix (e.g., `2026-05-30T14:30:55Z`).
- Day bucketing: local-time date adjusted by `day_cutoff` (default `04:00`). Session starting at 01:30 local on May 31 with default cutoff → `day_bucket: 2026-05-30`.

### Naming

- Subcommands: lowercase, single word (or hyphen-joined). `clast snapshot`, not `clast Snapshot` or `clast take-snapshot`.
- Flags: kebab-case long flags (`--day`, `--project`), single-letter shorts only where unambiguous and well-known (`-v` for verbose, `-h` for help).
- JSON keys: snake_case (`session_id`, `day_bucket`, `project_remote`).

---

## Cross-machine considerations

The journal is sync-friendly by design:
- Per-file artifacts (entries, breadcrumbs) have unique names per session/day.
- Manifest is append-only JSONL — concurrent appends from two machines merge cleanly with `sort -u` on session_id+captured_at.
- Registry is JSONL — line-level merges work without conflict.

Recommended sync mechanism is left to the user (syncthing, private git repo, iCloud). Hostname recorded in entry frontmatter as `machine: <hostname>` to make cross-machine origin visible.

---

## What this design explicitly does not address

- **Anthropic's built-in Session Memory** (Pro/Max API; not on Bedrock/Vertex/Foundry or self-hosted vLLM). Opaque, automatic, not user-curated. `clast` is the user-controlled durable layer; the two coexist without overlap.
- **Web/desktop Claude history.** Different stores, different machines, marginal value for the daily-briefing use case.
- **Semantic search across snapshots.** Out of scope for v1, but the storage layout doesn't preclude it. A future `clast search "bun signing"` is a natural extension since Beau already runs a `nomic-embed` + vLLM stack.

---

## Reference docs

- **[`cli-contract.md`](./cli-contract.md)** — full CLI subcommand contracts, output formats, JSON schemas.
- **[`skill-prompts.md`](./skill-prompts.md)** — SKILL.md content for each skill, LLM prompt templates, AskUserQuestion option sets.
- **[`repo-bootstrap.md`](./repo-bootstrap.md)** — full repo layout, distribution channels, packaging, CI.
- **[`build-steps.md`](./build-steps.md)** — meta-doc for generating self-executing `step-NN.md` build prompts.
