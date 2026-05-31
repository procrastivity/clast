#!/usr/bin/env bash
# test-manifest.sh — exercises clast-manifest-lib.bash.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

_CLAST_TEST_NAME="test-manifest"
# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
source lib/clast/clast-manifest-lib.bash

SID_A="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
SID_FIXTURE_TWO="11111111-1111-1111-1111-111111111111"
SID_FIXTURE_ONE="22222222-2222-2222-2222-222222222222"

# --- append round-trip ------------------------------------------------------
setup_test_journal >/dev/null
export CLAST_NOW_EPOCH=1748606400 # 2025-05-30T12:00:00Z
clast_manifest_append \
  "$SID_A" \
  "/tmp/proj/$SID_A.jsonl" \
  "transcripts/2025-05-30/-tmp-proj/$SID_A.jsonl" \
  "2025-05-30T11:55:00Z" \
  "1234" \
  "2025-05-30"
assert_file_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "append creates manifest"
line="$(clast_manifest_lookup "$SID_A")"
assert_eq "$SID_A" "$(jq -r '.session_id' <<<"$line")" "lookup session_id"
assert_eq "1234" "$(jq -r '.source_size' <<<"$line")" "lookup source_size value"
assert_eq "number" "$(jq -r '.source_size | type' <<<"$line")" "source_size is numeric"
assert_eq "2025-05-30T12:00:00Z" "$(jq -r '.captured_at' <<<"$line")" "captured_at honors CLAST_NOW_EPOCH"
assert_eq "2025-05-30" "$(jq -r '.day_bucket' <<<"$line")" "day_bucket round-trip"
unset CLAST_NOW_EPOCH
teardown_test_journal

# --- append arity check -----------------------------------------------------
setup_test_journal >/dev/null
err="$(clast_manifest_append a b c 2>&1)" && rc=$? || rc=$?
assert_eq "2" "$rc" "append wrong arity returns 2"
case "$err" in
  *expected\ 6\ args*) _clast_test_pass "append arity error message" ;;
  *) _clast_test_fail "append arity error message (got: $err)" ;;
esac
teardown_test_journal

# --- lookup missing manifest -------------------------------------------------
setup_test_journal >/dev/null
out="$(clast_manifest_lookup "$SID_A")" && rc=$? || rc=$?
assert_eq "1" "$rc" "lookup missing manifest returns 1"
assert_eq "" "$out" "lookup missing manifest prints nothing"
teardown_test_journal

# --- lookup most-recent-wins (fixture) --------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
line="$(clast_manifest_lookup "$SID_FIXTURE_TWO")"
assert_eq "2026-05-30T18:00:00Z" "$(jq -r '.captured_at' <<<"$line")" "most-recent-wins for SID with two lines"
assert_eq "2048" "$(jq -r '.source_size' <<<"$line")" "newer line's source_size returned"
teardown_test_journal

# --- lookup skips malformed lines -------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
line="$(clast_manifest_lookup "$SID_FIXTURE_ONE")"
assert_eq "$SID_FIXTURE_ONE" "$(jq -r '.session_id' <<<"$line")" "lookup finds valid line past garbage"
teardown_test_journal

# --- has-capture true / false -----------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
assert_exit_code 0 clast_manifest_has_capture "$SID_FIXTURE_TWO" "2026-05-30T17:55:00Z"
assert_exit_code 0 clast_manifest_has_capture "$SID_FIXTURE_TWO" "2026-05-30T09:55:00Z"
assert_exit_code 1 clast_manifest_has_capture "$SID_FIXTURE_TWO" "1999-01-01T00:00:00Z"
assert_exit_code 1 clast_manifest_has_capture "nope-no-such-session" "2026-05-30T17:55:00Z"
teardown_test_journal

# --- has-capture against missing manifest -----------------------------------
setup_test_journal >/dev/null
assert_exit_code 1 clast_manifest_has_capture "$SID_A" "2026-05-30T17:55:00Z"
teardown_test_journal

# --- iterate filters --------------------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
out="$(clast_manifest_iterate '.day_bucket == "2026-05-30"')"
count="$(printf '%s\n' "$out" | grep -c .)"
assert_eq "2" "$count" "iterate filters day_bucket 2026-05-30 (2 valid lines)"
out="$(clast_manifest_iterate '.day_bucket == "2026-05-29"')"
count="$(printf '%s\n' "$out" | grep -c .)"
assert_eq "1" "$count" "iterate filters day_bucket 2026-05-29 (1 valid line)"
teardown_test_journal

# --- iterate skips malformed lines (all valid lines counted, garbage ignored) -
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
total="$(clast_manifest_iterate '.session_id != null' | grep -c .)"
assert_eq "3" "$total" "iterate yields 3 valid lines, skips 2 malformed"
teardown_test_journal

# --- iterate against missing manifest ----------------------------------------
setup_test_journal >/dev/null
out="$(clast_manifest_iterate '.day_bucket == "2026-05-30"')" && rc=$? || rc=$?
assert_eq "0" "$rc" "iterate on missing manifest returns 0"
assert_eq "" "$out" "iterate on missing manifest prints nothing"
teardown_test_journal

# --- rebuild produces parseable manifest -------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree corrupt-manifest
rm -f "$CLAST_JOURNAL_DIR/.manifest.jsonl"
CLAST_QUIET=1 clast_manifest_rebuild_from_disk
assert_file_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "rebuild writes manifest"
# Every line must be valid JSON.
bad="$(grep -cv '^{' "$CLAST_JOURNAL_DIR/.manifest.jsonl" || true)"
assert_eq "0" "$bad" "rebuilt manifest has no non-JSON lines"
# One line per snapshot file (the fixture has 2).
nlines="$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')"
assert_eq "2" "$nlines" "rebuilt manifest has one line per snapshot file"
# iterate '.' yields every line.
iterated="$(clast_manifest_iterate '.' | grep -c .)"
assert_eq "2" "$iterated" "iterate '.' yields one entry per snapshot"
# Lossy fields are encoded as documented.
source_null_count="$(jq -c 'select(.source == null)' "$CLAST_JOURNAL_DIR/.manifest.jsonl" | grep -c .)"
assert_eq "2" "$source_null_count" "rebuild sets source=null"
zero_size_count="$(jq -c 'select(.source_size == 0)' "$CLAST_JOURNAL_DIR/.manifest.jsonl" | grep -c .)"
assert_eq "2" "$zero_size_count" "rebuild sets source_size=0"
teardown_test_journal

# --- rebuild empty transcripts tree -----------------------------------------
setup_test_journal >/dev/null
CLAST_QUIET=1 clast_manifest_rebuild_from_disk
assert_file_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "rebuild on empty tree writes (empty) manifest"
nlines="$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')"
assert_eq "0" "$nlines" "rebuild on empty tree produces 0 lines"
teardown_test_journal

# --- double-source guard -----------------------------------------------------
# Re-sourcing must not redefine functions or error out.
# shellcheck source=lib/clast/clast-manifest-lib.bash
source lib/clast/clast-manifest-lib.bash
_clast_test_pass "double-source is idempotent"

clast_test_summary
