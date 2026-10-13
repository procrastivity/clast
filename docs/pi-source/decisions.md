# `pi-source` — decisions this Matter made

This file records the calls that building the Pi capture source forced.
The wip Matter `pi-source` (under BDS-201, `pi-devin-sources` in
HANDOFF §4 item 1) set the intent; the sealed `source-capture-policy`
Matter (BDS-261, `docs/source-capture-policy/decisions.md`) supplies
the selection predicate, the absent-versus-failed distinction, and the
outcome/exit table. Pi is a local `source.FileTail` source modeled on
`internal/source/claude`; `internal/source/amp` (network) and
`internal/source/devin` (SQLite) are not models for it.

Status: step-01 (qualification), step-02 (contract) and step-03 (fixtures,
format boundary, validation recipes) done; step-04 (discovery and
correlation) is next; no production Go exists yet.

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

The calls below are what the Go package `internal/source/pi` must do.
They use only the existing seams (`source.Source`, `Presence`,
optionally `TranscriptRenderer` / `TranscriptReader`) and the existing
capture verb. Evidence tags (S1-S10, B1-B10) refer to the step-01
section above. "Item diagnostic" means a `source.Diagnostic`: stderr,
never changes the exit. "Source failure" means `Discover` returns an
error: sweep discloses it and exits 0, explicit `--harness pi` exits 1
`capture.source-unavailable` (shared policy; not re-decided here).

### 0. Position in the shared policy

Pi is `source.FileTail` and local. It joins a bare sweep unless listed
in `capture.exclude`; `capture --harness pi` captures it regardless of
exclusion; there is no `capture.pi.auto` setting. Absent storage is a
quiet `(nil, nil, nil)` from `Discover` on sweeps; `Present` makes the
explicit path disclose it. Registry name `pi`, so the journal directory
is `pi-<native-id>` (M11).

### 1. Artifact name, format string, version gate

- **Artifact:** `transcript.jsonl`, the journal default. Pi's file is
  already JSONL, so `TranscriptFingerprint.Artifact` stays empty (claude
  precedent; devin and amp needed a non-default name, Pi does not). The
  bytes are the Pi file, unchanged, including the header line and any
  torn tail. No sidecars exist (S1: nothing else lives in the tree).
- **`transcript.format`: `pi-jsonl-v3`.** The number is the Pi header
  `version` the parser was qualified against (S2), not the Pi release.
  It is stamped only when the header says `3`.
- **Version gate (Discover, re-checked by Capture).** Supported set is
  exactly `{3}`.
  - `version` 1 or 2: Pi migrates these in place on its next open
    (step-01 "Append vs rewrite"), and no sample of either exists (B2).
    Not captured; item diagnostic
    `pi: <path>: unsupported session version N (supported: 3)`.
    The session converges by itself: once Pi opens and migrates it, the
    file is v3 and the next sweep captures it.
  - `version` missing or not an integer: Pi itself treats this as v1
    (`session-manager.js:77`); same outcome and diagnostic text
    (`version missing`).
  - `version` > 3 (v4 harness files, B8): not captured; same diagnostic.
    This is the named "incompatible format" outcome; it is never a
    silent parse and never a whole-source failure, because other files
    are still valid.
  - Dropping a file this way loses nothing: the original stays in
    `~/.pi`, and no path deletes journal history. Supporting another
    version is a new Matter with its own qualification and format string
    (`pi-jsonl-v4`).
- A future decision to change fact rules does not retrigger recapture
  (hash unchanged); same limit as claude. Not addressed here.

### 2. Discovery

**Roots clast can honor without running Pi.** clast sees only its own
process environment and the files on disk.

| Relocator (step-01) | Honored? | Rule |
|---|---|---|
| `--session-dir` flag | No | clast never sees Pi's argv. Named limit |
| env `PI_CODING_AGENT_SESSION_DIR` | Yes | `~` expanded; must be absolute after expansion |
| `sessionDir` in global `<agentDir>/settings.json` | Yes | same path rule |
| `sessionDir` in project `.pi/settings.json` | No | needs the cwd of a Pi run; unbounded. Named limit |
| env `PI_CODING_AGENT_DIR` | Yes | moves `<agentDir>` (and so the settings file and default root) |

- `agentDir` = `PI_CODING_AGENT_DIR` (expanded) if set, else
  `~/.pi/agent`.
- **Effective root** follows Pi's precedence minus the flag: env
  session dir, else global settings `sessionDir`, else
  `<agentDir>/sessions` (S1).
- **Candidate roots, in order:** effective root, `<agentDir>/sessions`,
  `~/.pi/agent/sessions`, de-duplicated by cleaned path (best-effort
  `EvalSymlinks`). The extra two are history: a user who later set an
  override still has older sessions in the default tree. A relative
  override (env or settings) cannot be resolved because Pi resolves it
  against the cwd of the Pi process (`utils/paths.js:82-86`); it is
  ignored with an item diagnostic and the next candidates still apply. An
  unreadable or malformed `settings.json` is an item diagnostic and is
  treated as "no `sessionDir`". An env/settings difference between the
  user's shell and a hook/cron environment is a known limit (B6);
  the extra candidates soften it.
