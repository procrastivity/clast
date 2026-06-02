---
step: 16
title: npm-publish-prep
depends_on: [01, 11, 14, 15]
size: small
references:
  - docs/repo-bootstrap.md#packagejson
  - docs/repo-bootstrap.md#nix-flake
  - docs/repo-bootstrap.md#binclast
  - docs/repo-bootstrap.md#installsh--uninstallsh
  - docs/overview.md#distribution
---

# Step 16: npm publish prep

## Context

`package.json` has existed since step 01 as a scaffold; step 14 ratified its
`files` array against the real install layout; step 15 added the parallel
Nix package + overlay. Step 16 is the small finalization pass that gets
`@procrastivity/clast` ready for `npm publish` without actually publishing:
add a `prepublishOnly` guard so `npm publish` cannot run with broken tests
or lint, prove that `npm pack --dry-run` produces a tarball with exactly the
expected file set, and unify the version literal between `package.json` and
`flake.nix` (step 15 explicitly deferred the lockstep-bump ergonomics to
this step).

What is already on `main` and relevant to npm shipping:

- `package.json` — `"@procrastivity/clast"`, `version: "0.1.0"`, `bin.clast =
  "bin/clast"`, `files: ["bin/", "lib/", ".claude-plugin/", "hooks/",
  "examples/", "README.md", "LICENSE"]`, `scripts.test`, `scripts.lint`,
  `engines.node: ">=18"`. No `prepublishOnly` yet; no `homepage` field.
- `bin/clast` — already executable, already named in `bin.clast`. `npm
  install -g` will symlink it to `$(npm prefix -g)/bin/clast`. The
  realpath-based `CLAST_LIB` resolution works because `npm` preserves the
  package's `lib/clast/` next to the binary's installed location.
- `flake.nix` — `version = "0.1.0"` with the comment
  `# Bump in lockstep with package.json.` from step 15. This step turns
  the comment into a real constraint: one CI-checkable invariant that the
  two files agree.
- `Makefile` — `make lint` / `make test` / `make nix-smoke` are the three
  hard gates. `prepublishOnly` chains the first two (the third is
  Nix-specific and not relevant to the npm path).
- `contrib/nix-smoke.sh` — verification script pattern from step 15.
  This step adds a sibling `contrib/npm-pack-check.sh` with the same shape.

This step is **strictly the package.json finalization + dry-run verification
substrate**. CI integration (`.github/workflows/release.yml`, the
`npm publish --provenance` flow, the `release-please` integration if any) is
all step 17. The marketplace install flow for the Claude Code plugin is a
parallel concern; the npm package shipping the `.claude-plugin/` directory
is enough for v1.

**Run `direnv allow` (or `nix develop`) before starting** so `npm`
(provided by the dev shell — confirm with `command -v npm` before assuming)
is on PATH for `npm pack --dry-run`. If `npm` is NOT on the dev shell, stop
and ask — see the "Notes for the implementer" section below.

## Goal

Land the finalization edits to `package.json` (add `prepublishOnly`,
`homepage`, normalize the `lint` script, optionally tighten the `files`
list), add a `contrib/npm-pack-check.sh` script that runs `npm pack
--dry-run --json` and asserts the contents match the documented `files`
set, wire a `make npm-pack-check` target, and extend `README.md` with an
"Install via npm" section. No real `npm publish` happens; the version on
disk stays `0.1.0`. The acceptance criteria gate on the dry-run pack
producing the right tarball shape and on a new
`scripts/check-version-sync.sh` (or inline check) asserting
`package.json` and `flake.nix` carry the same version literal.

## References

Read before starting:

- `docs/repo-bootstrap.md#packagejson` — **canonical content for
  package.json**. Defines the npm package shape: `bin`, `files`, `scripts`,
  `engines`. Use it as the reference for what fields should exist after
  this step.
- `docs/repo-bootstrap.md#nix-flake` — confirms the parallel Nix package
  uses the same version literal. The lockstep-bump invariant is the
  contract between this step and step 15.
