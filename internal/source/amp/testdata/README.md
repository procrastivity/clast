# amp source fixtures

Fixtures for the `amp` capture source (BDS-262, step-03): pinned inputs
for the parser, the windowed-search enumerator, the completeness check,
and the correlate/install-match rules. Self-contained — nothing here is
referenced across repos at test time.

Amp ships on an hourly build train and the export has **no stable
document-schema version** (the top-level `v` is a per-thread revision
counter), so fixtures pin by *product version + digest*, per
terminal-multiplexers notes/63 §1.

## Producing versions

| Pin | Where it produced evidence |
|---|---|
| `0.0.1788048110-g570348` (2026-08-30) | ported Tier-B/C exports (see per-file table) |
| `0.0.1791504095-g229e99` (2026-10-09) | the field map all synthesized files follow — amp-source findings step-01 (discovery) + step-02 (export/identity/correlation) |

## Scrub policy

- **Ported** files were already scrubbed by the owning probe:
  `env.initial.platform.installationID`, `deviceFingerprint`, and
  `creatorUserID` replaced with `"SCRUBBED"`; repository `sha` scrubbed
  where noted below. Message bodies are probe-generated
  ("Reply with exactly one word: OK") on disposable, since-archived
  threads — no unreviewed personal transcript text.
- **Derived** files are byte-level edits of a ported file (fields
  flipped/dropped) — digests are ours, structure is the server's.
- **Synthesized** files are built from the step-02 field map of the
  2026-10-09 pin; all ids, timestamps, paths, and message bodies are
  fabricated placeholders (`example.com`, `example-org`, `T-01f00000-…`
  synthetic ids). The fixture-local installationID
  `11111111-2222-4333-8444-555555555555` matches `identity/device-id.json`;
  `99999999-…` and `aaaaaaaa-…` are fixture-foreign installs.
- No auth tokens, cookies, or live account data appear anywhere in this
  tree.

## Exports (`threads export <id>` documents)

| File | sha256 (first 16) | Provenance | What it pins |
|---|---|---|---|
| `exports/idle-local-client.json` | `1ea79a869afbc90e` | ported, terminal-multiplexers `fixtures/amp/export-v14.json`, pin g570348 | idle complete export; `lkas.state:"idle"`, `lkas.messageID` == tail assistant pmid; 1-based `messageId`; `protocolMessageID` present; `meta.sentAt` on user msgs (no `createdAt` at this pin); `pinned:false` explicit, `archived` absent (falsy omission) |
| `exports/idle-mode-pinned.json` | `fc00325697437bd8` | ported, `mode-transition-export.json`, pin g570348 | creation-pinned `meta.agentMode`/`usage.model` across later `-m` flags |
| `exports/idle-task-toolresult.json` | `0d74d875eef43672` | ported, `subagent-export.json`, pin g570348 | Task-tool opacity: parent's `tool_result {run:{result,status:"done"}}`, no nested subagent records |
| `exports/idle-mixed-provenance.json` | `0b315dd4b436cf5a` | ported, `collision-export.json`, pin g570348; repo `sha` also scrubbed | TUI-typed msgs (`protocolMessageVersion:1`, real `readAt`) vs injected (`0`, `null`); 10-msg thread, thinking/tool_use/tool_result blocks |
| `exports/midturn-streaming.json` | `df9d9f2306bcdfe2` | **derived** from idle-local-client (dropped tail msg, `v`/`updatedAt` moved) | mid-turn prefix: `lkas.state:"streaming"`, `lkas.messageID` names an in-flight pmid **absent** from `messages[]` — the retry/capture-and-flag case |
| `exports/midturn-tooluse.json` | `852d2850a118e5ca` | **derived** from idle-local-client (msg 4 rewritten as tool_use) | mid-turn `lkas.state:"tool_use"`, `lkas.messageID` == tail pmid, `stopReason:"tool_use"` — pending-approval capture case |
| `exports/idle-archived-pinned.json` | `c63c047a67de413b` | **derived** (archived/pinned `true`, title renamed, `v`/`updatedAt` moved) | metadata-only churn: identical `messages[]` projection ⇒ identical content fingerprint; exercises "stale must not fire on archive/rename" |
| `exports/old-epoch.json` | `b182fca8195d2ec4` | **synthesized** per step-02's old-epoch map (modeled on T-019ca6da) | old epoch: no `protocolMessageID`, 0-based `messageId`, `meta.sentAt` user-only, no per-msg `createdAt`, top-level `nextMessageId`, `meta.traces`, explicit `archived:false`/`pinned:false`, no `executorType`, `trees[].uri` as the only cwd evidence, foreign (darwin) install |
| `exports/virtual.json` | `0d6d9c7962cfea8c` | **synthesized** (modeled on T-01a11278/T-01a0fd6f) | `executorType:"virtual"`: **no `env` key at all**, `meta.puckVariant`, `agentMode:"puck"`, `userState.puckContext`, `createdOnServer:true` — the threads `threads list` drops |
| `exports/sandbox.json` | `19bb6cc1ff5494b9` | **synthesized** (modeled on T-01a11417) | `executorType:"sandbox"`: foreign install, no hostname, `wd=file:///home/user/workspace/repo`, real `trees[].repository.{url,sha}` — correlate must NOT resolve a local clone |
| `exports/foreign-runner.json` | `202a9f9553ae067d` | **synthesized** (modeled on T-01a0d1ba) | `local-client` on another machine: foreign `installationID`, `hostname`, `runnerID`, `wd=file:///homeassistant` — projectless locally |
| `exports/edge-empty.json` | `50b44f668ff528c1` | ported, prior-art `live-feed/synthetic/stale-partial-export.json` (pin gab1719-era; itself marked synthetic upstream) | minimal/empty document: `messages:[]`, no `env`/`title`/`creatorUserID`/lkas — the "0 messages for a non-empty thread" retry trigger |

