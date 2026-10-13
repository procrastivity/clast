# pi source fixtures

Fixtures for the `pi` capture source (`pi-source` Matter, step-03):
self-contained JSONL session files plus the directory layouts a
discovery test can point a root at directly. No binary files, nothing
referenced across repos at test time. The contract these expectations
follow is `docs/pi-source/decisions.md` ("Contract (step-02)").

## Producing versions

| Pin | Where it produced evidence |
|---|---|
| Pi 0.83.0, JSONL header `version` 3 | terminal-multiplexers `apps/transcript-tail/fixtures/pi/` and `notes/06-pi.md`, `notes/18-opencode-pi-refresh.md`; duo `internal/runtime/pi/testdata/` (basic-with-resume, forked, injected-abort shapes) |
| Pi 0.84.4, JSONL header `version` 3 | qualification of the installed package source (`session-manager.js`, `docs/session-format.md`) and read-only inspection of 31 retained files, 2026-10-13 (decisions.md "Qualification evidence") |

The header does not record the writing Pi version (B1), so no fixture
claims a per-file writer version. Every `version: 3` fixture is a
structural re-synthesis; there is no byte copy of any Pi-written file.

## Scrub policy

Everything here is **synthesized**. No text was copied from any live or
prior-art file, including the prior-art fixtures that are byte-identical
to retained live files; they were re-synthesized with the same structure.

- All text content (`text`, `thinking`, tool `command`, tool output) is
  the placeholder `FIXTURE-OK`; error text is `FIXTURE-ERROR`.
- cwd is `/home/user/Code/demo` or `/home/user/Code/other`; directory
  names are the Pi encoding of those (`--home-user-Code-demo--`).
- Session ids are `01900000-0000-7000-8000-<12 digits>` (uuidv7-shaped);
  entry ids are 8 hex characters; tool call ids look like
  `call_FIXT01|fc_FIXT01` (the `|` pairing shape is real); provider,
  model and api are `fixture-*`.
- Kept for structural fidelity: header and entry field sets and key
  order, ISO `timestamp` plus unix-ms `message.timestamp`, the
  `thinking`/`text`/`toolCall` block shapes, `toolResult` as its own
  role with `toolCallId` pairing, `usage` (with `reasoning` and `cost`),
  `stopReason`, `rawStopReason`, `responseId`, `errorMessage`.
- Timestamps are fixed synthetic dates in 2026. Entries tick one second
  apart; the resume fixture jumps two days between turns.
- Every fixture here is **synthesized** from the field map; fixtures
  marked `constructed` model a shape no Pi run on this machine produced
  (torn tails, in-file branches, bad lines, hostile ids, unsupported
  versions, duplicates). They exercise the contract and are **not Pi
  evidence**; the qualification doc lists the corresponding evidence
  gaps (B2-B8). `constructed-from-live-shape` and
  `constructed-from-prior-art-shape` mean the structure repeats a shape
  that was observed (S-tags in decisions.md), with synthetic content.

## Layout: roots

Each `roots/<scenario>/` is a complete discovery root: point a source
at it with the fixture seam (`NewAt(root)`). Directory names starting
with `--` are the real Pi per-cwd encoding. Tools reading them need a
`./` prefix or `--`.

| Root | Layout | Use |
|---|---|---|
| `roots/percwd/` | per-cwd subdirectories (`--home-user-Code-demo--`, `--home-user-Code-other--`, `--no-cwd--`, `--relative-cwd--`) | every capturable shape; a discovery test over this root expects exactly the sessions whose path starts with `percwd/` in the capture table (16) |
| `roots/flat/` | files directly in the root (a custom `sessionDir`) | flat walk, mixed cwds |
| `roots/rejected/` | per-cwd | files Discover must skip, silently or with a diagnostic |
| `roots/diagnosed/` | per-cwd | files captured **and** diagnosed (filename vs header id) |
| `roots/duplicate/` | two per-cwd directories | one header id in two files (B7) |
| `roots/walkshape/` | per-cwd plus ignorable neighbours | one valid session; everything else must be ignored |

