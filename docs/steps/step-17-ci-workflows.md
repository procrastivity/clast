---
step: 17
title: ci-workflows
depends_on: [01, 14, 15, 16]
size: medium
references:
  - docs/repo-bootstrap.md#ci
  - docs/repo-bootstrap.md#githubworkflowstestyml
  - docs/repo-bootstrap.md#githubworkflowsnixyml
  - docs/repo-bootstrap.md#githubworkflowsreleaseyml
  - docs/overview.md#distribution
---

# Step 17: CI workflows

## Context

`.github/workflows/` was scaffolded in step 01 with three minimal files
that compile but don't yet exercise the full test and release surface:

- `test.yml` — runs `make lint` and (conditionally) `make test` inside
  `nix develop` on `push` and `pull_request` against `main`. Does NOT
  yet run `make check-version-sync` (added in step 16) or
  `make npm-pack-check` (also step 16). Does NOT yet exercise
  `make nix-smoke` (added in step 15).
- `nix.yml` — runs `nix flake check` on `push` and `pull_request`. Does
  NOT yet run `nix build` (the spec calls for both) or a smoke `nix run`.
- `release.yml` — placeholder echo, triggered on `v*` tags, wiring
  deferred to this step.

Step 17 is the "make CI actually green and shippable" pass. By the end:

- A PR cannot land with a broken lint, broken test, mismatched version
  literal, broken npm pack shape, broken Nix flake, or broken
  `nix-smoke` invocation.
- A `v*` tag push runs a real release pipeline: regate every test, build
  the Nix package, build the npm tarball, publish to npm with
  provenance, and create a GitHub Release with the tarball attached.
- Concurrency is configured so a second push to the same branch cancels
  the first in-progress run (prevents wasted minutes during rebase
  storms).

What is already on `main` that this step ties together:

- `make lint` — shellchecks `bin/clast`, `lib/clast/**`, `test/*.sh`,
  `hooks/snapshot.sh`, `install.sh`, `uninstall.sh`,
  `contrib/nix-smoke.sh`, `contrib/npm-pack-check.sh`,
  `contrib/check-version-sync.sh`. Single source of truth.
- `make test` — `./test/test-clast.sh` (pure bash harness, no Node, no
  npm). Returns 0 on green, non-zero on failure.
- `make check-version-sync` — asserts `package.json` and `flake.nix`
  carry the same version literal.
- `make npm-pack-check` — runs `npm pack --dry-run --json` and asserts
  the documented file set is present and forbidden paths are absent.
  No-op when `npm` is not on `PATH`.
- `make nix-smoke` — `contrib/nix-smoke.sh`, builds the flake and
  invokes `clast --version` against the build result.
- `package.json` — `prepublishOnly` chains lint, test, version-sync,
  and pack-check. Means `npm publish` cannot run with any gate broken.
- `flake.nix` — `packages.default` and `overlays.default` ship a
  wrapped `bin/clast` with all runtime deps on PATH.

The dev shell intentionally does NOT include `nodejs`/`npm` — step 16
decided that `command -v npm` guards make the local pack-check skip
cleanly when Node isn't installed. CI must therefore install Node
explicitly (via `actions/setup-node`) so the npm gates actually run
there, not silently skip.

**Out of scope by design (see "Out of scope" below for the full list):**
real `npm publish` execution against the live registry, NPM_TOKEN /
PROVENANCE secret setup in the GitHub repo settings (the workflow
references them; the human wires them at release time), the bash
version matrix called out in `repo-bootstrap.md#githubworkflowstestyml`
(deferred — v1 ships with a single bash version from the Nix dev
shell), and the Claude Code marketplace publishing flow.

## Goal

Replace the three placeholder workflows under `.github/workflows/` with
production-shaped equivalents: a `test.yml` that runs every hard gate
(`lint`, `test`, `check-version-sync`, `npm-pack-check`, `nix-smoke`) on
push and PR, a `nix.yml` that runs `nix flake check` + `nix build` +
smoke `nix run`, and a `release.yml` that on a `v*` tag re-runs every
gate, builds the Nix and npm artifacts, publishes the npm tarball with
provenance, and creates a GitHub Release with the tarball attached.
Add a top-level `concurrency:` block to `test.yml` and `nix.yml` so
duplicate runs on the same branch get cancelled. Verify locally with
`act` if available, otherwise verify by manual `nix develop -c make
…` reproduction of every step the workflows execute. No real publish
happens; the version on disk stays `0.1.0`.

