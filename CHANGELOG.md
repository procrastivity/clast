# Changelog

All notable changes to this project will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/),
generated via [git-cliff](https://git-cliff.org/).
## [0.0.2] - 2026-06-07

### CI

- Switch npm publish to Trusted Publishing (OIDC) (#23)

### Documentation

- Restructure into Diátaxis layout (#25)

### Features

- Standalone clast-wake and clast-brief scripts, dismissed sessions, stale detection

## [0.0.1] - 2026-06-02

### Bug Fixes

- Check write/stat/date status on append and rebuild
- Test no longer fail in interactive terminal

### CI

- Use make lint/test so shellcheck runs with -x

### Documentation

- Import planning artifacts
- Add step-03 dispatcher-and-whereami plan
- Add step-04 manifest-lib plan
- Add step-05 registry-lib-and-subcommand plan (#3)
- Add step-07 query-subcommands plan (#5)
- Add step-08 entries-subcommand plan (#6)
- Add step-09 breadcrumb-subcommand plan (#7)
- Add step-12 skill-day-wakeup plan (#10)

### Features

- Implement core helpers and segment decoder with tests
- Add bin/clast dispatcher and whereami subcommand
- Add clast-manifest-lib.bash with append/lookup/iterate/rebuild
- Implement clast snapshot subcommand (#4)
- Scaffold .claude-plugin manifest + SessionStart snapshot hook (step 11) (#9)
- Add /wakeup read-only briefing skill (step 13) (#12)


