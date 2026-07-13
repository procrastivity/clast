# Changelog

All notable changes to this project will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/),
generated via [git-cliff](https://git-cliff.org/).
## [0.0.7] - 2026-07-13

### Bug Fixes

- Brace expansion before the en dash in the recorded window

### Documentation

- Remove references to original planning system

### Features

- Report model-call timing per draft (#44)
- Add --auto for non-interactive curation
- Skip too-short drafts in --auto (length guard)
- Mirror --auto batch mode into the /wake skill

## [0.0.6] - 2026-07-08

### Build

- Bump workflow actions (checkout v7, setup-node v6, install-nix-action v31) (#40)

### Documentation

- Add Linear project reference to AGENTS.md (#43)

### Features

- Show progress while summarizing (#41)

## [0.0.5] - 2026-07-06

### Bug Fixes

- Move skills to repo root + rename to brief/wake (#30)
- Handle large sessions — SIGPIPE and ARG_MAX overflows (#31)

### Features

- Per-directory labels, explicit-slug honoring, workspace-segmented brief (#32)
- Undismiss sessions + show recorded date/id in wake (#34)
- Clast retro — work summary grouped by actual work day (#33)
- Auto-skip no-op sessions before the LLM (#35)

## [0.0.4] - 2026-06-16

### Performance

- Make clast sessions / wake fast on large journals (7min → 3s) (#29)

## [0.0.3] - 2026-06-16

### Bug Fixes

- Entries tag validation, branch extraction, and list display
- Recognize type:user/assistant transcript shape

### Build

- Install clast-wake and clast-brief helpers (#26)
- Polish install.sh and add make install-local (#27)

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


