---
step: 03
title: dispatcher-and-whereami
depends_on: [01, 02]
size: small
references:
  - docs/overview.md#conventions
  - docs/cli-contract.md#global-flags
  - docs/cli-contract.md#clast-whereami
  - docs/repo-bootstrap.md#binclast
  - docs/repo-bootstrap.md#libclastsubcommandsnamebash
---

# Step 03: `bin/clast` dispatcher and `clast whereami`

## Context

Step 01 scaffolded the repo (dev shell, Makefile, package.json, empty `bin/`). Step 02 wrote the two foundational libraries (`clast-lib.bash`, `clast-decode-lib.bash`), the test harness (`test/helpers.sh`), and the `test/test-clast.sh` aggregator. Tests and lint are green at HEAD.

There is still no end-user-runnable `clast` binary. Subcommand files do not exist yet — `lib/clast/clast-subcommands/` is empty.

This step builds the dispatcher and the simplest subcommand (`whereami`) end-to-end. After this step, `bin/clast --version`, `bin/clast --help`, and `bin/clast whereami` all work. Whereami is the right first subcommand because it touches every wiring path (global-flag parsing, library sourcing, subcommand dispatch, JSON output) without depending on the manifest (step 04) or registry (step 05).

**Run `direnv allow` (or `nix develop`) before starting** so `jq` and `shellcheck` from the dev shell are on PATH.

## Goal

Implement the `bin/clast` dispatcher with global-flag handling, wire up subcommand dispatch, and ship `clast whereami` as the first subcommand — with `--json` and `--quiet` support, an integration test, and clean lint.

## References

Read before starting:

- `docs/overview.md#conventions` — exit code table (2 = usage error, 3 = missing dep/env), JSON keys are snake_case, dates are ISO 8601 UTC `Z`.
- `docs/cli-contract.md#global-flags` — `--help`, `--version`, `--json`, `--verbose`, `--quiet`, `--journal-dir`, `--projects-dir`. These are parsed by the dispatcher and applied before the subcommand runs.
- `docs/cli-contract.md#clast-whereami` — the exact output shape for the default human-readable format. JSON output is the same fields, snake_case, one object.
- `docs/repo-bootstrap.md#binclast` — the dispatcher skeleton (`set -euo pipefail`, `CLAST_LIB` resolution, sourcing pattern, subcommand `case` block).
- `docs/repo-bootstrap.md#libclastsubcommandsnamebash` — convention that each subcommand file defines a single function `clast_cmd_<name>` invoked by the dispatcher.

## Tasks

1. **Write `bin/clast`** per `docs/repo-bootstrap.md#binclast`. Specifically:
   - Shebang `#!/usr/bin/env bash`, `set -euo pipefail`.
   - Resolve `CLAST_LIB="${CLAST_LIB:-$(dirname "$(realpath "$0")")/../lib/clast}"`.
   - Source `clast-lib.bash` and `clast-decode-lib.bash` from `$CLAST_LIB`.
   - **Parse global flags first.** Walk argv before the subcommand name, peeling off any of: `--help`/`-h`, `--version`, `--json`, `--verbose`/`-v`, `--quiet`/`-q`, `--journal-dir PATH`, `--projects-dir PATH`. Set the corresponding env (`CLAST_QUIET=1`, `CLAST_VERBOSE=1`, `CLAST_JSON=1`) and export `CLAST_JOURNAL_DIR` / `CLAST_PROJECTS_DIR` overrides so libs pick them up.
   - Stop global-flag parsing at the first non-flag arg (the subcommand) or at `--`. Pass the remaining argv to the subcommand.
   - Subcommand dispatch: today only `whereami` is wired. The `case` block should also list the planned subcommands (`snapshot|projects|sessions|show|entries|breadcrumb|registry|stats|doctor`) and emit a helpful "not yet implemented (planned for step NN)" message with exit code 2 — so `bin/clast snapshot` doesn't look like a generic "unknown subcommand". Keep this stub list maintainable: future steps will replace each stub with a real `source` + dispatch.
   - `--help` / `-h` / `help` / no-arg → `clast_usage; exit 0`.
   - `--version` → `echo "clast $(clast_version)"; exit 0`.
   - Unknown subcommand → error to stderr, `clast_usage` to stderr, exit 2.
   - `chmod +x bin/clast`.

