---
step: 19
title: v1.0-release
depends_on: [01, 02, 03, 04, 05, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17, 18]
size: small
references:
  - docs/releasing.md
  - .github/workflows/release.yml
  - CHANGELOG.md
  - package.json
  - flake.nix
---

# Step 19: release tooling (contrib/release + git-cliff)

## Context

The original step-19 plan was "tag v1.0.0 by hand: bump
`package.json` + `flake.nix`, stamp `CHANGELOG.md`, merge, then the
human runs `git tag -a v1.0.0 && git push`." The Codex review on the
plan PR flagged the changelog-date instruction as fragile (the
execution date and the merge/tag date can differ), and the manual
three-edit-then-tag dance was already starting to feel like a
process-debt smell.

Pivot: model the release on `direnv-session-loader`'s
`contrib/release` pattern. One local script does the whole pre-tag
dance — bump both version literals in lockstep, regenerate
`CHANGELOG.md` from commit history via `git-cliff`, commit, push,
then create and push the annotated tag. The CI release workflow from
step 17 continues to do the irreversible parts (`npm publish
--provenance`, GitHub Release creation); `contrib/release` is the
**trigger** that human-launches that pipeline.

Two scope decisions:

1. **Initial version is `0.0.0`, not `1.0.0`.** clast is not ready
   for a v1.0 commitment. Reset both version literals to `0.0.0` so
   the first run of `contrib/release --patch` produces `v0.0.1`;
   `--minor` later produces `v0.1.0`; `--major` (whenever the human
   is ready) cuts `v1.0.0`. The CHANGELOG `[1.0.0]` entry from step
   18 was a hand-curated bridge written under the v1.0.0 assumption
   — that goes away in this step, replaced by a `git-cliff`-driven
   regen that produces an empty `[Unreleased]` placeholder until a
   real `contrib/release --patch` is invoked.

2. **`contrib/release` does NOT publish.** It bumps, regenerates the
   changelog, commits with `release: vX.Y.Z`, pushes the branch,
   creates the annotated `vX.Y.Z` tag, and pushes it. The tag push
   triggers `.github/workflows/release.yml`, which runs the gates,
   builds the Nix package, packs the npm tarball, and publishes with
   provenance. The split is deliberate: the script's local side
   effects are reversible (delete the tag, revert the commit); the
   workflow's side effects (a published npm version) are not.

What is already on `main` and ready for this step:

- `docs/releasing.md` from step 18 — currently describes a manual
  three-edit-then-tag flow. This step rewrites it to "run
  `contrib/release --patch|--minor|--major`."
- `cliff.toml` is **not** yet on disk. Step 01 referenced it in the
  layout doc; step 18 deliberately deferred curating it because the
  v1.0.0 changelog entry was hand-written. This step lands a real
  `cliff.toml`.
- `git-cliff` is **not** in the Nix dev shell. This step adds it.
- The release workflow (step 17) is already correct; no edits
  needed.
- `make check-version-sync` enforces the `package.json` ↔
  `flake.nix` lockstep — `contrib/release` relies on it.
- `Makefile` already has a `release:` target wired to
  `./contrib/release` from step 01's scaffold; that target was a
  placeholder until now.

## Goal

Land `contrib/release` (a local one-shot bump + cliff-regen + commit +
tag-push script, adapted from `direnv-session-loader`'s with two
version sources and a `v{version}` tag shape), a real `cliff.toml`
shaped for the project's conventional-commit history, `git-cliff` in
the Nix dev shell, a fresh `CHANGELOG.md` with only `[Unreleased]`
(no curated v1.0.0 entry), `package.json` and `flake.nix` both at
`version = "0.0.0"`, and a rewrite of `docs/releasing.md` that
documents the new one-script flow. **Do NOT run `contrib/release`**
inside this step — the script's tag push is the human's first-real-
release action, taken after this PR merges.

## Tasks

