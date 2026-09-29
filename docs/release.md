# Release and install

This page records how a clast release is made, what it publishes, and
what the curl installer does and does not promise. The rationale for the
shared shape is in toolsmith's `assets/playbook/release-and-hygiene.md`.
This page records only what is specific to clast.

## The one human action

Pushing an annotated `vX.Y.Z` tag is the only human action in a release
(C6.1). `.github/workflows/release.yml` runs on that tag push. It does
these steps in order:

1. It runs `make check` again, because a tag can sit on a commit that
   never went through branch CI.
2. It cross-compiles the binaries into `dist/`.
3. It copies `scripts/install.sh` to `dist/clast-install.sh`.
4. It generates `dist/CHANGELOG.md` and `dist/RELEASE_NOTES.md` with
   git-cliff.
5. It writes `dist/SHA256SUMS` for the binaries.
6. It creates the GitHub release. The body is `RELEASE_NOTES.md`. The
   step names every asset explicitly and never globs `dist/` (C6.4).

## Cutting a release

`contrib/release` makes the tag push repeatable (toolsmith T33). Run it
from a clean `main` in the dev shell:

```sh
contrib/release --minor      # or --major, --patch, or an explicit vX.Y.Z
```

The driver does these steps:

1. It checks that the tools are on `PATH` and that the tree is clean.
2. It reads the latest release tag with `git describe --tags --match
   'v[0-9]*' --abbrev=0` and computes the next tag. It stops when that
   tag exists or when there are no commits since the last tag.
3. It runs `make check`, the local twin of the release gate.
4. It runs `make release-notes TAG=<tag>` and stops when
   `dist/RELEASE_NOTES.md` is empty.
5. It creates the annotated tag with the notes as its message. It uses
   `--cleanup=whitespace`, because the default cleanup deletes the
   `## [x.y.z]` lines.
6. It pushes the branch, then only the new tag. It never force-pushes.

The tag push starts release.yml, which regenerates the release body from
the same `make release-notes`. To do a release by hand, run `make check`
and `make release-notes TAG=vX.Y.Z`, then `git tag -a --cleanup=whitespace
-F dist/RELEASE_NOTES.md vX.Y.Z` and push the tag.

## Published assets

Each release publishes exactly these assets:

| Asset | What |
|---|---|
| `clast-linux-amd64` | Static binary, Linux amd64. |
| `clast-darwin-arm64` | Static binary, macOS arm64. |
| `clast-install.sh` | The curl installer (`scripts/install.sh`). |
| `SHA256SUMS` | Checksums of the two binaries only. |
| `CHANGELOG.md` | The full generated history. |

The platform matrix is the Makefile's `cross-compile` target, and the CI
`cross-compile` job builds the same two targets. There is no Windows
asset and no other architecture. When you add a target, change the
Makefile, the CI matrix, the `checksums` and `publish` steps in
release.yml, and the `case` in `scripts/install.sh` together.

## The curl installer

```sh
curl -fsSL https://github.com/procrastivity/clast/releases/latest/download/clast-install.sh | sh
```

The installer installs only the binary. It does not project clast into
a harness. Run `clast install` (every detected harness) or `clast
install <harness>` after it, as a separate step.

The installer does these steps:

1. It selects `clast-linux-amd64` or `clast-darwin-arm64` from `uname`.
   On any other platform it stops before it downloads anything.
2. It downloads that binary and `SHA256SUMS` from the same release.
3. It verifies the binary against its `SHA256SUMS` entry with
   `sha256sum`, or `shasum -a 256` when `sha256sum` is not available.
4. Only after the check passes, it writes the binary to a temporary file
   in the install directory and moves it into place. When the check
   fails, an existing `clast` stays unchanged.

These environment variables change what it fetches and where it writes:

| Variable | Default | Effect |
|---|---|---|
| `CLAST_VERSION` | empty | Empty fetches `releases/latest/download`. A tag such as `v0.1.0` fetches `releases/download/v0.1.0`. |
| `CLAST_BASE_URL` | `https://github.com/procrastivity/clast` | The repository to fetch from, for a fork or another owner. It must publish the same asset names. |
| `CLAST_INSTALL_DIR` | `~/.local/bin` | The directory that receives `clast`. |

### What the checksum protects

`SHA256SUMS` covers the two binaries, not the installer. `curl | sh`
runs the installer script before anything can verify it. So you trust
the GitHub release asset and its HTTPS delivery for the script. The
checksum then protects the binary download that the script does. When
that trust is not enough, download `clast-install.sh`, read it, and run
it from the file. Or download a binary and `SHA256SUMS` yourself and run
`sha256sum -c --ignore-missing SHA256SUMS`.

### Tests

`scripts/release_install_test.go` runs the installer against a fake
`curl` and a fake `uname`, with a `SHA256SUMS` computed from the fixture
bytes. It checks these results:

- the ordered URLs for latest and pinned releases, and both asset names
- the installed bytes and executable mode
- checksum refusal, with and without an existing destination
- temp-file cleanup

`make test` runs it, and `make lint` runs shellcheck on the script.

## Tags from the legacy clast

Tags `v0.0.1` through `v0.0.8`, and their GitHub releases, come from the
legacy clast. That history is not an ancestor of the current `main`, and
its releases publish only a `procrastivity-clast-<version>.tgz`. Two
facts follow:

- `git describe --match 'v[0-9]*'` finds no release tag on `main`. The
  version stamp falls back to the commit hash until the first release of
  this history.
- Until that release, `releases/latest` resolves to the legacy `v0.0.8`,
  which has no `clast-install.sh`. The curl command above returns 404.

So the first release of this history must use a tag above `v0.0.8`, for
example `v0.1.0`. The `v0.0.x` names already exist. With no release tag
on `main`, `contrib/release` counts from `v0.0.0`, so use `--minor` or
an explicit `v0.1.0`. `--patch` computes `v0.0.1`, which exists, and the
driver stops before it runs the gate.
