# Data model

The vocabulary you'll see across the CLI, the plugin, and the journal on disk.

| Term | Meaning |
|---|---|
| **Session** | A single Claude Code conversation. Identified by a UUID. Lives as one `.jsonl` file in `~/.claude/projects/<segment>/<uuid>.jsonl`. |
| **Project** | A logical workspace, usually one git repo. May appear as multiple paths (worktrees, WSL2 vs macOS). Identified by a stable user-chosen **slug**. |
| **Slug** | Short user-chosen identifier for a project, e.g. `xesapps`, `pi-coding-agent`. The human-friendly key throughout `clast`. One slug may span several directories (clones / worktrees). |
| **Label** | Per-directory distinguisher within a shared slug, e.g. `dev` / `perf` / `review`. Defaults to the parent directory's basename. Only surfaced when a slug spans more than one directory. |
| **Segment** | Claude Code's dash-encoded path under `~/.claude/projects/`. E.g. `/home/beau/code/xesapps` → `-home-beau-code-xesapps`. Current Claude Code releases use dash-substitution; older docs referencing a hash scheme are outdated. |
| **Manifest** | `~/.claude/journal/.manifest.jsonl`. Append-only log of every snapshot event. Source of truth for "what has been captured." |
| **Registry** | `~/.claude/journal/projects.json` (line-oriented JSONL despite the name). One line per directory, mapping path → slug (+ label, remote). A shared remote groups clones under one slug; an explicit `--slug` always wins. |
| **Snapshot** | A durable copy of a session's `.jsonl` placed in `~/.claude/journal/transcripts/`. Decouples curation from Claude Code's auto-deletion. |
| **Entry** | A curated journal entry. Markdown with YAML frontmatter. Lives in `~/.claude/journal/entries/`. Written by `/wake` via `clast-plumbing entries write`. See [`reference/entry-frontmatter.md`](../reference/entry-frontmatter.md). |
| **Breadcrumb** | A one-line in-flight hint left mid-session. Lives in `~/.claude/journal/breadcrumbs/YYYY-MM-DD-<slug>.md`. |
| **Day bucket** | The local-time date a session is attributed to, computed using the day-cutoff offset (default `04:00`). A session starting at 1am Saturday is in Friday's bucket. |
| **Substantive / no-op session** | Deterministic classification of a session from its transcript, cached on the manifest line (`user_msg_count`, `assistant_msg_count`). A session is **substantive** when Claude replied (`assistant_msg_count > 0`); otherwise it is a **no-op** — an empty session, a slash-command-only session (`/clear`, `/model`, `/config`), or one abandoned before a response. `/wake` auto-dismisses no-op sessions before calling the LLM. See [`reference/cli.md`](../reference/cli.md#manifest-line). |
| **Dismissed** | A session explicitly excluded from `sessions` queries and curation. Recorded in `~/.claude/journal/.dismissed.jsonl` (`session_id`, `dismissed_at`, `reason`). Set manually via `sessions dismiss`, or automatically by `/wake` for no-op sessions; reversible via `sessions undismiss`. |
