---
step: 14
title: install-scripts
depends_on: [01, 03, 11]
size: small
references:
  - docs/repo-bootstrap.md#installsh--uninstallsh
  - docs/repo-bootstrap.md#repo-layout
  - docs/repo-bootstrap.md#binclast
  - docs/repo-bootstrap.md#makefile
  - docs/overview.md#distribution
---

# Step 14: `install.sh` / `uninstall.sh`

## Context

The Makefile already exposes `make install` and `make uninstall` targets that
shell out to `./install.sh` and `./uninstall.sh` (see `Makefile` lines for
`install:` and `uninstall:`), but those scripts do not yet exist — running
`make install` today fails with "No such file or directory". Step 14 lands the
two scripts so the targets work, completes the **manual prefix install** path
documented in `docs/repo-bootstrap.md#installsh--uninstallsh`, and gives
contributors a reproducible way to drop the in-tree build onto a real `PATH`
without going through `npm` (step 16) or `nix` (step 15).

What is already on `main` and available to package:

- `bin/clast` — the dispatcher (step 03). It computes `CLAST_LIB` from its own
  realpath as `$(dirname "$(realpath "$0")")/../lib/clast` unless `CLAST_LIB`
  is set, so a `$PREFIX/bin/clast` install with libs at `$PREFIX/lib/clast/`
  resolves correctly without a wrapper.
- `lib/clast/*.bash` and `lib/clast/clast-subcommands/*.bash` — all libs and
  every subcommand merged through step 10, plus the step-09 stub for
  `breadcrumb` that still lives in `bin/clast`'s dispatcher. Install.sh copies
  files verbatim; it does **not** gate on subcommand completeness, so shipping
  the stub is expected and intentional.
- `.claude-plugin/` — `plugin.json` (step 11) and `skills/wakeup/SKILL.md`
  (step 13). `skills/day-wakeup/` is not yet merged (step 12 plan is in but
  impl is not); install.sh copies whatever is present, so when step 12 lands
  later no re-edit of install.sh is required.
- `hooks/` — `hooks.json` and `snapshot.sh` (step 11).
- `examples/` — `config/`, `cron/`, `workflows/` directories, currently
  `.gitkeep`-only. Step 18 fills them in. Install.sh copies the tree as-is.
- `README.md`, `LICENSE` — top-level docs the npm `files` field already ships.

This step is **strictly the manual-prefix install path**. The nix flake
package (step 15), npm finalization (step 16), CI verification (step 17), and
Claude Code plugin marketplace integration are all separately scoped.

**Run `direnv allow` (or `nix develop`) before starting** so `shellcheck` is
on `PATH` for `make lint` and the new test has `bash`/`install`/`mktemp`
available.

## Goal

Land `install.sh` and `uninstall.sh` at the repo root — both POSIX-bash,
`shellcheck` clean, idempotent — that respectively copy the in-tree build to
`$PREFIX` (default `/usr/local`) and remove the same files from `$PREFIX`.
Wire them into `make lint`, add `test/test-install.sh` that exercises both
end-to-end against a `mktemp` prefix (no real `/usr/local` writes), and
extend `README.md` with a short "Install to a prefix" section.

## References

Read before starting:

- `docs/repo-bootstrap.md#installsh--uninstallsh` — **canonical content for
  `install.sh`**. The xcind-mirrored template (`PREFIX="${1:-/usr/local}"`,
  `install -m755`, copy `lib/clast/*`, `.claude-plugin`, `hooks`, `examples`,
  print the post-install message naming the plugin install command). Use the
  template verbatim except for the deviations called out in task 2.
- `docs/repo-bootstrap.md#repo-layout` — establishes which top-level dirs ship
  in the install: `bin/`, `lib/`, `.claude-plugin/`, `hooks/`, `examples/`,
  plus `README.md` and `LICENSE` (the latter two are in the npm `files` field;
  the install copies them too for parity).
- `docs/repo-bootstrap.md#binclast` — confirms the realpath-based `CLAST_LIB`
  resolution so installed `$PREFIX/bin/clast` works without a wrapper.
- `docs/repo-bootstrap.md#makefile` — the existing `install:` / `uninstall:`
  targets shell out to `./install.sh` / `./uninstall.sh`. Do not change the
  Makefile in this step.
