# Releasing clast

This is the tag-driven release runbook for maintainers.

## Pre-release checklist

- Confirm `main` is green in CI.
- Confirm the release version is bumped in both files:
  - `package.json`
  - `flake.nix`
- Run the local lockstep check:

  ```sh
  make check-version-sync
  ```

- Confirm `CHANGELOG.md` has no uncurated items under `[Unreleased]`.
- Confirm the release entry has the exact version and date you are about to tag.
- Confirm the npm automation token exists as the `NPM_TOKEN` Actions secret.

The version in `package.json` and `flake.nix` must match. The release workflow
also verifies the tag version against `package.json`, so a mismatch fails before
publishing.

## Cut the release

From a clean `main` checkout:

```sh
git pull --ff-only origin main
make lint
make test
make check-version-sync
make npm-pack-check
make nix-smoke
```

Create and push the annotated tag:

```sh
version=$(jq -r .version package.json)
git tag -a "v${version}" -m "v${version}"
git push origin "v${version}"
```

Watch the workflow:

```sh
gh run list --workflow release.yml --limit 1
gh run watch
```

## What the workflow does

`.github/workflows/release.yml` runs on pushed tags matching `v*`.

It performs these steps:

1. Checks out the repository.
2. Installs Nix.
3. Installs Node 20 with the npm registry configured.
4. Re-runs every gate inside the Nix dev shell:
   - `make lint`
   - `make test`
   - `make check-version-sync`
   - `make npm-pack-check`
   - `make nix-smoke`
5. Verifies the tag without the leading `v` matches `package.json`'s version.
6. Builds the Nix package with `nix build .#default --print-build-logs`.
7. Creates the npm tarball with `npm pack --silent`.
8. Publishes the tarball to npm with provenance:

   ```sh
   npm publish --provenance --access public <tarball>
   ```

9. Creates a GitHub Release for the tag with generated notes and attaches the
   npm tarball.

The workflow uses the automatic `GITHUB_TOKEN` for the GitHub Release and the
repo secret `NPM_TOKEN` for npm publishing.

## If something fails

If the workflow fails before npm publish, fix the problem, delete the tag, and
re-tag the corrected commit:

```sh
git tag -d "v${version}"
git push origin ":refs/tags/v${version}"
git tag -a "v${version}" -m "v${version}"
git push origin "v${version}"
```

If npm publish succeeded but the GitHub Release failed, do not delete and reuse
the npm version. Create the GitHub Release manually against the existing tag:

```sh
tarball=$(npm pack --silent)
gh release create "v${version}" \
  --title "v${version}" \
  --generate-notes \
  "$tarball"
```

If the tag version does not match `package.json`, delete the tag, fix the
version or tag, and push a corrected tag. Do not weaken the workflow check.

If npm rejects the publish because the version already exists, bump to the next
version, update the changelog, commit, and tag that new version.

## NPM_TOKEN setup

Create an npm automation token:

1. Go to npmjs.com.
2. Open account settings.
3. Open Access Tokens.
4. Create an Automation token.
5. Scope it so it can publish `@procrastivity/clast`.

Install it in GitHub:

1. Open the repository settings.
2. Go to Secrets and variables.
3. Open Actions.
4. Add or update the secret named `NPM_TOKEN`.
5. Paste the npm automation token value.

The workflow publishes with npm provenance, so `permissions.id-token: write` is
required and already present in `.github/workflows/release.yml`.

## Post-release

Optional follow-up after the release is verified:

```sh
npm version --no-git-tag-version 0.1.1-pre
```

Then update `flake.nix` to the same version, run:

```sh
make check-version-sync
```

Commit the next pre-release version to `main`.
