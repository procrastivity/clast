# `pi-source` — decisions this Matter made

This file records the calls that building the Pi capture source forced.
The wip Matter `pi-source` (under BDS-201, `pi-devin-sources` in
HANDOFF §4 item 1) set the intent; the sealed `source-capture-policy`
Matter (BDS-261, `docs/source-capture-policy/decisions.md`) supplies
the selection predicate, the absent-versus-failed distinction, and the
outcome/exit table. Pi is a local `source.FileTail` source modeled on
`internal/source/claude`; `internal/source/amp` (network) and
`internal/source/devin` (SQLite) are not models for it.

Status: step-01 (qualification) done; contract (step-02) not started.

## Qualification evidence (step-01)

### Versions examined

- **Installed Pi: 0.84.4** (`pi --version`), package
  `@earendil-works/pi-coding-agent` under
  `~/.npm-global/lib/node_modules/`, CHANGELOG date 2026-08-28. Below,
  `pi/` means that package root. The authoritative writer is
  `pi/dist/core/session-manager.js`. The shipped bundle
  (`pi/dist/bundle/chunks/chunk-OMWWHBTG.js`) was grepped and carries
  the same constant: `CURRENT_SESSION_VERSION=3`.
- **Historical pin: 0.83.0 / JSONL v3**, from
  `terminal-multiplexers/notes/06-pi.md` (2026-08-08) and
  `notes/18-opencode-pi-refresh.md` (2026-08-23), and the fixtures in
  `terminal-multiplexers/apps/transcript-tail/fixtures/pi/` and
  `duo/internal/runtime/pi/testdata/` (provenance README there).
- **Live retained store**, read-only, 2026-10-13: 31 files in 22
  per-cwd directories under `~/.pi/agent/sessions`, files dated
  2026-08-03 .. 2026-09-15 (spanning 0.83.x and 0.84.x writers). The
  header does **not** record the writing Pi version, so a given file
  cannot be pinned to a writer version (named gap, below). Three of
  the live files are byte-identical to the duo fixtures (`cmp`), so the
  prior-art fork/resume pair is also live data.
- No Pi session was launched, no file under `~/.pi` was written.

### Storage roots and overrides

Default: `~/.pi/agent/sessions/<encoded-cwd>/<file>.jsonl`.

Precedence of overrides in 0.84.4 (highest first):

1. `--session-dir <dir>` (`pi/dist/cli/args.js:88`, `main.js:530-532`).
2. env `PI_CODING_AGENT_SESSION_DIR`
   (`pi/dist/config.js:406`, `main.js:530`; `pi/docs/environment-variables.md:82`).
3. `sessionDir` in settings.json (`pi/dist/core/settings-manager.js:451`;
   `pi/docs/settings.md:250-256`). Global `~/.pi/agent/settings.json` or
   a project `.pi/settings.json`; the live global file sets none.
4. Default `<agentDir>/sessions` (`config.js:456`), where `agentDir` is
   env `PI_CODING_AGENT_DIR` if set, else `~/.pi/agent`
   (`config.js:405,421-425`). `PI_CODING_AGENT_DIR` also moves
   `settings.json`, `auth.json`, extensions, and so on.
5. `--no-session` writes no file at all (`pi/docs/usage.md:83`).

Env in the qualification shell had no `PI_*` variable. Relative paths
resolve lexically against the process cwd (`utils/paths.js:82-86`);
`~` expands.

**Layout is not the same under every override.** The default creates one
subdirectory per cwd. With any `sessionDir` override, `SessionManager`
writes files **flat** into that directory
(`session-manager.js:669,1100,1259`, `join(this.getSessionDir(), ...)`),
and mixed cwds share it; `list` and `continueRecent` then filter by the
cwd in the header (`session-manager.js:1219,1286`, `sessionCwdMatches`).
`listAll(customDir)` reads only that directory, not subdirectories
(`session-manager.js:1293-1295`). The default-root `listAll` reads
`*.jsonl` one level below `sessions/` and also follows symlinked
directories (`:1307-1314`).

