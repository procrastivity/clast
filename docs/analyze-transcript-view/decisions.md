# `analyze-transcript-view` — decisions this Matter made

This file records the calls that building the transcript view forced. The
workplan of the wip Matter `analyze-transcript-view` set the intent. It left
the details below to the Builder. The Matter extends `clast analyze` (see
`docs/analyze-verb/decisions.md`).

Status: all five Steps are complete. Step 05 (this file) closes the Matter.
The seal condition holds. A session page shows the full conversation with tool
calls collapsed. Subagent transcripts are reachable. The overview page weighs
4.3% more, and it holds no transcript content. Two e2e tests cover the seal
condition: `TestAnalyze_Serve_TranscriptPagesEndToEnd` and
`TestAnalyze_Overview_CarriesNoTranscriptContent`.

## Transcripts are serve-only

Beau decided on 2026-09-29 that transcripts exist only while `clast analyze`
serves. `--out` still writes one static file. That file holds no transcript
content.

Each session card links to `/t/<session-id>`. A static file cannot follow that
link, so the card also shows a hint. The hint says the transcript needs the
server.

The served page and the `--out` file stay byte-identical. This is the seal of
the `analyze-verb` Matter. So both outputs hold the link and the hint. A small
script removes the hint when the page does not load from a `file:` URL. The
check runs on the client only. The server sends no different bytes.

## A new `TranscriptReader` interface sits beside `TranscriptRenderer`

A format survey of the live journal showed what the view must handle:

- 180 transcripts and 348 subagent files. The largest file is 3.9 MB.
- About 25 record types. `attachment` is the most numerous, with 24,000 records.
- `tool_use` and `tool_result` blocks pair by id. Each appears 17,346 times.
- `subagents/agent-<id>.meta.json` holds a `toolUseId`. It links a subagent
  to the Agent call of its parent.

`source.TranscriptRenderer` returns only `Role` and `Text`. It drops tool
calls, thinking, sidechains, and meta records. That output is too thin for
this view.

The Matter added the optional interface `source.TranscriptReader`. It has these
methods:

- `TranscriptFormats`
- `ReadTranscript(io.Reader) ([]Event, error)`
- `ListSubagents(sessionDir) ([]Subagent, error)`

`registry.LookupTranscriptReader(format)` resolves a reader by
`transcript.format`. This is how the registry resolves a renderer.

The alternative was to parse claude-jsonl inside `analyzeverb`. That would
put source-specific knowledge in a verb. The seam keeps it in the source.
`show --transcript` is unchanged and still uses `TranscriptRenderer`.

## The event model

`ReadTranscript` returns source-agnostic `Event` values. The kinds are:

- `prompt`
- `assistant`
- `thinking`
- `tool_call`, which holds its paired `ToolResult`
- `tool_result`, only for a result with no call
- `meta`, with a `Label` that names the sub-kind

Every kind of record that is not conversation becomes `meta`. The label keeps
the sub-kind, so the page can show what was hidden. The survey counted 42,706
meta events against 1,100 prompts. So the page hides meta by default.

Four rules classify the rest:

1. Results pair to calls by `tool_use_id`. An unpaired result becomes an
   orphan `tool_result` event.
2. Thinking with a signature and no text is `Redacted`. Real data has 4,537
   such blocks among 5,088. The page folds them into one muted line.
3. An injected user turn is `meta`, not a prompt. This covers `isMeta` records
   and non-human origins such as `task-notification`, `peer`, and
   `auto-continuation`. The label is `user:<origin>` or `user:meta`, and the
   text stays.
4. The reader drops a repeated `uuid`. Forked history repeats records. The
   rule matches the identity rule in `scan.go`.

Each real assistant entry holds one block. So one event per block needs no
merging. Main transcripts hold no sidechain entries. Subagent files are all
sidechain, and the reader keeps them.

A bad JSON line is skipped. If the scanner fails, the reader returns the
events read so far with the error.

## Subagent linkage and `ListSubagents`

`Subagent` holds `ID`, `Path`, `AgentType`, `Description`, and `ToolUseID`.
`Path` is relative to the session directory.

`ListSubagents` sits on the reader because `.meta.json` is specific to
claude. A missing or bad meta file still lists the transcript, with empty
fields.

The Agent call on the parent page links to `/t/<id>/agents/<agent-id>`. The
subagent page links back to `/t/<id>#call-<tool-id>`.

## Rendering rules

The page is `assets/analyze/transcript.html`. A user can override it, as with
`index.html`.

