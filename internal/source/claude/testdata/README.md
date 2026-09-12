# claude source fixtures

Authored fresh (H4) against the field shapes SOURCES.md §2/§5 records
for Claude Code 2.1.22x (`version` on each entry names the writer
version the shape was verified against; no live journal or transcript
data was copied). Layout mirrors
`${CLAUDE_CONFIG_DIR:-~/.claude}/projects/<cwd-slug>/<uuid>.jsonl`.

- `-home-user-Code-demo/aaaaaaaa-….jsonl` — substantive session: two
  human prompts, two assistant replies, plus every entry shape counting
  must ignore (queue-operations, an attachment, a tool_result carrier,
  an isMeta prompt, an origin-kind:task notification, a system
  turn_duration). gitBranch moves from `main` to `feature/hello`
  mid-session (last one wins).
- `-home-user-Code-demo/bbbbbbbb-….jsonl` — no-op session: one human
  prompt, no assistant entry (M3's auto-dismiss case).
- `-home-user-Code-demo/cccccccc-….jsonl` — live session with a
  half-written trailing line and no final newline (tolerant-parse and
  unterminated-line cases).
- `-home-user-Code-fork/dddddddd-….jsonl` — fork: parent history copied
  verbatim (parent uuids kept, sessionId rewritten), one copied entry
  duplicated to pin M14's dedup-by-entry-id, then the fork's own turn.
- `subagents/` under a session-id directory — sidecar files recapture
  must copy (added with the capture step).
