#!/usr/bin/env bash
# hooks/snapshot.sh
#
# Fired on Claude Code SessionStart. Backgrounds `clast snapshot` so it doesn't
# block session start. Silent if clast isn't installed — the plugin can still
# load cleanly even if the CLI isn't on PATH.
#
# Idempotent. Safe to run repeatedly. Best-effort: never propagates a non-zero
# exit to Claude Code (a failed snapshot is not a session-start failure).
# shellcheck shell=bash

if command -v clast >/dev/null 2>&1; then
  (clast snapshot >/dev/null 2>&1 &)
fi
exit 0
