#!/usr/bin/env bash
# test-clast.sh — top-level test aggregator. Invokes every test/test-*.sh
# script and exits non-zero if any of them fail.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

declare -a suites=(
  test/test-lib.sh
  test/test-decode.sh
  test/test-dispatcher.sh
  test/test-whereami.sh
  test/test-manifest.sh
  test/test-registry.sh
  test/test-registry-cmd.sh
)

fail=0
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