## References

Read before starting:

- `docs/repo-bootstrap.md#ci` — **the canonical spec for the three
  workflow files.** Notes from this file that this step honors:
  - `test.yml` should cover shellcheck + tests.
  - `nix.yml` should run `nix flake check` AND `nix build`.
  - `release.yml` triggers on tag, builds, publishes to npm, builds
    nix flake, attaches tarball to GH release.
  - The bash version matrix is mentioned but deferred per the
    `## Out of scope` section below.
- `docs/repo-bootstrap.md#packagejson` — `prepublishOnly` already
  guards `npm publish`. The release workflow re-runs the chain
  explicitly (defense in depth) so the publish step doesn't rely on
  `prepublishOnly` silently invoking it.
- `docs/repo-bootstrap.md#nix-flake` — the package + overlay layout
  built by step 15. `release.yml` builds the same `packages.default`
  to verify the Nix path before tagging.
- `docs/overview.md#distribution` — the three distribution channels
  (manual prefix, Nix, npm). Skim only.
- `.github/workflows/test.yml`, `.github/workflows/nix.yml`,
  `.github/workflows/release.yml` — the existing placeholders this
  step rewrites. Read each in full before editing.
- `package.json#scripts.prepublishOnly` — the release workflow's gate
  chain mirrors this; keep them aligned.

## Tasks

1. **Rewrite `.github/workflows/test.yml`** to cover every hard gate.
   Target shape:

   ```yaml
   name: test

   on:
     push:
       branches: [main]
     pull_request:
       branches: [main]

   concurrency:
     group: test-${{ github.ref }}
     cancel-in-progress: true

   jobs:
     gates:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: cachix/install-nix-action@v25
           with:
             nix_path: nixpkgs=channel:nixos-unstable
         - uses: actions/setup-node@v4
           with:
             node-version: '20'
         - name: Lint
           run: nix develop -c make lint
         - name: Tests
           run: nix develop -c make test
         - name: Version sync
           run: nix develop -c make check-version-sync
         - name: npm pack shape
           run: nix develop -c make npm-pack-check
         - name: Nix smoke
           run: nix develop -c make nix-smoke
   ```

   Notes:
   - `actions/setup-node@v4` puts `npm` on `PATH` so
     `make npm-pack-check` actually exercises the pack-shape
     assertion. Without it the script would no-op-skip and the gate
     would be a false-positive green.
   - `nix develop -c <cmd>` keeps every gate running inside the same
     dev shell the developer uses locally (one source of truth).
   - The "no tests yet — skipping" fallback in the existing
     `test.yml` is no longer needed; `test/test-clast.sh` has existed
     since step 02 and is now load-bearing.
   - Do NOT add a bash version matrix here — see `## Out of scope`.

2. **Rewrite `.github/workflows/nix.yml`** to exercise both flake
   check AND build AND a smoke run of the built artifact. Target shape:

   ```yaml
   name: nix-check

   on:
     push:
       branches: [main]
     pull_request:
       branches: [main]

   concurrency:
     group: nix-${{ github.ref }}
     cancel-in-progress: true

   jobs:
     check:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: cachix/install-nix-action@v25
           with:
             nix_path: nixpkgs=channel:nixos-unstable
         - name: Flake check
           run: nix flake check
         - name: Build package
           run: nix build .#default --print-build-logs
         - name: Smoke run
           run: ./result/bin/clast --version
   ```

   Notes:
   - `nix flake check` validates the flake schema; `nix build`
     actually constructs the package; `./result/bin/clast --version`
     proves the wrapped binary launches and resolves its libs.
     Three independent failure modes, three independent assertions.
   - Trigger narrowed from `[push, pull_request]` (the placeholder's
     too-broad shape) to `branches: [main]` for both events, matching
     `test.yml`. Branches without an open PR don't burn CI minutes.

