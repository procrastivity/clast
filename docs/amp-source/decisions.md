# `amp-source` — decisions this Matter made

This file records the calls that building the amp capture source forced.
The wip Matter `amp-source` (Linear BDS-262) set the intent; the sealed
`source-capture-policy` Matter (BDS-261) supplied the selection predicate,
explicit-request authorization, the absent-versus-failed distinction, and
the outcome/exit table. Everything transport-, enumeration-, and
storage-shaped below is what this Matter owned, including the limits it
chose to ship with.

Status: steps 01–10 are locally complete — the source, the scoped
checkpoint, the recapture veto, the convergence suite, and the sweep
integration with honest diagnostics all hold end to end. Step-11's
daily-use dogfood caught two convergence leaks that step-12 sealed:
list corroboration re-queued every below-floor row into pending each
sweep (the whole Amp-visible universe was being re-verified), and an
unchanged projectless commit re-ran a full `threads export` per sweep
just to re-answer "no project".

## The amp CLI is the whole transport

`internal/source/amp` never speaks HTTP itself. Discovery runs
`amp threads search` (windowed, `-n 100 --json`) plus one
`threads list --include-archived --limit 500` corroboration pass; capture
runs `threads export <id>` and stores the bytes verbatim. The CLI's own
auth is used ambiently — `AMP_API_KEY` or whatever stored credential the
harness already has — and clast injects nothing into the environment.

Every invocation carries a `callBudget`: 30s and 8 MiB for search and
list, 120s and 64 MiB for export, with stderr itself capped. Per-call
timeouts deliberately do not unwrap `context.DeadlineExceeded`, so an
item's own budget can never masquerade as the fatal run cancellation.
Stdin stays the null device and stderr text is classified
(`authy`/`classifyExit`): auth-flavored failures are real errors that
fail closed fast — never a prompt, never a login flow, never a retry.
`Present` is the cheap side-effect-free half of the same question (binary
resolvable + credential material exists); sweeps never call it.

## Sweep admission is one `Model()` declaration

`Source.Model()` returns `source.Network`. That single line is the entire
gate: the shared predicate `implemented && !excluded && (local ||
opted-in)` drops amp from every bare sweep until `capture.amp.auto: true`
opts in. `capture.exclude: [amp]` wins over the opt-in, and
`--harness amp` wins over both — for that invocation only. There is no
grant machinery: nothing persists authorization, and installing amp or
being logged in is never authorization (the contract's own words). An
opted-in sweep runs the transport unconditionally — it does not probe
`Present`, so absent credentials surface as the CLI's own auth verdict (a
disclosed source failure, exit 0), while the explicit path answers the
same absence earlier and more precisely through the probe
(`capture.source-unavailable`, exit 1, naming what was probed).

## Completeness is proven by subdivision, not counts

`threads search` silently truncates at 100 rows with no cursor or
truncation marker, so a full page is only *maybe* complete. The
enumeration covers `[day(floor)-1d, tomorrow)` in complementary 7-day
windows; a capped window splits into its days, a capped day splits on
`archived:` then `pinned:` — two facets that fully partition the
universe. A leaf still at cap after both is the named residual: its rows
are absorbed (they are real threads), the window is disclosed and marked
dirty so the floor never crosses it, and `threads list` corroboration
queues ids it saw but enumeration missed into the pending lane. Selection
is `updatedAt`-driven end to end — `list` is corroboration only, never
the enumerator — with a millisecond-precision post-filter the day-grained
DSL cannot express.

Corroboration is *span-aware* (step-12): `list` sees the account's whole
history while the windows legitimately cover only the tail above the
floor, so an unemitted listed id is a real gap only when evidence puts it
inside the scanned span. The gate is cheapest-evidence-first — the row's
own `updated` stamp (a lower bound on `updatedAt`), then the journal's
committed revision, with never-committed ids always queued — and only
in-span misses enter pending. A floorless scan queues every miss as
before, and a run with no journal evidence conservatively queues too;
the residue the gate accepts is a miss whose every stamp is stale, the
price of not re-verifying the entire below-floor universe each sweep.

## Scan progress is a scoped cache, and pending is emitted