- `docs/overview.md#distribution` — context on why the manual prefix path
  exists alongside npm and nix (skim only; this step does not touch those).

## Tasks

1. **Create `install.sh`** at the repo root. `#!/usr/bin/env bash`, `set -euo
   pipefail`. Single positional argument is `PREFIX`, defaulting to
   `/usr/local`. Compute `SRC` as `$(cd "$(dirname "$0")" && pwd)` so the
   script works when invoked via a symlink or from another directory.
   - `mkdir -p "$PREFIX/bin" "$PREFIX/lib/clast" "$PREFIX/share/clast"`.
   - `install -m755 "$SRC/bin/clast" "$PREFIX/bin/clast"`.
   - `rm -rf "$PREFIX/lib/clast"` then `cp -R "$SRC/lib/clast/." "$PREFIX/lib/clast/"`
     so a re-install with a renamed/removed subcommand file does not leave a
     stale `.bash` behind. The trailing `/.` form avoids the BSD-vs-GNU
     `cp -r DIR/ DST` divergence.
   - `rm -rf "$PREFIX/share/clast/.claude-plugin" "$PREFIX/share/clast/hooks"
     "$PREFIX/share/clast/examples"` then `cp -R` each tree under
     `$PREFIX/share/clast/`. Same rationale: stale files (e.g. a removed
     skill directory) must not survive a re-install.
   - `install -m644 "$SRC/README.md" "$PREFIX/share/clast/README.md"` and
     `install -m644 "$SRC/LICENSE" "$PREFIX/share/clast/LICENSE"`.
   - Final stdout block (verbatim from the canonical doc, with a trailing
     line added for the uninstaller hint):
     ```
     Installed clast to $PREFIX
       Binary: $PREFIX/bin/clast
       Plugin: $PREFIX/share/clast/.claude-plugin

     Add the plugin via:
       claude plugin install $PREFIX/share/clast

     Uninstall with:
       $SRC/uninstall.sh $PREFIX
     ```
   - Make executable: `chmod +x install.sh` and check it into git that way.

2. **Deviations from the canonical doc template (apply during the verbatim
   copy):**
   - **Idempotent re-installs.** The canonical template uses plain `cp -r`,
     which leaves orphans when a subcommand file is renamed between installs.
     Wrap the tree copies in `rm -rf` + `cp -R DIR/. DST/` as described in
     task 1. Inline-comment the rationale (`# Drop stale files from a prior
     install before re-copying.`); the canonical doc is silent on this.
   - **Preserve the executable bit on `hooks/snapshot.sh`.** `cp -R` preserves
     mode on most platforms but not all. After the hooks copy, run
     `chmod +x "$PREFIX/share/clast/hooks/snapshot.sh"` explicitly so the
     SessionStart hook is executable regardless of the source filesystem's
     mode bits.
   - **No fancy prefix expansion.** Accept the literal `$1` as `PREFIX` and
     do not attempt `~`-expansion (`/bin/sh` does, the user's invoking shell
     already did, and re-expanding would surprise CI). The canonical template
     also takes the literal — keep it.
   - **No symlink mode.** A future ergonomic could `ln -sf $SRC/bin/clast
     $PREFIX/bin/clast` for an "editable install" — out of scope for v1.

3. **Create `uninstall.sh`** at the repo root. Same shebang + flags + `SRC`
   resolution + `PREFIX` arg as install.sh. Remove the exact set the
   installer wrote:
   - `rm -f "$PREFIX/bin/clast"`.
   - `rm -rf "$PREFIX/lib/clast"`.
   - `rm -rf "$PREFIX/share/clast"` (this nukes `.claude-plugin`, `hooks`,
     `examples`, `README.md`, `LICENSE` together — they all live under
     `share/clast/`).
   - Each `rm` is unconditional (no `[ -e ]` guard). `rm -f` and `rm -rf` are
     already silent on missing paths; an explicit guard adds noise without
     value.
   - Final stdout: `Uninstalled clast from $PREFIX`.
   - Do NOT remove the parent `$PREFIX/bin`, `$PREFIX/lib`, or `$PREFIX/share`
     dirs themselves — those may belong to other installs.
   - Make executable: `chmod +x uninstall.sh`.