2. **Expand `clast_usage`** in `lib/clast/clast-lib.bash`. Replace the step-02 placeholder with a real usage block listing the subcommands (real + stubbed), the global flags, and a one-line description for each. Keep it under ~30 lines. Print to stdout when invoked with `--help`; the dispatcher redirects it to stderr for usage errors.

3. **Write `lib/clast/clast-subcommands/whereami.bash`** defining `clast_cmd_whereami`. Per `docs/cli-contract.md#clast-whereami`:
   - Default output is the labeled key/value block (one field per line, fixed-width labels).
   - `--json` flag (or `CLAST_JSON=1` set by the dispatcher) emits a single JSON object with snake_case keys.
   - Fields:
     - `pwd` — `$PWD`.
     - `git_root` — `git rev-parse --show-toplevel 2>/dev/null`, or `null`/`—` if not a git repo.
     - `registered` — always `"no"` for now. Registry lookup lands in step 05; document this with a `# TODO(step-05)` comment.
     - `slug` — `null`/`—` for now (registry-dependent).
     - `remote` — `git -C "$git_root" config --get remote.origin.url` if available; null/`—` otherwise.
     - `last_snapshot` — `null`/`—` for now (manifest-dependent; step 04).
     - `journal_dir` — `clast_journal_dir`.
     - `projects_dir` — `clast_projects_dir`.
     - `day_cutoff` — `${CLAST_DAY_CUTOFF:-04:00}`.
     - `machine` — `hostname` (short form; strip domain if present).
   - JSON output: use `jq -n` with `--arg`/`--argjson` so empty fields serialize as `null` rather than empty strings.
   - Human output: render `null` fields as `—` (em-dash) to match `cli-contract.md`.

4. **Write `test/test-dispatcher.sh`** covering:
   - `bin/clast --version` prints `clast <version>` matching the `package.json` version, exits 0.
   - `bin/clast --help` and `bin/clast` (no args) both print usage to stdout and exit 0.
   - `bin/clast bogus-cmd` prints an error to stderr and exits 2.
   - A stubbed subcommand (e.g. `bin/clast snapshot`) exits 2 with a "not yet implemented" message — proving the stub-list path works.
   - `bin/clast --journal-dir /tmp/x whereami` causes `journal_dir` in the output to be `/tmp/x` (proves global-flag forwarding).

5. **Write `test/test-whereami.sh`** covering:
   - Default human output contains all 10 labeled fields in the documented order.
   - `--json` output is valid JSON, has the expected keys, and `pwd` equals `$PWD`.
   - In a non-git directory (a temp dir), `git_root` is `null` in JSON and `—` in human output.
   - In a git directory (use the clast repo itself, or `git init` a tmpdir), `git_root` is the repo root.
   - `--quiet` does not suppress whereami output (whereami is the output itself, not an info log) — but no `clast: info:` chatter appears on stderr.
   - `CLAST_DAY_CUTOFF=06:00 bin/clast whereami --json` reports `day_cutoff: "06:00"`.

6. **Wire both new tests into `test/test-clast.sh`** alongside `test-lib.sh` and `test-decode.sh`. Order: lib → decode → dispatcher → whereami.

7. **Confirm `make lint` passes.** `bin/clast` has no `.sh` extension; the Makefile lint rule from step 02 already picks it up via `[ -f bin/clast ] && echo bin/clast`. Add `# shellcheck source=lib/clast/clast-lib.bash` directives on the dispatcher's `source` lines.

## Acceptance criteria

