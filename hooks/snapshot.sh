#!/usr/bin/env bash
# SessionStart hook: background `clast snapshot` and return immediately.
# Foreground must stay sub-second (only a few stats) — do not add blocking work.
set -eu

clast_bin=""

if [ -n "${CLAUDE_PLUGIN_ROOT:-}" ] && [ -x "${CLAUDE_PLUGIN_ROOT}/bin/clast" ]; then
  clast_bin="${CLAUDE_PLUGIN_ROOT}/bin/clast"
else
  sibling="$(cd "$(dirname "$0")/.." && pwd)/bin/clast"
  if [ -x "$sibling" ]; then
    clast_bin="$sibling"
  elif path_bin="$(command -v clast 2>/dev/null)"; then
    clast_bin="$path_bin"
  fi
fi

if [ -z "$clast_bin" ]; then
  exit 0
fi

"$clast_bin" snapshot </dev/null >/dev/null 2>&1 &
disown
exit 0