Reboot SOURCES §3 says `${PI_CODING_AGENT_SESSION_DIR:-~/.pi/agent/sessions}`.
That is incomplete: `PI_CODING_AGENT_DIR`, the settings key, and the
flag all relocate the store too (surprise 1, below).

**Directory name.** `--` + cwd with the leading separator removed and
every `/`, `\`, `:` replaced by `-` + `--`
(`session-manager.js:242-247`, `getDefaultSessionDirPath`). The cwd is
`resolvePath`ed: lexical only, symlinks are **not** resolved. The
encoding is lossy (`/a-b` and `/a/b` both give `--a-b--`; `.` and
other characters are kept). The header `cwd` is the only trustworthy
cwd. Live check: all 31 directory names equal the encoding of their
header `cwd`.

**File name.** `<ISO-ts with ':' and '.' replaced by '-'>_<id>.jsonl`,
for example `2026-08-08T18-52-22-125Z_<uuidv7>.jsonl`
(`session-manager.js:668-669`). 31/31 live files: filename timestamp =
header `timestamp`, filename id = header `id`. The default id is a
uuidv7 (`:12-14`), but `--session-id <id>` / `newSession({id})` accept
any `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$` string
(`assertValidSessionId`, `:15-20`). Split the filename at the **first**
`_` (the timestamp never contains one; a custom id may). Session ids
are not guaranteed uuid-shaped (M11), and they are not guaranteed
unique across files (see import and fork below).

**What else lives in the sessions tree.** Live: only the per-cwd
directories and `.jsonl` files (`find -type f ! -name '*.jsonl'` is
empty). Pi reads only `*.jsonl` (`:402,557,1314`). Large bash output is spilled to a temp file referenced by
`fullOutputPath` on `bashExecution` (`bash-executor.js:83`), not stored
in the tree. No sidecar, lock, or index file is
used by the session manager. Other `~/.pi/agent` content (settings,
auth, models-store, extensions, skills, npm, bin) is not session data
and must never be read by capture (`auth.json` holds credentials).

**Tooling hazard.** Every directory name starts with `--`, so
`ls <dir>`, `cat <dir>/*`, `jq ... <dir>/*` parse it as options. Use a
`./` prefix or `--`. (Hit during this step; Go code is unaffected.)

### Header (line 1) field map

Live headers: 31/31 `{"type":"session","version":3,...}`.

| Field | Type | Always | Meaning / evidence |
|---|---|---|---|
| `type` | `"session"` | yes | First parsed line must be this and carry a string `id`, else Pi treats the file as not-a-session (`:315-317`, `:327-336`) |
| `version` | int | yes (live) | `3` in 31/31 live files and in `CURRENT_SESSION_VERSION` (`:11`). Absent means v1 (`:77`) |
| `id` | string | yes | Session id. Equals the filename id 31/31 |
| `timestamp` | ISO-8601 string | yes | Creation time of this **file** (forks: fork time, not the history's start). Equals filename ts 31/31 |
| `cwd` | string | yes (live) | Session working directory, lexically resolved. Pi tolerates a missing cwd on old files (`getSessionHeaderCwd`, `:389-392`) |
| `parentSession` | string path | only forks | Absolute path of the source file. Key is **absent** (not null) otherwise (1/31 live) |

No Pi version, no model, no branch name in the header. The header has
no `id`/`parentId` and is not part of the tree.

### Entry-type and message-role table

The 0.84.4 writer's entry union (`session-manager.d.ts:24-98`,
`session-manager.js` append methods at `:769-935,1055-1078`) has nine
types. Every non-header entry has `type`, `id` (8 hex, `generateId`
`:21-30`, full uuid only on 100 collisions), `parentId` (string or
null) and `timestamp` (ISO string).

| `type` | Extra fields | Live count (3047 entries) | Notes |
|---|---|---|---|
| `message` | `message` (AgentMessage) | 2948 | the workhorse; roles below |
| `model_change` | `provider`, `modelId` | 42 | at session start and on switch; not on resume |
| `thinking_level_change` | `thinkingLevel` | 55 | same |
| `compaction` | `summary`, `firstKeptEntryId`, `tokensBefore`, `details`, `usage`, `fromHook` | 1 | does **not** truncate the file (see below) |
| `session_info` | `name` | 1 | latest wins; empty string clears (`getSessionName`) |
| `branch_summary` | `fromId`, `summary`, `details?`, `usage?`, `fromHook?` | 0 | source only |
| `custom` | `customType`, `data` | 0 | extension state, not in LLM context; source only |
| `custom_message` | `customType`, `content`, `display`, `details?` | 0 | extension message, in LLM context; source only |
| `label` | `targetId`, `label` | 0 | bookmark on another entry; source only |

Types named in the historical note that the 0.84.4 coding agent does
**not** write: `leaf` and `active_tools_change`. They exist only in the
bundled `pi-agent-core` v4 harness types
(`pi/node_modules/@earendil-works/pi-agent-core/dist/harness/session/types.d.ts`),
not in `SessionManager` (`grep` over `pi/dist/core`: none). A capture
source must treat any unknown `type` as opaque and keep the bytes.

`message.role` values:

| `message.role` | Live count | Fields (live key sets) | Notes |
|---|---|---|---|
| `user` | 79 | `role, content, timestamp` | `content` is an array of blocks in 79/79 live files (a string is legal in the type) |
| `assistant` | 1261 | `role, content[], api, provider, model, usage, stopReason, timestamp` + optional `responseId`, `rawStopReason`, `errorMessage`, `diagnostics` | blocks: `thinking` (1209), `text` (76), `toolCall` (1609: `id, name, arguments, type`) |
| `toolResult` | 1608 | `role, toolCallId, toolName, content, isError, timestamp` + optional `details` (398) | own role; **not** a user content block |
| `bashExecution` | 0 | `command, output, exitCode, cancelled, truncated, ...` | `!` command messages; type-only evidence (`docs/session-format.md`) |
| `custom` | 0 | `customType, content, display, details` | v3 rename of `hookMessage` (`:56-67`) |
| `branchSummary`, `compactionSummary` | 0 | | synthesized for LLM context, not stored as `message` entries (`appendMessage` doc, `:762-767`) |

`stopReason` live: `toolUse` 1182, `stop` 74, `aborted` 3, `error` 2.
`aborted`/`error` assistants carry `errorMessage` and may have zero
content blocks.

Where time lives: entry `timestamp` (ISO string, every entry) and
`message.timestamp` (unix milliseconds, number, 2948/2948 messages).
Header `timestamp` is file creation. Entry timestamps were monotone
non-decreasing within all 31 files. In a fork, copied entries keep their
original (older) timestamps, so first-entry time can be **earlier** than
the header time (live fork: -44 s).

### What counts turns, messages, and tool calls

Candidate mapping (the contract choice is step-02; this is what the
evidence supports):

- User turns: `type=="message" && message.role=="user"`. No other
  user-authored kind exists (`custom_message` is extension-injected;
  `bashExecution` is a shell command).
- Assistant messages: `message.role=="assistant"`. One assistant
  message is one model response, not one turn: a tool-using turn is
  many assistant entries (1182 `toolUse` assistants vs 79 user
  messages live).
- Tool calls: `toolCall` blocks inside assistant `content`; results are
  separate `toolResult` entries. Pairing key is exact string equality of
  `toolCall.id` and `toolResult.toolCallId` (ids contain `|`, for
  example `call_x|fc_y`). Live: 1609 calls, 1608 results, 0 results
  without a call, 1 call without a result (a file ending on an
  interrupted `toolUse` assistant message).
- No explicit turn-end marker. A turn ends at an assistant message with
  `stopReason != "toolUse"` (note 06).
- Pi's own `messageCount` counts **all** `type=="message"` entries of
  any role (`buildSessionInfo`, `:442-515`), and Pi's own "last
  activity" is the max of user/assistant `message.timestamp` falling
  back to header time (`getMessageActivityTime`, `:429-441`). Neither is a clast fact; do
  not copy them without a decision.

### id / parentId tree and branch semantics

- Entry ids are random 8 hex characters, unique inside one file
  (0 duplicate ids in 31 files). `parentId` is the previous leaf at
  append time; the first entry has `parentId: null`. A well-formed file
  has one root; orphans are tolerated as extra roots (`getTree`,
  `:990-1030`).
- **The leaf is not stored.** Pi keeps a leaf pointer in memory only.
  On load the leaf is the **last parsed non-header entry in the file**
  (`_buildIndex`, `:673-693`: `leafId = entry.id` for every entry). The
  current branch is the `parentId` walk from that last entry to the root
  (`getBranch` `:945-957`, `buildSessionPath` `:124-137`). No `leaf`
  entry is written by this version.
- Branching **inside one file** (`/tree`): `branch(id)` only moves the
  in-memory leaf (`:1036-1042`); the next append becomes a child of an
  earlier entry (`parentId` != previous line's id), with
  `resetLeaf()` giving a second root (`parentId: null`, `:1047`).
  `branchWithSummary` appends a `branch_summary` whose `parentId` is
  the new branch point (`:1055-1078`). Nothing is rewritten; abandoned
  branches stay in the file. Because the leaf is "last line", the
  active branch is always the one most recently appended to.
- Branching **to a new file** (`/fork`, `/clone`):
  `createBranchedSession(leafId)` writes a **new** file holding only the
  root-to-leaf path (not abandoned branches), same entry ids, new header
  id, `parentSession` = previous file (`:1080-1178`; runtime
  `agent-session-runtime.js:195-245`). Entries of type `label` on the
  path are dropped and re-appended at the end, and the retained path is
  re-chained, so `parentId` values can differ from the source when a
  label sat on the path (`:1086-1100`).
- Branching **by CLI** (`pi --fork <path|id>`): `forkFrom` writes a
  new header (new id, target cwd, `parentSession`), then copies **every**
  source entry verbatim, abandoned branches included (`:1237-1275`).

Counting hazard: a count over all `message` entries counts abandoned
branches; a count over the leaf path does not. On a file with no
in-file branching (all 31 live files: `parentId` always equals the
previous line's id) the two agree.

### Resume behavior

- `--continue`, `--resume`, `--session`, and import all end in
  `SessionManager.open` and then **append to the same file**
  (`:1189-1213`, `:1217-1227`). No new header, no new
  `model_change` / `thinking_level_change`, the `parentId` chain
  continues from the last line, header `timestamp` stays the creation
  time (`06-pi.md` resume section; confirmed in the fixture).
- Live resume evidence (supported):
  `basic-with-resume_2026-08-08T18-52-22-125Z_...` (8 entries, 2 user
  messages; the second prompt was a `pi -c -p` run per the fixture
  README at 0.83.0): 1 header, no second header, chain linear, second
  `user` entry's `parentId` = previous assistant entry. Also, the
  `2026-08-05T03-03-17-816Z_...` file under `--home-dev-Code-wip--`
  has header time 2026-08-05 03:03 and last entry 2026-08-07 03:21 with
  a 2-day gap, mtime = last entry; chain linear. That is consistent with
  continuation, but it cannot distinguish a reopen from one idle
  process, so it is weaker evidence than the fixture.
- `--session-id <id>` with an existing id in the project opens it
  (`main.js:278-345`); with a missing id it creates a file.
- `import` (`importFromJsonl`, `agent-session-runtime.js:258-282`)
  `copyFileSync`s an external file into the session directory **keeping
  the original header id and entry ids**. Two files with the same
  header id can therefore exist. No live example.

### Append vs rewrite vs truncation

Writes by the 0.84.4 `SessionManager` to a session file:

| Operation | Mechanism | Rewrites existing bytes? |
|---|---|---|
| First persist (when the first `assistant` message arrives) | `openSync(file, "wx")` + one `writeFileSync` per entry for everything so far (`:726-756`) | No (new file). **No file exists before the first assistant message**; `--no-session` never writes |
| Every later entry | `appendFileSync(file, JSON.stringify(entry)+"\n")`, one call per entry (`:731-733,752-753`) | No |
| Open of a file lacking a trailing newline | `appendFileSync(file, "\n")` (`:318-319`; 0.84.3 fix for corrupted next append, CHANGELOG #8345) | Appends one byte |
| Open of a file with header version < 3 | `migrateToCurrentVersion` then `_rewriteFile` = `openSync(file, "w")` (truncate) and rewrite all entries (`:75-85,626-631,695-707`) | **Yes, in place, non-atomic.** v1 gets new random entry ids; v2 role `hookMessage` becomes `custom` |
| Open of a zero-byte file | `_rewriteFile` writes a fresh header (`:607-619`) | Yes (file was empty) |
| `createBranchedSession`, `forkFrom` | write a **new** file (`_rewriteFile` on the new path, or `wx` + appends) | No existing file touched |
| Compaction | appends one `compaction` entry | No. Live: 190 pre-compaction entries still in the file, 108 after, `firstKeptEntryId` points into the retained earlier region |
| Deleting a session | deletes the file (`trash` CLI if present; `docs/session-format.md`) | file disappears |

So for v3 files the model is **append-only with whole-line writes**
(note 06: open/append/close per entry, one `write` per entry; entries
appear only when complete, never streamed). Truncation or rewrite of an
existing v3 file was not observed and is not in the 0.84.4 code paths
except the two exceptions above (pre-v3 migration; empty file).

Torn tail: Pi's own reader skips unparseable lines silently
(`parseSessionEntryLine`, `:265-275`; `loadEntriesFromFile`, `:277-324`) and does not stop at them.
A last line without `\n` is parsed if valid, otherwise dropped from the
in-memory view; either way `loadEntriesFromFile` appends a newline to
the file on open (`:318-320`). Consequences for a verbatim copier: a
copy taken mid-write can end with a partial line; the bytes Pi later
writes after repairing it differ from what the copy ended with only by
that appended `\n` plus new whole lines, so a prefix copy stays a prefix
of the final file unless the partial line was the **last** thing and Pi
repaired it. All 31 live files end in `\n` and have 0 unparseable lines.
No live torn tail exists; torn-tail handling is therefore source-code
evidence only.

### Version differences, 0.83.0 evidence vs 0.84.4

- Header version: unchanged, 3. Writer constants unchanged.
- Pi 0.84.0 introduced a **v4 lane-based harness session API**
  (`JsonlSessionRepo`, `SessionStorage`; CHANGELOG 0.84.0, lines
  294-298) in the bundled `pi-agent-core`. It is a library export. The
  shipped CLI bundle defines the class but contains no
  `new JsonlSessionRepo` and `SessionManager` still writes v3
  (`grep` of `pi/dist/bundle`). A v4 file reaching the same directory
  is a named risk, not an observed fact (see blockers).
- 0.84.3/0.84.4: resume of a file lacking a trailing newline no longer
  corrupts the next appended entry (#8345). Older 0.83.x writers could
  therefore leave a glued-line defect (two JSON values on one line =
  an unparseable line). Not observed in the 31 live files.
- Optional additions seen in live 0.84-era data and absent from the
  0.83.0 note: `diagnostics` on one assistant message, `usage` on the
  `compaction` entry (CHANGELOG 0.81.0 added usage accounting for
  compaction and branch summaries), `details` on `toolResult` (note 06
  listed the toolResult key set without it, but the type allows it).
  All optional; none changes the mapping.
- `usage` objects: `reasoning` field present in 1256/1261 assistants;
  5 assistants lack it (older writer). Treat as optional.
- Header, filename, `toolResult` role, `id`/`parentId`, fork-copy and
  resume-append behavior all match the 0.83.0 facts.

### Validation done

All commands are repeatable; scratch lives in
`/tmp/claude-1000/pi-step01-scratch` (not in the repo). No prompt
content was copied anywhere; only key names, counts, and ids were read.

1. **Independent double parse.** A throwaway Go single-pass scanner
   (`gomap/main.go`: header id/cwd/parentSession/version, entry types,
   user/assistant/toolResult counts, toolCall pairing by id, root count,
   leaf-path length via `parentId` walk from the last entry) was run on
   all 31 live files plus the four prior-art fixtures
   (`transcript-tail/fixtures/pi/{basic-with-resume,forked,real-prefix}`
   and `duo/.../injected-abort`). A separate `jq -s` pipeline computed
   entries, user, assistant, toolResult, and toolCall counts. Results
   agree exactly: live 3047 entries, 79 user, 1261 assistant, 1608
   toolResult, 1609 toolCall; per fixture 8/10/19/8 entries and
   2/3/1/2 user, 3/4/5/3 assistant, 1/1/11/1 toolResult, 1/1/11/1 calls.
   A Python structural survey (`survey.py`) found for 31/31 files: one
   root, 0 orphan parents, 0 duplicate ids, parent precedes child,
   `parentId` = previous line's id on every line (no in-file branch),
   timestamps monotone, trailing newline, 0 bad lines.
2. **(a) Resume.** `basic-with-resume` (also live): 2 user entries in one
   file, one header, chain linear, no second `model_change`. Go scan:
   `user=2 asst=3 roots=1 leafpath=8`.
3. **(b) Fork with shared ids.** `forked_..._019fe2b8-be63` vs
   `basic-with-resume_..._019fe2b8-12ed`: the parent's 8 entry lines are
   byte-identical in the fork (`tail -n +2 | md5sum` equal), the 8 entry
   ids are shared, the 2 new entries are unique, the header ids differ,
   `parentSession` basename = the parent filename, and the parent file
   (mtime 18:52:54) was not touched by the fork (created 18:53:06). Only
   these 8 ids collide across all 31 live files, so cross-file entry-id
   collision is a fork effect, not random chance. The Go scan gives the
   fork `parent=true user=3 asst=4`: counting the fork alone double
   counts the parent's history relative to the pair (M14).
4. **Source cross-check.** The writer behaviors above were read from
   `session-manager.js` (line cites inline), not inferred from files.
5. **Constructed cases (not Pi evidence).** Derived from the fixtures
   only to exercise the field map: torn tail (last line cut at 120
   bytes, no newline), file without trailing newline, header-only file,
   and an appended entry whose parent is an early entry (in-file branch).
   Go scan: torn tail gives `bad=1`, the 9 complete entries still count;
   no-newline complete file counts all 8 entries; header-only gives 0
   entries and a valid header; the branched file gives `entries=9
   user=3` but `leafpath=3` (the file-wide and leaf-path counts diverge,
   as predicted). These show the mapping tolerates the shapes; they do
   not prove Pi writes them.

### Supported evidence

- S1. Default root, directory encoding, file naming, and the
  flag / env / settings overrides for 0.84.4, from source with line
  cites; matches the live tree (31/31 directory and filename checks).
- S2. Header is `{type, version=3, id, timestamp, cwd[, parentSession]}`;
  header id = filename id; `parentSession` absent unless a fork.
- S3. Nine entry types in the writer; five seen live (`message`,
  `model_change`, `thinking_level_change`, `compaction`, `session_info`).
  Roles `user`, `assistant`, `toolResult` seen live.
- S4. Tool call to result pairing by `toolCall.id` = `toolResult.toolCallId`
  (1609 calls, 1608 results, 0 orphan results).
- S5. Resume appends to the same file with no new header (fixture + live
  file + source).
- S6. CLI fork: new file, new header id, `parentSession`, all parent
  entry lines copied byte-identical with the same entry ids (fixture
  pair, also live).
- S7. Leaf = last parsed non-header entry; current branch = `parentId`
  walk from it (source `_buildIndex`; consistent with all live files).
- S8. v3 files are append-only with one whole-line `appendFileSync` per
  entry; compaction appends and truncates nothing (live: 190 pre-
  compaction entries retained).
- S9. Pi tolerates and skips unparseable lines and repairs a missing
  trailing newline by appending `\n` on open (source).
- S10. Timestamps: entry ISO `timestamp` on every entry; `message.timestamp`
  in unix ms on every message; header time is file-creation time.

### Unsupported / missing evidence (named blockers, not defaults)

- B1. **Writer version is not recorded.** No header or entry names the Pi
  version; per-file qualification of "0.83 vs 0.84 writer" is impossible.
  Do not gate behavior on Pi version; gate only on header `version`.
- B2. **No pre-v3 sample.** No live or prior-art v1/v2 file exists, so the
  in-place rewrite migration (the one real rewrite of an existing file)
  is source-evidence only. Whether a v1/v2 file is ever opened again by
  0.84.4 on this machine is unknown. The contract must define what
  happens if a captured file shrinks or its prefix changes
  (`Supersede`-style or incomplete), but nothing here proves it occurs.
- B3. **No live in-file branch (`/tree`).** All 31 live files are linear.
  `parentId` != previous-line-id, a second root, `branch_summary`,
  `label`, `custom`, `custom_message`, and `bashExecution` have never been
  seen in a real file here. Leaf-path semantics rest on source reading
  plus a constructed test.
- B4. **No live `/fork` or `/clone` (`createBranchedSession`) file.** The
  only live fork is the CLI `--fork` (`forkFrom`). The path-only copy,
  label drop, and `parentId` re-chaining are source-evidence only.
- B5. **No live torn tail and no live "glued line" (pre-#8345) file.**
  Torn tail and newline-repair behavior are source-evidence plus
  constructed tests. The size of the window in which a reader can see a
  partial line (large entries with images) is unmeasured.
- B6. **No live custom-`sessionDir` store and no `PI_CODING_AGENT_DIR`
  relocation**, so the flat layout and mixed-cwd behavior are from source
  only.
- B7. **No duplicate-id import example.** `importFromJsonl` keeps the
  source header id (source only), so "header id unique per store" is
  **not** guaranteed; the sibling `forkFrom` makes new ids.
- B8. **v4 harness files unsupported.** `pi-agent-core` v4
  `JsonlSessionRepo` uses the same filename scheme but a different
  schema. The shipped CLI does not instantiate it. If a future Pi writes
  v4 into the sessions tree, a `version` other than 3 must be a named
  incompatible-format outcome, not a silent parse.
- B9. **`usage`-based and cost facts are not qualified.** Fields exist
  (`usage.cost`, `totalTokens`) but their sums were not validated against
  the provider, and the key set varies (`reasoning` optional).
- B10. **cwd resolution for correlation.** Header `cwd` is lexically
  resolved (no symlink resolution) and the directory slug is lossy; a
  cwd that Pi saw through a symlink may not match a repo's realpath.
  Correlation policy is a step-02 decision, not a qualified fact.

### Surprises versus the historical 0.83.0 facts

1. `PI_CODING_AGENT_SESSION_DIR` is not the only relocator. Four sources
   (flag, that env, `settings.json` `sessionDir`, `PI_CODING_AGENT_DIR`)
   exist, and any `sessionDir` override gives a **flat** layout, not
   per-cwd subdirectories. The reboot SOURCES table shows one variable.
2. The leaf is derived (last line), never stored; the historical note's
   `leaf` entry and `active_tools_change` are v4 harness types the
   0.84.4 CLI does not write.
3. Pi rewrites a file in place in one real case (pre-v3 migration) and
   always touches a no-newline file on open (adds `\n`). "Append-only"
   holds for v3 files only.
4. `createBranchedSession` (`/fork`, `/clone`) copies only the root-to-leaf
   path and can change `parentId` values; `--fork` copies everything. The
   historical note covered only `--fork`.
5. Header `parentSession` is **absent**, not null, on non-forks, and a
   header-only file is a legal (rare) state. No file exists at all until
   the first assistant message.
6. Session ids need not be uuids, and an imported file can carry a
   duplicate header id.
7. Directory names start with `--`; shell tools treat them as options.

## Contract (step-02)

TODO: step-02 decides the discovery set, native-id and correlation rules,
the single-pass tee facts (counts, `StartedAt`/`LastActiveAt`, branch,
`Substantive`), the fingerprint format name, torn-tail and shrink
handling, unknown-version policy, and the fixture matrix; it must answer
B1-B10 above rather than assume them.
