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

```
nix profile install github:procrastivity/clast   # the binary, system-wide
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

The design of record lives in a sidecar planning repo
(`clast-reboot`): MODEL.md holds the concepts and store layout;
SURFACE.md holds the verb surface and projection. Decisions are cited
from code as M-numbers and V-numbers. What building each Matter forced
lands in `docs/<matter>/decisions.md` here.
