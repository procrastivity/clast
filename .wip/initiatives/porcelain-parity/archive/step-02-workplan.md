# Workplan — step-02 · brief: `--help` and an arg loop

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Started: 2026-07-11.

Delivers BDS-86: `_clast_brief_usage` + an arg loop in `clast_cmd_brief`
(`lib/clast/clast-porcelain-subcommands/brief.bash`) so `clast brief --help`
prints usage and exits 0, and an unknown flag exits 2 instead of being
silently dropped. Prerequisite for the BDS-89 parity guard (step-07), which
diffs `clast <cmd> --help` output against a manifest.

## Decisions (made here, feed later steps)

_Locked choices that future steps can rely on. Each entry is a sentence the
next step can re-read without ambiguity._

- **Shape source of truth is `_clast_retrosum_usage` / `clast_cmd_retro` in
  `lib/clast/clast-porcelain-subcommands/retro.bash`** (already merged on
  this branch): heredoc usage function, `case "$1" in ... esac` loop,
  `-h|--help) <usage>; return 0 ;;`, `--) shift; break ;;`, and an unknown-arg
  branch that logs via `clast_porcelain_log_error` and `return 2`. Correction
  to the brief given to this researcher: the retro usage function is named
  `_clast_retrosum_usage`, not `_clast_retro_usage` — that name does not exist
  in this repo. This step still names brief's function `_clast_brief_usage`
  per the tracker/roadmap wording; the retro name mismatch is noted only so
  nobody goes looking for a function that isn't there.
- **`feat/wake-auto` (`git show feat/wake-auto:lib/clast/clast-porcelain-subcommands/wake.bash`,
  PR #45, unmerged)** confirms the same idiom for a second subcommand
  (`_clast_wake_usage`, identical case shape, `--auto` as its one extra
  flag). Used only as a secondary shape reference, per the brief's
  instruction — nothing else from that branch is in scope.
- **Positional slug vs. flags — the one place brief's loop differs from
  retro's.** `retro` and `wake` take no positional argument, so their loops
  can treat *any* unmatched `$1` as an error. `brief` takes an optional
  bare project slug (`clast brief [<project-slug>]`), which must still work
  after this change. The loop therefore distinguishes "looks like a flag"
  from "is the positional slug": only an argument starting with `-` that
  doesn't match a known case hits the error branch; a bare word (no leading
  `-`) breaks out of the loop and is left as `$1` for
  `_clast_brief_resolve_project`, unchanged from today. Concretely:
  ```bash
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) _clast_brief_usage; return 0 ;;
      --)        shift; break ;;
      -*)        clast_porcelain_log_error "brief: unknown argument '$1'"; return 2 ;;
      *)         break ;;
    esac
  done
  ```
  This mirrors the top-level dispatcher in `bin/clast` (lines ~17-34), which
  already makes exactly this `-*` vs bare-word split for its own arg loop —
  same idiom, one level up.
- **Arg loop runs before `clast_porcelain_preflight_llm`.** Retro's loop
  also precedes its `clast_porcelain_preflight_llm` call. Same ordering for
  brief means `--help` and an unknown flag both short-circuit before any
  LLM env-var check, so `clast brief --help` works even with no
  `CLAST_LLM_*` vars set — required for the DoD's `clast brief --help`
  check to be crisp, and for the step-07 guard to shell out to `--help`
  without needing LLM credentials.
- **Env vars documented in the usage heredoc: only `CLAST_LLM_BASE_URL`,
  `CLAST_LLM_API_KEY`, `CLAST_LLM_MODEL`.** Read `_clast_brief_resolve_project`,
  `_clast_brief_gather_entries`, `_clast_brief_gather_breadcrumbs`,
  `_clast_brief_gather_sessions`, `_clast_brief_build_user_prompt`, and
  `clast_cmd_brief` itself end to end: none of them read a `CLAST_BRIEF_*` or
  other brief-specific env var (unlike `retro`, which reads
  `CLAST_JOURNAL_DIR` via `_clast_retrosum_journal_dir` — and notably retro's
  own usage heredoc does *not* document that var either, it only documents
  the `CLAST_LLM_*` line). The only env surface brief touches is what
  `clast_porcelain_preflight_llm` (`lib/clast/clast-porcelain-lib.bash`)
  requires: `CLAST_LLM_BASE_URL`, `CLAST_LLM_API_KEY`, `CLAST_LLM_MODEL`. So
  brief's usage heredoc gets the identical closing line retro uses:
  `Requires the CLAST_LLM_* env vars (see \`clast --help\`).` — no
  brief-specific env section needed.
