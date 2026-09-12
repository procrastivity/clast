# clast

TODO(clast): one paragraph on what this tool is and who it is for.

Built on the [toolsmith contract](https://github.com/procrastivity/toolsmith)
(`toolsmith/v1` — the manifest declares it): a single static Go binary
that projects itself into agent harnesses as generated, stamped skills.

## Install

```
nix profile install github:OWNER/clast   # the binary, system-wide
clast install                            # project into every detected harness
```

`clast install <harness>` targets one harness; `clast uninstall
<harness>` removes exactly what install wrote; `clast doctor` reports
stale or drifted projections.

## Develop

```
direnv allow      # or: nix develop
make check        # lint + test
make hooks        # pre-commit, both stages
```

Version stamps come from release tags (`git describe --match 'v[0-9]*'`);
pushing an annotated `vX.Y.Z` tag is the only human release action.
CHANGELOG.md is generated per release, never committed.

## Design of record

TODO(clast): name where the design of record lives (a sidecar planning
repo, docs/ — whatever this tool uses), so a reader knows where decisions
come from.