## Search pages (`threads search <query> -n 100 --json`)

Row shape pinned by step-01: bare array of `{id, title, updatedAt}`,
ordered most-recent-update first; **no** count, cursor, or truncation
marker. All synthesized (real pages carry personal thread titles).

| File | sha256 (first 16) | What it pins |
|---|---|---|
| `search/window-2026-09-28.json` | `fb3f829cc628cba6` | one day window `after:2026-09-28 before:2026-09-29` (3 rows, desc) |
| `search/window-2026-09-29.json` | `0c5677bedfb9845e` | the adjacent window — complements with zero overlap, zero gap |
| `search/window-empty.json` | `37517e5f3dc66819` | an empty window (`[]`) — sparse periods are normal |
| `search/boundary-100.json` | `ff6ca629179298bb` | exactly 100 rows — the silent cap; indistinguishable from complete ⇒ must subdivide |
| `search/subdivide-100-archived-false.json` | `88312020264869ec` | the same 100 ids split `archived:false` (70 rows) |
| `search/subdivide-100-archived-true.json` | `5165febd57e02896` | …and `archived:true` (30 rows); union == the capped set |

## List page (`threads list --include-archived --limit 500 --json`)

| File | sha256 (first 16) | What it pins |
|---|---|---|
| `list/page.json` | `7a431cc3750a6c79` | row shape `{id, title, updated, tree, messageCount}`; synthesized — note there is no `archived`/`pinned`/visibility field and `updated` is the lagging `lastUserMessageAt` semantic, never authoritative |
| `list/tail-empty.json` | `37517e5f3dc66819` | `--offset` past the tail returns `[]` |

## Identity

| File | sha256 (first 16) | What it pins |
|---|---|---|
| `identity/device-id.json` | `74b8692c4f329dfa` | `~/.local/share/amp/device-id.json` shape — `{"installationID": <uuid>}` (real shape verified 2026-10-09; value is the fixture-local uuid) |

## Errors (stderr text; transcribed from step-01/02 findings, not byte-captured)

| File | sha256 (first 16) | What it pins |
|---|---|---|
| `errors/export-nonexistent.txt` | `add653c2b070215e` | `threads export` of a nonexistent/deleted id → exit 1, "Thread … does not exist." — per-item Diagnostic, not retryable |
| `errors/export-malformed-id.txt` | `9908f9b1a30d9880` | malformed id → exit 1, "Error: Invalid thread URL or ID: …" |
| `errors/offline.txt` | `86dd80b2a10a604a` | transport down → exit 1, "Cannot reach Amp servers" (notes/62 §2) — a source-level Discover error, never a quiet empty |

## Probe commands that produced this evidence

Read-only probes run on the owner's account (beau@beausimensen.com),
amp `0.0.1791504095-g229e99`, 2026-10-09 — full detail in amp-source
findings step-01/step-02:

```
amp --version
amp account list
amp threads list --include-archived --limit 500 --json
amp threads search 'after:2026-09-28 before:2026-09-29' -n 100 --json
amp threads search 'archived:true' -n 100 --json
amp threads export <thread-id>            # one JSON doc on stdout, exit 0
cat ~/.local/share/amp/device-id.json     # {"installationID": "<uuid>"}
```

## Known fixture gaps

- No real mid-turn or foreign-install capture: the derived/synthesized
  files stand in until a disposable-thread probe is authorized.
- No real `search` page bytes (titles are personal data) — shapes are
  pinned, not server bytes.
- Old-epoch `meta.status` value is guessed (`"idle"`); only the field's
  presence is pinned.