- **Fixture seam:** `NewAt(root)` replaces the whole candidate list with
  one root, like `claude.NewAt`.

**Walk shape (S1).** For each candidate root that exists: read its
entries. `*.jsonl` files directly in the root (flat layout) and
`*.jsonl` files one level down in each subdirectory (per-cwd layout)
are candidates. Symlinked directories and files are followed (`os.Stat`;
Pi follows directory symlinks in `listAll`). No deeper recursion, no
cwd filtering (all cwds in a flat root are captured; Correlate binds
each by header). Non-regular files, directories named `*.jsonl`, and
other suffixes are ignored without comment.

**File selection (Discover reads only the first line, at most 1 MiB,
Pi's own header scan cap).** The first line must be a JSON object with
`type=="session"`, a string `id` matching Pi's id rule
`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`, and `version==3`
(section 1). The id rule is a security boundary, not politeness: the id
becomes a journal directory name (`pi-<id>`, `journal/paths.go:22`),
and a hand-edited or imported header could carry `../`.
The header must be the first line. Pi would also accept junk lines
before the header (it skips unparseable lines when searching); clast is
stricter on purpose, since Pi never writes that shape (S9).

Outcome per candidate file:

| State | Outcome |
|---|---|
| zero bytes | skipped silently (transient between create and first write; Pi may also initialize it) |
| first line unterminated and not valid JSON, no newline anywhere in the file | skipped silently (header being written) |
| first line unparseable (newline-terminated), or JSON but not `type=="session"`, or `id` missing or not a string | item diagnostic, skipped |
| `id` fails the id rule | item diagnostic, skipped |
| header line longer than 1 MiB | item diagnostic, skipped |
| `version` != 3 | item diagnostic (section 1), skipped |
| open or stat error | item diagnostic, skipped |
| valid header, no entries (header-only) | discovered and captured (section 7) |

**Absent / empty / unreadable root.**
- Every candidate root missing: `Discover` returns `(nil, nil, nil)`.
  `Present` returns the error for the effective root (the
  `os.PathError` naming path and reason, verbatim, as claude does).
- A candidate root exists but is empty: present, no sessions, quiet.
- A candidate root exists but cannot be enumerated: if at least one
  other root enumerated, this is an item diagnostic; if none did,
  `Discover` returns that error (source failure). A subdirectory that
  cannot be read is always an item diagnostic (claude precedent).
- `Present` returns nil iff at least one candidate root is a readable
  directory.

`Discovered` = `{NativeID: header id, Path: file path, ModTime: file
mtime}`. `ModTime` is a liveness hint only; it is never compared for
freshness (section 6).

### 3. Session identity, duplicates, forks

- **Native id = header `id`** (S2). Pi opens a file by its header id
  (`sessionId = header.id`, `session-manager.js` `_setSessionFile`), not
  by the filename, so the header is Pi's own authority. M11: no uuid
  assumption anywhere; ids may be any string passing the id rule
  (`--session-id`, step-01 "File name").
- **Filename is a cross-check only.** Filename is split at the first
  `_` into `<ts>_<id>`. If the id part differs from the header id, or
  the name has no `_`, the header id wins and an item diagnostic
  `pi: <path>: filename id X does not match header id Y` is emitted;
  the file is still captured. Header-vs-filename timestamp is not checked.
- **Duplicate header ids across files (B7).** Normal Pi operation never
  produces two files with one header id, but `importFromJsonl` copies a
  file with its original header (step-01 "Resume behavior"). The journal
  key is `(pi, id)`, so two files cannot both be captured. Rule: within
  one `Discover`, among files sharing a header id, the one with the
  newest `ModTime` is returned (ties: lexicographically smallest path);
  each other file gets an item diagnostic
  `pi: <path>: duplicate session id X, using <winner>`. The result is
  deterministic for a given disk state. Known cost: if both copies grow,
  the winner can change and the committed transcript is replaced by the
  other copy's divergent content (the default recapture posture). No
  evidence says this occurs (B7); the diagnostic makes it visible.
  Whole-file dedupe never looks at entry ids.
- **Forks are separate sessions.** A fork has its own header id (S6),
  so it is its own journal session with its own counts and its own
  transcript copy, including the copied history. `parentSession` is not
  a `Facts` field and no shared model has a slot for it; it stays
  readable in the captured header line. Nothing resolves it to a parent
  session key.
- **Cross-session fork dedup is out of scope**, per M14 and
  `SOURCES.md` §4 item 1: clast keys content on entry id when a
  feature counts or diffs inside a transcript; deduping copied history
  across a fork family is a consumer (wake) concern. Capture never
  subtracts, hides, or links parent history.

### 4. Correlation and timestamps

- **`Correlate`** reads only the header line: `cwd` if it is a
  non-empty absolute path, else `""` (projectless, still captured; M17
  empty-means-no-fact). It is passed through lexically as Pi wrote it:
  no `EvalSymlinks`, no slug decoding (the slug is lossy; S1/B10). The
  registry then resolves it with `ResolveCurrentClone`, exactly as for
  claude. A session run through a symlinked path that the registry holds
  as a realpath stays projectless; that is B10 and is not fixed here.
  Forks carry the target cwd in their own header (`forkFrom` writes
  `resolvedTargetCwd`), so a cross-project `--fork` correlates to the
  project where it now lives. An unreadable file is an item diagnostic
  (the verb skips that item).
- **`StartedAt` = header `timestamp`** (ISO-8601, parsed
  `RFC3339Nano`). It is the birth of this session file. It also drives
  the day shard (verb: `facts.StartedAt` local date).
- **`LastActiveAt` = the latest of the header timestamp and every
  parseable entry `timestamp`** (ISO, every entry type counts, as
  bookkeeping counts for claude). Entry ISO `timestamp` is used, not
  `message.timestamp` (unix ms): the ISO field exists on all entry types,
  the ms field only on messages (S10). Unparseable timestamps are
  skipped; an unparseable header timestamp leaves `StartedAt` zero and
  the verb falls back to capture time for the shard.
  `LastActiveAt` takes the max over **all** entries, not only the leaf
  path: it is "when did this file last move", and an abandoned branch is
  still activity.
- The header term in the max is load-bearing: copied fork history
  carries older entry timestamps than the fork's own header (live fork:
  first entry 44 s before the header, S6), so without it a fresh fork
  would have `LastActiveAt < StartedAt`.

