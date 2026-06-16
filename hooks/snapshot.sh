#!/usr/bin/env bash
# hooks/snapshot.sh — SessionStart hook. Backgrounds `clast-plumbing snapshot`.
# Idempotent. Best-effort: never propagates a non-zero exit.
# shellcheck shell=bash

# Derive plugin root from this script's location.
# $0 is the expanded path from ${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh.
_snap_dir="$(cd "$(dirname "$0")" && pwd)"
_plugin_root="$(cd "$_snap_dir/.." && pwd)"

# Prefer the bundled binary (version-matched to this plugin).
# Fall back to a system-level install on PATH.
_clast=""
if [[ -x "$_plugin_root/bin/clast-plumbing" ]]; then
  _clast="$_plugin_root/bin/clast-plumbing"
elif command -v clast-plumbing >/dev/null 2>&1; then
  _clast="clast-plumbing"
fi

if [[ -n "$_clast" ]]; then
  ($_clast snapshot >/dev/null 2>&1 &)
fi
exit 0