4. **Extend `make lint`.** `Makefile`'s `lint` target builds the file list
   dynamically with `find` over `lib/clast`, `test`, plus `bin/clast` and
   `hooks/snapshot.sh`. Add `install.sh` and `uninstall.sh` to that list:
   in the `lint:` target's file-list expression, append `[ -f install.sh ] &&
   echo install.sh; [ -f uninstall.sh ] && echo uninstall.sh` (or fold them
   into the existing `find` over the repo root if cleaner). Verify `make
   lint` still exits 0 and now covers the two new scripts.

5. **Create `test/test-install.sh`.** A new test that exercises both scripts
   against a `mktemp -d` prefix. Sourcing pattern is the same as the existing
   suite (see `test/test-clast.sh` for shape): set `set -euo pipefail`, source
   `test/test-lib.sh` for assertion helpers and the `TEST_TMPDIR` trap.
   - **Setup.** `PREFIX="$(mktemp -d -t clast-install.XXXXXX)"`; register a
     trap to `rm -rf "$PREFIX"` on EXIT.
   - **Install.** Run `./install.sh "$PREFIX"`. Assert exit 0.
   - **Layout assertions.** Each is an individually-named assertion so the
     failure message points at the missing file:
     - `$PREFIX/bin/clast` exists and is executable (`-x`).
     - `$PREFIX/lib/clast/clast-lib.bash` exists.
     - `$PREFIX/lib/clast/clast-decode-lib.bash` exists.
     - `$PREFIX/lib/clast/clast-registry-lib.bash` exists.
     - `$PREFIX/lib/clast/clast-manifest-lib.bash` exists.
     - `$PREFIX/lib/clast/clast-subcommands/whereami.bash` exists (used as a
       canary — if subcommand dir copying broke, this misses).
     - `$PREFIX/share/clast/.claude-plugin/plugin.json` exists.
     - `$PREFIX/share/clast/hooks/hooks.json` exists.
     - `$PREFIX/share/clast/hooks/snapshot.sh` exists and is executable.
     - `$PREFIX/share/clast/README.md` exists.
     - `$PREFIX/share/clast/LICENSE` exists.
   - **Functional assertion.** Run `"$PREFIX/bin/clast" --version` with
     `CLAST_LIB` **unset** (`unset CLAST_LIB`) and assert exit 0 and stdout
     matches `^clast `. This proves the realpath-based `CLAST_LIB` resolution
     works from the installed location — the load-bearing claim about the
     install layout.
   - **Re-install idempotency.** Run `./install.sh "$PREFIX"` a second time;
     assert exit 0 and re-run a representative layout assertion (e.g. the
     `--version` invocation) still passes.
   - **Stale-file pruning.** Drop a sentinel file at
     `$PREFIX/lib/clast/clast-subcommands/_obsolete.bash`, re-run
     `./install.sh "$PREFIX"`, then assert the sentinel is gone. This is the
     regression test for the `rm -rf` + `cp -R DIR/.` deviation in task 2.
   - **Uninstall.** Run `./uninstall.sh "$PREFIX"`. Assert exit 0. Then
     assert `$PREFIX/bin/clast`, `$PREFIX/lib/clast`, and `$PREFIX/share/clast`
     all no longer exist. Assert `$PREFIX/bin`, `$PREFIX/lib`, `$PREFIX/share`
     **still** exist (uninstall leaves the parent dirs alone — see task 3).
   - **Uninstall idempotency.** Run `./uninstall.sh "$PREFIX"` a second time;
     assert exit 0 (no-op should not fail).
   - Make executable (`chmod +x test/test-install.sh`).

6. **Register the new test in `test/test-clast.sh`.** The umbrella runner
   invokes each `test/test-*.sh` script; add `test-install.sh` to its
   invocation list in the same shape as the existing entries (e.g. the
   `test-stats.sh` invocation pattern). Confirm `make test` runs the new
   suite and exits 0.

7. **Update `README.md`.** Add a short "Install to a prefix" section
   immediately before the existing "Install as a Claude Code plugin" section.
   3–6 lines. State the default prefix, show `./install.sh ~/.local` as the
   recommended non-root example, mention that `make install` is the wrapper,
   and link to `docs/repo-bootstrap.md#installsh--uninstallsh` for the
   rationale. Mention the uninstall counterpart in one sentence.

