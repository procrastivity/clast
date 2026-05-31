---
step: 02
title: core-libs-and-decoder
depends_on: [01]
size: medium
references:
  - docs/overview.md#glossary
  - docs/overview.md#conventions
  - docs/cli-contract.md#date-parsing
  - docs/repo-bootstrap.md#libclastclast-libbash
  - docs/repo-bootstrap.md#libclastclast-decode-libbash
  - docs/repo-bootstrap.md#test-strategy
---

# Step 02: Core libs and segment decoder

## Context

Step 01 scaffolded the repo and provided a working dev shell via `flake.nix` (`devShells.default`). Directory structure exists; tooling configs exist; the dev shell provides `bash`, `jq`, `shellcheck`, `git`, `coreutils`, `pre-commit`. No code yet. **Run `direnv allow` (or `nix develop`) before starting this step** so `jq` and `shellcheck` are on PATH; if Nix isn't available, run `make deps-check` to verify the same tools are present some other way.

This step writes the first two libraries — `clast-lib.bash` (common helpers) and `clast-decode-lib.bash` (Claude Code's segment encoder/decoder) — plus the test harness (`test/helpers.sh`) and the first batch of test fixtures.

These two libs are the foundation everything else depends on. The decoder in particular is the trickiest piece in `clast` because of dash-substitution ambiguity (see `docs/overview.md#glossary` for what "segment" means). Getting this right with thorough test coverage now saves debugging later.

No subcommands exist yet after this step. The `bin/clast` dispatcher is also not built yet (step 03). After step 02, you can source `lib/clast/clast-lib.bash` and `lib/clast/clast-decode-lib.bash` from a test script and exercise their functions, but there's no end-user-runnable `clast` binary.

## Goal

Implement the common-helpers library, the segment encoder/decoder library, and the test harness, with comprehensive test coverage for the decoder's ambiguity resolution.

## References

Read before starting:

- `docs/overview.md#glossary` — definitions of "segment", "slug", "day bucket".
- `docs/overview.md#conventions` — JSON keys snake_case, dates in ISO 8601 UTC with `Z`, exit code table.
- `docs/cli-contract.md#date-parsing` — what `clast_parse_date` must accept: ISO date, `today`/`yesterday`/`last-week`, relative offsets `-1d` / `-3d` / `-1w`. Also the day-cutoff behavior.
- `docs/repo-bootstrap.md#libclastclast-libbash` — list of functions to expose in `clast-lib.bash`.
- `docs/repo-bootstrap.md#libclastclast-decode-libbash` — encoder/decoder function list and the ambiguity-resolution algorithm.
- `docs/repo-bootstrap.md#test-strategy` — how tests are organized; which fixtures exist.

## Tasks

1. **Write `lib/clast/clast-lib.bash`** exposing these functions per `docs/repo-bootstrap.md`:
   - `clast_journal_dir` — returns `${CLAST_JOURNAL_DIR:-$HOME/.claude/journal}`.
   - `clast_projects_dir` — returns `${CLAST_PROJECTS_DIR:-$HOME/.claude/projects}`.
   - `clast_today` — local date (`YYYY-MM-DD`), respecting `day_cutoff` (default `04:00`). Reads `CLAST_DAY_CUTOFF` env or config file (config is optional in v1 — env-only is fine).
   - `clast_parse_date <input>` — accepts ISO date, `today`, `yesterday`, `last-week`, `-1d`, `-3d`, `-1w`. Returns `YYYY-MM-DD` on stdout.
   - `clast_log_info`, `clast_log_warn`, `clast_log_error` — all write to stderr with a prefix tag like `clast: info: …`. `clast_log_info` is silent when `--quiet` (global flag handled later in step 03).
   - `clast_json_get <jq-expression> <input>` — thin wrapper around `jq`. Just enough to keep call sites short.
   - `clast_atomic_write <path> <content>` — writes to `<path>.tmp.$$` then renames. Fails if the temp file write fails; preserves the original on rename failure.
   - `clast_version` — returns the version string from `package.json` (read once and cached).
   - `clast_usage` — placeholder; will be filled in step 03 when the dispatcher exists.

2. **Write `lib/clast/clast-decode-lib.bash`** exposing:
   - `clast_encode_path <absolute-path>` — replaces `/` with `-`, returns segment.
   - `clast_decode_segment <segment>` — primary decoder. Algorithm:
     1. Naive decode: replace each `-` with `/`. Try `test -d` on the result. If it exists, return it.
     2. If naive decode doesn't exist on disk, generate all candidate splits (paths with literal dashes encode ambiguously). Try `test -d` on each. If exactly one exists, return it.
     3. If multiple candidates exist on disk, look at the corresponding `~/.claude/projects/<segment>/sessions-index.json` if present and use its `projectPath` field. If absent, run `git -C <candidate> rev-parse --show-toplevel` on each — return the one that succeeds (a real git repo is a strong signal).
     4. If still ambiguous or none exist, return the naive decode and exit 1 (caller may surface to user).
   - `clast_decode_candidates <segment>` — returns all possible decodings as a newline-separated list, without filesystem checks. Used by `clast doctor` for diagnostic output.
   - **Windows/WSL2 case**: segments starting with `<letter>--` (e.g., `C--Users-...`) decode the leading `<letter>:` then continue with normal logic.

3. **Write `test/helpers.sh`** — minimal test framework:
   - `assert_eq <expected> <actual> [<message>]` — exit non-zero on mismatch, print diff.
   - `assert_file_exists <path>` — exit non-zero if missing.
   - `assert_file_not_exists <path>` — exit non-zero if present.
   - `assert_exit_code <expected-code> <command> [<args>...]` — runs command, compares exit code.
   - `setup_test_journal` — creates `mktemp -d` for the test, sets `CLAST_JOURNAL_DIR` and `CLAST_PROJECTS_DIR` to it. Returns the dir path.
   - `teardown_test_journal` — removes the temp dir.
   - `make_fixture_projects_tree <fixture-name>` — copies `test/fixtures/<fixture-name>/` into `$CLAST_PROJECTS_DIR`.

4. **Create test fixtures** under `test/fixtures/`:
   - **`empty/`** — just a `.gitkeep` inside, representing a `~/.claude/projects/` with no projects.
   - **`simple/`** — two directories representing two segments:
     - `-tmp-clast-test-fixtures-simple-proj-a/<uuid>.jsonl` — fake JSONL file with 2 lines of plausible session content (first line has `cwd` and timestamp; subsequent lines have basic message structure).
     - `-tmp-clast-test-fixtures-simple-proj-b/<uuid>.jsonl` — similar, different content.
     - The first line of each JSONL should be valid JSON containing at minimum `{"cwd": "<decoded-path>", "timestamp": "<ISO>"}` so the decoder can cross-reference if needed.
   - **`ambiguous-decode/`** — one segment whose naive decode doesn't exist, but two candidates do exist (the test will set up the candidates as part of the test harness). Specifically: a segment `-tmp-clast-foo-bar-baz` that could decode to `/tmp/clast/foo/bar/baz` or `/tmp/clast/foo-bar/baz` or `/tmp/clast-foo/bar/baz`. Test will create the directories matching the *intended* decoding and verify the decoder picks it.

5. **Write `test/test-decode.sh`** covering:
   - Encode/decode round-trip for paths without literal dashes (any depth).
   - Naive decode resolves correctly when the path exists.
   - Ambiguous decode: two candidates exist on disk → decoder picks the right one via filesystem check + git-repo signal.
   - Windows/WSL2 case: `C--Users-Beast-foo` decodes to `C:/Users/Beast/foo` (without disk check — pure syntactic transform).
   - Empty segment: returns empty, exit 0.
   - Segment with no dashes (single component): decodes to `/<segment>`.
   - Segment for nonexistent path: returns naive decode, exit 1.

6. **Write `test/test-lib.sh`** covering `clast-lib.bash`:
   - `clast_journal_dir` returns env override when set; returns `~/.claude/journal` otherwise.
   - `clast_today` returns ISO date.
   - `clast_today` with `CLAST_DAY_CUTOFF=04:00` and current time = `01:00 local` returns yesterday's date.
   - `clast_parse_date today` and `clast_parse_date yesterday` return correct dates.
   - `clast_parse_date -1d` equals `clast_parse_date yesterday`.
   - `clast_parse_date 2026-01-15` returns `2026-01-15`.
   - `clast_parse_date invalid` exits non-zero.
   - `clast_atomic_write` creates the file with the given content; partial-write failure (simulate by giving a non-writable destination) leaves the original untouched.

7. **Wire tests into `Makefile`**: `make test` should run `test/test-decode.sh` and `test/test-lib.sh`. The `test/test-clast.sh` aggregator stub from step 01 should now invoke both.

8. **Confirm `make lint` passes**: shellcheck on the new files. Use `# shellcheck source=lib/clast/clast-lib.bash` directives where libs are sourced from libs/tests.

## Acceptance criteria

- `lib/clast/clast-lib.bash` and `lib/clast/clast-decode-lib.bash` exist, are bash-sourceable, and define all functions listed under tasks 1 and 2.
- `test/helpers.sh` defines the assertion functions listed under task 3.
- `test/fixtures/empty/`, `test/fixtures/simple/`, and `test/fixtures/ambiguous-decode/` exist with the structure described under task 4.
- `test/test-decode.sh` runs and passes all cases under task 5.
- `test/test-lib.sh` runs and passes all cases under task 6.
- `make test` runs both test files and exits 0.
- `make lint` runs shellcheck on the new files and exits 0.
- Calling the decoder against an unambiguous segment from the `simple/` fixture returns the correct path; against `ambiguous-decode/` it returns the unique disk-resident candidate.
- `clast_today` produces correct day-bucket output when invoked at 01:00 local with default cutoff (should return yesterday). This is testable by setting `TZ=` and using `faketime` or by directly invoking the underlying function with an injected "current time" hook — pick whichever is simpler in pure bash.

## Out of scope

- **Do not implement `bin/clast` yet** — step 03 builds the dispatcher.
- **Do not implement the manifest lib** — step 04.
- **Do not implement the registry lib** — step 05.
- **Do not implement any subcommand** — steps 03+.
- **Do not implement config-file reading** for `day_cutoff`. v1 reads only the env var `CLAST_DAY_CUTOFF`. A TOML config file is future work.
- **Do not optimize the decoder** for trees with thousands of projects. Linear-time per-call is fine for v1. If performance becomes a concern, that's a separate later step.
- **Do not write the `worktree/` or `corrupt-manifest/` fixtures yet** — those land in steps that actually need them (step 05 for worktree, step 04 for manifest corruption).
- **Do not handle the case where `sessions-index.json` is corrupted** during decoder ambiguity resolution. If it's unreadable, just skip it and fall through to the git-repo check. The `doctor` subcommand (step 10) handles index corruption diagnostics.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke: source the libs and exercise them in a subshell
bash -c '
  source lib/clast/clast-lib.bash
  source lib/clast/clast-decode-lib.bash

  # Encode/decode round-trip
  encoded=$(clast_encode_path "/home/beau/code/xesapps")
  echo "encoded: $encoded"

  decoded=$(clast_decode_segment "$encoded")
  echo "decoded: $decoded"

  # Day handling
  echo "today: $(clast_today)"
  echo "yesterday: $(clast_parse_date yesterday)"
  echo "-3d: $(clast_parse_date -3d)"
'

# Ambiguity test (set up candidates, run decoder)
mkdir -p /tmp/clast/foo/bar/baz /tmp/clast-foo/bar/baz
bash -c '
  source lib/clast/clast-lib.bash
  source lib/clast/clast-decode-lib.bash
  clast_decode_segment "-tmp-clast-foo-bar-baz"
'
rm -rf /tmp/clast /tmp/clast-foo
```

## Notes for the implementer

- **The decoder is the load-bearing piece.** Spend time on the test cases. Every later subcommand that touches a segment depends on this lib being correct.
- **Day-cutoff math**: `clast_today` at 01:00 with cutoff 04:00 should return yesterday's date. Implementation: subtract `cutoff` hours from current time, then take the date. Use `date -d` (GNU) — note that this differs on BSD/macOS; Beau's environment is Linux/WSL2, so GNU is fine, but make the dependency explicit in a comment.
- **`jq` is a required dependency** — declare it in `clast_lib.bash` with an early check: if `jq` isn't on PATH, exit 3 with a clear error message. Don't try to fall back to grep/sed for JSON parsing — the failure mode is much worse than a clean dependency error.
- **`test/helpers.sh` keeps it minimal**: do not pull in `bats` for v1. Plain bash with focused assertion functions is enough, and matches the patterns in `xcind` and `direnv-session-loader`. If test output becomes painful later, adding `bats` is a non-breaking change.
- **Ambiguous-decode fixture**: the test must `mkdir` the candidate dirs as part of setup and `rm -rf` them in teardown. Don't commit the actual ambiguous paths under `test/fixtures/` (you can't commit a directory tree that requires `/tmp/clast/...` to exist at test time anyway). Commit only the *fixture metadata* — a small README under `test/fixtures/ambiguous-decode/` describing the test setup.
- **Sourcing convention**: tests source libs via relative path from the repo root: `source lib/clast/clast-lib.bash`. The dispatcher (step 03) uses `$CLAST_LIB` to allow installed-elsewhere behavior, but tests assume repo-root cwd. Add `cd "$(dirname "$0")/.."` at the top of test scripts to enforce this.
- **Shellcheck cleanliness**: expect to add `# shellcheck source=...` directives whenever one bash file sources another. Don't disable warnings to make shellcheck pass — fix the underlying issue (usually quoting).
- **Conventional commit suggestion**: `feat(lib): implement core helpers and segment decoder with tests`. Squash to one commit if you wrote in increments; the next steps inspect git log via `depends_on`, and one logical commit per step keeps the history readable.