Not committed, built by tests in `t.TempDir()` because they depend on
the checkout path, the filesystem, or size: absolute-path `settings.json`
(`sessionDir`), `PI_CODING_AGENT_*` environment cases, symlinked
directories and files, mtimes (git does not keep them: the duplicate
pair needs `os.Chtimes`), an empty-but-existing root, an absent root, an
unreadable root (mode 0), a header line over 1 MiB, a single line over
16 MiB, a file that changes between Discover and Capture.

## Expected facts: capturable sessions

Contract columns: `StartedAt` = header timestamp; `LastActiveAt` = max of
header and all parseable entry timestamps; `Lines` = newline bytes plus
one for a non-empty unterminated final line (`source.FingerprintFile`
semantics); `User`/`Assistant` = leaf-path counts (whole-file on a broken
path); `Substantive` per the contract; `Correlate` = header cwd when it is
an absolute path, else `""`; `Format` = `pi-jsonl-v3` and artifact
`transcript.jsonl` for all of them; `Branch` is always `""`. The
native id is the header id (shown here by its last 6 characters; the full
id is `01900000-0000-7000-8000-` plus 12 digits).

| File (under `roots/`) | Key | Correlate | StartedAt | LastActiveAt | Lines | User | Assistant | Substantive | Diagnostics |
|---|---|---|---|---|---|---|---|---|---|
| `percwd/--home-user-Code-demo--/2026-01-10T10-00-00-000Z_01900000-0000-7000-8000-000000000001.jsonl` | basic | `/home/user/Code/demo` | 2026-01-10T10:00:00.000Z | 2026-01-10T10:00:06.000Z | 9 | 1 | 3 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-10T11-00-00-000Z_01900000-0000-7000-8000-000000000002.jsonl` | resume | `/home/user/Code/demo` | 2026-01-10T11:00:00.000Z | 2026-01-12T09:30:02.000Z | 9 | 2 | 3 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-11T08-00-00-000Z_01900000-0000-7000-8000-000000000003.jsonl` | fork-parent | `/home/user/Code/demo` | 2026-01-11T08:00:00.000Z | 2026-01-11T08:00:04.000Z | 7 | 2 | 2 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-11T12-00-00-000Z_01900000-0000-7000-8000-000000000004.jsonl` | fork-child | `/home/user/Code/demo` | 2026-01-11T12:00:00.000Z | 2026-01-11T12:00:02.000Z | 9 | 3 | 3 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-11T13-00-00-000Z_01900000-0000-7000-8000-000000000005.jsonl` | fork-fresh | `/home/user/Code/demo` | 2026-01-11T13:00:00.000Z | 2026-01-11T13:00:00.000Z | 7 | 2 | 2 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-13T09-00-00-000Z_01900000-0000-7000-8000-000000000006.jsonl` | branch | `/home/user/Code/demo` | 2026-01-13T09:00:00.000Z | 2026-01-13T09:00:06.000Z | 9 | 2 | 2 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-14T09-00-00-000Z_01900000-0000-7000-8000-000000000007.jsonl` | torn-tail | `/home/user/Code/demo` | 2026-01-14T09:00:00.000Z | 2026-01-14T09:00:02.000Z | 6 | 1 | 1 | yes | none (torn tail is silent) |
| `percwd/--home-user-Code-demo--/2026-01-14T10-00-00-000Z_01900000-0000-7000-8000-000000000008.jsonl` | unterminated-valid | `/home/user/Code/demo` | 2026-01-14T10:00:00.000Z | 2026-01-14T10:00:02.000Z | 5 | 1 | 1 | yes | none |
| `percwd/--home-user-Code-demo--/2026-01-14T11-00-00-000Z_01900000-0000-7000-8000-000000000009.jsonl` | bad-lines | `/home/user/Code/demo` | 2026-01-14T11:00:00.000Z | 2026-01-14T11:00:07.000Z | 10 | 2 | 2 | yes | 3 bad line(s), first at line 6 |
| `percwd/--home-user-Code-demo--/2026-01-14T12-00-00-000Z_01900000-0000-7000-8000-000000000010.jsonl` | broken-chain | `/home/user/Code/demo` | 2026-01-14T12:00:00.000Z | 2026-01-14T12:00:04.000Z | 7 | 2 | 1 | yes | 1 bad line(s), first at line 5; leaf path broken -> whole-file counts |
| `percwd/--home-user-Code-demo--/2026-01-15T09-00-00-000Z_01900000-0000-7000-8000-000000000011.jsonl` | header-only | `/home/user/Code/demo` | 2026-01-15T09:00:00.000Z | 2026-01-15T09:00:00.000Z | 1 | 0 | 0 | no | none |
| `percwd/--home-user-Code-demo--/2026-01-15T10-00-00-000Z_01900000-0000-7000-8000-000000000012.jsonl` | error-only | `/home/user/Code/demo` | 2026-01-15T10:00:00.000Z | 2026-01-15T10:00:02.000Z | 5 | 1 | 1 | no | none |
| `percwd/--home-user-Code-demo--/2026-01-15T11-00-00-000Z_01900000-0000-7000-8000-000000000013.jsonl` | aborted-partial | `/home/user/Code/demo` | 2026-01-15T11:00:00.000Z | 2026-01-15T11:00:02.000Z | 5 | 1 | 1 | yes | none |
| `percwd/--no-cwd--/2026-01-16T09-00-00-000Z_01900000-0000-7000-8000-000000000014.jsonl` | no-cwd | `""` | 2026-01-16T09:00:00.000Z | 2026-01-16T09:00:02.000Z | 5 | 1 | 1 | yes | none |
| `percwd/--relative-cwd--/2026-01-16T10-00-00-000Z_01900000-0000-7000-8000-000000000015.jsonl` | relative-cwd | `""` | 2026-01-16T10:00:00.000Z | 2026-01-16T10:00:02.000Z | 5 | 1 | 1 | yes | none |
| `percwd/--home-user-Code-other--/2026-01-16T11-00-00-000Z_01900000-0000-7000-8000-000000000016.jsonl` | other-cwd | `/home/user/Code/other` | 2026-01-16T11:00:00.000Z | 2026-01-16T11:00:02.000Z | 5 | 1 | 1 | yes | none |
| `flat/2026-02-01T09-00-00-000Z_01900000-0000-7000-8000-000000000020.jsonl` | flat-a | `/home/user/Code/demo` | 2026-02-01T09:00:00.000Z | 2026-02-01T09:00:02.000Z | 5 | 1 | 1 | yes | none |
| `flat/2026-02-01T10-00-00-000Z_01900000-0000-7000-8000-000000000021.jsonl` | flat-b | `/home/user/Code/other` | 2026-02-01T10:00:00.000Z | 2026-02-01T10:00:02.000Z | 5 | 1 | 1 | yes | none |
| `diagnosed/--home-user-Code-demo--/2026-03-02T09-00-00-000Z_01900000-0000-7000-8000-999999999999.jsonl` | filename-id-mismatch | `/home/user/Code/demo` | 2026-03-02T09:00:00.000Z | 2026-03-02T09:00:02.000Z | 5 | 1 | 1 | yes | filename id differs from header id |
| `diagnosed/--home-user-Code-demo--/no-underscore-name.jsonl` | filename-no-underscore | `/home/user/Code/demo` | 2026-03-02T10:00:00.000Z | 2026-03-02T10:00:02.000Z | 5 | 1 | 1 | yes | filename id differs from header id |
| `duplicate/--home-user-Code-demo--/2026-03-03T09-00-00-000Z_01900000-0000-7000-8000-000000000050.jsonl` | dup-original | `/home/user/Code/demo` | 2026-03-03T09:00:00.000Z | 2026-03-03T09:00:02.000Z | 5 | 1 | 1 | yes | duplicate id (loser) if not newest mtime |
| `duplicate/--home-user-Code-copy--/2026-03-03T09-00-00-000Z_01900000-0000-7000-8000-000000000050.jsonl` | dup-import-copy | `/home/user/Code/demo` | 2026-03-03T09:00:00.000Z | 2026-03-03T09:00:04.000Z | 7 | 2 | 2 | yes | duplicate id (loser) if not newest mtime |
| `walkshape/--home-user-Code-demo--/2026-03-04T09-00-00-000Z_01900000-0000-7000-8000-000000000060.jsonl` | walk-control | `/home/user/Code/demo` | 2026-03-04T09:00:00.000Z | 2026-03-04T09:00:02.000Z | 5 | 1 | 1 | yes | none |

