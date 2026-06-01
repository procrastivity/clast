---
step: 15
title: nix-flake-package
depends_on: [01, 11, 14]
size: medium
references:
  - docs/repo-bootstrap.md#nix-flake
  - docs/repo-bootstrap.md#repo-layout
  - docs/repo-bootstrap.md#binclast
  - docs/repo-bootstrap.md#installsh--uninstallsh
  - docs/overview.md#distribution
---

# Step 15: Nix flake package + overlay

## Context

Step 01 shipped a `flake.nix` with only `devShells.default` (contributors run
`direnv allow` or `nix develop` and get `bash`/`jq`/`shellcheck`/`pre-commit`).
Step 14 shipped `install.sh` / `uninstall.sh` as the manual-prefix install path
and proved out the installed layout (`$PREFIX/bin/clast` resolving libs via the
realpath trick, `$PREFIX/share/clast/{.claude-plugin,hooks,examples,README.md,LICENSE}`,
explicit `chmod +x` on `hooks/snapshot.sh`, idempotent re-install). Step 15
adds the **second distribution channel**: a Nix flake package + overlay so
`nix build` and `nix run github:procrastivity/clast` work for any user with
Nix, and so Beau's Home Manager flake can pull `pkgs.clast` via an overlay.

What is already on `main` and available to package:

- `bin/clast` — the dispatcher (step 03). Uses `CLAST_LIB="${CLAST_LIB:-$(dirname
  "$(realpath "$0")")/../lib/clast}"` so the realpath-relative resolution works
  for an unwrapped install; the Nix wrapper additionally pins `CLAST_LIB` via
  `wrapProgram --set` so the binary is robust against PATH games.
- `lib/clast/*.bash` + `lib/clast/clast-subcommands/*.bash` — every subcommand
  (whereami, snapshot, projects, sessions, show, entries, breadcrumb,
  registry, stats, doctor) is real after steps 02–10 and 09; no stubs ship
  any more.
- `.claude-plugin/` — `plugin.json` (step 11), `skills/wakeup/SKILL.md`
  (step 13), and `skills/day-wakeup/SKILL.md` (step 12).