3. **Rewrite `.github/workflows/release.yml`** as a real tag-triggered
   release pipeline. Target shape:

   ```yaml
   name: release

   on:
     push:
       tags:
         - 'v*'

   permissions:
     contents: write       # create the GitHub Release
     id-token: write       # npm publish --provenance via OIDC

   jobs:
     release:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: cachix/install-nix-action@v25
           with:
             nix_path: nixpkgs=channel:nixos-unstable
         - uses: actions/setup-node@v4
           with:
             node-version: '20'
             registry-url: 'https://registry.npmjs.org'
         - name: Re-run every gate
           run: |
             nix develop -c make lint
             nix develop -c make test
             nix develop -c make check-version-sync
             nix develop -c make npm-pack-check
             nix develop -c make nix-smoke
         - name: Verify tag matches package version
           run: |
             tag="${GITHUB_REF_NAME#v}"
             pkg=$(jq -r '.version' package.json)
             if [ "$tag" != "$pkg" ]; then
               echo "tag $tag does not match package.json version $pkg" >&2
               exit 1
             fi
         - name: Build Nix package
           run: nix build .#default --print-build-logs
         - name: Pack npm tarball
           id: pack
           run: |
             tarball=$(npm pack --silent)
             echo "tarball=$tarball" >> "$GITHUB_OUTPUT"
         - name: Publish to npm
           env:
             NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
           run: npm publish --provenance --access public "${{ steps.pack.outputs.tarball }}"
         - name: Create GitHub Release
           env:
             GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
           run: |
             gh release create "$GITHUB_REF_NAME" \
               --title "$GITHUB_REF_NAME" \
               --generate-notes \
               "${{ steps.pack.outputs.tarball }}"
   ```

   Notes:
   - `permissions:` is explicit — `contents: write` for `gh release
     create`, `id-token: write` for OIDC-backed npm provenance. No
     other scopes.
   - The gate chain is RE-RUN inside the release job (not delegated
     to a `needs:` reference to `test.yml`'s run). Reason: the
     `test.yml` run on the merge commit predates the tag and might
     not exist if someone tags an old commit; running the gates
     inline guarantees they ran against the exact tag SHA.
   - Tag-vs-package-version check is the final invariant: if a human
     types `git tag v0.1.0` while `package.json` still says
     `0.0.9-pre`, the release fails fast before publish.
   - `npm pack --silent` produces a deterministic tarball name
     (`procrastivity-clast-<version>.tgz`); piping the output into
     `$GITHUB_OUTPUT` makes it available to the publish + release
     steps without re-computing.
   - `npm publish --provenance --access public <tarball>` is the
     exact published artifact (no `prepublishOnly` re-run, because
     the gates already ran). `--access public` is required for
     scoped packages.
   - `gh release create --generate-notes` lets GitHub auto-derive
     release notes from commits since the previous tag. If `cliff`
     was used to curate `CHANGELOG.md`, a future step can switch
     to `--notes-file CHANGELOG.md`; for v1 the auto-generated notes
     are sufficient.

4. **Document the required secrets** in a short comment block at the
   top of `release.yml`, just under the `name:` line. Two lines:

   ```yaml
   # Required repo secret: NPM_TOKEN (npmjs.com automation token,
   # type: Automation, scope: publish for @procrastivity/clast).
   # GITHUB_TOKEN is provided automatically by Actions.
   ```

   This is the **only** out-of-band setup a human must do before the
   first tag push. Document it in the workflow itself so future-Beau
   doesn't have to grep this step file.

5. **Do NOT remove or rename `.github/workflows/.gitkeep`.** It is
   harmless and keeping it preserves git's record of the directory in
   the (theoretical) future where all three workflows are temporarily
   deleted. No-op task; mentioning it so the executor doesn't tidy it
   away.

6. **Do NOT add a `marketplace.yml` workflow.** Claude Code plugin
   marketplace publishing is a separate distribution channel — `npm`
   already ships `.claude-plugin/` and `hooks/`, which is enough for
   v1. Marketplace metadata flow is step 18 or later.

7. **Update `README.md`** with a brief "CI / Release" subsection (3–6
   lines) under the existing install / development docs. Mention:
   - PRs run lint, test, version-sync, npm-pack, nix-smoke, flake-check,
     and nix-build automatically.
   - Releases trigger on `v*` tags; tag must match `package.json`
     version exactly; publishes to npm with provenance and creates a
     GitHub Release with the source tarball.
   - `NPM_TOKEN` must be configured in repo secrets before the first
     release.
   Place under a new `## CI / Release` heading near the end of the
   README (after the existing install sections, before any
   "Contributing" section if one exists).

8. **Lint the new workflow YAML.** Run `yamllint` if it's available
   in the dev shell; if not, parse each file with `python3 -c
   'import yaml,sys; yaml.safe_load(open(sys.argv[1]))' <file>` as a
   minimal sanity check that the YAML is valid. Both fallbacks are
   acceptable; surfaces a syntax error before the workflow file ever
   reaches `push`. Do NOT add `yamllint` to the dev shell's
   buildInputs in this step; if it's missing, use the python
   fallback.

9. **Optionally validate with `act`** (https://github.com/nektos/act)
   if installed. `act` runs GitHub Actions locally via Docker. The
   `test` workflow can be exercised with `act push -W
   .github/workflows/test.yml`. This is OPTIONAL — `act` is not in
   the dev shell, not a hard dep, and reproducing the Nix install
   action under Docker is slow. If `act` is not on PATH, skip.

10. **Confirm `make lint`, `make test`, `make check-version-sync`,
    `make npm-pack-check`, and (if `nix` is available) `make nix-smoke`
    all still exit 0** before committing. The workflow files
    themselves don't get exercised in this step — that happens when
    the PR pushes and Actions runs them.

## Acceptance criteria

- `.github/workflows/test.yml` runs (in `nix develop -c`): `make
  lint`, `make test`, `make check-version-sync`, `make npm-pack-check`,
  and `make nix-smoke`, in that order, with `actions/setup-node@v4`
  installing Node so `npm` is actually on PATH for the pack check.
- `.github/workflows/test.yml` has a `concurrency:` block keyed on
  `${{ github.ref }}` with `cancel-in-progress: true`.
- `.github/workflows/nix.yml` runs `nix flake check`, `nix build
  .#default --print-build-logs`, and `./result/bin/clast --version` in
  that order.
- `.github/workflows/nix.yml` has a `concurrency:` block keyed on
  `${{ github.ref }}` with `cancel-in-progress: true`.
- `.github/workflows/nix.yml`'s `on:` trigger is restricted to
  `branches: [main]` for both `push` and `pull_request` (matches
  `test.yml`).
- `.github/workflows/release.yml` triggers on `tags: ['v*']`,
  declares `permissions: { contents: write, id-token: write }`,
  re-runs every gate, verifies the tag matches `package.json`'s
  `version`, builds the Nix package, runs `npm pack` to produce a
  deterministic tarball, publishes to npm with `--provenance
  --access public` using `NPM_TOKEN`, and creates a GitHub Release
  with the tarball attached.
- `.github/workflows/release.yml` carries a top-of-file comment block
  documenting the `NPM_TOKEN` requirement.
- Every workflow YAML parses cleanly (yamllint OR python
  `yaml.safe_load` returns 0).
- `make lint`, `make test`, `make check-version-sync`, and
  `make npm-pack-check` all exit 0 after the edits. `make nix-smoke`
  also exits 0 if `nix` is available.
- `README.md` has a new "CI / Release" section describing the
  workflow trio and the `NPM_TOKEN` requirement.
- The `.github/workflows/.gitkeep` placeholder is unchanged.
- No new files appear in `.github/workflows/` beyond `test.yml`,
  `nix.yml`, `release.yml`, `.gitkeep`.

## Out of scope

- **Actually publishing to npm.** This step prepares the release
  workflow; an actual `v0.1.0` tag push is step 19 (release).
- **Configuring the `NPM_TOKEN` secret.** That happens in the GitHub
  repo settings UI by a human; the workflow references it but doesn't
  create it.
- **Bash version matrix** (5.0, 5.1, 5.2) from
  `repo-bootstrap.md#githubworkflowstestyml`. The Nix dev shell
  pins a single bash version. Adding a matrix requires either a
  Docker-based reproduction or installing system bash from source
  per-matrix-entry — both worth doing eventually, but a separate
  step (not v1-blocking).
- **`cliff` / `git-cliff` integration in the release workflow.** The
  workflow uses `gh release create --generate-notes` for v1.
  Switching to `--notes-file CHANGELOG.md` with a cliff-curated
  changelog is a clean follow-up but adds dependencies and tuning
  cycles. Defer.
- **Claude Code plugin marketplace publishing.** A separate
  distribution channel; npm + Nix + manual install cover v1.
- **`semantic-release` / `release-please` integration.** v1's
  release flow is human-triggered tag pushes; bot-driven version
  bumps are not in scope.
- **macOS / Windows CI matrices.** Ubuntu-only for v1. The clast
  install paths *should* work on macOS, but verifying that in CI
  requires a `macos-latest` runner (slow, occasional flake on
  `cachix/install-nix-action`). Defer to a follow-up step.
- **Caching `cachix/install-nix-action`** or the Nix store. The
  first PR run takes ~3 min for Nix install; subsequent runs ride
  the action's cache. If runtimes balloon later, add a Cachix
  binary cache; not needed at v1's commit volume.
- **Renovate / Dependabot configuration** for action versions. The
  three workflows pin actions to `@v4` / `@v25` — drift is slow.
  Adding automation is a separate concern.
- **Touching the existing `make` targets.** All gates already exist
  with the right contract; this step wires them into CI, doesn't
  redefine them.
- **A separate `release-dry-run.yml`** that runs the release flow
  without publishing. Tempting (catches release-flow bugs without
  burning a tag), but doubles surface area for v1. The
  `prepublishOnly` chain + the test workflow already cover most
  failure modes locally.
- **Signing the GitHub Release.** Sigstore / cosign on the tarball
  is a v1.x security hardening, not v1.0.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Version sync (must pass before any tag push)
make check-version-sync

# npm pack shape
make npm-pack-check

# Nix smoke (skipped if nix unavailable)
make nix-smoke

# YAML parse sanity for each workflow
for wf in .github/workflows/test.yml \
          .github/workflows/nix.yml \
          .github/workflows/release.yml ; do
  python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1])); print('ok:', sys.argv[1])" "$wf"