Notes per fixture:

- `basic` (constructed-from-live-shape): basic linear session: model/thinking header entries, two toolCall/toolResult pairs (ids contain `|`), final stop; leaf path == whole file.
- `resume` (constructed-from-live-shape): resume-append: one header, second user turn two days later appended with no new model_change; the chain continues (S5).
- `fork-parent` (constructed-from-prior-art-shape): parent of the --fork pair (4 messages after model/thinking entries).
- `fork-child` (constructed-from-prior-art-shape): --fork child: new header id + parentSession; parent entry lines byte-identical (same entry ids), then one new turn. Counted on its own (M14, S6).
- `fork-fresh` (constructed-from-prior-art-shape): fresh --fork with no new entries: copied entries are older than the header, so LastActiveAt must be the header time (not before StartedAt).
- `branch` (constructed): /tree branch inside one file: u3 re-asks from a1; leaf path = u1,a1,u3,a3 -> user 2 asst 2 (whole-file would be 3/3).
- `torn-tail` (constructed): torn tail: final line cut mid-field, no newline; verbatim in the copy, excluded from facts, no diagnostic; Lines counts the unterminated line.
- `unterminated-valid` (constructed): last line is complete valid JSON but has no trailing newline: parsed into facts (Pi parses it too); Lines still counts it once.
- `bad-lines` (constructed): three mid-file bad lines (truncated JSON; two objects glued on one line; JSON with no string id): skipped from facts, one aggregated diagnostic, first at line 6.
- `broken-chain` (constructed): leaf walk hits a missing parent (the assistant line was corrupted): counts fall back to whole-file (user 2 asst 1) with a "leaf path broken" diagnostic plus the bad-line diagnostic.
- `header-only` (constructed): valid v3 header, no entries: captured, counts 0, not substantive, StartedAt = header time.
- `error-only` (constructed-from-live-shape): only assistant reply is stopReason "error" with zero content blocks (live shape, 5 of 1261): Assistant counts 1, Substantive false.
- `aborted-partial` (constructed): assistant "aborted" but with a content block: substantive true (the user saw partial work).
- `no-cwd` (constructed): header without cwd: Correlate "" (projectless), still captured.
- `relative-cwd` (constructed): header cwd is not absolute: Correlate "" (projectless), still captured.
- `other-cwd` (constructed): second per-cwd directory (different cwd): the per-cwd walk covers more than one subdirectory.
- `flat-a` (constructed): flat layout (a custom sessionDir): files directly in the root, mixed cwds, no --cwd-- directories.
- `flat-b` (constructed): flat layout (a custom sessionDir): files directly in the root, mixed cwds, no --cwd-- directories.
- `filename-id-mismatch` (constructed): filename id differs from header id: captured under the header id with a diagnostic.
- `filename-no-underscore` (constructed): filename has no "_": header id wins, diagnostic, captured.
- `dup-original` (constructed): duplicate header id (B7), original copy: 1 turn. Same basename as the import copy below; the test sets mtimes with os.Chtimes (git does not keep them).
- `dup-import-copy` (constructed): duplicate header id (B7), import copy that later grew to 2 turns. Newest mtime wins; the other path gets a duplicate diagnostic.
- `walk-control` (constructed): the one valid session in a root that also holds ignorable neighbours.

