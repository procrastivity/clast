# `devin-source` — decisions this Matter made

This file records the calls that building the Devin capture source
forced. The wip Matter `devin-source` (Linear BDS-264, under BDS-201)
set the intent; the sealed `source-capture-policy` Matter (BDS-261)
supplied the selection predicate, explicit-request authorization, the
absent-versus-failed distinction, and the outcome/exit table, and the
`amp-source` Matter (BDS-262) supplied the seams this source reuses.
Everything storage-, schema-, and export-shaped below is what this
Matter owned.

Status: implemented and dogfooded — the live store is refinery schema
**17** under CLI **3000.11.3** (inspected read-only 2026-10-11, no
Devin session launched; historical pins are schema 16 / 3000.4.25 and
3000.6.x). Schema support floor is **v14** (the `tool_call_state`
column set); the fixture matrix carries the schema-16 boundary case.
Fixtures live under `internal/source/devin/testdata/` with a
provenance manifest. The live dogfood that sealed the Matter is under
"Validation recipes" below.

## The store, qualified

`${XDG_DATA_HOME:-~/.local/share}/devin/cli/sessions.db` (WAL;
`XDG_DATA_HOME` relocates it, `--config` relocates the data dir,
Windows is `%APPDATA%\devin\cli\`). No user-facing path option exists —
a test path override stays a code seam only.

- `sessions.id` is a human slug (`confirmed-soprano`), not a uuid —
  M11's "no uuid assumptions" is load-bearing here.
- `working_directory` is the correlation column; `created_at`/
  `last_activity_at` are unix **seconds**; `main_chain_id` is a
  `message_nodes.node_id` of the same session (0 violations in 85 live
  sessions) and is **not** `MAX(node_id)` (3 live counterexamples).
- `message_nodes` is a per-session **forest**: `node_id` space starts
  at 0 per session, `UNIQUE(session_id,node_id)`, `parent_node_id`
  NULL = root, 4–137 roots observed per session. `chat_message` is
  native JSON (`message_id` is stable identity; `node_id` is only a
  position — 13,690 duplicated `(session,message_id)` pairs exist via
  context-rebuild copies, always as siblings: 0 ancestor-descendant
  pairs and 0 duplicates on one active chain, verified live).
- `tool_call_state(session_id, tool_call_id)` holds serialized ACP
  `ToolCall`/`ToolCallUpdate` — the verdict evidence
  (`_meta["cognition.ai/rejected"|"canceled"]`, `status`,
  `inferenceToolName`). `tool_call_id` formats vary by era and collide
  across sessions (`exec_0` in 16 live sessions): session-scoped keys
  are the only safe keys, always.
- `subagent_heads` (schema 17) tracks in-flight subagent chain heads;
  subagent conversations are separate chains **inside the same forest**,
  linked by `subagent/agent_id` and `subagent/chain_node_id` metadata.
- `prompt_history` is input-line history (`is_shell` marks `!` lines);
  `rendered_commits`, `app_state` are UI/global state. Session
  metadata, `cogs_json`, `hidden`, `workspace_dirs` ride verbatim in
  the sessions row.

## One read transaction is the snapshot

`modernc.org/sqlite` v1.34.5 (duo's proven pin, pure Go — the
single-static-binary promise holds, `CGO_ENABLED=0` builds unaffected),
opened `file:<path>?mode=ro`. `mode=ro` can never create a missing
database and reads through the WAL — a read transaction sees a
consistent snapshot including committed `-wal` frames. `immutable=1`
is banned on the live store (the WAL moved 133 MB during inspection);
absent `-shm` with live `-wal` (post-crash state) fails open →
store-unreadable, a named limit.

Each `Capture` runs `BeginTx(ctx, ReadOnly)` and reads the sessions
row, `message_nodes`, `tool_call_state`, and optional tables inside it
— one snapshot makes session row, forest, and tool state mutually
consistent. `PRAGMA busy_timeout=2000` covers the rare reader lock;
every query takes `ctx`; handles roll back and close like duo's
reader. The export streams through an `io.Pipe` — one row in flight,
no whole-document buffer.

Schema gate: the required shape is the v14+ column set (sessions core
columns, `message_nodes`, `tool_call_state`, `prompt_history`); a
missing required table/column is an **incompatible store** —
`SourceFailure` on sweeps (disclosed, exit 0), `capture.
source-unavailable` on explicit `--harness` (exit 1). `subagent_heads`
is optional (schema ≥17): absent exports an empty section. A
per-session anomaly (vanished row, malformed `chat_message`, broken
chain) is a per-item `Diagnostic`, never a source failure.

## The artifact is the whole forest, the conversation is one chain

`transcript.json` (the `transcript.artifact` amendment name), format
`devin-export-v1`. One deterministic JSON document: header `{format,
store_schema_version, session_id}` then `session` (full row),
`message_nodes` (ORDER BY `node_id`), `tool_call_state` (ORDER BY
`tool_call_id`), `prompt_history` (ORDER BY `id`), `subagent_heads`
(ORDER BY `agent_id`). Columns verbatim — TEXT stays a JSON string so
native JSON text (`chat_message`, `metadata`, `cogs_json`, the ACP
payloads) is byte-preserved and malformed content can never fail the
export; INTEGER stays a number; NULL stays null. No timestamps or
paths in the document — identical store content produces byte-
identical exports under any insertion order.

The active chain is derived, never scanned: walk `main_chain_id` →
`parent_node_id` to the NULL root, reverse for display. Missing
parent → keep the walked prefix and flag `Incomplete`; repeat
`node_id` → break, same flag. Readers pair `assistant.tool_calls[]`
with `role:"tool"` nodes by `tool_call_id`; `tool_call_update_json.
status` is the authoritative verdict, `tool_call_json.rawInput` +
`inferenceToolName` fill gaps, and an update-only row (no tool node)
synthesizes `tool-result:<id>`. Off-chain `tool_call_state` rows are
forest evidence only — they never render (duo's abandoned-branch
rule). `user` counts only `is_user_input` truthy (auto-"continue" and
compaction payloads are not human turns); `assistant` counts every
on-chain assistant message including `cache_keepalive`; `Substantive`
= assistant ≥ 1.

## Freshness is the fingerprint, recomputed — never a proxy

`Transcript.Lines` = exported `message_nodes` count. `Transcript.
SHA256` covers exactly the evidence whose change makes the committed
artifact a wrong description of the session:

- `sessions.main_chain_id` — which chain **is** the conversation; a
  head move over identical rows (rewind, branch switch, legitimate
  shortening) is a different conversation.
- `sessions.working_directory` — the correlation evidence the
  artifact pins.
- every `message_nodes` row and every `tool_call_state` row of the
  session, marshaled exactly as the export emits them.

Deliberately excluded: title, `hidden`, `cogs_json`, model,
`agent_mode`, `sessions.metadata`, activity timestamps,
`prompt_history`, `subagent_heads` — bookkeeping churn must never
mark a curated transcript stale. The same tag-prefixed projection is
hashed at capture time over the streamed export and recomputed at
recapture time, so the verdict is the artifact's own evidence and is
reproducible offline from `transcript.json` alone.

`Unchanged` implements the optional seam (required — a `dbPath#id`
address can never use the file-tail default): inside one read-only
tx, missing session row → changed; `message_nodes` count ≠
`prior.Transcript.Lines` → changed (a cheap early-out the hash
implies); recomputed sha256 = `prior.Transcript.SHA256` → unchanged;
anything else → changed. No timestamp, count, mtime, or node-id
shortcut is claimed — each can prove a change happened, none can
prove one did not. `Supersede` is **not** implemented — the one-tx
snapshot cannot produce a torn export, legitimate chain shortening
must succeed (rewinds are real), and every identified failure mode
lands upstream as error or diagnostic.

