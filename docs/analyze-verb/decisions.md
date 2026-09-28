# `analyze-verb` — decisions this Matter made

This file records the calls that building `clast analyze` forced. The
workplan of the wip Matter `analyze-verb` (Linear BDS-236) set the intent. It
left the details below to the Builder, or it required that this file record
them.

Status: all six Steps are locally complete. The seal condition holds. The
verb serves the explorer for a real journal window. The `--out` file is
byte-identical to the served page. Retro cache keys did not change. The e2e
suite passes.

## The default window is today with `--since -6d`

`clast analyze` shows the last seven days by default. Retro defaults to
`yesterday`. This is a deliberate divergence. An explorer browses a stretch of
days. A retro renders one day.

`--since` is a real flag default, not a special case. `clast analyze
2026-09-20` shows the seven days that end on the 20th. `--since -0d` shows one
day.

## The verb serves by default and never opens a browser

Bare `clast analyze` starts an HTTP server with stdlib `net/http`. It binds
`--addr` (default `127.0.0.1:0`), prints the bound URL to stdout, and blocks
until the user stops it. With `--json`, it prints a `{"url": ...}` line.

The verb never opens a browser. The URL is the interface. On a remote host, the
user forwards the port.

The handler re-gathers the window on every page request. A reload always shows
the current journal. The handler answers `/` only and accepts `GET` and `HEAD`
only. It sends `Cache-Control: no-store`. A gather or render error gives a 500
and a line on stderr. A bind failure gives the error `analyze.listen-failed`.

## `--out` writes the same page, and excludes `--addr`

`--out file.html` writes the page to one static file and exits. It writes a
temporary file and renames it, so a reader never sees half a page. No shared
atomic-write helper exists, so the verb has its own. A failure gives the error
`analyze.write-failed`.

`--out` and `--addr` are mutually exclusive. Cobra enforces this with
`MarkFlagsMutuallyExclusive`. The rule applies only when the user sets both
flags.

`renderPage` is the single render path. `NewHandler` and the `--out` writer
both call it. The e2e test compares the served body with the `--out` file for
one window and requires equal bytes. This is the seal condition.

## Summaries come from the cache only

Analyze never calls the LLM. A session with an entry but no cached retro
summary shows "not summarized — run `clast retro <day>`". A missing
`llm.model` setting makes every lookup miss. The page shows the hint and the
verb does not fail.

### Why `internal/retrocache` exists

Analyze must compute the same cache key as retro. The layering rule says no
verb imports another verb's package. So the cache code moved out of
`retroverb` into `internal/retrocache`. It holds `Dir`, `Fingerprint`, `Get`,
`Put`, `Entry`, and `EntryKey`. Both verbs import it.

`Entry` takes plain fields, not `retroplumbing.EntryRow`. This keeps
`retrocache` below both verbs. Retro renders its real prompt and its key from
the same `Entry`.

### How the key is built

- `EntryKey` blanks `{{project}}` before it fingerprints the rendered prompt
  pair. This matches retro's recipe. Retro reuses one summary across projects.
- `Day` is always `row.Day`. Retro's `{{day}}` is the top day only for a
  single-day window. There, `windowSessions` guarantees `row.Day` equals the top
  day. So analyze keys an entry without knowing retro's window.
- The model comes from the new `llm.ConfiguredModel`. It reads the same config
  path as `NewClient` but needs no `base_url` and no API key. Its value equals
  retro's `client.Model()`.

### How the refactor was verified

The keys did not change. Two checks show this.

1. A throwaway test printed `EntryKey` for a fixed fixture before and after the
   refactor. It covered single-day, multi-day without a project, and multi-day
   cases. The keys were identical.
2. A helper recomputed the keys for 13 real sessions after the refactor. All 13
   were cache hits.

`TestEntryKey_IsFingerprintOfBlankedPair` and `TestEntryKey_IgnoresProject` now
pin this behavior. The e2e test also seeds the cache with a real `clast retro`
run against the LLM stub. It then breaks the stub and clears the API key. The
`--out` run still succeeds.

## Rendering is server-side with CSS `:target` views

`html/template` renders the nav, the overview, and every session and
breadcrumbs view. The page works without script. Views switch on the CSS
`:target` selector. The default view is the overview.

Each session has the anchor `#s-<session-id>`. Each day with breadcrumbs has
`#crumbs-<day>`. The follow-on Matter `analyze-transcript-view` hangs off
`#s-<session-id>`. Do not rename it without that Matter.

The only script highlights the active nav link.

The page model is in `internal/verbs/analyzeverb/page.go`. It holds raw facts.
Derived values, such as word counts and labels, are methods. An override
template can reach them.

The template is `assets/analyze/index.html`. The build embeds it. A user can
override it with a file under `$XDG_CONFIG_HOME/clast/`. `Render` executes into
a buffer first. A template error gives `analyze.page-unavailable` and never a
half-written page.

Gather converts times to local time before it builds the page. The template
formats them in the zone they carry. Breadcrumb days use `cutoff.DayOf` on the
original timestamp, then `.Local()` for display.

## Word counts keep the prototype's method

The count is `strings.Fields` over the raw markdown, markers included. This
matches the retired prototype. Verbosity baselines from before and after this
Matter stay comparable. On the prototype's dataset, the port's counts for all 16
sessions equal the prototype's counts. The baseline numbers themselves belong to the verbosity effort.

## Signals: the verb handles them itself

The root command context is not signal-aware. `ExecuteC` receives
`context.Background()`. So `runServe` wraps the context in
`signal.NotifyContext` for `SIGINT` and `SIGTERM`. A stop shuts the server
down with a 5-second grace period and exits 0. The server uses a
`ReadHeaderTimeout` of 10 seconds.

## Surface kind is Plumbing, with no flow doc

`analyze` is a standalone verb, like `init` and `doctor`. Its surface kind is
Plumbing. It is not a skill shape, so it has no flow doc.

The generated plumbing verb table lists `clast plumbing …` verbs only. So
`skilltable_test` lists `analyze` in `nonPlumbingVerbs`: its kind is Plumbing,
but it is a top-level verb.

The SURFACE V-entry lands in `clast-reboot`. That step is Beau's. This repo
does not hold it.

## The prototype is retired

The gitignored `analyze/` directory is deleted. It held the prototype viewer
template, its Python builder `build.py`, and the `analyze/tools/retrofp`
fingerprint helper. `.gitignore` no longer ignores `analyze/`.

Go replaces each part. The Go port of the markdown conversion is
`markdown.go`. It cuts out code spans before bold and link handling, so markup
inside backticks stays literal. The one deliberate drop is the prototype's
`.claude/projects/…jsonl` pointer. The journal holds its own transcript copy.

## A known failure comes from before this Matter

`TestSeal_StaleAloneFiltersCorrectly` in `internal/cli` fails. It fails on the
base commit `18e7546` too, where `--stale` returns `[]`. The fixture day is
2026-09-11. The failure probably comes from a date-relative default window.
This Matter left it alone.