- `docs/repo-bootstrap.md#binclast` — the realpath-based `CLAST_LIB`
  resolution works for the npm `bin` symlink layout (the installed
  `clast` binary's realpath is inside `node_modules/@procrastivity/clast/bin/`,
  so `../lib/clast` resolves to the package's `lib/clast/`). Treat that as
  the load-bearing claim the npm-pack-check script verifies.
- `docs/repo-bootstrap.md#installsh--uninstallsh` — the manual-prefix
  layout. npm's tarball contents intentionally match the install set
  (same `files` array) so users who try `./install.sh` from an extracted
  tarball get the same result as a `git clone`.
- `docs/overview.md#distribution` — the three-channel distribution model
  (manual prefix, Nix, npm). Skim only.

## Tasks

1. **Add a `prepublishOnly` script to `package.json`.** Recipe:
   `"prepublishOnly": "make lint && make test && ./contrib/npm-pack-check.sh"`.
   This runs the two existing hard gates (lint, test) plus the new
   pack-shape verification before `npm publish` is allowed to proceed.
   `make nix-smoke` is intentionally NOT in the chain — Nix is not an npm
   concern and `prepublishOnly` should not require Nix to publish.

2. **Add a `homepage` field** to `package.json`: `"homepage":
   "https://github.com/procrastivity/clast#readme"`. Matches the npm
   convention; surfaces the README on the package's npmjs.com page.

3. **Tighten the `lint` script** in `package.json` so it matches what
   `make lint` actually runs. The current value (`shellcheck bin/clast
   lib/clast/**/*.bash test/*.sh hooks/snapshot.sh`) drops `install.sh`,
   `uninstall.sh`, and `contrib/nix-smoke.sh` — all of which `make lint`
   covers. Two options:
   - **Preferred**: replace the script with `"lint": "make lint"` so the
     Makefile is the single source of truth for which files get
     shellchecked. Avoids npm-vs-Makefile drift.
   - **Fallback** (if a reviewer prefers the explicit list in
     `package.json`): expand the inline shellcheck command to match the
     Makefile's dynamic file set. Document the duplication risk in a
     trailing JSON comment line — except JSON does not allow comments, so
     this fallback inevitably drifts. Pick the preferred option unless
     the reviewer pushes back.
   Apply the same single-source-of-truth pattern to `scripts.test`:
   change `"test": "test/test-clast.sh"` to `"test": "make test"` so npm
   and Makefile agree.

4. **Do NOT change the `files` array** even though step 18 will fill in
   `examples/`. The current array correctly enumerates the install set
   and matches what `install.sh` / Nix copy. A future tightening (e.g.
   excluding `examples/**/.gitkeep`) belongs to step 18, not here.

5. **Do NOT change the `version` field.** Both `package.json` and
   `flake.nix` carry `0.1.0`. The first real release (step 19) bumps
   both. This step adds the invariant *check*; it does not exercise it.

6. **Create `contrib/npm-pack-check.sh`** (new file, mode 0755). Recipe:
   ```bash
   #!/usr/bin/env bash
   # contrib/npm-pack-check.sh — verify npm pack --dry-run produces the
   # documented file set, without actually publishing.
   set -euo pipefail

   if ! command -v npm >/dev/null 2>&1; then
       echo "npm not on PATH; skipping" >&2
       exit 0
   fi

   echo "==> npm pack --dry-run --json"
   pack_json=$(npm pack --dry-run --json 2>/dev/null)

   # Required top-level paths in the tarball.
   required=(
       "bin/clast"
       "lib/clast/clast-lib.bash"
       "lib/clast/clast-decode-lib.bash"
       "lib/clast/clast-manifest-lib.bash"
       "lib/clast/clast-registry-lib.bash"
       "lib/clast/clast-subcommands/whereami.bash"
       "lib/clast/clast-subcommands/snapshot.bash"
       "lib/clast/clast-subcommands/breadcrumb.bash"
       "lib/clast/clast-subcommands/stats.bash"
       "lib/clast/clast-subcommands/doctor.bash"
       ".claude-plugin/plugin.json"
       ".claude-plugin/skills/wakeup/SKILL.md"
       ".claude-plugin/skills/day-wakeup/SKILL.md"
       "hooks/hooks.json"
       "hooks/snapshot.sh"
       "README.md"
       "LICENSE"
       "package.json"
   )
   for path in "${required[@]}"; do
       if ! jq -e --arg p "$path" '.[0].files[] | select(.path == $p)' \
               <<<"$pack_json" >/dev/null ; then
           echo "MISSING from tarball: $path" >&2
           exit 1
       fi
   done

   # Forbidden top-level paths — things that should NEVER ship.
   forbidden=(
       "test/"
       "docs/"
       ".github/"
       ".envrc"
       "flake.nix"
       "flake.lock"
       "Makefile"
       "install.sh"
       "uninstall.sh"
       "contrib/"
       ".pre-commit-config.yaml"
       "cliff.toml"
   )
   for prefix in "${forbidden[@]}"; do
       if jq -e --arg p "$prefix" \
               '.[0].files[] | select(.path | startswith($p))' \
               <<<"$pack_json" >/dev/null ; then
           echo "FORBIDDEN in tarball: $prefix*" >&2
           exit 1
       fi
   done

   # Tarball size sanity: warn if > 1 MiB (the source tree is small; a
   # large tarball means something snuck in via the bin/lib trees).
   size=$(jq -r '.[0].size' <<<"$pack_json")
   if [ "$size" -gt 1048576 ]; then
       echo "WARN: tarball size is $size bytes (> 1 MiB)" >&2
   fi

   echo "OK ($(jq -r '.[0].files | length' <<<"$pack_json") files, $size bytes)"
   ```
   Mode `0755` in git. The `command -v npm` guard makes the script a
   no-op when npm is absent (parallels `contrib/nix-smoke.sh`).

7. **Add a `make npm-pack-check` target** to the Makefile. New target,
   added to `.PHONY` alongside the existing ones. Recipe:
   ```makefile
   npm-pack-check:
       @if ! command -v npm >/dev/null 2>&1; then \
           echo "npm-pack-check: skipping (npm not on PATH)" ; \
           exit 0 ; \
       fi
       ./contrib/npm-pack-check.sh
   ```
   Mirrors the `nix-smoke` target shape. Do NOT add `npm-pack-check` to
   the `test:` target's prerequisites — npm may not be on every
   contributor's PATH, and `make test` stays pure-bash.

8. **Update `make lint`** so `shellcheck` covers `contrib/npm-pack-check.sh`
   alongside the existing file list. The simplest edit: extend the
   dynamic file-list expression in the Makefile's `lint:` target to
   include `contrib/npm-pack-check.sh` when present (same pattern step
   15 used for `contrib/nix-smoke.sh`).

9. **Add a version-sync check.** Create
   `contrib/check-version-sync.sh` (mode 0755) that exits 0 if
   `package.json`'s `version` and `flake.nix`'s `version = "..."`
   literal match, exits 1 with a clear diff otherwise. Recipe:
   ```bash
   #!/usr/bin/env bash
   # contrib/check-version-sync.sh — assert package.json and flake.nix
   # carry the same version literal.
   set -euo pipefail

   pkg_version=$(jq -r '.version' package.json)
   flake_version=$(grep -E '^\s*version = "[^"]+";' flake.nix \
       | head -1 \
       | sed -E 's/.*version = "([^"]+)";.*/\1/')

   if [ "$pkg_version" != "$flake_version" ]; then
       echo "version mismatch:" >&2
       echo "  package.json: $pkg_version" >&2
       echo "  flake.nix:    $flake_version" >&2
       exit 1
   fi
   echo "version sync: $pkg_version"
   ```
   Add a `make check-version-sync` target that invokes it (no
   conditional guard — `jq` is always available in the dev shell and the
   check is cheap). Add `contrib/check-version-sync.sh` to `make lint`'s
   shellcheck file list.

10. **Add `make check-version-sync` to the `prepublishOnly` chain.** Update
    `package.json`'s `prepublishOnly` to
    `"make lint && make test && make check-version-sync && ./contrib/npm-pack-check.sh"`.
    A future bumper who edits only one of `package.json` / `flake.nix`
    now cannot accidentally publish a mismatched version.

11. **Add a brief "Install via npm" section to `README.md`.** Place it
    immediately after the "Install with Nix" section from step 15. 3–6
    lines. Show:
    - `npm install -g @procrastivity/clast` — global install (most common).
    - `npx -p @procrastivity/clast clast --version` — one-shot run
      without install.
    Mention that the npm install ships `bin/`, `lib/`, `.claude-plugin/`,
    `hooks/`, `examples/`, `README.md`, `LICENSE` (i.e. the same file set
    `install.sh` and the Nix package ship). Note the plugin can then be
    registered via `claude plugin install $(npm root -g)/@procrastivity/clast`.

12. **Confirm `make lint`, `make test`, and the new
    `make npm-pack-check` + `make check-version-sync` all exit 0.**
    Do NOT actually run `npm publish` (or `npm publish --dry-run` — that
    last form would still attempt to authenticate). `npm pack
    --dry-run` is the safe verification.

## Acceptance criteria

- `package.json` has a `prepublishOnly` script that chains `make lint`,
  `make test`, `make check-version-sync`, and `./contrib/npm-pack-check.sh`.
- `package.json` has a `homepage` field set to
  `"https://github.com/procrastivity/clast#readme"`.
- `package.json`'s `scripts.test` is `"make test"` and `scripts.lint` is
  `"make lint"` (Makefile is the single source of truth).
- `package.json`'s `version` is still `0.1.0`; the `files` array is
  unchanged from the pre-step state.
- `contrib/npm-pack-check.sh` exists, is executable (mode 0755 in git),
  is shellcheck-clean, and exits 0 against the current source tree.
- `contrib/npm-pack-check.sh` asserts every required path is in the
  tarball AND every forbidden path is absent (test/, docs/, .github/,
  flake.nix, flake.lock, Makefile, install.sh, uninstall.sh, contrib/,
  cliff.toml, .pre-commit-config.yaml, .envrc).
- `contrib/check-version-sync.sh` exists, is executable, is
  shellcheck-clean, and exits 0 (versions match at `0.1.0`).
- `Makefile` exposes `make npm-pack-check` (no-op when npm absent) and
  `make check-version-sync`.
- `make lint` covers both new contrib scripts.
- `make lint`, `make test`, `make npm-pack-check`, and
  `make check-version-sync` all exit 0.
- `README.md` has a new "Install via npm" section between the existing
  "Install with Nix" and "Install as a Claude Code plugin" sections.
- `npm pack --dry-run --json` (executed by the new check script)
  produces a tarball containing every required path and none of the
  forbidden ones.

## Out of scope

- **A real `npm publish`.** This step prepares for publishing; the v1.0
  release (step 19) is what actually publishes.
- **CI release workflow.** `.github/workflows/release.yml` and the
  tag-trigger / `npm publish --provenance` flow is step 17. Do not add
  workflow files here.
- **`npm publish --dry-run` (the auth-touching one).** `npm pack
  --dry-run` is the safe verification; `publish --dry-run` requires npm
  auth even though it doesn't push.
- **Version bumping.** Both files stay at `0.1.0`. The bump is part of
  step 19's release flow.
- **`release-please` / `semantic-release` integration.** Out of scope for
  v1; the changelog is hand-curated via `cliff.toml`.
- **`provenance` / OIDC publish.** Belongs to step 17 with the rest of
  CI.
- **Tightening the `files` array beyond what it ships today.** The
  current array matches step 14's install set; future tightening (e.g.
  excluding empty `examples/` placeholders) belongs to step 18.