## Expected outcomes: files that are not captured

| File (under `roots/`) | Key | Outcome | Diagnostic |
|---|---|---|---|
| `rejected/--home-user-Code-demo--/2026-03-01T09-00-00-000Z_01900000-0000-7000-8000-000000000030.jsonl` | version-2 | skipped | `unsupported session version N (supported: 3)`; "version missing" when absent |
| `rejected/--home-user-Code-demo--/2026-03-01T10-00-00-000Z_01900000-0000-7000-8000-000000000031.jsonl` | version-4 | skipped | `unsupported session version N (supported: 3)`; "version missing" when absent |
| `rejected/--home-user-Code-demo--/2026-03-01T11-00-00-000Z_01900000-0000-7000-8000-000000000032.jsonl` | version-missing | skipped | `unsupported session version N (supported: 3)`; "version missing" when absent |
| `rejected/--home-user-Code-demo--/2026-03-01T12-00-00-000Z_hostile-dotdot.jsonl` | hostile-dotdot | skipped | id fails the id rule |
| `rejected/--home-user-Code-demo--/2026-03-01T13-00-00-000Z_trailing.jsonl` | hostile-trailing-dot | skipped | id fails the id rule |
| `rejected/--home-user-Code-demo--/2026-03-01T14-00-00-000Z_zero-byte.jsonl` | zero-byte | skipped silently | none |
| `rejected/--home-user-Code-demo--/2026-03-01T15-00-00-000Z_header-inflight.jsonl` | header-inflight | skipped silently | none |
| `rejected/--home-user-Code-demo--/2026-03-01T16-00-00-000Z_no-header.jsonl` | no-header | skipped | first line is not a session header |
| `rejected/--home-user-Code-demo--/2026-03-01T17-00-00-000Z_junk-first-line.jsonl` | junk-before-header | skipped | first line is not a session header |
| `walkshape/--home-user-Code-demo--/deeper/nested/2026-03-04T10-00-00-000Z_01900000-0000-7000-8000-000000000061.jsonl` | too-deep | never a candidate | none |
| `walkshape/--home-user-Code-demo--/notes.txt` | other-suffix | never a candidate | none |
| `walkshape/--home-user-Code-demo--/2026-03-04T09-00-00-000Z_01900000-0000-7000-8000-000000000060.jsonl.bak` | bak-suffix | never a candidate | none |
| `walkshape/stray-file-in-root.txt` | root-stray | never a candidate | none |