- `bin/clast` exists, is executable, and runs end-to-end.
- `bin/clast --version` prints `clast 0.1.0` (matching `package.json`), exit 0.
- `bin/clast --help` prints usage to stdout, exit 0.
- `bin/clast bogus` prints error + usage to stderr, exit 2.
- `bin/clast snapshot` (stub) prints "not yet implemented" to stderr, exit 2.
- `bin/clast whereami` prints the 10-field labeled block, exit 0.
- `bin/clast whereami --json | jq -e .pwd` succeeds and equals `$PWD`.
- `CLAST_JOURNAL_DIR=/tmp/x bin/clast whereami --json | jq -r .journal_dir` prints `/tmp/x`.
- `bin/clast --journal-dir /tmp/x whereami --json | jq -r .journal_dir` prints `/tmp/x` (proves global-flag forwarding from the dispatcher into the lib).
- `test/test-dispatcher.sh` and `test/test-whereami.sh` both pass.
- `make test` runs all four test files and exits 0.
- `make lint` exits 0.

## Out of scope

- **Do not implement `registered` / `slug` / `remote` lookup against a real registry.** That ships with the registry lib in step 05. For now these fields are `null` / `—` and a `# TODO(step-05)` comment marks the spot.
- **Do not implement `last_snapshot` lookup against the manifest.** That ships with step 04. For now this field is `null` / `—` with a `# TODO(step-04)` comment.
- **Do not implement any other subcommand.** `snapshot`, `projects`, `sessions`, `show`, `entries`, `breadcrumb`, `registry`, `stats`, `doctor` are *stubbed* in the dispatcher with a "not yet implemented (planned for step NN)" message. The stub does **not** source a subcommand file (those files don't exist yet).
- **Do not handle `--verbose` semantics beyond setting `CLAST_VERBOSE=1`.** Subcommands consume it as they're built.
- **Do not write a config-file reader** for global flags. Env-var + CLI-flag override is sufficient for v1.
- **Do not change the existing libs from step 02 except to expand `clast_usage`.** Behavioral changes to `clast_lib.bash` or `clast_decode_lib.bash` belong in a separate step if needed.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Manual smoke
bin/clast --version
bin/clast --help
bin/clast whereami
bin/clast whereami --json | jq .
bin/clast bogus 2>&1 | head -3 ; echo "exit=$?"
bin/clast snapshot 2>&1 | head -3 ; echo "exit=$?"

# Global-flag forwarding
bin/clast --journal-dir /tmp/x whereami --json | jq -r '.journal_dir'  # → /tmp/x

# Cutoff propagation
CLAST_DAY_CUTOFF=06:00 bin/clast whereami --json | jq -r '.day_cutoff'  # → 06:00
```

## Notes for the implementer

- **Global-flag parsing is the only fiddly bit.** Walk argv with a `while [[ $# -gt 0 ]]` loop, peeling flags into env vars until you hit a non-flag arg (the subcommand) or `--`. The peeled values *must* be exported before sourcing the subcommand file, because libs read them at function-call time.
- **`--journal-dir` / `--projects-dir` are equivalent to setting `CLAST_JOURNAL_DIR` / `CLAST_PROJECTS_DIR`.** Set both the env var and (for clarity) re-export them. Step 02's `clast_journal_dir` already prefers the env var, so no lib changes needed.
- **`--json` propagation.** The dispatcher sets `CLAST_JSON=1`. Subcommands check that var; they don't re-parse `--json` themselves. But also accept `--json` as a subcommand-level flag for ergonomics — `clast whereami --json` should work even though the dispatcher could have peeled it. Easiest: subcommand checks `CLAST_JSON=1` OR scans its own argv for `--json`.
- **Stubbed subcommands** should print to stderr (not stdout) and exit 2. Example: `clast: snapshot is not yet implemented (planned for step 06)`. This keeps the stub honest — if you accidentally pipe `clast snapshot` to something expecting JSON, you get a usage error, not silent empty output.
- **`hostname` portability.** macOS and Linux `hostname` differ slightly. Use `hostname -s 2>/dev/null || hostname` to get the short form on both.
- **Whereami in JSON vs human.** Don't write the labels into JSON. The two output paths should share field computation but diverge cleanly at the rendering step — one switch statement at the bottom.
- **Don't print `clast: info: …` from `whereami`.** The subcommand's *output is data*; info logs would corrupt JSON consumers. Reserve `clast_log_info` for snapshot/registry/entries write paths.
- **Conventional commit suggestion**: `feat(cli): add bin/clast dispatcher and whereami subcommand`. One commit per step keeps `depends_on` history readable.
