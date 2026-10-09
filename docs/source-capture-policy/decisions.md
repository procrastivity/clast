# `source-capture-policy` — decisions this Matter made

This file records the calls that building the shared capture policy forced.
The wip Matter `source-capture-policy` (Linear BDS-261) set the intent. The
owner decisions of 2026-10-08 (P1–P6) settled the contract in the clast-reboot
sidecar — MODEL.md §7 (M12, M13) and §9, SURFACE.md V13/V30/V31/V34/V35 — and
this file records what landing it in code decided.

Status: all five Steps are complete. The compatibility matrix for claude
holds end to end, the full suite and `make check` pass, and the consumer
contract is ready for the amp source's Matter (BDS-262).

## The sweep's walk set is a filtered copy, never the registry

A bare `clast plumbing capture` walks every implemented source that is not
excluded and is either local or opted in:

    implemented(name) && !excluded(name) && (local(name) || opted-in(name))

- Implemented means a row in `internal/source/registry.All`. The registry
  stays the sole authority for what a source is.
- Excluded means `name` is listed in `capture.exclude`.
- Local versus network rides the source's existing `Model()` axis:
  `source.Network` is the only model a sweep gates on. There is no separate
  transport flag. `Model()` is a required `Source` member, so no source can
  forget the declaration.
- Opted in means `capture.<name>.auto: true`. It defaults off at read time
  and is a no-op on local sources.

`selectSweep` builds the walk set as a new slice over `registry.All`. The
table is never mutated and no row carries a skip flag. That is the
reader-independence rule: `sessions`, `show`, and `analyze` resolve through
the same registry regardless of exclusion, and no path deletes local
history.

## `--harness` is the authorization, per invocation

`capture --harness <name>` captures one source regardless of exclusion or
the opt-in gate. Nothing is persisted; installation or login state is never
authorization. An unknown name still refuses with
`validation.unknown-harness`, and flag validation deliberately runs before
`config.Load` — a bad name reports even under malformed config.

## `source.Presence` is the absent-versus-unavailable seam

Discover's contract was already pinned: absent storage returns quiet
`(nil, nil, nil)`. To tell "absent" from "present but empty" on the
explicit path, the Matter added an optional interface beside `Source`, in
the `TranscriptRenderer`/`TranscriptReader` mold:

    type Presence interface {
        Present(ctx context.Context) error
    }

Nil means storage present; a non-nil error names the probed root and
reason verbatim. It is cheap and side-effect-free — no enumeration, no
prompt, no fetch. Capture's explicit path is its only caller; sweeps never
probe. A source without it degrades to the pre-contract quiet-exit-0. Every
registered source implements it anyway — `All` is a static table, so a
missing probe is a test-visible gap (`TestAllRowsImplementPresence`), not
a runtime leak.

Claude's probe reads the same `<configDir>/projects` root `Discover`
enumerates, so the two verdicts cannot disagree.

## A source failure is a disclosure, never a failed sweep

`Run` now returns `([]Captured, []Diagnostic, []SourceFailure, error)`. A
source whose `Discover` cannot enumerate at all is collected as a
`SourceFailure` and the sweep continues — one broken source never denies
another's valid capture (P1 overrode the recommended exit-1
`capture.incomplete`).

The outcome table, as implemented:

- Sweep over a failed source: one `capture: <name>: <err>` line on stderr,
  other sources still run, captured rows still emit, exit 0. `--json`
  gains an additive `"unavailable"` list, present only when non-empty.
- Explicit `--harness` on unavailable storage or a failed source:
  `capture.source-unavailable`, exit 1, naming the source and the probed
  root.
- Store writes, the journal walk, and cancellation abort the run as
  `internal.capture`, exit 4. Cancellation is checked on the context
  before each source and session, and on every returned error with
  `errors.Is(err, context.Canceled/DeadlineExceeded)` — it is never
  swallowed into a per-source failure or a per-item diagnostic.
- Per-item failures stay diagnostics on both request types and never
  affect the exit.

Claude's unreadable-root case moved to match: a `projects/` directory that
exists but cannot be enumerated is a real `Discover` error now, where it
used to be a downgraded `Diagnostic`. Absent `projects/` stays quiet.

## Cancellation is fatal on the probe path too

Step-05 review found one gap in the failure-isolation landing:
`checkPresence` wrapped every `Present` error as
`capture.source-unavailable`. A `Present` that returns context teardown —
the shape a network source's credential check takes, unlike claude's local
readdir — would have exited 1 "unavailable" instead of aborting. The probe
now checks `ctx.Err()` first and `isCancellation` on the probe's error;
both classify `internal.capture`, the same verdict `Run` gives inside the
sweep. `TestCheckPresence` pins it.

## Capture's config keys follow the read-time-default pattern

`config.Load` merges the user override over the shipped default at the top
level only, so a user `capture:` section replaces the shipped one
wholesale. Every capture key therefore reads its own default when absent:
`auto_dismiss_noop` stays true, `exclude` stays empty, `<name>.auto` stays
off, even when the user wrote only one of them. A mistyped known value
errors as `validation.config` naming the key; an unimplemented source name
in `exclude` or as a `capture.<name>` section errors naming the value and
the implemented set; unknown scalar keys stay silently ignored (P6).