- `version-2` (constructed): header version 2 (Pi migrates it in place on open, B2): skipped with the unsupported-version diagnostic.
- `version-4` (constructed): header version 4 (v4 harness class, B8): skipped with the unsupported-version diagnostic; entry shapes here are v3-like placeholders only.
- `version-missing` (constructed): header with no version (Pi treats as v1; v1 entries have no id/parentId): skipped, "version missing".
- `hostile-dotdot` (constructed): header id "../../escape" fails the id rule (would escape the journal session directory): skipped.
- `hostile-trailing-dot` (constructed): header id ends with "." (Pi id rule requires an alphanumeric end): skipped.
- `zero-byte` (constructed): zero-byte file: skipped silently (transient create).
- `header-inflight` (constructed): unterminated, unparseable first line and no newline anywhere: a header being written; skipped silently.
- `no-header` (constructed): first line is an entry, not a header: skipped with a diagnostic.
- `junk-before-header` (constructed): unparseable newline-terminated line before a valid header: Pi would skip the junk and accept the file; clast is stricter (header must be line 1) and skips with a diagnostic.
- `too-deep` (constructed): valid file two directories below a root subdirectory: the walk is one level only, so never discovered.
- `other-suffix` (constructed): non-.jsonl neighbour, ignored without comment.
- `bak-suffix` (constructed): .jsonl.bak copy, ignored (suffix is not .jsonl).
- `root-stray` (constructed): non-session file directly in the root, ignored.

## How tests use these

- Copy a root into `t.TempDir()` when a test sets mtimes or mutates
  files (resume growth, truncate and regrow, newline repair); read the
  committed tree directly otherwise.
- Resume/regrowth, newline repair, truncate-then-regrow to the same line
  count, and the freshness table are mutation scenarios built on
  `percwd` copies, not separate files: the fixtures pin bytes, not
  events over time.
- The fork pair's parent lines are byte-identical (checked with `tail -n
  +2` and a hash when the fixtures were generated); `fork-child`'s
  `parentSession` is an absolute path that does not exist on a test
  machine, deliberately.
- Header-line cases at the size bound (1 MiB) and the 16 MiB line bound
  are generated in the test, not committed.

## Known fixture gaps

- No real Pi byte content: structure only. Nothing here proves how Pi
  1) writes a `/fork` or `/clone` file (path-only copy, label drop), 2)
  writes a v1/v2 file, 3) leaves a glued line, or 4) lays out a flat
  custom `sessionDir` (decisions.md B3-B6).
- `custom`, `custom_message`, `label`, `branch_summary` and
  `bashExecution` entries are absent: they were never seen live and
  carry no fact; an unknown entry type is covered by "nothing else
  counts" in the contract and should get a test-local one-liner.
- No images or large payloads.
- Windows path forms (`\`, drive colons in the cwd encoding) are not
  pinned.

