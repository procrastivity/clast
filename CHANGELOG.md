# Changelog

All notable changes to this project will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/),
generated via [git-cliff](https://git-cliff.org/).

## [Unreleased]

## [1.0.0] - YYYY-MM-DD

First public release.

### Added

- `clast` CLI: snapshot, sessions, projects, show, entries, breadcrumb, stats, doctor, registry, whereami.
- Manifest-backed JSONL to entry curation pipeline.
- Claude Code plugin shipping `/day-wakeup` and `/wakeup` skills.
- SessionStart hook for zero-effort capture.
- Three install channels: manual `install.sh`, Nix flake (`packages.default` and `overlays.default`), npm (`@procrastivity/clast`).
- `examples/cron/`, `examples/config/`, `examples/workflows/` reference material.
- CI: lint, test, version sync, npm-pack-check, and nix-smoke on every PR; tag-triggered release workflow with npm provenance and GitHub Release.
