---
step: 20
title: stale-help-fix
depends_on: [03, 05, 07, 08, 09, 15, 16, 19]
size: small
references:
  - lib/clast/clast-lib.bash
  - bin/clast
  - docs/cli-contract.md
---

# Step 20: drop "(planned)" labels from `clast --help`

## Context

Step 03 wrote the top-level usage block in `lib/clast/clast-lib.bash`
when only `whereami` was implemented. The block tagged every other
subcommand as `(planned)` so the help output honestly reflected the
v0.0 state of the CLI. Subsequent steps (05 registry, 07 query, 08
entries, 09 breadcrumb) shipped those subcommands but each missed
updating the usage block, so `clast --help` today still labels six
working subcommands as `(planned)`:

```
projects      List projects with activity in a window  (planned)
sessions      List sessions in a window                (planned)
show          Dump session metadata                    (planned)
entries       List or read curated journal entries     (planned)
breadcrumb    Append a one-line in-flight hint         (planned)
registry      Manage the project registry              (planned)
```

This was flagged at the end of step 19 as a follow-up. It does not
fall under any of the original 19 planned steps in
`docs/build-steps.md`; step 20 is a small post-v1-plan patch.

This is also the final PR in the original build series. Once it
merges, the codebase reflects the planned end state and the human's
next action is `contrib/release --patch` to cut v0.0.1.

## Goal

Drop the six `(planned)` suffixes in `lib/clast/clast-lib.bash`'s
usage block, leaving only honest one-line descriptions of each
shipped subcommand. Add a regression test that fails if any
subcommand description ever regrows a `(planned)` suffix without a
matching dispatcher stub.

## References

- `lib/clast/clast-lib.bash:208-232` — the usage heredoc emitted by
  the `--help` global flag.
- `bin/clast` — the dispatcher; every case branch corresponds to a
  shipped subcommand. Cross-reference: every name in the help
  output should have a `bin/clast` case branch sourcing a real
  `.bash` file under `lib/clast/clast-subcommands/`.
- `docs/cli-contract.md` — canonical one-line descriptions if a
  tighter wording is needed; the existing lines are accurate, so
  this step is just dropping the `(planned)` suffix, not rewording.
- `test/test-dispatcher.sh` — where the regression test lands.

## Tasks

1. **Edit `lib/clast/clast-lib.bash`** to drop the six `(planned)`
   tokens from the usage heredoc. Preserve column alignment so the
   help output stays visually consistent. Touch only the six lines;
   do not reword the descriptions.

2. **Add a regression test** to `test/test-dispatcher.sh` (or a new
   sibling test file if the dispatcher test is the wrong home). It
   should:
   - Invoke `bin/clast --help`.
   - Assert the output contains no `(planned)` substring.
   - For each subcommand listed in the help, confirm it has a
     matching dispatcher branch in `bin/clast` (regex match on the
     subcommand name appearing in a case pattern).
   - Exit 0 on green, non-zero with a clear diagnostic on red.

3. **Confirm `make lint`, `make test`, `make check-version-sync`
   exit 0.** Run `make npm-pack-check` and `make nix-smoke` for
   thoroughness; they may skip cleanly without their tooling.

4. **Do NOT edit `docs/cli-contract.md`** or any other reference doc.
   The help block is the only stale surface; the reference docs
   describe the CLI's design, which has been correct throughout.

5. **Do NOT bump the version.** The version literal stays at
   `0.0.0`; `contrib/release --patch` is the human's release action,
   not this step's.

## Acceptance criteria

- `bin/clast --help` output contains no `(planned)` substring.
- Each subcommand listed in `bin/clast --help` has a matching
  dispatcher case branch in `bin/clast`.
- A test under `test/` enforces both invariants and is wired into
  `make test`.
- `make lint`, `make test`, and `make check-version-sync` exit 0.
- `make npm-pack-check` and `make nix-smoke` exit 0 OR skip cleanly.
- No other files are modified (verify with `git diff main..HEAD
  --name-only`).
- `package.json` and `flake.nix` remain at version `0.0.0`.

## Out of scope

- **Rewording subcommand descriptions.** Drop `(planned)`; do not
  rewrite the descriptions.
- **Adding `--help` text to individual subcommands.** Each
  subcommand already prints its own help block (steps 05–10
  handled per-subcommand usage). This step touches only the
  top-level usage.
- **Editing `docs/cli-contract.md` or other reference docs.**
- **Bumping the version.** Reserved for `contrib/release`.
- **Generating a new CHANGELOG entry.** `git-cliff` regenerates the
  changelog on the next `contrib/release` invocation; this fix
  appears under `### Fixed` in v0.0.1's auto-generated entry.

## Verification

```bash
# Top-level help has no "(planned)" labels
! bin/clast --help 2>&1 | grep -q '(planned)'

# Every help-listed subcommand has a dispatcher branch
bin/clast --help 2>&1 \
  | awk '/^Subcommands:/{p=1; next} /^Global flags:/{p=0} p && NF{print $1}' \
  | while read -r sub; do
      grep -qE "^[[:space:]]*${sub}\)" bin/clast \
        || { echo "no dispatcher branch for: $sub" >&2; exit 1; }
    done

# Gates
make lint
make test
make check-version-sync
make npm-pack-check
make nix-smoke

# Diff scope (the test file path may vary)
git diff main..HEAD --name-only
# expect: docs/steps/step-20-stale-help-fix.md, lib/clast/clast-lib.bash, test/test-…
```

## Notes for the implementer

- **Why a regression test, not just the fix.** This kind of stale-
  text drift is the failure mode that lets the same bug recur the
  next time someone adds a subcommand. A two-line test means the
  next would-be-planted `(planned)` either becomes intentional
  (dispatcher stub gone, test updated) or fails CI immediately.
- **Column alignment.** The current help heredoc aligns descriptions
  in a column. Dropping `  (planned)` leaves trailing whitespace if
  the lines were padded for alignment with the suffix; tidy that
  trailing whitespace so `shellcheck` and any whitespace pre-commit
  hook stay quiet.
- **Conventional commit suggestion**: `fix(cli): drop stale "(planned)"
  labels from --help and add regression test`.