done

# Optional: run the test workflow under act if installed.
if command -v act >/dev/null 2>&1 ; then
  act push -W .github/workflows/test.yml --container-architecture linux/amd64
fi

# Confirm no unexpected workflow files
ls .github/workflows/
# expect: .gitkeep  nix.yml  release.yml  test.yml
```

## Notes for the implementer

- **The release workflow does not need to invoke `npm run
  prepublishOnly`.** The release job runs the gate chain explicitly
  (with the exact tool versions pinned by `setup-node` and
  `install-nix-action`); invoking `prepublishOnly` from `npm
  publish` would re-run the same gates redundantly. The explicit
  chain is what's audited; `prepublishOnly` exists for local
  `npm publish` invocations a human might run from a laptop.
- **Why `npm pack` before `npm publish`.** `npm publish` against a
  tarball file (rather than the current directory) is deterministic
  — the file you tested in `make npm-pack-check` is the file that
  ships. Publishing from the directory rebuilds the tarball with
  whatever's currently on disk, which is one more failure mode than
  necessary.
- **`registry-url` on `setup-node`.** It sets `~/.npmrc`'s registry
  to npmjs.org and configures `npm publish` to pick up
  `NODE_AUTH_TOKEN` from the environment. Without `registry-url`,
  `npm publish` looks for credentials in the wrong file.
- **`permissions: id-token: write`.** Required for OIDC-backed
  provenance attestation. Without it, `npm publish --provenance`
  fails with `Error: Provenance generation in GitHub Actions requires
  "write" access to the "id-token" permission`.
- **Concurrency keying.** Both `test.yml` and `nix.yml` use the same
  `${{ github.ref }}` key shape but with distinct prefixes
  (`test-` and `nix-`) so a push doesn't cancel a queued nix run.
  Each workflow's own duplicate runs cancel; the workflows don't
  cancel each other.
- **Tag-version mismatch failure mode.** If `git tag v0.1.0` runs
  while `package.json` still reads `0.0.9`, the verify step exits
  before npm publish. Recovery: `git tag -d v0.1.0`, fix
  `package.json`, push, re-tag.
- **`nix flake check` vs `nix build`.** `flake check` validates the
  flake schema and runs the checks the flake declares (currently
  none). `nix build` constructs the actual derivation. Both are
  cheap; running both catches different failure modes (e.g. a
  syntactically valid flake whose derivation fails to build).
- **Why `nix run` is not used in `nix.yml`.** `./result/bin/clast
  --version` against the build output exercises the wrapper exactly
  as installed; `nix run .#default -- --version` re-builds and is
  slower. The verification is identical.
- **`branches: [main]` filter on `nix.yml`.** Narrowed from the
  placeholder's `[push, pull_request]` (which fires on every branch
  push). The original was a step-01 placeholder; the production
  shape mirrors `test.yml`. If a future workflow needs feature-branch
  triggers (e.g. for a deploy preview), it adds its own trigger
  block — `test.yml` and `nix.yml` are PR gates.
- **Conventional commit suggestion**: `ci: flesh out test, nix, and
  release workflows`. Single commit for the three workflow rewrites
  + the README edit is fine.