1. **Write `contrib/release`** (new file, mode 0755, bash). Behavior:
   - Argument: `--major | --minor | --patch` (required).
   - Preconditions: required tools on PATH (`bash`, `jq`, `git`,
     `git-cliff`), clean working tree, branch is `main`.
   - Bump current `package.json#version` via pure-bash semver
     arithmetic; write back via `jq` into a temp file + `mv`.
   - Bump `flake.nix`'s `version` literal via `sed -i.bak -E`,
     remove the `.bak`, verify with `grep` that the new value
     landed.
   - Verify lockstep: invoke `./contrib/check-version-sync.sh`.
   - Regenerate `CHANGELOG.md`: `git cliff --tag "v${NEW}" -o
     CHANGELOG.md` (full regen, deterministic).
   - Run the gates BEFORE committing: `make lint && make test &&
     make check-version-sync` (plus `npm-pack-check` /
     `nix-smoke` if their tooling is on PATH — both skip cleanly
     otherwise). Failure aborts before any commit.
   - `git add package.json flake.nix CHANGELOG.md && git commit
     -m "release: v${NEW}"`. The `release:` prefix matches a
     `cliff.toml` skip rule so the release commit itself stays out
     of future changelogs.
   - `git push` the branch, then `git tag -a "v${NEW}" -m
     "v${NEW}"`, then `git push origin "v${NEW}"`.
   - Final stdout: a `gh run watch` hint pointing at the release
     workflow.
   - Shellcheck-clean (`shellcheck -x contrib/release` exits 0).
   - **Do NOT** include a "notify marketplace" step (no marketplace
     repo for clast).

2. **Write `cliff.toml`** at the repo root. Standard
   conventional-commits config:
   - Keep-a-Changelog-flavored body template.
   - `[git]` config: `conventional_commits = true`,
     `filter_unconventional = true`, `tag_pattern = "v[0-9]*"` so
     only `v*` tags are considered (not per-step branch tags).
   - Commit-type → section mapping: `feat` → "Added", `fix` →
     "Fixed", `docs` → "Documentation", `ci` → "CI", `chore` and
     anything matching `^release:` are skipped (so the release
     commit itself stays out of future changelogs), `test` is
     skipped (non-user-facing).
   - ~80 lines, well-commented. Use git-cliff's
     `keepachangelog`/`detailed` example as the starting point;
     trim to the sections clast uses.

3. **Reset `package.json#version`** from `"0.1.0"` to `"0.0.0"`.

4. **Reset `flake.nix`'s `version` literal** from `"0.1.0"` to
   `"0.0.0"`. `make check-version-sync` must pass post-edit.

5. **Reset `CHANGELOG.md`** to the bootstrap shape:

   ```markdown
   # Changelog

   All notable changes to this project will be documented here.
   Format follows [Keep a Changelog](https://keepachangelog.com/),
   generated via [git-cliff](https://git-cliff.org/).

   ## [Unreleased]
   ```

   Drop the entire `## [1.0.0] - YYYY-MM-DD` curated entry from step
   18; cliff regenerates `[Unreleased]` content from commit history
   on the next `contrib/release` invocation. The hand-curated
   bullets are recoverable from git history if anyone needs the
   narrative.

6. **Add `git-cliff` to the Nix dev shell.** Edit `flake.nix`'s
   `devShells.default.buildInputs` array, adding `git-cliff` after
   `pre-commit`.

7. **Rewrite `docs/releasing.md`** to document the new flow:
   - **Pre-release checklist**: tests green on `main`, clean working
     tree, on `main`, `NPM_TOKEN` repo secret installed,
     conventional-commit hygiene on commits being released.
   - **Cut the release**:
     ```sh
     git checkout main && git pull --ff-only origin main
     contrib/release --patch   # or --minor / --major
     gh run watch
     ```
   - **What the workflow does**: unchanged 1:1 walkthrough of
     `.github/workflows/release.yml`.
   - **What `contrib/release` does**: bullet-list expansion of the
     script's behavior.
   - **Recovery**: if `contrib/release` failed AFTER pushing the
     tag, recovery is `git tag -d v…`, `git push origin
     :refs/tags/v…`, fix, re-run. If it failed BEFORE the tag
     push, the gate-run-before-commit guard rails should have kept
     the tree clean; if a commit landed without a tag, `git reset
     --hard origin/main^` (or `git revert <sha>`) cleans up.
   - **NPM_TOKEN setup**: unchanged.
   - **Post-release verification**: `npm view @procrastivity/clast
     version`, `gh release view v${version}`, `nix run
     github:procrastivity/clast/v${version} -- --version`.

8. **Restore the pre-1.0 banner in `README.md`.** Step 18 replaced
   it with "v1.0 — CLI contract is stable"; that was premature.
   Replace with "🚧 Pre-1.0 — APIs may change." (or equivalent
   wording). No other README changes in this step.

9. **Update `make lint`** so shellcheck covers `contrib/release`.
   Extend the dynamic file-list expression in the `lint:` target.

10. **Pre-flight `NPM_TOKEN`.** Run `gh secret list --repo
    procrastivity/clast`. Report present / missing / unable-to-
    verify in the end-of-execution summary.

