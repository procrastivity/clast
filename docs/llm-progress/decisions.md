# `llm-progress` — decisions this Matter made

This file records the calls that adding a progress line to `retro`, `wake`, and
`brief` forced. The wip Matter `llm-progress` set the intent. It left the
details below to the Builder, or it required that this file record them.

Status: Steps 01-05 are done. Step 06 records this file and the smoke check. The three verbs show a one-line
status on stderr while they wait on the LLM. The line shows only on a terminal.
The e2e suite needed no output changes. Two defaults are not confirmed by Beau
yet. The first section lists them.

## Two defaults wait for Beau

Beau confirmed two calls on 2026-10-02 (the standard-library TTY check, and
wake interactive phases only). The workplan set the rest at planning time, and
the executor and verifier made the implementation calls. Beau has not confirmed
these two:

- `brief` shows phases only. It does not stream the brief text to stdout.
- No progress shows when stderr is not a terminal, even with `--verbose`.

The code follows both defaults. The `--verbose` rule is a small edit to change.
Streaming the brief text to stdout is not. It is a real feature, which touches
stdout against stderr, `--json`, and the order of `Clear` against the output.
The workplan put it out of scope.

## The reporter lives in `internal/progress`, behind a context

`progress.New(streams, flags)` returns a `*Reporter`. `WithReporter` and
`FromContext` carry it in the `context.Context`. `Summarize`, `RunAuto`,
`RunInteractive`, and `Synthesize` keep their signatures, so no caller and no
test needed an edit.

Every method of `*Reporter` is safe on a nil receiver. `New` returns nil when
progress is off. It also returns nil for nil streams or a nil `streams.Err`. A
verb never checks for nil. This is also why no unit test or
e2e test sees the reporter (see the test gap below).

`llm-progress` waited only for `llm-streaming/step-01`. That Step added the
observer seam. The phase events come from there. `Reporter.Observer()` returns
an `llm.Observer` that only updates state under a mutex, as the `llm-streaming`
brief requires.

## The terminal check uses the standard library

`New` returns a reporter only when `streams.Err` is an `*os.File` whose `Stat()`
mode has `os.ModeCharDevice`. `TERM=dumb` also turns progress off. Beau chose
the standard-library check on 2026-10-02. The repo takes no `golang.org/x/term`
dependency for this.

Observation, not a Beau decision: a character device is not always a terminal.
`/dev/null` is one, so `2>/dev/null` turns the reporter on. It then writes
harmlessly to `/dev/null`. A fix for the width limit below would need `x/term`
or a `TIOCGWINSZ` call, so this call can come back.

## No progress with `--json`, or without a terminal, even with `--verbose`

`New` returns nil when `--json` is set. It also returns nil when stderr is not a
terminal. `--verbose` does not override either rule. The e2e tests run the
binary with stderr in a `bytes.Buffer`, so the reporter is off there. Their
expected output did not change.

The no-terminal rule with `--verbose` is one of the two unconfirmed defaults.
A user who pipes stderr to a file and passes `--verbose` gets no status lines.

## One line, redrawn in place

The line is `<frame> <label> · <phase> · <elapsed>`. The frame is a braille
spinner. The line starts with `\r\x1b[K` and has no newline.

`Status` draws one frame at once, under the mutex. A short wait still shows a
line. A goroutine redraws the line every 100 ms. It starts on the first
`Status` and runs until `Stop`. `Clear` only stops the drawing. It does not end
the goroutine, so a verb can alternate `Status` and `Clear` with no goroutine
churn. Because of this, every verb that builds a reporter must `defer Stop`.

`Stop` closes the quit channel once. It waits for the goroutine outside the
mutex, and it does not wait when the goroutine never started. `Status` resets
the phase and the delta count on every call, even when the label is the same.
The reporter ignores an unknown `llm.Phase`.

## A verb clears the line before it prints anything

Each verb calls `Clear` before any output on the same stream and before any
error return. The cleared line is `\r\x1b[K`. The next write then starts on a
clean row. A first pty probe of `wake` showed that removing `Clear` puts the
diagnostic on the status line, so the `Clear` is load-bearing.

## Phase text, and the Delta count

The phase shows after the label. `thinking` shows for reasoning deltas. The
phase `receiving` shows a character count (`receiving · 1985 chars`) once Delta
events arrive. A JSON reply, and any call before `llm-streaming/step-03`, shows
`receiving` without a count. `connecting` and `connected` last less than one
100 ms tick against a fast host, so they rarely show.

## The width comes from `COLUMNS`

`draw` reads `COLUMNS` on every draw. A value below 2, or one that is not a
number, falls back to 80. The line is cut to `width-1` runes, frame included.

This is a known risk (see the follow-ups). Most shells do not export `COLUMNS`
to child processes, so the width is usually 80. On a narrower terminal, the line
wraps.

## retro shows a counter only

`retro` shows `summarizing N/M`, with ` · K cached` added only when K is above
zero. It shows no phases. Four workers run at once, so one phase line would
flip between unrelated calls.

The label comes from the unexported `statusLabel`. `Summarize` calls
`defer rep.Clear()` right after it reads the reporter. Every return path, the
first-error cancel included, clears the line before `RunE` prints. A failed job
calls no `Status`. Jobs that are in flight and succeed after a failure still
update the counter.