## Acceptance criteria

- `install.sh` exists at the repo root, is executable (mode `0755` in git),
  starts with `#!/usr/bin/env bash`, uses `set -euo pipefail`, accepts a
  single optional `PREFIX` argument defaulting to `/usr/local`, and shellcheck
  passes on it.
- `uninstall.sh` exists at the repo root with the same shape (executable,
  `set -euo pipefail`, optional `PREFIX` argument defaulting to `/usr/local`)
  and shellcheck passes on it.
- After `./install.sh "$TMP"` against a fresh `mktemp -d` prefix, every layout
  item from task 5 is present at the expected path with the expected mode.
- `"$TMP/bin/clast" --version` (with `CLAST_LIB` unset) exits 0 and prints a
  `clast ...` version line — proves the installed binary resolves its libs by
  realpath.
- A second `./install.sh "$TMP"` invocation against the same prefix exits 0,
  and a sentinel file dropped under `$TMP/lib/clast/clast-subcommands/`
  between installs is removed by the second install (idempotent + stale-file
  pruning).
- After `./uninstall.sh "$TMP"`, `$TMP/bin/clast`, `$TMP/lib/clast`, and
  `$TMP/share/clast` no longer exist; `$TMP/bin`, `$TMP/lib`, `$TMP/share`
  still exist.
- A second `./uninstall.sh "$TMP"` invocation exits 0 (no-op safe).
- `test/test-install.sh` encodes every assertion above, is executable, is
  invoked by `test/test-clast.sh`'s umbrella runner, and `make test` exits 0.
- `make lint` runs `shellcheck` over `install.sh` and `uninstall.sh` (in
  addition to its previous coverage) and exits 0.
- `README.md` has a new "Install to a prefix" section that names the default
  prefix, shows a non-root example, and links the canonical doc section.

## Out of scope

- **Nix packaging.** `packages.default` / `overlays.default` are step 15. Do
  not touch `flake.nix` in this step.
- **npm packaging finalization.** The `prepublishOnly` script, dry-run pack,
  and any tweaks to `package.json`'s `files` array are step 16. Do not touch
  `package.json` here even though the file list overlaps — the install script
  is filesystem-prefix, not npm.
- **CI verification.** Wiring `install.sh` / `uninstall.sh` into
  `.github/workflows/test.yml` (or a new install-smoke workflow) is step 17.
  The new `test/test-install.sh` runs locally via `make test`; that is the
  full v1 surface for this step.
- **Real `/usr/local` writes.** No part of acceptance writes to
  `/usr/local`. Every assertion uses `mktemp -d`. Reviewers should not need
  `sudo` to verify.
- **Symlink/editable install mode.** Out of scope — see task 2.
- **`claude plugin install` execution.** The install script *prints* the
  command; it does not run it. The plugin install integration is the user's
  call, not the installer's.
- **Implementing missing subcommands.** `clast breadcrumb` is still a stub
  (step 09 impl not merged). `install.sh` ships the stub as-is; do not
  retrofit a breadcrumb subcommand here. If the stub is gone by the time
  this step is executed, install.sh still works unchanged — it copies files,
  not the dispatcher's case statement.
- **Skill files that have not landed.** If
  `.claude-plugin/skills/day-wakeup/SKILL.md` is still absent (step 12 impl
  not merged), install.sh copies whatever is present and that is correct;
  do not stub the missing skill here.
- **Cron / workflow examples content.** `examples/cron/`, `examples/workflows/`,
  `examples/config/` are still `.gitkeep`-only (step 18). Install.sh copies
  the empty trees; do not fill them in this step.
- **Cross-platform path edge cases.** Targets POSIX `install`, `cp`, `mkdir`,
  `rm` — i.e. macOS and Linux. Windows / MSYS / Cygwin install paths are not
  in scope for v1.
- **Permissions auditing.** install.sh does not check whether the user can
  write to `$PREFIX`; the underlying `install`/`mkdir` failures are
  sufficient. Adding a pre-flight `[ -w "$PREFIX" ]` check is out of scope.