11. **Run the gates locally.** `make lint`, `make test`,
    `make check-version-sync`. All exit 0.
    `make npm-pack-check` / `make nix-smoke` skip cleanly when
    their tooling is missing.

12. **Do NOT run `contrib/release`.** First invocation is the
    human's post-merge action.

## Acceptance criteria

- `contrib/release` exists, mode 0755 in git, shellcheck-clean,
  supports `--major | --minor | --patch`, refuses to run with a
  dirty tree, on a non-`main` branch, with required tools missing,
  or with a `package.json` ↔ `flake.nix` mismatch.
- `cliff.toml` exists at the repo root and `git-cliff --tag v0.0.1
  --unreleased` against current history runs without erroring.
- `flake.nix`'s `devShells.default.buildInputs` includes
  `git-cliff`.
- `package.json#version` is `"0.0.0"`. `flake.nix`'s `version`
  literal is `"0.0.0";`. `make check-version-sync` prints
  `version sync: 0.0.0`.
- `CHANGELOG.md` is the bootstrap shape (header + `[Unreleased]`;
  no `[1.0.0]` entry).
- `docs/releasing.md` documents the `contrib/release` flow with
  the sections enumerated above.
- `README.md` no longer claims v1.0 stability; pre-1.0 banner is
  restored.
- `make lint` covers `contrib/release`.
- `make lint`, `make test`, `make check-version-sync` exit 0.
- `make npm-pack-check` / `make nix-smoke` exit 0 OR skip cleanly.
- No `v0.0.1` / `v1.0.0` tag exists locally or on `origin`.

## Out of scope

- **Running `contrib/release`.** Human runs it post-merge.
- **Configuring `NPM_TOKEN`.** Pre-flight only verifies presence.
- **Curating commit messages** retroactively for cliff aesthetics.
- **Editing canonical reference docs.** Pre-1.0 language change is
  README-only.
- **`-pre` suffix on the version.** The pre-release state is
  `0.0.0`; first release is `0.0.1`.
- **Marketplace-dispatch step.** No marketplace repo for clast.
- **Touching the release workflow.** Step 17's workflow is correct
  for `v{version}` tags.
- **Adding a `release-dry-run` mode** to `contrib/release`.
  Gate-run-before-commit covers the common failure modes.
- **Bumping version beyond `0.0.0`** in this step. The bump belongs
  to `contrib/release`'s first invocation.
- **Generating GH release notes from `CHANGELOG.md`.** Workflow
  uses `--generate-notes`; switching is a follow-up.
- **Smoke-testing the published v0.0.1.** Human's post-release
  step.

## Verification

```bash
# 1. Versions reset to 0.0.0
test "$(jq -r .version package.json)" = "0.0.0"
grep -E '^\s*version = "0\.0\.0";' flake.nix
make check-version-sync

# 2. CHANGELOG bootstrap shape
test "$(grep -cE '^## \[' CHANGELOG.md)" = "1"
grep -q '^## \[Unreleased\]' CHANGELOG.md

# 3. contrib/release present, executable, shellcheck-clean
test -x contrib/release
shellcheck -x contrib/release

# 4. cliff.toml present and usable
test -f cliff.toml
command -v git-cliff
git-cliff --tag v0.0.1 --unreleased >/dev/null

# 5. Hard gates
make lint
make test
make check-version-sync

# 6. Optional gates skip cleanly without tooling
make npm-pack-check
make nix-smoke

# 7. No release tags
! git rev-parse --verify --quiet v0.0.1
! git rev-parse --verify --quiet v1.0.0
! git ls-remote --exit-code --tags origin refs/tags/v0.0.1
! git ls-remote --exit-code --tags origin refs/tags/v1.0.0
```

## Notes for the implementer

- **Why bash `sed` for `flake.nix`.** The version literal is one
  line with a stable shape; `sed -i.bak -E` is portable and
  reversible. A Nix-aware edit is over-engineering for a one-line
  bump.
- **Why push branch before tagging.** The annotated tag references
  the just-pushed commit SHA; pushing the branch first guarantees
  the tag points at a commit already on `origin`.
- **Why `release: vX.Y.Z` commit message.** Matches the
  `cliff.toml` skip rule so release commits stay out of future
  changelogs.
- **First-release case.** With no prior `v*` tag, `git cliff
  --tag v0.0.1` treats every conventional-commit since repo init
  as part of v0.0.1. The first changelog is long; acceptable for a
  v0.x first cut.
- **Conventional commit suggestion**: `chore(release): add
  contrib/release script and reset version to 0.0.0`.