Hidden sessions are enumerated and captured (hidden is `devin list`
UI filtering, not deletion). A session `/rm-session`'d mid-sweep is a
per-item diagnostic; the committed journal record stands — no path
deletes local history. Orphan `transcripts/<id>.json` ATIF files are
ignored (evidence showed they can be stale or sessionless); the
database is the only capture source.

## Validation recipes

- Fixtures (`internal/source/devin/testdata/stores/*.sql`, one
  statement per line) replay into temporary databases — basic,
  edge-cases (shared ids, hidden, empty, interrupted), branched-
  shortened, schema-16 floor, incompatible. Concurrent-WAL and
  mutation scenarios build on them in temp DBs.
- The 2026-10-11 live validation this contract rests on: an
  independently walked active chain matched the session's own ATIF
  1:1 (292 nodes = 24 system + 5 user + 127 assistant + 136 tool
  vs 156 ATIF steps + 136 `observation.results`); `exec_0`-era id
  reuse across 16 sessions; a stale auto-transcript missing 85
  on-chain results; an orphan transcript with no session row.
- The 2026-10-11 live dogfood (built binary, `--harness devin`,
  `CLAST_JOURNAL_DIR` into a temp root, real `XDG_DATA_HOME`; store
  opened `mode=ro`, never written): **85/85 sessions** captured from
  a 515 MB store with a 133 MB live WAL (45,470 `message_nodes`,
  6,980 `tool_call_state`, schema 17) in ~18 s with zero diagnostics;
  a follow-up sweep took ~3.6 s and recaptured exactly one session —
  the one actively growing during the run — proving per-session
  fingerprint selectivity on live data. `plumbing sessions`,
  `show --transcript` (prompt/assistant prose), and `analyze --out`
  all rendered real sessions; `tool_call_state` exports carried full
  ACP payloads (edit diffs included). With `XDG_DATA_HOME` pointed at
  an empty dir — the store made unavailable — the same sessions,
  transcript, analyze, and `plumbing curate` all worked from the
  journal alone, and a bare sweep stayed quiet on Devin (0 captured,
  no diagnostics, healthy sources unaffected).
- Ordinary tests must not touch sibling repositories or personal
  stores (the fixture tree is self-contained per S5). The dogfood
  journal was disposable; no personal session data was committed.

## Named limitations

- Subagent conversations are chains inside the session forest and
  land in the export (`subagent_heads` section + forest rows), but
  the structured reader does not yet walk them per-subagent —
  `ListSubagents` returns empty by design. Evidence is preserved;
  per-subagent rendering is a documented gap.
- `prompt_history` and `subagent_heads` ride the artifact but are
  outside the fingerprint — changing them alone never stales a
  transcript. That is the bookkeeping-churn rule applied
  consistently; if a future consumer needs prompt-history
  freshness, the projection is the seam to extend.
- A post-crash store with a live `-wal` but missing `-shm` fails to
  open read-only — store-unreadable, disclosed as a source failure,
  not data loss.
- Artifact and `session.json` are not one commit — the crash window
  (artifact landed, record missing) converges on the next sweep via
  `journal.Walk`'s missing-`session.json` diagnostic, never silently.
- `Unchanged` pays one full conversation read per offered session
  (a whole-sweep no-op over 85 live sessions measured ~3.6 s).
  Correctness won over an
  activity-time shortcut; if scale demands it, a *positive-proof*
  cache (e.g. SQLite `PRAGMA data_version` is a session-scope dead
  end — per-table change tracking would be required) can be added
  without weakening the verdict.
