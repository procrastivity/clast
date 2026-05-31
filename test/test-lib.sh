#!/usr/bin/env bash
# test-lib.sh — exercises clast-lib.bash.
set -euo pipefail

cd "$(dirname "$0")/.." || exit 1

_CLAST_TEST_NAME="test-lib"
# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash

# --- journal_dir / projects_dir env override ---------------------------------
CLAST_JOURNAL_DIR="/tmp/clast-override-journal" assert_eq \
  "/tmp/clast-override-journal" "$(CLAST_JOURNAL_DIR=/tmp/clast-override-journal clast_journal_dir)" \
  "clast_journal_dir respects env"

unset_journal_default() {
  unset CLAST_JOURNAL_DIR
  clast_journal_dir
}
assert_eq "$HOME/.claude/journal" "$(unset_journal_default)" "clast_journal_dir default"

# --- clast_today returns ISO date --------------------------------------------
today_out="$(clast_today)"
if [[ "$today_out" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  _clast_test_pass "clast_today shape ($today_out)"
else
  _clast_test_fail "clast_today shape ($today_out)"
fi

# --- clast_today with CLAST_DAY_CUTOFF=04:00 at 01:00 local = yesterday ------
# Construct an epoch for "today at 01:00 local" using GNU date. clast_today
# subtracts the cutoff in seconds; with cutoff 04:00 and now=01:00, adjusted
# epoch lands on yesterday.
fixed_now="$(date -d "today 01:00" +%s)"
expected_yesterday="$(date -d "yesterday" +%Y-%m-%d)"
got="$(CLAST_NOW_EPOCH="$fixed_now" CLAST_DAY_CUTOFF="04:00" clast_today)"
assert_eq "$expected_yesterday" "$got" "01:00 with 04:00 cutoff → yesterday"

# Same time, default cutoff is 04:00, so explicit and default agree.
got_default="$(CLAST_NOW_EPOCH="$fixed_now" clast_today)"
assert_eq "$expected_yesterday" "$got_default" "default cutoff matches 04:00"

# Cutoff 00:00 at 01:00 should give today.
expected_today="$(date -d "today" +%Y-%m-%d)"
got_zero="$(CLAST_NOW_EPOCH="$fixed_now" CLAST_DAY_CUTOFF="00:00" clast_today)"
assert_eq "$expected_today" "$got_zero" "00:00 cutoff stays on today"

# --- clast_parse_date --------------------------------------------------------
assert_eq "$(clast_today)" "$(clast_parse_date today)" "parse_date today"
assert_eq "$expected_yesterday" "$(CLAST_NOW_EPOCH="$fixed_now" CLAST_DAY_CUTOFF="04:00" clast_parse_date today)" "parse_date today with frozen clock"

# Note: in a real shell, parse_date yesterday is "today - 1 day"; we
# compare it with offset -1d using the same frozen clock.
y1="$(clast_parse_date yesterday)"
y2="$(clast_parse_date -1d)"
assert_eq "$y1" "$y2" "parse_date yesterday == parse_date -1d"

assert_eq "2026-01-15" "$(clast_parse_date 2026-01-15)" "parse_date ISO passthrough"

assert_exit_code 2 clast_parse_date invalid
assert_exit_code 2 clast_parse_date ""

# -1w / last-week
lw1="$(clast_parse_date last-week)"
lw2="$(clast_parse_date -1w)"
assert_eq "$lw1" "$lw2" "last-week == -1w"

# --- clast_atomic_write ------------------------------------------------------
tmpdir="$(mktemp -d -t clast.atomic.XXXXXX)"
trap 'rm -rf "$tmpdir"' EXIT

target="$tmpdir/file.txt"
clast_atomic_write "$target" "hello world"
assert_file_exists "$target"
assert_eq "hello world" "$(cat "$target")" "atomic_write content"

# Failure case: write into a non-writable destination directory. The
# original file (if present) must survive untouched.
echo "original" >"$tmpdir/keep.txt"
ro_dir="$tmpdir/ro"
mkdir -p "$ro_dir"
echo "preserved" >"$ro_dir/file.txt"
chmod 555 "$ro_dir"
# Trying to write into the read-only dir should fail without clobbering.
if clast_atomic_write "$ro_dir/file.txt" "new content" 2>/dev/null; then
  _clast_test_fail "atomic_write should fail on read-only dir"
else
  _clast_test_pass "atomic_write fails on read-only dir"
fi
chmod 755 "$ro_dir"
assert_eq "preserved" "$(cat "$ro_dir/file.txt")" "original survives failed write"

# --- clast_version -----------------------------------------------------------
ver="$(clast_version)"
if [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  _clast_test_pass "clast_version shape ($ver)"
else
  _clast_test_fail "clast_version shape ($ver)"
fi
# Cached call returns identical value.
assert_eq "$ver" "$(clast_version)" "clast_version cached"

clast_test_summary