### 5. Facts

`Facts.Branch` is `""`: Pi records no git branch (S2, S3). Pi's
conversation branch (tree leaf) is not a git fact.

**Counts are leaf-path counts (the active branch), not whole-file.**
Definition: the leaf is the last non-header line that parsed as an entry
with a string `id` (exactly Pi's `_buildIndex`, S7). Walk `parentId`
from the leaf to a `null` parent, with last-occurrence-wins for a
duplicate id (Pi's `byId` map) and a visited set against cycles.
Over that path:
- `Counts.User` = entries with `type=="message"` and
  `message.role=="user"`.
- `Counts.Assistant` = entries with `type=="message"` and
  `message.role=="assistant"` (each is one model response; same unit as
  claude's assistant entries).
- Nothing else counts: `toolResult` (own role), `bashExecution`,
  `custom_message`, `custom`, `label`, `compaction`, `branch_summary`,
  `model_change`, `thinking_level_change`, `session_info`, unknown
  types. Later `type=="session"` lines (Pi's own comments mention a
  past duplicate-header bug) are ignored and never become the leaf.
- Entries injected by an extension through `sendUserMessage` are stored
  as ordinary `user` messages (note 18 §5) and count as user. Pi's format
  cannot tell them apart. Named limit.

Why the leaf path and not the whole file (the step-01 divergence):
1. It is Pi's own definition of "the conversation" (`buildSessionContext`
   walks leaf to root) and matches the devin precedent (on-chain only).
   A count over every line includes alternatives the user abandoned.
2. The leaf is the last line, so the leaf path is a pure function of the
   file bytes (S7). Together with the whole-file sha256 (section 6) the
   fact cannot go stale relative to the fingerprint: any leaf move
   appends a line.
3. Live cost is nil: all 31 live files are linear and both definitions
   agree on every file (validated, see "Validation" below), so the choice
   only bites once `/tree` branching or `--fork` copies of branched
   files appear (B3).

Fallback: if the walk meets a missing parent id or a cycle before a
`null` parent (damaged file, or an oversize line dropped from the scan),
fall back to whole-file counts and emit one item diagnostic
`pi: <path>: leaf path broken at <id>; counts are whole-file`.
Over-counting damaged files is safer than silently under-counting.
Memory: one small record per entry (id, parent id, role, substantive
flag), no message bodies, so facts never materialize prompt text (M10).
Line size bound: 16 MiB, as claude (`maxLineSize`); a longer line is
bad-line counted (and therefore may break the chain, which triggers the
fallback above).

**`Substantive`** = the leaf path contains at least one `assistant`
message with a non-empty `content` array and `stopReason != "error"`.
Not `Counts.Assistant >= 1` (claude's rule) because Pi creates the file
only at the first assistant message (S8), so nearly every Pi file has
one, and live files include assistant messages with `stopReason`
`error`/`aborted` and zero content blocks (5 of 1261). A session whose
only reply is a provider error did nothing and should auto-dismiss as a
no-op. `aborted` with content counts (the user saw partial work).
Live: 31/31 files substantive under both rules.

**Facts the format cannot know or that stay unqualified:**
- The Pi version that wrote the file (B1): no `Facts` field, and no
  behavior may depend on it; gate only on header `version`.
- Model and provider: present in `model_change` and assistant messages
  but `Facts` has no field and none is added; they remain in the
  artifact for readers.
- Cost, token totals (B9): not extracted.
- Tool-call and tool-result counts: derivable (S4) but
  `journal.SessionCounts` has only `User` and `Assistant`; not added
  (section 9).
- Session display name (`session_info`), labels, compaction state: stay
  in the artifact.

### 6. Freshness and fingerprint

`TranscriptFingerprint{Format: "pi-jsonl-v3", Lines, SHA256}` is
computed in the single-pass tee over exactly the bytes written, like
claude: `SHA256` over all bytes; `Lines` = newline bytes plus one for a
non-empty unterminated final line (identical to
`source.FingerprintFile`, so a capture's own fingerprint and a later
default `Unchanged` verdict compare like for like).

**The `FingerprintFile` default suffices; `Unchanged` is not
implemented.** The verb's default compares `lines` and `sha256` of the
live file with the prior record. Walking the Pi-specific write
behaviors (step-01):

| Event | Effect on bytes | Default verdict |
|---|---|---|
| resume / new turn (S5, S8) | appended lines | changed (both fields) |
| newline repair on open (`appendFileSync "\n"`) | +1 byte, same line count | changed (sha256) |
| truncate then regrow to the same line count | different bytes | changed (sha256); `lines` alone would say unchanged |
| pre-v3 in-place rewrite (B2) | whole file rewritten | changed; see below |
| `touch` / mtime change with identical bytes | none | unchanged (mtime is never evidence) |
| `/tree` leaf move | in memory only; the next append is the new leaf, so bytes change then (S7) | changed once the next line lands |
| fork created (S6) | new file only; parent untouched | parent unchanged |

What `Unchanged` (the default) compares: the live file's whole-byte
sha256 and line count against `prior.Transcript`. The sha256 is the
verdict; `Lines` is a cheap early field. Why that is the right unit:
every fact in section 5 is a pure function of the file bytes, so equal
bytes imply equal facts. No timestamp, size, mtime, or `ModTime`
shortcut is claimed.

The pre-v3 rewrite cannot overwrite a committed capture, because v1/v2
files are never captured (section 1): a rewrite that completes yields a
new v3 file that is then captured as a first capture or as a changed
recapture of a v3 prefix. A reader that catches the rewrite midway sees a
header already stamped v3 (Pi migrates in memory first) followed by a
prefix of the entries; that prefix is committed and the finished rewrite
changes the bytes, so the next sweep replaces it. Convergent, no
veto needed.

`Supersede` is not implemented, for the devin reason: a v3 file does
not legitimately shrink, a torn read is a prefix of the file that
converges at the next sweep, and a veto would need prior bytes to prove
"worse". `JournalScope` is not needed (no cross-run state).

### 7. Malformed input

The copy is always verbatim and total: Capture never edits, trims, or
drops bytes. What varies is what the facts see and what is disclosed.

| Input | Copy | Facts | Outcome class |
|---|---|---|---|
| Unparseable line mid-file (including a glued two-object line from the pre-0.84.3 newline defect, and an oversize line) | kept | skipped, counted | one aggregated item diagnostic per capture: `pi: <path>: N unparseable line(s) skipped (first at line L)`; capture succeeds |
| Entry line that is JSON but has no string `id` | kept | skipped, counted as unparseable | same diagnostic |
| **Torn tail**: final line unterminated and not valid JSON | kept, including the partial line | partial line excluded from facts and from the bad-line count | no diagnostic, no `Incomplete`: transient on a live file. Counted in `Lines` (FingerprintFile semantics) |
| Unterminated final line that is valid JSON | kept | included (Pi's loader parses it too, S9) | none |
| Header-only file (valid header, nothing else) | kept | counts 0, `Substantive` false, `StartedAt` = header timestamp | captured; the verb's no-op auto-dismissal applies |
| Zero-byte file | not captured | | skipped at Discover (section 2); if it empties between Discover and Capture, header check fails: item diagnostic |
| First line not a header, or version != 3 at Capture time (file changed after Discover) | staged copy discarded | | `Capture` returns an error: item diagnostic, committed capture stands (verb behavior) |
| Header `id` at Capture differs from `d.NativeID` (file replaced) | discarded | | `Capture` error: item diagnostic |
| Read error mid-copy | discarded | | `Capture` error: item diagnostic |
| Context cancelled | | | fatal, never folded into a diagnostic (shared policy) |

Why the torn tail stays in the copy (not truncated at the last
newline): the default `Unchanged` re-hashes the live file; a copy that
dropped the partial bytes would never match, so a stalled torn tail
(crashed Pi) would be re-captured on every sweep. A verbatim copy is a
fixpoint. It also keeps the "artifact == source bytes" promise (M13).
Why not an error: a live Pi session is the main case, and it would be
uncapturable whenever a write is in flight. Why not `Incomplete`: the
flag means the source's own check could not prove the bytes cover the
revision recorded; here the recorded revision is exactly the bytes
copied, and `Facts.Incomplete` is documented as never set for whole
local-file copies. When Pi later repairs the tail (`\n` appended, or the
write completes) the bytes change and the next sweep recaptures.

A bad line that sits permanently mid-file (the stalled torn tail after
Pi appended `\n`) is disclosed on each capture of that file, not on
each sweep: unchanged sessions are skipped before any parse.

### 8. Failure boundaries

| Condition | Bare sweep | Explicit `--harness pi` |
|---|---|---|
| No candidate root exists | quiet, nothing captured | `Present` error: `capture.source-unavailable`, exit 1 |
| Root exists, empty | quiet | exit 0, nothing captured |
| Root exists, none enumerable | source failure line on stderr, other sources run, exit 0 | `capture.source-unavailable`, exit 1 |
| One root unreadable, another fine | item diagnostic | item diagnostic |
| Subdirectory unreadable | item diagnostic | item diagnostic |
| `settings.json` unreadable or malformed; relative override | item diagnostic, fall back to other roots | same |
| Zero-byte or header-in-flight file | skipped silently | skipped silently |
| Bad or missing header, bad id, oversize header | item diagnostic, skipped | same |
| Unsupported `version` (1, 2, missing, 4+) | item diagnostic, skipped | same (exit 0) |
| Duplicate header id | item diagnostic for each loser | same |
| Filename id disagrees with header | item diagnostic, captured | same |
| Bad lines mid-file; broken leaf path | captured, item diagnostic | same |
| Torn tail | captured, silent | same |
| Capture-time open/read/header failure | item diagnostic, committed capture stands | same |
| Journal store write failure, cancellation | fatal, exit 4 (`internal.capture`) | same |

No Pi condition is a whole-source failure except "roots exist and none
can be enumerated". Explicit and sweep differ only at the root level,
exactly as for claude.

### 9. Shared model and seam amendments

**None needed.** Specifically:
- `source.Source` / `Facts` / `Discovered`: used as is. No new `Facts`
  field (no model, provider, cost, tool counts, parent session).
- `journal.TranscriptFingerprint` / `SessionCounts`: unchanged; default
  artifact name; only the format string `pi-jsonl-v3` is new.
- Optional seams: `Presence` implemented (required by the registry
  conformance test); `Unchanged`, `Supersede`, `JournalScope` not
  implemented, with reasons above. `TranscriptRenderer` /
  `TranscriptReader` are optional and out of scope for capture; if
  added later they declare `pi-jsonl-v3` and must follow the leaf path
  for turns and pair `toolCall.id` with `toolResult.toolCallId` (S4).
- Registry: one mechanical row in `registry.All` (not an amendment).
  No change to `capture.exclude` validation beyond the new name
  becoming implemented.
- MODEL §7/M13 and SOURCES §3 need documentation touch-ups only:
  SOURCES §3's `pi` row should name the four relocators and the
  flat-versus-per-cwd layouts (surprise 1), and the "DAG" wording is
  right for the format but the active conversation is the leaf path.

### Asymmetric examples

Synthetic, placeholder text only (`T`). `u`/`a` are ids; each line is
abbreviated to `type/role id<-parent`. All were run through a throwaway
implementation of this contract in the scratch directory
(`contract.py`); outputs below are its results.

1. **Fork counted separately, shared ids kept.** Parent file P (header
   id `AAAA`, ts day 1): `u1<-null`, `a1<-u1`, `u2<-a1`, `a2<-u2`.
   Fork F (header id `BBBB`, ts day 2, `parentSession`=P): the same four
   lines byte-identical, then `u3<-a2`, `a3<-u3`.
   Chosen: two sessions, `pi-AAAA` {user 2, assistant 2} and `pi-BBBB`
   {user 3, assistant 3}, F's shard is day 2, `StartedAt` day 2.
   Plausible-wrong: de-duplicating F's copied entries against P at
   capture would give F {1,1}, and which file "owns" shared ids would
   depend on sweep order (nondeterministic); taking `StartedAt` from F's
   first entry would file F under day 1 and show a fork as days old.
2. **Fresh fork, no new entries.** F header ts 10:00 day 2; copied
   entries end at 09:00 day 1. Chosen: `StartedAt = LastActiveAt =
   10:00 day 2`. Plausible-wrong (max of entries only): `LastActiveAt`
   09:00 day 1, before `StartedAt`. Counts are the copied history's
   (fixture: user 1, assistant 1).
3. **Branch inside one file.** Lines: `u1<-null`, `a1<-u1`, `u2<-a1`,
   `a2<-u2`, `u3<-a1`, `a3<-u3` (user went back to `a1` and re-asked).
   Chosen (leaf `a3`, path `u1,a1,u3,a3`): user 2, assistant 2.
   Plausible-wrong (whole file): user 3, assistant 3. Script output:
   `user=2 asst=2 wholeU=3 wholeA=3`. `LastActiveAt` is `a3`'s time
   under both; had the abandoned branch ended later it would count.
4. **Torn tail.** Header, `u1<-null`, `a1<-u1`, then the bytes
   `{"type":"message","id":"c3","parentId":"a1","timest` with no newline.
   Chosen: the artifact holds all those bytes; `Lines`=4 (3 newline
   lines + the unterminated one, equal to `FingerprintFile`), counts
   {user 1, assistant 1}, no diagnostic, `Incomplete` false. After Pi
   finishes the write, the sha256 changes and the next sweep recaptures
   with counts +1. Plausible-wrong A: copy only up to the last newline
   (`Lines`=3): every later default `Unchanged` check re-hashes the full
   live file, never matches, and a crashed session is re-captured forever.
   Plausible-wrong B: fail the capture on the partial line: a live
   session is uncapturable whenever a write is in flight.
5. **Truncate and regrow to the same line count.** Captured file has 5
   lines (sha S1). Later the file again has 5 lines with different
   bytes (rewrite, or a replaced file; sha S2). Chosen: changed,
   recapture (sha256 decides). Plausible-wrong: compare `Lines` or size
   or mtime: unchanged, stale transcript stays curated against changed
   content. Converse: a `touch` with identical bytes is unchanged and
   writes nothing.
6. **Error-only session.** Header, `u1<-null`, `a9<-u1` with
   `stopReason:"error"`, `content:[]`. Chosen: user 1, assistant 1,
   `Substantive` false, auto-dismissed. Plausible-wrong (claude's
   `assistant >= 1`): substantive true, a provider-failure-only session
   surfaces in wake. Script output: `subst=False`.
7. **Unsupported version.** Header `{"type":"session","version":4,...}`
   (or no `version`). Chosen: skipped with
   `unsupported session version 4 (supported: 3)`; nothing in the
   journal. Plausible-wrong: copy it and stamp `pi-jsonl-v3`, which
   mislabels an unqualified format (B2, B8).
8. **Duplicate header id.** Files in two dirs both with header id
   `AAAA`, mtimes 10:00 and 12:00. Chosen: the 12:00 file is `pi-AAAA`;
   the 10:00 path gets a `duplicate session id AAAA, using <path>`
   diagnostic. Plausible-wrong: capture both under one key and thrash
   the committed copy every sweep (each looks "changed" against the
   other's fingerprint).
9. **Hostile id.** Header `"id":"../../x"`: fails the id rule, skipped
   with a diagnostic. Plausible-wrong: use it as the native id and
   write outside the session directory.
10. **Header-only file.** Valid v3 header, no entries: captured,
    counts 0, `Substantive` false, `StartedAt` = header time,
    auto-dismissed as a no-op. Plausible-wrong: skip it (hides a real
    session start) or treat it as unsupported.

### Validation done in this step

- Throwaway rule implementation `contract.py` (scratch, not in repo)
  over all 31 live files: leaf-path counts equal whole-file counts for
  31/31, no broken leaf paths, `Substantive` identical under both rules
  for 31/31. So the live store cannot distinguish the chosen rules; only
  the synthetic cases above do (examples 3, 4, 6), which are constructed
  and not Pi evidence.
- Seams read: `source.go`, `fingerprint.go`, `claude/{capture,correlate,
  claude}.go`, `verbs/capture/capture.go` (`unchangedVerdict`,
  `captureOne`), `journal/{paths,records,walk}.go`. Facts found there
  that the contract depends on: the journal key is `(harness, native
  id)` and the id becomes a directory name; the default `Unchanged` is
  `FingerprintFile` on `d.Path`; a `Capture` error discards the stage
  and keeps the committed capture; `Facts.Incomplete` is for non-whole
  reads only.

### Settled calls (owner, 2026-10-13)

The three questions raised at the end of step-02 are closed:

1. **No `capture.pi.roots` config key now.** The roots clast cannot see
   (the `--session-dir` flag, project-level `.pi/settings.json`) are a
   **named limitation**, to be revisited on demand. Until then a Pi
   install using only those relocators is not discovered; the doc and
   any user-facing text must say so rather than imply full coverage.
2. **Duplicate header ids: newest `ModTime` wins** (tie: smallest
   path), every loser gets a diagnostic (section 3). Final.
3. **Counts are leaf-path counts** (section 5), with the whole-file
   fallback only for a broken path. Final; whole-file comparability with
   claude is not a goal.

### Pinned details the step-02 text left implicit

- **Blank lines.** A line that is empty or only whitespace is neither a
  bad line nor an entry (Pi skips it too). It still counts as a line in
  `Lines` (newline bytes).
- **Line numbers in diagnostics** are 1-based physical lines in the file
  (the header is line 1), blank lines included. `first at line L` uses
  that numbering (fixtures `bad-lines`: 6, `broken-chain`: 5).
- **What parses as an entry for the leaf walk:** a JSON object on its
  own line, not on line 1, `type != "session"`, with a string `id`. A
  later `type:"session"` line is ignored (never the leaf, not counted
  bad). `parentId` absent, `null`, or a string; any other type is
  treated as `null`.
- **Id length.** The id rule has no length cap (Pi has none). A name too
  long for the filesystem fails at stage/commit, which is an item
  diagnostic through the verb, not a Discover rule.
- **No decoding of message text** anywhere in discovery or facts
  (M10). Correlation reads only line 1.

## Format compatibility boundary

**Supported: `pi-jsonl-v3`** = a file whose first line is a
`type:"session"` header with integer `version` exactly 3 and a string
`id` passing the Pi id rule, followed by zero or more newline-framed
JSON entry lines. Evidence base: Pi 0.83.0 (prior art) and 0.84.4
(installed source plus 31 retained files); the header version did not
move between them. Entry shapes read by the contract: `type`, `id`,
`parentId`, `timestamp`, and for `message` entries `message.role`,
`message.content` (array emptiness only) and `message.stopReason`. Every
other field, and every unknown entry type, is carried verbatim and
ignored by facts. Optional additions seen in 0.84-era data
(`diagnostics`, `details`, `usage` variants) do not matter.

**Not supported (each is a named outcome, not a default):**

| Input | Outcome | Basis |
|---|---|---|
| header `version` 1, 2, missing, or non-integer | skipped, `unsupported session version` diagnostic | B2 |
| header `version` 4 or higher (v4 harness files) | same | B8 |
| header not on line 1; file starting with a non-header line | skipped, diagnostic | step-02 section 2 |
| header line over 1 MiB | skipped, diagnostic | Pi's own scan cap |
| id failing `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$` | skipped, diagnostic | journal path safety |
| `--session-dir` and project-level `sessionDir` stores | not discovered | settled call 1 |
| per-file writer version, cost/tokens, tool-call counts, model/provider, parent linkage as facts | not extracted | B1, B9, section 9 |
| `/fork` or `/clone` files, `/tree` files, `custom*`/`label`/`branch_summary`/`bashExecution` entries | captured verbatim, counted by the generic rules; shapes are source-evidence only | B3, B4 |
| duplicate header ids across files | one winner captured, others diagnosed | B7 |
| Windows path forms, non-Unix cwd encodings | out of scope | not qualified |

Anything else Pi adds later inside a v3 file is carried and ignored. A
v3 header with a different meaning would be undetectable; B1 means the
format version is the only gate.

## Fixtures

`internal/source/pi/testdata/` (README.md is the manifest; the table
there gives per-fixture provenance, scrub transformation, and expected
`StartedAt`, `LastActiveAt`, `Lines`, `User`, `Assistant`,
`Substantive`, diagnostics). 36 fixture files in six discovery roots
under `roots/`: `percwd` (16 capturable sessions in four per-cwd
directories), `flat`, `rejected`, `diagnosed`, `duplicate`, `walkshape`.
All text is placeholder, none was copied from a live or prior-art file;
every fixture is labelled `constructed`,
`constructed-from-live-shape`, or `constructed-from-prior-art-shape`,
and none is Pi evidence. The generator and checker were throwaway
scratch scripts; the committed files are the artifact. Hand edits must
keep the README table in step with the bytes (Lines in particular).

Fixture-to-contract map:

| Contract point | Fixture(s) |
|---|---|
| basic counts, tool pairing | `basic` |
| resume appends, LastActiveAt later than StartedAt by days | `resume` |
| fork: own session, shared ids, StartedAt from header | `fork-parent`, `fork-child` |
| fresh fork: LastActiveAt = header time | `fork-fresh` |
| leaf path vs whole file | `branch` |
| torn tail verbatim, silent | `torn-tail` |
| valid unterminated last line counted | `unterminated-valid` |
| bad lines aggregated | `bad-lines` |
| broken chain fallback | `broken-chain` |
| header-only, not substantive | `header-only` |
| error-only vs aborted-with-content | `error-only`, `aborted-partial` |
| Correlate empty | `no-cwd`, `relative-cwd` |
| version gate | `version-2`, `version-4`, `version-missing` |
| id rule | `hostile-dotdot`, `hostile-trailing-dot` |
| header-in-flight, zero byte, no header, junk before header | `header-inflight`, `zero-byte`, `no-header`, `junk-before-header` |
| filename vs header id | `filename-id-mismatch`, `filename-no-underscore` |
| duplicate header id | `dup-original`, `dup-import-copy` (test sets mtimes) |
| flat vs per-cwd walk, depth limit, suffix filter | `flat-a/b`, `percwd`, `walkshape` |

### Fixture validation (step-03)

A throwaway implementation of the step-02 rules (`contract` header
classification plus fact extraction) was run over every file under
`roots/`: 36/36 outcomes match the README manifest (capture, silent
skip, diagnosed skip, ignored); for the 23 files that reach Capture,
`StartedAt`, `LastActiveAt`, `User`, `Assistant`, `Substantive`,
`bad`/first-bad line, broken-path flag, torn flag, id, and Correlate
value match; `Lines` matches an independent `awk 'END{print NR}'` count
for all of them; whole-file role counts match `jq` for the files with
no branch or bad lines; the branch fixture shows leaf 2/2 vs whole 3/3;
the fork child's copied lines are byte-identical to its parent (hash
equal). The scrub check found only `FIXTURE-OK` / `FIXTURE-ERROR` as
text values. Ignore check (global gitignore): see below.

Ignore check: `git status --porcelain --untracked-files=all
internal/source/pi` lists all 37 files (36 fixtures + README) as `??`,
`git check-ignore -v --no-index` over every one prints nothing (exit 1),
and `git add -n internal/source/pi` lists 37. Directory names beginning
with `--` add normally. Keep file names off the user's global patterns
(`*.com`, `*.sql`, `*.log`, archives, `.DS_Store`): a future fixture
named like a domain or a log would vanish silently, so re-run the
check after adding any.

## Validation recipes

Run from the repo root; step-04 and later steps use these verbatim.

```
go test ./internal/source/pi/...           # targeted, fast loop
go test ./...                               # full suite
make check                                  # lint + test (the gate)
CGO_ENABLED=0 go build ./cmd/clast          # static-binary promise
make cross-compile                          # darwin/arm64 + linux/amd64
git status --porcelain --untracked-files=all internal/source/pi
git check-ignore -v --no-index $(git ls-files --others --cached internal/source/pi)   # must print nothing
```

Registry conformance: `go test ./internal/source/registry/...` must
pass with the new row (it pins `Presence` on every row).

**Read-only dogfood recipe (after capture lands; step-04 only needs the
discovery/correlation part).** Never point at a real journal and never
launch Pi:

```
J=$(mktemp -d)
go build -o "$J/clast" ./cmd/clast
# discovery + correlation only (step-04): enumerate against the real store
#   via a throwaway test or the plumbing sessions verb once capture exists
CLAST_JOURNAL_DIR="$J/journal" XDG_CONFIG_HOME="$J/cfg" "$J/clast" plumbing capture --harness pi
CLAST_JOURNAL_DIR="$J/journal" XDG_CONFIG_HOME="$J/cfg" "$J/clast" plumbing capture --harness pi   # 2nd run: expect 0 captured (fingerprint selectivity)
CLAST_JOURNAL_DIR="$J/journal" XDG_CONFIG_HOME="$J/cfg" "$J/clast" plumbing sessions
# explicit-absent: expect capture.source-unavailable, exit 1
PI_CODING_AGENT_DIR=/nonexistent CLAST_JOURNAL_DIR="$J/journal" XDG_CONFIG_HOME="$J/cfg" "$J/clast" plumbing capture --harness pi
# bare sweep with the store absent: quiet for pi, exit 0
HOME=$(mktemp -d) CLAUDE_CONFIG_DIR="$J/none" CLAST_JOURNAL_DIR="$J/journal" XDG_CONFIG_HOME="$J/cfg" "$J/clast" plumbing capture
```

Checks: the store under `~/.pi` is byte-unchanged afterwards (compare a
`find ~/.pi/agent/sessions -type f -exec sha256sum {} +` taken before and
after); the journal copy of a retained file equals its source byte for
byte; no stderr diagnostics beyond the ones the fixtures predict for the
live shape (the 31 retained files predict none); a second sweep captures
only files that grew. `capture` and `sessions` spellings follow the
current CLI; confirm with `clast plumbing --help` before scripting.

## Handoff to step-04 (discovery and correlation)

Scope: package `internal/source/pi` with `Name`, `Model`
(`source.FileTail`), `New`/`NewAt(root)`, `Present`, `Discover`,
`Correlate`, the shared header reader, and the registry row. `Capture`
is a later step but the header reader, the id rule and the version gate
are shared with it, so write them once.

Everything needed is in this document; do not re-open these:

- Names and strings: registry name `pi`; format `pi-jsonl-v3`; artifact
  `transcript.jsonl` (section 1).
- Roots and walk (section 2); candidate order; `NewAt` replaces the list;
  relative overrides rejected with a diagnostic.
- Header rules and the outcome table (section 2); id rule; version gate
  (section 1); header read bound 1 MiB.
- Identity (section 3): header id; filename cross-check diagnostic;
  duplicate winner rule; forks are separate sessions.
- Correlate (section 4): header line only; absolute `cwd` or `""`.
- Failure classes (section 8). Presence semantics (section 2).
- The fixtures and their expected outcomes (README manifest).

A discovery test per root in `roots/` is the natural first test set:
`percwd` yields the sessions listed, `flat` two, `rejected` zero (with
the diagnostics listed), `diagnosed` two (with diagnostics), `duplicate`
one (after `Chtimes`), `walkshape` one.

### Unresolved essential assumptions

None blocks step-04, but each is an assumption someone may need to
confirm; none was settled by evidence:

1. **Registry correlation of a lexical cwd.** `registry.ResolveCurrentClone`
   is assumed to accept the header cwd exactly as claude's `cwd` field is
   accepted (no trailing-slash or symlink normalization by the source).
   Not exercised against symlinked checkouts (B10).
2. **Environment visibility.** Whether `PI_CODING_AGENT_*` is set in the
   environment of clast hook/cron runs is unknown (B6). The default
   history roots mitigate it.
3. **Pi version drift inside v3.** B1: nothing records the writer
   version; the gate is the header version only. A later Pi that changes
   v3 semantics silently would be miscounted until noticed.
4. **Entry-id uniqueness inside a file** is assumed (0 duplicates in 31
   files); the walk's last-wins rule covers it without proof.
5. **Header rule strictness.** Requiring the header on line 1 is stricter
   than Pi (which skips junk before it). Assumed harmless because Pi
   never writes that shape; a hand-edited file would be skipped with a
   diagnostic.
6. **mtime source for the duplicate rule** is `os.Stat` on the path
   after symlink resolution; behavior on filesystems with coarse mtimes
   (ties) falls through to the path tiebreak.
