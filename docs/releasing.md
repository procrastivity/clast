# Releasing clast

This is the tag-driven release runbook for maintainers. Cutting a
release is a single command: `contrib/release --patch|--minor|--major`.

## Pre-release checklist

- `main` is green in CI.
- Your working tree is clean (`git status` is empty).
- You are on `main` (not a feature branch) — `contrib/release`
  refuses to run elsewhere.
- `make check-version-sync` passes (the script also runs it before
  editing anything, but checking ahead saves a feedback loop).
- The npm Trusted Publisher for `@procrastivity/clast` is configured
  on npmjs.com (one-time setup; see
  [Trusted Publishing setup](#trusted-publishing-setup) below). There
  is no CLI to verify this — confirm via the npm web UI.
- Commits since the last release follow Conventional Commits
  (`feat:`, `fix:`, `docs:`, etc.). `git-cliff` ignores anything
  unconventional, so non-conventional messages silently drop from
  the changelog.

## Cut the release

From a clean `main` checkout:

```sh
git checkout main && git pull --ff-only origin main
contrib/release --patch     # or --minor / --major
gh run watch                # follow the release workflow
```

`contrib/release` exits non-zero before pushing anything if a
precondition fails or a gate is red. After the script returns, the
release workflow does the rest.

## What `contrib/release` does

In order:

1. Validates preconditions: `bash`, `jq`, `git`, `git-cliff` on
   PATH; clean working tree; branch is `main`; `package.json` ↔
   `flake.nix` version literals already agree.
2. Computes the new version by bumping the current one
   (`--major` / `--minor` / `--patch`).
3. Writes the new version into `package.json` via `jq` (temp file +
   `mv`).
4. Writes the new version into `flake.nix` via `sed`, then verifies
   the substitution landed.
5. Re-runs `./contrib/check-version-sync.sh` against the post-edit
   tree.
6. Regenerates `CHANGELOG.md` from commit history:
   `git-cliff --tag v<version> -o CHANGELOG.md`.
7. Runs every gate **before** committing: `make lint`, `make test`,
   `make check-version-sync`, `make npm-pack-check`, `make
   nix-smoke`. A failure aborts before any commit lands.
8. Commits the bump + changelog with `release: v<version>`. (The
   `release:` prefix is what `cliff.toml` skips so the release
   commit itself never appears in a changelog.)
9. Pushes the branch.
10. Creates the annotated tag `v<version>` and pushes it. The tag
    push triggers `.github/workflows/release.yml`.

## What the workflow does

`.github/workflows/release.yml` runs on pushed tags matching `v*`.

1. Checks out the repository.
2. Installs Nix.
3. Installs Node 20 with the npm registry configured.
4. Re-runs every gate inside the Nix dev shell: `make lint`,
   `make test`, `make check-version-sync`, `make npm-pack-check`,
   `make nix-smoke`.
5. Verifies the tag (without the leading `v`) matches
   `package.json`'s version.
6. Builds the Nix package: `nix build .#default --print-build-logs`.
7. Packs the npm tarball: `npm pack --silent`.
8. Publishes to npm with provenance:
   `npm publish --provenance --access public <tarball>`.
   Authentication is via npm Trusted Publishing (OIDC) — the
   workflow exchanges its GitHub Actions ID token for a short-lived
   npm publish token. No `NPM_TOKEN` secret is involved.
9. Creates a GitHub Release for the tag with auto-generated notes
   and attaches the npm tarball.

The workflow uses the automatic `GITHUB_TOKEN` for the GitHub
Release. npm publishing uses OIDC via the workflow's
`permissions.id-token: write`.

## If something fails

**`contrib/release` failed before committing.** Working tree may have
edited but uncommitted files (e.g. a half-applied `sed` against
`flake.nix`). Inspect with `git status` / `git diff`, then
`git checkout -- .` to revert. Re-run.

**`contrib/release` failed after committing but before tagging.** The
release commit landed on `main` (locally and possibly remotely) but
no tag exists. Recovery:

```sh
# If the commit hasn't been pushed yet:
git reset --hard HEAD^

# If the commit has been pushed:
git revert HEAD
git push
```

Then fix the underlying issue and re-run `contrib/release`.

**The workflow failed before npm publish.** Fix the problem, delete
the tag, then re-tag the corrected commit:

```sh
version=$(jq -r .version package.json)
git tag -d "v${version}"
git push origin ":refs/tags/v${version}"
git tag -a "v${version}" -m "v${version}"
git push origin "v${version}"
```

**npm publish succeeded but the GitHub Release failed.** Do NOT
delete and reuse the npm version. Create the release manually:

```sh
version=$(jq -r .version package.json)
tarball=$(npm pack --silent)
gh release create "v${version}" \
  --title "v${version}" \
  --generate-notes \
  "$tarball"
```

**The tag version does not match `package.json`.** Delete the tag,
fix the version or tag, push the corrected tag. Do not weaken the
workflow's verify step.

**npm rejects the publish because the version already exists.**
That version is permanently consumed. Bump to the next version with
`contrib/release --patch` and tag the new version.

## Trusted Publishing setup

The workflow uses npm Trusted Publishing (OIDC), which means there
is no long-lived `NPM_TOKEN` to manage. Setup is per-package and
one-time.

1. Sign in to https://www.npmjs.com with an account that has
   publish rights on the `@procrastivity` org.
2. Avatar → **Packages** → **Trusted Publishers** → **Add trusted
   publisher for an unpublished package** (use this path because the
   package doesn't exist on npm yet; after the first publish the
   trusted publisher is also editable from the package's own access
   page at `https://www.npmjs.com/package/@procrastivity/clast/access`).
3. Fill in (case-sensitive, exact match):
   - **Package name**: `@procrastivity/clast`
   - **Publisher**: GitHub Actions
   - **Organization**: `procrastivity`
   - **Repository**: `clast`
   - **Workflow filename**: `release.yml` (filename only, no path)
   - **Environment name**: leave blank
   - **Allowed actions**: tick `npm publish`

The workflow already sets `permissions.id-token: write`, which is
the only requirement on the GitHub side. The npm CLI auto-detects
the OIDC environment when `id-token: write` is present and the
publisher binding exists.

Requirements (already satisfied by the workflow):
- `actions/setup-node` with `registry-url: https://registry.npmjs.org`.
- Node 24 (ships npm ≥ 11.5.1, the minimum for trusted publishing).
- `permissions.id-token: write`.

## Post-release verification

After the workflow goes green:

```sh
version=$(jq -r .version package.json)

# npm: registry shows the new version
npm view @procrastivity/clast version

# GitHub: release exists with the tarball attached
gh release view "v${version}"

# Nix: the public flake can run the tagged version
nix run "github:procrastivity/clast/v${version}" -- --version
```

If any of these fail and the workflow itself was green, that points
at a registry / propagation issue. npm and the GitHub API are
generally fast (under a minute); if either is still empty after
five, dig into the workflow logs to confirm the publish actually
ran.
