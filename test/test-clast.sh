#!/usr/bin/env bash
# test-clast.sh — top-level test aggregator. Invokes every test/test-*.sh
# script and exits non-zero if any of them fail.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

# Static checks on the Claude Code plugin assets. The plugin layer has no
# behavioral surface to integration-test — there's nothing to invoke and
# nothing to assert beyond "files exist and are well-formed."
_assert_plugin_assets() {
  local ok=0
  if ! jq -e . .claude-plugin/plugin.json >/dev/null; then
    printf 'plugin asset check: .claude-plugin/plugin.json is not valid JSON\n' >&2
    ok=1
  fi
  if ! jq -e '.hooks | type == "array" and length == 1' hooks/hooks.json >/dev/null; then
    printf 'plugin asset check: hooks/hooks.json must have exactly one hook entry\n' >&2
    ok=1
  fi
  if ! shellcheck --shell=bash hooks/snapshot.sh; then
    printf 'plugin asset check: hooks/snapshot.sh failed shellcheck\n' >&2
    ok=1
  fi
  return "$ok"
}

declare -a suites=(
  test/test-lib.sh
  test/test-decode.sh
  test/test-dispatcher.sh
  test/test-whereami.sh
  test/test-manifest.sh
  test/test-registry.sh
  test/test-registry-cmd.sh
  test/test-snapshot.sh
  test/test-query.sh
  test/test-entries.sh
  test/test-doctor.sh
  test/test-stats.sh
)

fail=0
printf '== plugin assets ==\n'
if ! _assert_plugin_assets; then
  fail=$((fail + 1))
fi

for suite in "${suites[@]}"; do
  printf '== %s ==\n' "$suite"
  if ! bash "$suite"; then
    fail=$((fail + 1))
  fi
done

if (( fail > 0 )); then
  printf '\n%d test suite(s) failed\n' "$fail" >&2
  exit 1
fi
printf '\nall test suites passed\n'
