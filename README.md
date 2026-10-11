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

## Session capture

`clast plumbing capture` sweeps your session stores and journals
anything new or grown — silently: nothing on stdout when there is
nothing to do. The Claude Code SessionStart hook runs it in the
background on every session start, so it also fits a cron line.

Two sources exist today. **claude** reads local files and always joins
a sweep. **amp** fetches Amp threads over the network through the `amp`
CLI's own subprocess surface, so it is opt-in: installing amp or being
logged in is never authorization by itself.

```yaml
# $XDG_CONFIG_HOME/clast/config.yaml — join amp to every sweep
capture:
  amp:
    auto: true
```

`capture.exclude: [amp]` removes it again (the exclusion wins over the
opt-in), and `clast plumbing capture --harness amp` captures amp for
that one run regardless of either setting — the flag itself is the
authorization; nothing is persisted.

What the sweep reports is honest about which kind of "not there" it
met: no amp CLI on PATH reads as absent storage — quiet in a sweep,
`capture.source-unavailable` (exit 1) when `--harness amp` names it.
An amp that answers with an auth, network, or other enumeration
failure is disclosed as one `capture: amp: …` stderr line (plus an
`"unavailable"` row under `--json`) while the rest of the sweep still
runs and exits 0; the same failure named explicitly is
`capture.source-unavailable`. Capture never prompts, never reads
stdin, and never starts a login flow — a missing or expired credential
is an error, not a request.

Captured amp sessions are local journal records from then on:
`sessions`, `show --transcript`, and `analyze` read them through the
recorded transcript format with no availability check anywhere in the
read path, so they stay fully usable with amp excluded, opted out,
uninstalled, or unreachable.

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
