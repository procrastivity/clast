#!/usr/bin/env bash
# test-dispatcher.sh — `bin/clast` dispatcher behavior:
# version, help, unknown subcommand, stubbed subcommand, global-flag
# forwarding into the lib.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-dispatcher"

CLAST_BIN="$PWD/bin/clast"

# --- --version ----------------------------------------------------------

expected_version="$(jq -r '.version' package.json)"
actual_version="$("$CLAST_BIN" --version)"
assert_eq "clast $expected_version" "$actual_version" "--version prints clast <pkg-version>"
assert_exit_code 0 "$CLAST_BIN" --version

# --- --help and no-arg --------------------------------------------------

help_out="$("$CLAST_BIN" --help)"
case "$help_out" in
  *"Usage:"*"whereami"*) _clast_test_pass "--help prints usage to stdout" ;;
  *) _clast_test_fail "--help prints usage to stdout"; printf '%s\n' "$help_out" >&2 ;;
esac
assert_exit_code 0 "$CLAST_BIN" --help

noarg_out="$("$CLAST_BIN")"
case "$noarg_out" in
  *"Usage:"*) _clast_test_pass "no-arg prints usage to stdout" ;;
  *) _clast_test_fail "no-arg prints usage to stdout" ;;
esac
assert_exit_code 0 "$CLAST_BIN"

# --- unknown subcommand -------------------------------------------------

bogus_stderr="$("$CLAST_BIN" bogus-cmd 2>&1 1>/dev/null)"
case "$bogus_stderr" in
  *"unknown subcommand"*"bogus-cmd"*) _clast_test_pass "unknown subcommand error on stderr" ;;
  *) _clast_test_fail "unknown subcommand error on stderr"; printf '%s\n' "$bogus_stderr" >&2 ;;
esac
assert_exit_code 2 "$CLAST_BIN" bogus-cmd

# --- stubbed subcommand -------------------------------------------------

stub_stderr="$("$CLAST_BIN" projects 2>&1 1>/dev/null)"
case "$stub_stderr" in
  *"projects is not yet implemented"*) _clast_test_pass "stub: projects reports not yet implemented" ;;
  *) _clast_test_fail "stub: projects reports not yet implemented"; printf '%s\n' "$stub_stderr" >&2 ;;
esac
assert_exit_code 2 "$CLAST_BIN" projects

# --- global-flag forwarding into the lib --------------------------------

forwarded_journal="$("$CLAST_BIN" --journal-dir /tmp/clast-test-x whereami --json | jq -r .journal_dir)"
assert_eq "/tmp/clast-test-x" "$forwarded_journal" "--journal-dir forwards into clast_journal_dir"

forwarded_projects="$("$CLAST_BIN" --projects-dir /tmp/clast-test-y whereami --json | jq -r .projects_dir)"
assert_eq "/tmp/clast-test-y" "$forwarded_projects" "--projects-dir forwards into clast_projects_dir"

# Env-var path also works (and is what subcommands ultimately read).
env_journal="$(CLAST_JOURNAL_DIR=/tmp/clast-test-z "$CLAST_BIN" whereami --json | jq -r .journal_dir)"
assert_eq "/tmp/clast-test-z" "$env_journal" "CLAST_JOURNAL_DIR env var honored"

clast_test_summary