`defer rep.Stop()` is inside the `HasEntries` block in `command.go`. It runs
when `RunE` returns. This is harmless. `Summarize` has already cleared the line.

## wake --auto shows the project and the call phases

`wake --auto` shows `drafting N/M · <project>` and the call phases. The command
builds the reporter after `llm.NewClient`, `Hostname`, and `ParseDay`, just
before the auto or interactive branch. An empty working set returns earlier, so
it never creates a reporter.

`RunAuto` attaches `rep.Observer()` to the context of `Draft` only. It calls
`Clear` right after `Draft` returns, before the error branch. The elapsed time
restarts for each session. The command also stores the reporter in the context
with `WithReporter`, which `RunInteractive` reads.

## wake interactive shows phases only

Beau chose this on 2026-10-02. `RunInteractive` shows `drafting · <project>`
with no N/M. The `== Session i/n ==` header already carries the count. The full
draft still prints with the menu, as before. The draft text is not streamed.

The Edit case of `disposition` shows `redrafting · <project>`. It computes the
label after the feedback-EOF return, so that path draws nothing. `Clear` runs
right after each `Draft` or `DraftWithFeedback`, before the failure diagnostic
and before the menu reprint. The draft and the menu reprint are byte-identical
with and without a live reporter. A test covers success and a failed redraft.

## brief shows phases only

`brief` shows `synthesizing brief · <slug>` and the call phases. The command
builds the reporter after the empty-brief return and after `llm.NewClient`, so
an empty brief or a bad config never creates one. It calls `Clear` before the
error return and before the JSON or human output. The brief text goes to stdout
as before.

This is the other unconfirmed default (see the first section).

## Verification came from pty probes, not from tests

The reporter is nil in every unit test and e2e test, and `progress` has no
writer seam. Probes with `script` on a stub, and the smoke check below on the
live endpoint, are the only evidence for the verb wiring.

Step 4 shows why this matters. The first pass reported an edit to `disposition`
that never applied. A scripted string replace failed on indentation, and no test
noticed. The verifier caught it from the diff and from a pty probe. Executors
now confirm each edit with `git diff` and `grep` output.

## Pty smoke check against the live endpoint

On 2026-10-02, with `CLAST_LLM_API_KEY` set and `llm.base_url` from the user
config, `script -qec '<cmd>' /dev/null | cat -v` ran both commands. The cache
was in a scratch `XDG_CACHE_HOME`.

- `retro --since -7d --refresh` drew `\r\x1b[K` frames of `summarizing 0/30 · 0s`
  up to `summarizing 30/30 · 26s`. The last write was a bare `\r\x1b[K`, and the
  `# Retro:` heading started on that row. No residue.
- `brief` drew `synthesizing brief · clast · 0s`, then
  `... · receiving · 1985 chars · 0s`, then a bare `\r\x1b[K`, then the brief.
  No `thinking` showed. The brief call's stream had no reasoning deltas: 0
  Thinking events, about 0.1 s in all, the same bytes on each run, consistent
  with a cached reply. A fresh prompt on the same endpoint streams reasoning, and
  the reporter draws `thinking`.
- `bin/clast brief 2>err.txt` wrote no bytes to `err.txt`. It has no `\x1b[K`.
- At 40 columns the `brief` line (about 58 characters) wraps. `\r\x1b[K` erases
  only the second row. The first row, `⠙ synthesizing brief · clast · receiving`,
  stays on screen above the brief. This confirms the width risk. The verifier
  reproduced the residue with the `pyte` terminal emulator on live bytes. With
  `COLUMNS=40` exported, the frame is cut to 39 runes and leaves no residue.
- The retro time varies with endpoint caching: 26 s in one run, 1.6 s in the
  verifier's run.

## Follow-ups outside this Matter

- Narrow terminals. `COLUMNS` is usually not exported, so the width is 80. On a
  terminal narrower than 80, a long line wraps and the clear leaves the first
  row on screen (the 40-column smoke run shows it). A real fix needs the window
  size from `x/term` or a `TIOCGWINSZ` call. Beau has this question.
- Test gap. No unit test and no e2e test fails when the verb wiring is removed
  (`Status`, `Clear`, or the observer). A possible fix is an exported test seam
  in `internal/progress`, for example a context-injected writer, so verb tests
  can assert that `Clear` comes before the next write. Three smaller gaps remain.
  No test covers a non-terminal `*os.File` stderr (a regular file or a pipe). No
  test checks that `Stop` waits for the goroutine. No in-package test covers
  `statusLabel`.
- Mutex held during the write. The draw loop holds the reporter mutex while it
  writes to the terminal. The observer needs the same mutex, and observer time
  counts toward `IdleTimeout`. A terminal that blocks for longer than 30s would
  stall the call and trip `IdleTimeout`. Any fix must keep the write
  order. A frame built before `Clear` must not reach the terminal after the
  erase of `Clear`, or a status line stays under the next verb output. Two
  options: a separate write mutex plus a generation check, or an observer that
  updates the phase through its own lock or atomics, so it never waits on
  terminal I/O.
- Doubled verb prefix. Error lines repeat the verb, for example
  `clast: brief: brief: synthesizing ...` and `clast: retro: retro: ...`. The
  `clasterr` messages already start with the verb, and `Execute` adds it again.
  This predates this Matter. It is not fixed here.
- The two unconfirmed defaults in the first section.