- `hooks/` — `hooks.json` + `snapshot.sh` (step 11). `snapshot.sh` is
  mode 0755 in git; Nix preserves the source mode via `cp -r`, but the
  installPhase still explicitly `chmod +x`'s it as a belt-and-suspenders
  guard (same rationale as step 14's install.sh deviation).
- `examples/` — `config/`, `cron/`, `workflows/` directories, still
  `.gitkeep`-only (step 18 fills them in). The flake copies the tree as-is.
- `README.md`, `LICENSE` — top-level docs already shipped by step 14's
  install.sh under `$PREFIX/share/clast/`; the Nix package follows the same
  FHS-ish layout under `$out/share/clast/` for parity.

This step is **strictly the Nix flake package + overlay**. CI verification of
`nix flake check` / `nix build` (`.github/workflows/nix.yml`) is step 17. The
npm-distribution finalization is step 16. Cachix / binary caches, the Home
Manager module wiring, nix-darwin support, and any non-`x86_64-linux` /
`aarch64-darwin` cross-build assertions are all explicitly out of scope.

**Run `direnv allow` (or `nix develop`) before starting** so `nix` (with
flakes enabled) is on PATH for the verification commands.

## Goal

Land an updated `flake.nix` that adds `packages.default`, `packages.clast` (an
alias of default), and `overlays.default` to the existing `devShells.default`
shape, leaving the dev shell behavior byte-identical. Regenerate `flake.lock`
if `nix flake check` warrants. Verify `nix build` produces a working binary
(`./result/bin/clast --version` exits 0 with `CLAST_LIB` unset and unset PATH
for system jq/git), `nix run` works the same way, and the installed layout
under `./result/share/clast/` matches the FHS layout from step 14. Wire the
verification into a new `make nix-smoke` target (no CI yet — that is step 17)
and extend `README.md` with a "Install with Nix" section.

## References

Read before starting:

- `docs/repo-bootstrap.md#nix-flake` — **canonical content for the
  flake.nix**. The xcind-mirrored outline (lines 285–334 of that doc)
  defines `packages.default` with `installPhase`, `wrapProgram --set
  CLAST_LIB`, `--prefix PATH` for `jq` / `coreutils` / `git`, plus the
  `packages.clast` alias and `overlays.default` block. Use the outline
  verbatim except for the deviations called out in task 2.
- `docs/repo-bootstrap.md#repo-layout` — establishes which top-level dirs
  ship in the package: `bin/`, `lib/`, `.claude-plugin/`, `hooks/`,
  `examples/`, plus `README.md` and `LICENSE`. Matches step 14's install
  set.
- `docs/repo-bootstrap.md#binclast` — confirms the realpath-based
  `CLAST_LIB` resolution. The Nix wrapper pins `CLAST_LIB` explicitly,
  which means the package would work even if the realpath trick broke;
  treat that as defense in depth, not the primary mechanism.
- `docs/repo-bootstrap.md#installsh--uninstallsh` — step 14's installed
  layout. The Nix package mirrors it: `$out/bin/clast` for the binary,
  `$out/lib/clast/` for the libs, `$out/share/clast/{.claude-plugin,hooks,
  examples,README.md,LICENSE}` for the rest. Parity matters because users
  who switch between manual-prefix and Nix installs should see the same
  shape under `share/clast/`.
- `docs/overview.md#distribution` — context on why three distribution
  channels exist side-by-side (skim only; this step does not touch the
  others).

## Tasks

1. **Rewrite `flake.nix`** to the final shape from
   `docs/repo-bootstrap.md#nix-flake`, with the deviations in task 2. Update
   the top-level `description` from the current `"clast — Claude Code
   session journal (dev shell)"` to `"clast — Claude Code session journal"`
   (drop the `(dev shell)` qualifier — the flake is no longer dev-shell-only).
   Preserve the existing `devShells.default` block verbatim: same
   `buildInputs` list (`bash`, `jq`, `coreutils`, `git`, `shellcheck`,
   `pre-commit`) and same `shellHook` that prints `clast dev shell — bash
   $(...)`. Do NOT swap to the canonical doc's leaner `bats`-instead-of-
   `pre-commit` dev shell — that is a doc example, not the lived shape, and
   contributors rely on the current set. The trailing comment in the existing
   flake (`# packages.default and overlays.default land in step 15.`) is now
   stale; replace it with the real packages/overlays blocks.

2. **Deviations from the canonical doc template (apply during the literal
   transcription):**
   - **Belt-and-suspenders `chmod +x hooks/snapshot.sh`**. After the
     `cp -r hooks $out/share/clast/` line, run `chmod +x
     $out/share/clast/hooks/snapshot.sh` in the installPhase. Same rationale
     as step 14's install.sh deviation: `cp -r` preserves source mode on
     macOS/Linux, but a future filesystem with looser mode preservation
     would silently break the SessionStart hook. The explicit chmod is
     cheap insurance. Add an inline shell comment in the installPhase
     (`# Belt-and-suspenders: ensure the SessionStart hook is executable.`)
     so a reader knows why it's there.
   - **`README.md` and `LICENSE` under `$out/share/clast/`**. The canonical
     outline does not include these — but step 14's install.sh ships them
     under `$PREFIX/share/clast/`, and parity is the point. Add
     `install -m644 README.md $out/share/clast/README.md` and
     `install -m644 LICENSE $out/share/clast/LICENSE` after the directory
     copies. If `LICENSE` is missing for any reason, `install` fails the
     build — that is correct behavior; do not guard the call with a `[ -f
     ]` test.
   - **`cp -r DIR/. $out/share/clast/DIR/`** (with the trailing `/.`) for
     `.claude-plugin`, `hooks`, and `examples`. The canonical outline uses
     `cp -r hooks $out/share/clast/` which on some BSD `cp` builds creates
     `$out/share/clast/hooks/hooks` (a sub-`hooks` dir). Switch to the
     `mkdir -p $out/share/clast/<name>` + `cp -R <name>/. $out/share/clast/<name>/`
     idiom step 14 uses, for cross-platform consistency. Apply to all three
     copied trees.
   - **No version pin in `pname` / `version`**. Use `version = "0.1.0"`
     (matches `package.json` today). Pulling the version from
     `package.json` via `builtins.fromJSON (builtins.readFile ./package.json)`
     is cleaner but adds a Nix dependency on the package.json shape;
     reserve that ergonomic for step 16's npm-prep work. For now, keep
     the literal `"0.1.0"` in flake.nix and add a one-line comment
     (`# Bump in lockstep with package.json.`) above it so a future bumper
     sees both spots.
   - **`packages.clast` is a literal alias**, not a `self.packages.${system}.default`
     re-reference at the flake-output level. The canonical outline uses
     the self-reference form; the alias form (`packages.clast =
     self.packages.${system}.default;`) is what makes `nix run
     github:procrastivity/clast#clast` work alongside `nix run
     github:procrastivity/clast`. Keep both — `packages.default` is the
     primary; `packages.clast` is the named alias.
   - **Pure overlay**, no version override. The overlay shape is
     `overlays.default = final: prev: { clast = self.packages.${prev.system}.default; };`
     — exactly as the canonical doc. Do not parameterize it; a future
     overlay-factory ergonomic belongs to a follow-up step.

3. **Regenerate `flake.lock` if needed.** Run `nix flake update` only if the
   existing `nixpkgs` / `flake-utils` inputs fail to evaluate against the
   new `packages.default` block. If `nix flake check` passes against the
   existing lock, do NOT bump pins for the sake of bumping — keep the diff
   minimal. If a bump IS required, commit `flake.lock` alongside `flake.nix`
   in the same commit.

4. **Add a `make nix-smoke` target** to the Makefile. New target, listed in
   `.PHONY` alongside the existing ones. Recipe:
   ```makefile
   nix-smoke:
       @if ! command -v nix >/dev/null 2>&1; then \
           echo "nix-smoke: skipping (nix not on PATH)" ; \
           exit 0 ; \
       fi
       nix build --print-out-paths --no-link .#default
       nix build --print-out-paths --no-link .#clast
       result_default=$$(nix build --print-out-paths --no-link .#default) ; \
       env -u CLAST_LIB PATH=/usr/bin:/bin "$$result_default/bin/clast" --version ; \
       env -u CLAST_LIB PATH=/usr/bin:/bin "$$result_default/bin/clast" whereami --help >/dev/null ; \
       test -f "$$result_default/share/clast/.claude-plugin/plugin.json" ; \
       test -x "$$result_default/share/clast/hooks/snapshot.sh" ; \
       test -f "$$result_default/share/clast/README.md" ; \
       test -f "$$result_default/share/clast/LICENSE"
   ```
   The `env -u CLAST_LIB PATH=...` invocation proves the Nix wrapper pins
   `CLAST_LIB` and provides `jq`/`coreutils`/`git` via `--prefix PATH` (so
   stripping the user PATH to system minimums still works). The `nix-smoke`
   target deliberately does NOT add `nix-smoke` to the `test:` target's
   prerequisites — Nix may not be available in every contributor's
   environment, and `make test` is supposed to work in plain bash. The
   `command -v nix` guard makes the target a no-op when Nix is absent so
   `make nix-smoke` doesn't fail in environments where it can't run.

5. **DO NOT add `nix-smoke` to `.github/workflows/test.yml`.** CI for the
   Nix path lives in `.github/workflows/nix.yml`, scheduled for step 17.
   This step ships only the local-developer make target.

6. **Add a verification script at `contrib/nix-smoke.sh`** (new file,
   mode 0755) that does the same thing as the Makefile recipe but with
   richer output for ad-hoc debugging. Recipe shape:
   ```bash
   #!/usr/bin/env bash
   # contrib/nix-smoke.sh — verify the nix flake package builds and runs.
   set -euo pipefail

   if ! command -v nix >/dev/null 2>&1; then
       echo "nix not on PATH; skipping" >&2
       exit 0
   fi

   echo "==> nix flake check"
   nix flake check --no-build

   echo "==> nix build .#default"
   result=$(nix build --print-out-paths --no-link .#default)
   echo "    $result"

   echo "==> nix build .#clast (alias)"
   nix build --print-out-paths --no-link .#clast >/dev/null

   echo "==> $result/bin/clast --version (with CLAST_LIB unset, minimal PATH)"
   env -u CLAST_LIB PATH=/usr/bin:/bin "$result/bin/clast" --version

   echo "==> layout assertions"
   for path in \
       bin/clast \
       lib/clast/clast-lib.bash \
       lib/clast/clast-subcommands/whereami.bash \
       share/clast/.claude-plugin/plugin.json \
       share/clast/hooks/hooks.json \
       share/clast/hooks/snapshot.sh \
       share/clast/README.md \
       share/clast/LICENSE ; do
       test -e "$result/$path" || { echo "MISSING: $path" >&2 ; exit 1 ; }
   done
   test -x "$result/share/clast/hooks/snapshot.sh" \
       || { echo "snapshot.sh not executable" >&2 ; exit 1 ; }

   echo "OK"
   ```
   `make nix-smoke` may shell out to `contrib/nix-smoke.sh` instead of
   inlining the recipe — pick whichever keeps the Makefile readable. The
   `make lint` target should now include `contrib/nix-smoke.sh` in its
   shellcheck file list.

7. **Update `make lint`** so `shellcheck` covers `contrib/nix-smoke.sh`
   alongside the existing `install.sh` / `uninstall.sh` / `bin/clast` /
   `lib/clast/**/*.bash` / `hooks/snapshot.sh` / `test/*.sh` set. The
   simplest edit: extend the existing dynamic file-list expression in the
   Makefile's `lint:` target to also include `contrib/nix-smoke.sh` when
   present.

8. **Do NOT touch `package.json`** in this step. The `prepublishOnly`
   script, `files` field tweaks, and any Nix-related field on the npm
   package are step 16's territory. The `package.json` and `flake.nix`
   version fields will be unified in step 16's "bump in lockstep" follow-up.

9. **Do NOT touch `install.sh` / `uninstall.sh`** in this step. The
   manual-prefix install path is step 14's territory; the Nix path is
   parallel, not a replacement. If you find a missing piece in install.sh
   that affects the FHS-parity claim (e.g. `LICENSE` is not copied), stop
   and ask — do not patch it as part of step 15.

10. **Update `README.md`.** Add a new "Install with Nix" section
    immediately after the existing "Install to a prefix" section (step 14
    added that one). 4–8 lines. Three modes to show, one line each plus a
    one-line preamble:
    - `nix run github:procrastivity/clast -- whereami` — ephemeral run
      from the public flake (works for any user with Nix flakes enabled).
    - `nix profile install github:procrastivity/clast` — install to the
      user's Nix profile.
    - `nix build .#default && ./result/bin/clast --version` — local-checkout
      verification (the same command `make nix-smoke` runs).
    Mention the overlay one-liner (`overlays.default` exposes `pkgs.clast`)
    for Home Manager / nix-darwin users. Link the canonical doc
    (`docs/repo-bootstrap.md#nix-flake`) for the full overlay wiring.

11. **Confirm `make lint`, `make test`, and `make nix-smoke` pass.**
    `make lint` and `make test` are the same gates as every step. `make
    nix-smoke` is the new gate for this step — it must exit 0 against a
    fresh `nix build`. If `nix` is not available on the executor's
    machine, the smoke target's `command -v nix` guard exits 0 — but the
    step's acceptance is gated on the smoke ACTUALLY running, so the
    executor must have Nix available (the dev shell from step 01 provides
    it via direnv).

## Acceptance criteria

- `flake.nix` exists with the following top-level outputs per
  `flake-utils.lib.eachDefaultSystem`: `packages.default`, `packages.clast`
  (alias), `devShells.default`; plus a top-level (non-system-scoped)
  `overlays.default` block.
- `flake.nix`'s `devShells.default` block is byte-identical to the pre-step
  shape (same `buildInputs` set, same `shellHook`).
- `description` at the top of the flake reads `"clast — Claude Code session
  journal"` (no `(dev shell)` qualifier).
- `nix flake check` exits 0 against the new flake.
- `nix build .#default` exits 0 and produces a `result/bin/clast` that is
  executable.
- `env -u CLAST_LIB PATH=/usr/bin:/bin result/bin/clast --version` exits 0
  and prints a `clast ...` version line — proves the Nix wrapper pins
  `CLAST_LIB` and provisions `jq` / `coreutils` / `git` via PATH.
- `nix build .#clast` exits 0 and produces an equivalent result (alias).
- `result/share/clast/.claude-plugin/plugin.json`, `result/share/clast/hooks/hooks.json`,
  `result/share/clast/hooks/snapshot.sh` (executable),
  `result/share/clast/README.md`, and `result/share/clast/LICENSE` all
  exist with the correct modes after a `nix build`.
- `overlays.default` is defined as a pure overlay
  (`final: prev: { clast = self.packages.${prev.system}.default; }`) at the
  top level of the flake outputs (outside the per-system block).
- `Makefile` exposes a `make nix-smoke` target that no-ops cleanly when
  `nix` is not on PATH and exits 0 after running the full smoke when it is.
- `contrib/nix-smoke.sh` exists, is executable (mode 0755 in git), and
  passes `shellcheck`.
- `make lint` covers `contrib/nix-smoke.sh` and exits 0.
- `make test` exits 0 (no new test scripts in this step — `nix-smoke` is
  intentionally separate).
- `make nix-smoke` exits 0 against a Nix-equipped environment.
- `README.md` has a new "Install with Nix" section showing the three
  invocations (`nix run`, `nix profile install`, `nix build`) and the
  overlay one-liner, linking the canonical doc.

## Out of scope

- **CI workflows.** `.github/workflows/nix.yml` (and integration with the
  existing `.github/workflows/test.yml`) is step 17. Do not add a Nix
  workflow file in this step.
- **npm packaging finalization.** `prepublishOnly`, `files` field
  rewrites, dry-run packs, and the version-sync between `package.json`
  and `flake.nix` are step 16. Do not touch `package.json`.
- **Cachix / binary cache setup.** Useful in CI to make `nix-smoke` fast;
  belongs in step 17 alongside the Nix workflow.
- **Home Manager module / nix-darwin module.** The overlay is the integration
  point users need; module wrappers are downstream packaging and not in v1.
- **Cross-compilation assertions.** `flake-utils.lib.eachDefaultSystem`
  already iterates over the standard four-tuple (`x86_64-linux`,
  `aarch64-linux`, `x86_64-darwin`, `aarch64-darwin`). Asserting that
  each one builds is CI work, not this step.
- **Pulling `version` from `package.json` at flake-eval time.** Deliberately
  deferred to step 16's lockstep-bump work. For now, both files carry
  `0.1.0` independently with a comment cross-reference.
- **A `nix flake check` with `--all-systems`.** The default system-scoped
  check is sufficient for v1; broader cross-system evaluation is CI's job.
- **Replacing `install.sh` / `uninstall.sh` with a Nix-only path.** The
  three channels (manual prefix, Nix, npm) are intentionally parallel.
  Killing one to simplify is a v1.1 ergonomic, not a v1 step.
- **Adding a `nix-smoke` step to `make test`.** Hard requirement of step
  4 — keep `make test` pure-bash so contributors without Nix can still
  run the suite.
- **Filling in `examples/cron/`, `examples/workflows/`, `examples/config/`.**
  Those directories are still `.gitkeep`-only; step 18 fills them in.
  The Nix package copies whatever is present.
- **Embedding `jq` / `coreutils` / `git` in `$out/bin/`.** The
  `--prefix PATH` form is the right shape: the binary inherits the user's
  PATH and prepends Nix-store paths so the runtime deps resolve even
  when the user's PATH is hostile. Vendoring full binaries under
  `$out/bin/` would multiply package size for no gain.
- **A `passthru.tests` block on the package.** Tempting (`nix flake check`
  could run the suite), but the test suite already lives in bash and
  `make test` is the canonical entry point. Wiring it into Nix-eval-time
  testing is a CI-side ergonomic, not a package-shape one.

## Verification

```bash
# Lint (covers contrib/nix-smoke.sh now)
make lint

# Tests (no new suite this step; the existing pre-step-15 set must still pass)
make test

# Nix smoke — the load-bearing verification for this step
make nix-smoke

# Manual reproductions of the smoke (run inside the repo root)
nix flake check
nix build .#default
ls -la result/                                  # symlink to /nix/store/...-clast-0.1.0
ls result/bin result/lib/clast result/share/clast
env -u CLAST_LIB PATH=/usr/bin:/bin result/bin/clast --version
env -u CLAST_LIB PATH=/usr/bin:/bin result/bin/clast whereami --help >/dev/null

# Plugin assets present under share/clast
test -f result/share/clast/.claude-plugin/plugin.json && echo "ok: plugin manifest"
test -x result/share/clast/hooks/snapshot.sh         && echo "ok: hook executable"
test -f result/share/clast/README.md                 && echo "ok: README"
test -f result/share/clast/LICENSE                   && echo "ok: LICENSE"

# Overlay shape (eval-only — confirms self.packages.<sys>.default is exposed)
nix eval --raw .#clast.pname

# Cleanup
rm -f result
```

## Notes for the implementer

- **The Nix wrapper is the second line of defense for `CLAST_LIB`.** The
  realpath trick in `bin/clast` resolves libs without any env var being
  set. `wrapProgram --set CLAST_LIB` pins it explicitly. Both should work
  independently — the smoke deliberately unsets `CLAST_LIB` and a minimal
  PATH to exercise the wrapper, but a future test that ALSO sets
  `CLAST_LIB` to a wrong value should still fail loudly (the wrapper's
  `--set` is per-process; a hostile `CLAST_LIB=` exported before `nix run`
  would override the wrapper's `--set`). That second test belongs to
  step 17's CI, not here.
- **`packages.clast` vs `packages.default`.** Both exist deliberately:
  `nix run github:procrastivity/clast` resolves `packages.default`;
  `nix run github:procrastivity/clast#clast` resolves the named alias.
  Some users prefer the explicit form for readability in module configs;
  some tooling (Home Manager flake refs) consumes the named alias. Keep
  both.
- **Overlay is at the flake-output top level**, NOT inside
  `eachDefaultSystem`. The system-scoped block returns
  `packages`/`devShells`; the overlay is system-aware via `prev.system`
  but is itself a single, non-system-scoped attribute. The `// { overlays
  = ... }` merge at the end of the `outputs` function is the load-bearing
  shape — get it wrong and `nix flake check` will reject it.
- **`cp -R DIR/. DST/` over `cp -r DIR DST`** — same portability lesson
  as step 14. macOS BSD `cp` and Linux GNU `cp` disagree on the trailing
  slash; the `/.` form is unambiguous on both.
- **`install.sh` parity matters because of muscle memory.** Users who
  switch between `nix profile install` and `./install.sh ~/.local` should
  see the same `share/clast/` layout. If step 14's install.sh ships
  `README.md` and `LICENSE` under `share/clast/` and the Nix package
  doesn't, a user inspecting `$prefix/share/clast/` between installs will
  notice the asymmetry and assume one is broken. Adding the two
  `install -m644` lines in the Nix `installPhase` keeps parity for ~2
  lines of code.
- **`nix flake check` does not run the bash test suite.** That is fine
  for v1 — `make test` is the canonical entry point; the Nix smoke
  verifies the *packaged* artifact, not the source-tree's correctness.
  Wiring `passthru.tests` into the package would let `nix flake check`
  also run the suite, but that doubles eval time and is best left to
  step 17's CI ergonomics.
- **Why `contrib/nix-smoke.sh` exists alongside the Makefile target.**
  Two reasons: (1) the Makefile recipe gets unreadable past ~10 lines;
  (2) `contrib/nix-smoke.sh` is a debuggable artifact a user can `bash
  -x` when something breaks, whereas a `make` recipe's `@`-prefixed
  silence is hostile to debugging. The Makefile recipe can either
  inline the same checks or `exec contrib/nix-smoke.sh` — pick by
  readability.
- **Conventional commit suggestion**: `feat(nix): add packages.default and
  overlays.default to flake`. One commit is fine; if `flake.lock` is
  regenerated, include it in the same commit (do not split into a
  separate `chore(nix): bump flake.lock` — they belong together).
