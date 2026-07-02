#!/usr/bin/env bash
# test-dismiss.sh — `clast sessions dismiss` / `undismiss` round-tripping and
# the underlying clast-dismissed-lib.bash helpers.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-dismiss"

CLAST_BIN="$PWD/bin/clast-plumbing"

A="11111111-1111-1111-1111-111111111111"
B="22222222-2222-2222-2222-222222222222"

# --- library-level round trip via clast_dismissed_remove --------------------

setup_test_journal >/dev/null
# Source the libs into the current shell (not a subshell) so assertion
# counts feed the summary. helpers.sh is already sourced.
export CLAST_LIB="$PWD/lib/clast"
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-dismissed-lib.bash
source lib/clast/clast-dismissed-lib.bash

clast_dismissed_add "$A" "reason one"
clast_dismissed_add "$B" "reason two"
clast_dismissed_add "$A" "duplicate record for A"

# Removing A drops every record for A and leaves B untouched.
removed="$(clast_dismissed_remove "$A")"; rc=$?
assert_eq "0" "$rc" "remove A: exit 0"
assert_eq "2" "$removed" "remove A: reports 2 records removed"

if clast_dismissed_check "$A"; then _clast_test_fail "A still dismissed after remove"; else _clast_test_pass "A no longer dismissed"; fi
if clast_dismissed_check "$B"; then _clast_test_pass "B still dismissed"; else _clast_test_fail "B wrongly removed"; fi

# Removing an id that isn't dismissed is a no-op returning 1 / count 0.
removed="$(clast_dismissed_remove "$A")"; rc=$?
assert_eq "1" "$rc" "remove A again: exit 1 (nothing to remove)"
assert_eq "0" "$removed" "remove A again: reports 0 removed"

teardown_test_journal

# --- malformed lines are preserved through a removal (regression) -----------

# A non-JSON line in the log must survive when an unrelated session is
# removed — the rewrite tolerates junk rather than silently deleting it.
setup_test_journal >/dev/null
dfile="$CLAST_JOURNAL_DIR/.dismissed.jsonl"
clast_dismissed_add "$A" "one"
printf 'CORRUPT NON-JSON LINE\n' >>"$dfile"
clast_dismissed_add "$B" "two"
removed="$(clast_dismissed_remove "$A")"; rc=$?
assert_eq "0" "$rc" "remove A (mixed log): exit 0"
assert_eq "1" "$removed" "remove A (mixed log): 1 record removed"
if grep -q 'CORRUPT NON-JSON LINE' "$dfile"; then _clast_test_pass "malformed line preserved"; else _clast_test_fail "malformed line dropped"; fi
if clast_dismissed_check "$B"; then _clast_test_pass "unrelated record B preserved"; else _clast_test_fail "record B dropped"; fi
teardown_test_journal

# --- a rewrite failure surfaces as an error, not a silent no-op -------------

# Make the journal dir read-only so mktemp(1) fails; a matching record is
# present, so this exercises the hard-error (exit 2) path rather than the
# "not dismissed" no-op (exit 1).
setup_test_journal >/dev/null
clast_dismissed_add "$A" "one"
chmod a-w "$CLAST_JOURNAL_DIR"
removed="$(clast_dismissed_remove "$A" 2>/dev/null)"; rc=$?
chmod u+w "$CLAST_JOURNAL_DIR"
assert_eq "2" "$rc" "rewrite failure returns exit 2"
if clast_dismissed_check "$A"; then _clast_test_pass "record intact after failed rewrite"; else _clast_test_fail "record lost after failed rewrite"; fi
teardown_test_journal

# --- CLI round trip: dismiss then undismiss ---------------------------------

setup_test_journal >/dev/null

# A dismissed session is undismissed cleanly (human output path).
"$CLAST_BIN" sessions dismiss "$A" --reason "cli test" >/dev/null 2>&1
out="$("$CLAST_BIN" --json sessions undismiss "$A" 2>/dev/null)"
assert_eq "1" "$(jq -r '.undismissed' <<<"$out")" "undismiss reports 1 restored (json)"

# Undismissing again restores nothing.
out="$("$CLAST_BIN" --json sessions undismiss "$A" 2>/dev/null)"
assert_eq "0" "$(jq -r '.undismissed' <<<"$out")" "undismiss again reports 0 restored"

teardown_test_journal

# --- CLI argument validation ------------------------------------------------

setup_test_journal >/dev/null
assert_exit_code 2 "$CLAST_BIN" sessions undismiss           # no ids
assert_exit_code 2 "$CLAST_BIN" sessions undismiss not-a-uuid
assert_exit_code 2 "$CLAST_BIN" sessions undismiss "$A" --bogus
teardown_test_journal

# --- porcelain `clast undismiss` passthrough --------------------------------

# The porcelain verb calls bare `clast-plumbing`, so put the repo bin on PATH.
CLAST_PORCELAIN="$PWD/bin/clast"
setup_test_journal >/dev/null
PATH="$PWD/bin:$PATH" "$CLAST_BIN" sessions dismiss "$A" >/dev/null 2>&1
out="$(PATH="$PWD/bin:$PATH" CLAST_JSON=1 "$CLAST_PORCELAIN" undismiss "$A" 2>/dev/null)"
assert_eq "1" "$(jq -r '.undismissed' <<<"$out")" "porcelain undismiss restores the session"
# Bad UUID still rejected (validation happens in plumbing).
assert_exit_code 2 env "PATH=$PWD/bin:$PATH" "$CLAST_PORCELAIN" undismiss not-a-uuid
teardown_test_journal

clast_test_summary