- **Top-of-file `# Usage:` comment** (`brief.bash` lines 1-9) gets updated
  from `# Usage: clast brief [<project-slug>]` to also mention `--help`,
  matching the level of detail already in that header comment block — small
  in-scope touch, still inside `brief.bash`.

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough to
land in one focused commit._

1. **Add `_clast_brief_usage` + wire the arg loop into `clast_cmd_brief`.**
   - New `_clast_brief_usage()` heredoc function (placed next to
     `clast_cmd_brief`, mirroring where `_clast_retrosum_usage` sits relative
     to `clast_cmd_retro` in `retro.bash`), covering:
     `Usage: clast brief [<project-slug>]`, a one-line description (already
     exists in the file header comment — reuse that wording), the implicit
     "no slug ⇒ resolve from cwd" behavior `_clast_brief_resolve_project`
     already implements, `-h, --help` in a Flags section, and the closing
     `Requires the CLAST_LLM_* env vars (see \`clast --help\`).` line.
   - Arg loop at the top of `clast_cmd_brief`, before the
     `clast_porcelain_preflight_llm` call, per the Decisions section above.
   - Update the top-of-file `# Usage:` comment.
   - Conventional commit: `feat(brief): add --help and an arg loop`.
2. **Test coverage in `test/test-brief.sh`.**
   - `test/test-brief.sh` today only sources `brief.bash` directly (no
     `clast-porcelain-lib.bash`), because its existing coverage
     (`_clast_brief_gather_entries`) never calls a `clast_porcelain_*`
     function. Testing `clast_cmd_brief`'s arg loop needs
     `clast_porcelain_log_error` (unknown-arg path) and, for the
     post-`--`-positional-preserved test, the full `clast_porcelain_*`
     surface `clast_cmd_brief` calls on its happy path. Add
     `source lib/clast/clast-porcelain-lib.bash` before the existing
     `source lib/clast/clast-porcelain-subcommands/brief.bash` line, mirroring
     the include order `test/test-retro-summary.sh` already uses.
   - New `# === arg validation ===` section (place after the existing
     gather-entries cases, before `clast_test_summary`), mirroring
     `test/test-retro-summary.sh`'s own `# === arg validation ===` block
     (lines ~204-211) line for line:
     - `assert_exit_code 2 clast_cmd_brief --bogus`
     - `out="$(clast_cmd_brief --help 2>/dev/null)" && rc=$? || rc=$?` /
       `assert_eq "0" "$rc" "help: exits 0"` / a `case "$out" in
       *"Usage: clast brief"*)` check for usage text, exactly as retro's
       block does for `Usage: clast retro`.
     - A positional-preserved check: `clast brief --` still resolves the
       slug that follows. Needs `CLAST_LLM_BASE_URL`/`CLAST_LLM_API_KEY`/
       `CLAST_LLM_MODEL` set to dummy values first (so
       `clast_porcelain_preflight_llm` doesn't `exit 1` on missing env —
       note this is a real `exit`, not `return`, per
       `clast-porcelain-lib.bash`, so an unset var would kill the whole test
       script, not just fail one assertion) — set them once near the top of
       the new section the way `test-retro-summary.sh` does at file scope.
       With no journal fixtures seeded, `clast brief -- xesapps` hits the
       "No curated entries, breadcrumbs, or sessions" early-return path
       (`clast_cmd_brief` returns 0 without an LLM call), so assert on the
       stdout containing `Briefing for project: xesapps` — proves the
       positional after `--` reached `_clast_brief_resolve_project`
       correctly, without needing to stub `clast_porcelain_llm_chat`.
   - Conventional commit: `test(brief): cover --help, --, and unknown-arg exit codes`.

