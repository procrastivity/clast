# devin source fixtures

Fixtures for the `devin` capture source (BDS-264, step-03): small text
SQL inputs a test helper replays into disposable SQLite stores — no
binary database, nothing referenced across repos at test time.

## Producing versions

| Pin | Where it produced evidence |
|---|---|
| refinery schema 16, CLI 3000.4.25 / 3000.6.2–3000.6.7 | terminal-multiplexers `fixtures/devin{,/tierc}` + notes/26/27/59 (ported shapes) |
| refinery schema 17 (`subagent_heads`), CLI 3000.11.3 | live read-only inspection 2026-10-11 — devin-source findings step-01 (field map) + step-02 (contract) |

## Scrub policy

Everything here is **synthesized**: session ids, message_ids, paths,
and all chat_message/tool_call content are fabricated placeholders
(`FIXTURE-OK`, `/home/user/Code/demo`, `11111111-…`-style uuids). The
shapes — column sets, extension keys, id formats, verdict `_meta`
flags — are transcribed from the two pins above; no live transcript
text was copied.

## Store inputs (`stores/*.sql`)

One SQL statement per line, each line ending `;` — a helper reads the
file and executes each line against a temporary database (modernc.org/
sqlite, the v1.34.5 pin in go.mod). Every store below validates:
`main_chain_id` always resolves to a `message_nodes.node_id` of the
same session.

| File | Schema | What it pins |
|---|---|---|
| `stores/basic.sql` | 17 | `quiet-meadow`: complete active chain (system→user→assistant+call→tool result→assistant), abandoned rebuild branch (node 6 copies message_id …003 with `summarized_from`/`compact/prior_node_ids`), tool_call_state completed pair, `is_shell` prompt_history row |
| `stores/edge-cases.sql` | 17 | four sessions: `amber-fox` (SIGINT cancel + user-reject + NULL `tool_call_json` hook-block + malformed chat_message + NULL metadata), `brisk-owl` (reuses node_id 0–2 AND tool_call_id `exec_0` — session-scoped keying proof; unknown `custom_unknown_field`), `hidden-wren` (`hidden=1`), `empty-rook` (NULL main_chain_id, zero nodes, NULL title) |
| `stores/branched-shortened.sql` | 17 | `rapid-lark`: active chain (6→7→10) legitimately shorter than the abandoned 0–5 branch — the recapture-not-a-failure case; subagent sidechain 8→9 + `subagent_heads` row + `<subagent_completion_notification>` node with `subagent/chain_node_id`. `crashed-stork`: main_chain tip is a `tool` node — the SIGKILL shape; `exec:0#0a1b2c` id format; `tool_call_update_json` NULL |
| `stores/schema16.sql` | 16 | `old-pine`: the compatibility floor — same shape minus `subagent_heads`; `call_<24hex>` id era |
| `stores/incompatible.sql` | 5 | `proto-mole`: pre-`tool_call_state` schema (refinery 5), missing required columns — the SourceFailure / `capture.source-unavailable` case |

## Building a store in tests

```
db, _ := sql.Open("sqlite", "file:"+tmp+"/sessions.db")
for each line in the fixture file { db.Exec(line) }
db.Close()  // then open mode=ro the way the source does
```

Concurrent-WAL and mutation scenarios (a writer inserting rows while a
reader's tx is open, a store whose WAL outgrows its main file) build on
these inputs in temporary databases — the fixtures pin row content,
not concurrency.

## Known fixture gaps

- No auto-written `transcripts/<id>.json` ATIF oracle — live ATIF
  files are personal data; synthesized ATIF is deferred to the reader
  steps that need it.
- `cogs_json` content is `[]` everywhere (real rows carry ~27 KB of
  injected-context config; the column's presence is what is pinned).
- No multi-machine / `backend_type` != `windsurf` row (cloud handoff
  stays a research gap).
- `prompt_history` `is_shell` semantics inferred from the flag name —
  no live `is_shell=1` row was observed.