- Prompts and assistant turns show as markdown prose.
- Thinking and each tool call sit in a closed `<details>`. The summary line
  names the tool and a digest of its most telling input. A digest holds at most
  120 runes.
- Meta records are hidden. A hidden `#showmeta` checkbox and sibling CSS toggle
  them. This needs no script.

### The output caps

The page caps every tool result and every pretty-printed input at 200 lines or
20 KiB. A note marks each cut. The cut is safe for UTF-8.

The caps apply to inputs too, because a `Write` call carries a whole file.
Without caps, one result can swamp the page. The largest real result is about
72 KB, and a `Write` input can be larger.

### Markdown safety

The only `template.HTML` in the package is `markdownHTML`. It escapes the text
first. It then adds markup for a small subset. It links `http` and `https`
URLs only. Transcript text is untrusted, so no other path may emit raw HTML.

## The URL scheme

All paths are absolute and use `url.PathEscape`:

| Helper | Result |
| --- | --- |
| `TranscriptURL` | `/t/<id>` |
| `AgentURL` | `/t/<id>/agents/<agent-id>` |
| `OverviewURL` | `/#s-<id>` |
| `CallAnchor` | `call-<tool-id>` |

The session card shows the link when `TranscriptLines` is greater than zero.

## The lookup scope is the window of the request

The handler gathers the window on each request, as the overview does. It finds
the session in that window. A session outside the window gives a 404 that names
the window.

The overview `Session` supplies the header facts, because `session.json` has no
title. `GatherTranscript(sessionDir, sess, agentID)` takes that `Session`. An
empty `agentID` reads `transcript.jsonl` and fills the subagent list.

`Session` gained an unexported `dir` field, set by `journal.SessionDir`. No
template can read it. A test asserts that no path appears in the overview.

## Routes are parsed by hand

The handler reads the route from `EscapedPath`. It does not use the Go 1.22
`ServeMux` patterns. The mux has two behaviors this view cannot accept:

- It answers a cleaned path such as `/t/x/agents/../../etc` with a 301 redirect.
- It decodes `%2f` inside a wildcard.

With hand parsing, the handler sees the raw segments and can refuse them.

## Path-safety rules

- The handler refuses an id that is empty or a dot segment.
- It refuses an id that has a slash, a backslash, or a NUL byte.
- It never joins the agent id into a path. It matches the id against the
  list from `ListSubagents` and uses the `Path` that the list returns.
- It refuses a listed `Path` that leaves the session directory
  (`filepath.IsLocal`).

## Error mapping

| Condition | Response |
| --- | --- |
| Session outside the window | 404, names the window |
| `analyze.agent-not-found` | 404 |
| Zero events read | 500 |
| Partial read | 200 with a warning banner |
| Other failure (`analyze.transcript-unavailable`) | 500, logged |

A partial read renders what it got. The banner text is generic, and the
handler logs the real error. Only a line over 16 MiB causes a partial read.

## The overview weight

The overview grew from 72,812 to 75,972 bytes. This is +4.3%. The change is 16
links, the hint, and a little CSS and script.

The overview holds no transcript content. `TestAnalyze_Overview_CarriesNoTranscriptContent`
proves it. It puts a marker in a prompt, a tool result, and a thinking block.
The marker never appears in `/` or in the `--out` file. It does appear at
`/t/<id>`. The test puts the marker in the second prompt, because the first
prompt feeds the session title. `/` still equals the `--out` file byte for
byte.

## Measured sizes and timings

Real data, on the three largest transcripts:

- HTML is about 0.25 times the input size. A 3.9 MB transcript gives about
  990 KB.
- Reading takes 37 ms or less. Rendering takes 17 ms or less.
- The reader parsed all 533 files with no error. The slowest took 33 ms.
- A session page with 46 subagents is 547 KB and takes 31 ms.

`TestGatherAndRenderTranscript_Large` uses a generated transcript of more than
3 MB with 2,000 tool pairs. The output is smaller than the input. It shows 200
truncation notes. No `<pre>` block exceeds 200 lines or 40 KB of escaped
bytes. The test takes about 0.5 s. The repo uses neither `testing.Short` nor
timing assertions, so the test has none.

The serve e2e helpers `startAnalyzeServe` and `httpGet` came out of the older
tests for reuse.

## A failure that this Matter did not cause

`TestSeal_StaleAloneFiltersCorrectly` in `internal/cli` fails. It also fails
on commit `18e7546`, before `analyze-verb` began, and neither Matter touched
its code. Someone should look at it
separately.