`$XDG_CACHE_HOME/clast/amp/scan-state-<scope>.json` is a cache, never
truth: absent, corrupt, or scope-mismatched means a full rescan from the
2025-01-01 history floor. The scope descriptor is the smallest non-secret
evidence separating progress — `accounts.json`'s active user, else an
`AMP_API_KEY` digest, else a stat fingerprint of credential files, plus
`device-id.json`'s installationID and the run's journal root. A scope
with no account evidence never keeps a floor (it could hide another
account's history): every run rescans, pending still records.

The floor only advances over coverage whose discoveries are committed or
recoverably queued: every emitted id is pending until a witness settles
it — the journal read-back inside `Unchanged`, a clean empty `id:` page,
or a terminal verdict (does-not-exist, invalid id, or the over-cap
quarantine). Capture success deliberately does not settle (the artifact
is staged; session.json may never commit — that crash window is exactly
what the pending lane heals). A cross-process `O_EXCL` lockfile with
bounded wait and bounded steals serializes concurrent sweeps'
read-modify-write; a lock that cannot be taken degrades to an unlocked
write rather than hang a sweep on bookkeeping.

Two settle caches ride the same checkpoint file (step-12) — both pure
cache, both lost-cost-free: `correlate` records each thread's
correlation verdict keyed on the enumerated revision and the answering
host, where `""` (projectless by evidence) is a verdict — an unchanged
projectless commit never pays a second `threads export`; `quarantined`
records ids whose export exceeded the byte cap and the cap it failed
under. Exports only grow, so a recorded cap at a same-or-smaller budget
can never succeed — the id settles instead of retrying every sweep, and
only a raised budget reopens the fetch. A byte-cap failure is itself a
terminal verdict inside a run: a same-budget retry is provably wasted.

## The committed record is never worsened

The artifact is the verbatim export; the fingerprint is a sha256 over a
canonical one-line-per-message projection, so `v`/`updatedAt`/meta churn
can never report a conversation change that did not happen. On
recapture, `Supersedes` vets the fresh doc against the committed session:
only a strictly-ahead doc with at least as many messages, or a
same-revision repair of a flagged-incomplete commit, may replace it.
Append-only export evidence makes a shorter or not-ahead read proof of a
partial or stale read — the staged bytes are discarded, the refusal is
disclosed, and the id stays owed for the next sweep.

## Diagnostics keep their distinct mappings

Verified through the real command surface:

- Absent binary: sweep stays quiet and writes no checkpoint; explicit
  `--harness amp` is `capture.source-unavailable` naming the `LookPath`
  probe.
- Absent credentials: explicit is `capture.source-unavailable` naming
  the credential probe; an opted-in sweep runs the transport and takes
  the CLI's verdict.
- Auth rejection mid-sweep: one `capture: amp: …` stderr line plus a
  `--json` `"unavailable"` row, exit 0; explicit, the same failure is
  `capture.source-unavailable`.
- Malformed export: a per-item stderr diagnostic naming the thread —
  the run exits 0, nothing commits, the id stays pending, and a healthy
  sibling still captures.
- Journal/write failure: `internal.capture`, exit 4 — fatal, never a
  per-source failure.
- Unknown `--harness` name: `validation.unknown-harness` naming the
  implemented set, decided before config is even read.

## Sweep latency is bounded per call and per workload

For the hook path the bound is trivially zero: the SessionStart shim
backgrounds `clast plumbing capture` entirely, so session start never
waits on it. For the sweep itself — the number that matters to cron —
the worst case is the call structure times the per-call ceiling:

- Enumeration: at most one search call per 7-day window between the
  floor and tomorrow (~96 for a first-ever scan, ~1–2 for a daily
  catch-up), plus bounded subdivision (≤ 1 + 2 + 4 calls per capped
  day), plus one `threads list`, plus one `id:` lookup per pending id —
  each call ≤ 30s.
- Per emitted item: ≤ 3 export attempts (≤ 120s each) plus ~10s of
  retry waits; the pending set itself is capped at 8192 before it resets
  to a full rescan.
- The checkpoint lock costs ≤ 2s of waiting plus ≤ 3 steals.

So a catch-up sweep's network time is roughly `30s × (windows + pending)
+ 370s × (new or moved items)` — workload-proportional, never
unbounded, and a transport that hangs to its per-call ceiling degrades
linearly rather than stalling the run. `TestRealRunnerTimeout` pins the
per-call bound on the real exec path.

## Limits this Matter chose to ship with

- **>100 updates in one day** is unenumerable below the DSL's day
  granularity. The residual is honest forever: disclosed each sweep,
  corroboration-queued, and re-probed on the `id:` lane until it
  enumerates — never lost, never duplicated, at a bounded per-sweep
  re-verification cost (`TestCommandAmpCappedPageConvergesThroughPending`
  drives 107 equal-stamp threads through it).
- **>500 threads** exceeds the `threads list` page corroboration can
  read: the pass is skipped with a diagnostic and the miss-queue safety
  net is gone — windowed search alone carries enumeration (it already
  does the real work; list was only ever a cross-check).
- **Equal-millisecond updatedAt stamps** dedupe to the freshest row and
  emit in `(ModTime, NativeID)` order; a stamp tied exactly at the
  checkpoint floor drops from the windows but stays reachable through
  the pending lane (`TestDiscoverUpdatedAtTieAtFloor`).
- **M5's one-writer premise** does not hold verbatim for a
  network-resident source — the same `(amp, thread-id)` is fetchable
  from every machine. Resolution adopted: conflict-copy-acceptable, per
  M5's own posture. The artifact is the verbatim export — byte-identical
  for a given revision on any machine — so concurrent captures cannot
  diverge on content; session.json differences are provenance (machine,
  captured_at) and machine-local correlation, where each copy is a valid
  record. Journals are machine-local and no clast path merges roots;
  scan progress is keyed per journal and install, so no machine trusts
  another's floor. No deterministic writer was needed.
- **Account scope** is the ambient credential's whole visible universe —
  no narrower owned-thread selector exists, so a shared or service
  account sweeps everything it can see.
- **Explicit scope stays per-invocation** — `--harness amp` authorizes
  one run and nothing more; re-running it is the only renewal.
