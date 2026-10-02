# `llm-streaming` — decisions this Matter made

This file records the calls that adding SSE streaming and call-lifecycle events
to `internal/llm` forced. The wip Matter `llm-streaming` set the intent. It left
the details below to the Builder, or it required that this file record them.

Status: all five Steps are locally complete. `Complete` streams by default and
reports its phases to an optional observer. The Matter `llm-progress` reads the
brief and builds the progress output on this seam.

## The event seam lands first and travels in the context

Step 1 added the observer before any SSE parsing. `llm-progress` waits only for
this Step. Progress output can ship before the SSE parser does.

`WithObserver(ctx, o)` stores an `Observer` in the context. `Complete` reads it
from there. The `Complete` signature did not change, so the three verbs compile
without edits. `WithObserver(ctx, nil)` returns the same context.

`Complete` wraps the observer once with `serialize`, a per-call mutex. Every
emit, `PhaseStart` and `PhaseDone` included, goes through it. Callbacks for one
call never overlap, because `httptrace` callbacks can run on transport
goroutines. An observer shared across concurrent calls must lock itself. No
test covers the mutex. Review checks it.

## The phase order is a contract

`PhaseStart` comes first. `PhaseDone` comes last and only on success. An error
ends the call with no `PhaseDone`. `PhaseFirstByte` comes before any
`PhaseThinking` or `PhaseDelta`. `PhaseConnected` may repeat.

`PhaseRequestSent` is not promised. When the server answers before it reads the
whole request body, `WroteRequest` reports an error and the phase is skipped.
The call can still succeed. A consumer must not wait for it.

`PhaseDone` is emitted after the empty-choices check on the JSON path. A reply
with no choices is a failure and emits no `PhaseDone`.

## Streaming is on by default

The request carries `"stream": true` unless `llm.stream` is `false`. Beau chose
this on 2026-10-02. Streaming is what makes progress output possible, and the
opt-out keeps an unusual endpoint usable.

`llm.stream` is a new key under `llm:`, beside `base_url` and `model`. A value that is not a bool gives a plain
error that names `llm.stream`. The error comes before the API key check.
`ConfiguredModel` does not read the key.

## A JSON reply falls back by Content-Type

`Complete` chooses the reader from the reply `Content-Type`, not from what it
asked for. `text/event-stream` (any parameters) on a 2xx reply takes the SSE
reader. Anything else takes the JSON reader. A server that ignores
`stream: true` therefore works. A non-2xx reply always takes the JSON reader, so
its status and body appear in the error as before.

## A clean EOF without `[DONE]` is success

The SSE reader ends at `data: [DONE]` or at a clean end of the connection. Some
proxies close the connection without `[DONE]`. The reader treats that as
success, and it flushes an unterminated final event at EOF.

The reader stops reading as soon as it sees `[DONE]`. A first version read one
more line. A server that held the connection open made `Complete` wait, and the
idle timeout would have reported it as stalled. A regression test pins the fix.

An error event (`error.message` at the top level) ends the stream. The reader
drops the content it collected and returns the message.

## Reasoning deltas are reported, not returned

The reader takes `choices[0].delta.content`, `reasoning_content`, and
`reasoning`. Content becomes `PhaseDelta` and joins the reply. Reasoning becomes
`PhaseThinking` and never joins the reply.

`reasoning_content` wins over `reasoning`. One event gives one `PhaseThinking`.
Empty strings emit nothing. The live endpoint (LiteLLM with a vllm Qwen
reasoning model) sends many `reasoning_content` events before any content. Without
`PhaseThinking`, the progress output would be silent through the whole think.

The parser ignores the rest of the event. The parser does not handle bare CR line
ends or a lone `data` line, which the SSE spec allows. The live endpoint does not
use them.

## A stream with no content returns an empty reply

A stream that ends with no content returns `""` and no error. The JSON path
does the same for empty or null content, and it errors only when `choices` is
empty. A reasoning model that spends its whole budget on reasoning gets `""` on
both paths. Both paths stay consistent. The verbs treat an empty reply as they did before
on the JSON path. Retro caches it.

## Three limits replace the one whole-request timeout

`DefaultTimeout` and `http.Client.Timeout` are gone. A whole-request timeout cuts
a long stream in the middle of the body.

| Constant | Value | Limit | Error text |
| --- | --- | --- | --- |
| `FirstByteTimeout` | 30s | request sent to response headers | `timeout awaiting response headers`, wrapped in `llm: calling <endpoint>` |
| `IdleTimeout` | 30s | silence between two body reads | `llm: <endpoint> stalled: no data for 30s` |
| `MaxDuration` | 5m | whole call, connect to last byte | `llm: <endpoint> exceeded 5m0s` |

The first-byte limit is `ResponseHeaderTimeout` on the transport. The idle
limit is a timer that starts after the headers arrive. Each body read that
returns data resets it. It covers both the SSE and the JSON path. The total
limit is a context deadline on the whole call.

A cancel of the parent context passes the parent's error through (wrapped),
so `errors.Is(err, context.Canceled)` holds. `classify` relabels an error only when
it is a cancellation-shaped error and the parent is still live. A malformed
event that arrives just after a timer fires keeps its own error.

## Observer time counts toward the idle limit

The observer runs inside the read loop. A slow observer delays the next read
and can trip `IdleTimeout` on a healthy stream. The `Complete` doc comment says
so. `llm-progress`'s observer only updates state under a mutex. The draw loop
holds that mutex while it writes to the terminal, so a terminal blocked for
longer than `IdleTimeout` would stall the call.

## Each client owns its transport

Each `NewClient` clones `http.DefaultTransport` so it can set
`ResponseHeaderTimeout`. Each clone has its own keep-alive pool. This is
harmless today, because each verb calls `NewClient` once. Share one transport if
that changes. `SetTimeouts` in `export_test.go` rebuilds the client for tests.

## The test stub gained stream modes and delays

`llmtest` can answer with SSE (`SetStream`), with raw SSE bytes
(`SetStreamRaw`), and with delays (`SetDelay`). The default behavior is
byte-identical to before. The stub cannot send `text/event-stream; charset=utf-8`
or SSE with a non-2xx status. The tests that need those use a local
`httptest.NewServer`.

`TestTimeout_LongStreamNotCut` uses a total limit of 5s, not 1s. The total limit
also covers the dial, and one loopback stall of about 1s on the WSL2 development
machine failed a 1s version. The test still proves that the first-byte and idle
limits do not cut a long stream.

## A rare 1s stall is a known flake source

In about 1,100 stress runs, the verifier saw one unexplained failure and a few
stalls of about 1.03s. 900 targeted runs gave no failure. The likely cause is a
SYN retransmit on WSL2 loopback. It is not proven. A test with a tight time
bound can flake for this reason.

## Follow-ups outside this Matter

- SURFACE V12 needs an amendment for streaming. It belongs in `clast-reboot`.
  That step is Beau's. This repo does not hold it.
- `assets/config.default.yaml` still says "SURFACE V31 — five knobs". `llm.stream`
  adds a key that V31 does not list. This is a SURFACE matter, so this Matter left the file alone.
- Retro caches an empty summary. A reasoning model that spends its whole budget
  on reasoning leaves an empty cached summary for that session. This behavior
  predates this Matter.
- `README.md` does not document the `llm:` keys, so it did not change.
