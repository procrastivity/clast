# clast

clast keeps a journal of your coding-agent sessions. It captures each
session's transcript and facts into a durable store, you curate the
sessions worth keeping into short entries, and the wake, brief, and
retro flows resurface that record when you return to the work. It is
for people who run many agent sessions across many projects and want
what happened to survive the terminal scrollback.

Built on the [toolsmith contract](https://github.com/procrastivity/toolsmith)
(`toolsmith/v1` — the manifest declares it): a single static Go binary
that projects itself into agent harnesses as generated, stamped skills.

## Install

Install the binary first. Use the release installer:

```sh
curl -fsSL https://github.com/procrastivity/clast/releases/latest/download/clast-install.sh | sh
```

It installs `clast` to `~/.local/bin` by default. Make sure that directory
is on `PATH`. Set `CLAST_VERSION` to pin a release tag instead of the
latest release, and `CLAST_INSTALL_DIR` to install somewhere else. Release
binaries exist for Linux amd64 and macOS arm64 only. The installer works
from the first release that publishes `clast-install.sh`. For the
details, see [docs/release.md](docs/release.md).

`curl | sh` runs the fetched installer before any checksum check. The
installer verifies the binary against `SHA256SUMS` from the same release
before it writes the destination. That check does not authenticate the
installer script itself.

Or install the binary with Nix:

```sh
nix profile install github:procrastivity/clast
```

Then, as a separate step, project clast into your agent harnesses:

```sh
clast install              # every detected harness
clast install <harness>    # one harness (today: claude-code)
```

`clast uninstall <harness>` removes exactly what install wrote. `clast
doctor` reports stale or drifted projections.

## Develop

```
direnv allow      # or: nix develop
make check        # lint + test
make hooks        # pre-commit, both stages
```

Version stamps come from release tags (`git describe --match 'v[0-9]*'`);
pushing an annotated `vX.Y.Z` tag is the only human release action.
`contrib/release --minor` (or `--major`, `--patch`, `vX.Y.Z`) gates,
tags with the generated notes, and pushes. See
[docs/release.md](docs/release.md). CHANGELOG.md is generated per
release, never committed.

## Design of record

The design of record lives in a sidecar planning repo
(`clast-reboot`): MODEL.md holds the concepts and store layout;
SURFACE.md holds the verb surface and projection. Decisions are cited
from code as M-numbers and V-numbers. What building each Matter forced
lands in `docs/<matter>/decisions.md` here.