No third chunk identified — the two above fully cover the tracker's scope
(usage heredoc + arg loop + tests). If review surfaces a gap, prefer folding
a fix into Chunk 1 or 2 rather than opening a speculative Chunk 3.

## Test strategy

- Function-level, matching the existing `test-brief.sh` / `test-retro-summary.sh`
  approach for this codebase: source the subcommand file(s) directly and call
  `clast_cmd_brief` in-process, no subprocess spawn of `bin/clast`. Consistent
  with how `test-retro-summary.sh` covers retro's identical arg-loop shape
  (`assert_exit_code 2 clast_cmd_retro --bogus`, `clast_cmd_retro --help`).
- `--bogus` and `--help` need no LLM stub and no journal fixture — the arg
  loop returns before `clast_porcelain_preflight_llm` runs (see Decisions).
- The `--` positional-preserved test is the one case that reaches
  `clast_porcelain_preflight_llm`, so it needs dummy `CLAST_LLM_*` env vars
  (no real LLM call happens on this path — it hits the early "no entries"
  return before `clast_porcelain_llm_chat` is ever invoked).
- Not covered, deliberately deferred: an actual `clast brief <slug>` full
  happy-path LLM synthesis test with a stubbed `clast_porcelain_llm_chat`
  (the way `test-retro-summary.sh` stubs it for retro). That's pre-existing
  gap in `test-brief.sh`, not something this step's tracker (BDS-86, `--help`
  + arg loop only) asks for — out of scope here, worth a follow-up if BDS-89
  or another step wants deeper `clast_cmd_brief` coverage.

## Definition of done

_Concrete checks that prove the step is finished. Lean toward observable
behaviour over file-level checklists._

- `clast brief --help` prints a usage heredoc (containing `Usage: clast
  brief`) to stdout and exits 0.
- `clast brief --bogus` (or any unrecognized `-`-prefixed flag) prints an
  error to stderr and exits 2.
- `clast brief` and `clast brief <slug>` behavior is unchanged (positional
  slug still resolves; no-slug still falls back to
  `clast-plumbing registry resolve`).
- `clast brief -- <slug>` works (the `--` end-of-flags marker is honored).
- `make test` is fully green — zero failures, no tolerated known-failures.
- `make lint` is fully green — zero failures (shellcheck etc. clean on the
  modified `brief.bash` and `test-brief.sh`).
- Only `lib/clast/clast-porcelain-subcommands/brief.bash` and
  `test/test-brief.sh` touched. `skills/brief/SKILL.md` (step-01, already
  shipped) and anything under `docs/` are untouched.
- Both commits (Chunk 1, Chunk 2) land directly on
  `beau/bds-82-porcelain-parity`, conventional-commit style, no new branch,
  no new PR (PR #46 covers the whole initiative).

## Open questions to resolve during execution

_Questions whose answers don't block starting but DO block finishing. Each
should have a "lean" so the worker isn't paralyzed._

- **Does an unrecognized *positional* (a second bare word after the slug,
  e.g. `clast brief myslug extra`) need to start erroring too, now that
  we're adding validation?** Lean: **no** — out of scope. Today's code
  silently ignores everything past `$1`; this step's tracker (BDS-86) is
  specifically about flags (`-h`/`--help`/`--`/unknown flag), not about
  tightening positional-arity validation, and retro/wake have no positional
  arg to set a precedent either way. Adding it would be scope creep beyond
  what the roadmap entry asks for. If a Builder disagrees mid-execution,
  escalate rather than guessing — this is a one-line judgment call, not a
  blocker.
- **Should the usage heredoc's synopsis line also show the implicit "no
  slug ⇒ resolve from cwd" behavior inline** (e.g.
  `Usage: clast brief [<project-slug>]  (default: resolve from cwd)`) **or
  leave it to prose below the synopsis, the way retro's heredoc separates
  the synopsis from an explanatory paragraph?** Lean: **prose below**,
  matching retro's structure exactly (synopsis line, blank line, one-line
  description paragraph, blank line, `Flags:` section) — keeps the two
  heredocs visually parallel for whoever reads them side by side in the
  BDS-89 guard's manifest work later.