- **Touching `flake.nix`** to add npm-related metadata. The two
  distributions are parallel; cross-references stay one-way (Nix
  references package.json's version via a comment, not vice versa).
- **Adding a `homepage` to `.claude-plugin/plugin.json`.** Plugin
  metadata is step 11's territory; the npm homepage is separate.
- **Setting up `npm` auth / `~/.npmrc`.** That is the user's call at
  publish time, not part of the package prep.
- **`bin` shebang rewriting on Windows.** npm handles
  `#!/usr/bin/env bash` on Unix; Windows shim generation is the user's
  problem if they install on Windows. Not a v1 concern.

## Verification

```bash
# Lint (covers contrib/npm-pack-check.sh and contrib/check-version-sync.sh)
make lint

# Tests (unchanged — no new test scripts in this step)
make test

# Version sync — load-bearing for the lockstep invariant
make check-version-sync

# npm pack dry-run — the central verification for this step
make npm-pack-check

# Manual reproduction
npm pack --dry-run --json | jq '.[0] | {name, version, files: (.files | length), size}'
npm pack --dry-run --json | jq -r '.[0].files[].path' | sort | head -30
npm pack --dry-run --json | jq -r '.[0].files[].path' | grep -E '^(test/|docs/|flake|Makefile|install\.sh|uninstall\.sh|contrib/)' \
    && echo "FAIL: forbidden path in tarball" \
    || echo "ok: no forbidden paths"

# prepublishOnly chain (runs lint + test + version-sync + pack-check)
npm run prepublishOnly
```

## Notes for the implementer

- **`make` is the single source of truth.** The pre-step `package.json`
  had inline `shellcheck` and `test/test-clast.sh` commands that have
  drifted from `make lint`/`make test` (now covering more files). The
  shortest path to durable agreement is `"lint": "make lint"`,
  `"test": "make test"`. npm users still get `npm run lint` /
  `npm test` working; contributors get one canonical command set.
- **The forbidden-paths check is the regression guard.** `npm` decides
  what ships from `files` + `.npmignore` + the default-include list,
  which has bitten projects before (e.g. `.git/`, `.gitignore`, test
  fixtures all sneaking in). Even with an explicit `files` array, a
  future contributor who adds a top-level `secrets.env` for local use
  would not have it shipped, but a top-level `notes.md` would. The
  forbidden list is the safety net; expand it as needed.
- **Lockstep version bumps.** Two files carry `version = "0.1.0"`. The
  check script makes them check in CI; the bumper still has to remember
  to edit both. A future ergonomic could template `flake.nix`'s version
  from `package.json` at eval time (mentioned but rejected in step 15);
  for v1, the explicit check + comment is sufficient.
- **`npm` availability in the dev shell.** The dev shell (step 01) does
  NOT install `npm` today. If you find `npm` missing, that is a stop-and-
  ask moment — either the dev shell needs an `nodejs` entry added (which
  is a small but real change to step 01's `flake.nix`), or this step's
  smoke targets accept that they only run in environments with a
  separate `nodejs` install. The `command -v npm` guards make the
  scripts no-op gracefully, but the step's acceptance is gated on the
  pack check actually running — so the executor must have npm available
  somehow. The simplest path: install `node` via the dev shell
  (`pkgs.nodejs_22`) as a one-line `buildInputs` addition in `flake.nix`.
  That edit IS in scope for this step *if and only if* the dev shell
  doesn't already provide npm; otherwise leave `flake.nix` alone.
- **`npm pack --dry-run --json` output shape.** The top-level is an
  array (with one entry for the current package). Use `.[0].files[]` to
  enumerate. The `.files[].path` field is the in-tarball path, not the
  on-disk source path — they happen to be the same for a flat-layout
  package like this one.
- **Why no `provenance` step here.** Provenance requires CI-side OIDC
  setup; doing it locally would burn user keys for no value. Step 17
  wires `npm publish --provenance` into the release workflow.
- **`npm install -g @procrastivity/clast`** symlinks `bin/clast` to
  `$(npm prefix -g)/bin/clast`. The realpath-based `CLAST_LIB`
  resolution then walks back through the symlink to the package's
  `node_modules/@procrastivity/clast/lib/clast/`. This is the same
  trick `install.sh` and the Nix wrapper rely on; the npm path
  inherits it for free.
- **Conventional commit suggestion**: `chore(npm): add prepublishOnly,
  pack-check, and version-sync invariants`. One commit fine.