## Verification

```bash
# Lint (must include install.sh and uninstall.sh in the file list)
make lint

# Tests (includes the new test-install.sh)
make test

# Targeted install/uninstall smoke against a temp prefix
PREFIX="$(mktemp -d -t clast-install.XXXXXX)"
./install.sh "$PREFIX"
ls -R "$PREFIX" | sed -n '1,40p'

# Realpath-based CLAST_LIB resolution must work from the install location.
unset CLAST_LIB
"$PREFIX/bin/clast" --version
"$PREFIX/bin/clast" whereami --help >/dev/null

# Re-install must be idempotent and prune stale subcommand files.
touch "$PREFIX/lib/clast/clast-subcommands/_obsolete.bash"
./install.sh "$PREFIX"
[ ! -e "$PREFIX/lib/clast/clast-subcommands/_obsolete.bash" ] \
  && echo "ok: stale file pruned" \
  || echo "FAIL: stale file survived re-install"

# Uninstall removes the install set but leaves $PREFIX skeleton dirs.
./uninstall.sh "$PREFIX"
[ ! -e "$PREFIX/bin/clast" ]      && echo "ok: bin gone"
[ ! -e "$PREFIX/lib/clast" ]      && echo "ok: lib gone"
[ ! -e "$PREFIX/share/clast" ]    && echo "ok: share gone"
[ -d "$PREFIX/bin" ]              && echo "ok: bin/ skeleton intact"

# Second uninstall must no-op cleanly.
./uninstall.sh "$PREFIX"

rm -rf "$PREFIX"
unset PREFIX
```

## Notes for the implementer

- **The realpath trick is the load-bearing claim.** `bin/clast` resolves
  `CLAST_LIB` as `$(dirname "$(realpath "$0")")/../lib/clast`. The install
  layout (`$PREFIX/bin/clast` + `$PREFIX/lib/clast/`) is designed around this:
  it works without a wrapper script and without an env-var export. If a
  future change to `bin/clast` adds a different lib-resolution path,
  `test/test-install.sh`'s `--version` invocation with `unset CLAST_LIB` is
  the regression test that catches the break.
- **`cp -R DIR/. DST/` portability.** Both BSD `cp` (macOS default) and GNU
  `cp` (Linux) interpret `cp -R src/. dst/` as "copy contents into dst". The
  alternative `cp -r src/ dst` differs across platforms (some create `dst/src/`).
  Use the `/.` form throughout.
- **`install -m755`.** POSIX `install(1)` is available on both BSD and GNU.
  Use `install -m755` for the binary, `install -m644` for `README.md` /
  `LICENSE`. Tree copies use `cp -R` (preserving permissions from source);
  the explicit `chmod +x snapshot.sh` after the hooks copy is the belt to
  `cp -R`'s suspenders.
- **Stale-file pruning is the only non-obvious behavior.** Plain `cp -r`
  leaves a `clast-subcommands/old.bash` behind when the source no longer has
  one. The `rm -rf $PREFIX/lib/clast && cp -R lib/clast/. $PREFIX/lib/clast/`
  pattern is what makes re-install equivalent to fresh-install. The sentinel
  assertion in `test/test-install.sh` exists specifically to catch a future
  "optimization" that drops the `rm -rf`.
- **`uninstall.sh` is intentionally dumb.** It deletes a fixed set of paths
  the installer is known to write. It does not introspect what was installed.
  If a future install step writes to a new location, both scripts update in
  lockstep — there is no "uninstall reads a manifest" indirection. The cost
  is simplicity; the trade-off is acceptable for v1.
- **Why no `--help` flag.** The scripts are one-arg-and-go. A `--help` flag
  is a future ergonomic — the canonical doc does not include one. Keep the
  surface minimal.
- **Why `$PREFIX/share/clast/{README,LICENSE}`.** Mirrors the FHS pattern
  (`/usr/local/share/<pkg>/`). The npm `files` field ships them at the
  package root; the manual install ships them under `share/clast/` so a
  single prefix has a self-contained install footprint.
- **Conventional commit suggestion**: `feat(install): add install.sh and
  uninstall.sh for prefix install`. One commit is fine.
