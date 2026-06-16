#!/usr/bin/env bash
# test-snapshot.sh — `clast snapshot` integration suite.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-snapshot"

CLAST_BIN="$PWD/bin/clast-plumbing"

# --- empty fixture, default mode → silent no-op ------------------------------
setup_test_journal >/dev/null
# Don't even create a projects dir (delete the one setup made).
rm -rf "$CLAST_PROJECTS_DIR"
out="$("$CLAST_BIN" snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "empty: exits 0"
assert_eq "" "$out" "empty: stdout silent"
assert_file_not_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "empty: no manifest written"
assert_file_not_exists "$CLAST_JOURNAL_DIR/transcripts" "empty: no transcripts dir"
teardown_test_journal

# --- empty fixture, --json → valid JSON shape --------------------------------
setup_test_journal >/dev/null
rm -rf "$CLAST_PROJECTS_DIR"
out="$("$CLAST_BIN" --json snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "empty --json: exits 0"
if jq -e . >/dev/null 2>&1 <<<"$out"; then
  _clast_test_pass "empty --json: valid JSON"
else
  _clast_test_fail "empty --json: valid JSON (got: $out)"
fi
assert_eq "0" "$(jq '.captured | length' <<<"$out")" "empty --json: captured=[]"
assert_eq "0" "$(jq '.skipped' <<<"$out")" "empty --json: skipped=0"
assert_eq "0" "$(jq '.errors | length' <<<"$out")" "empty --json: errors=[]"
teardown_test_journal

# --- simple fixture: fresh capture -------------------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree simple
out="$("$CLAST_BIN" snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "simple fresh: exits 0"
case "$out" in
  *"Captured 2 session(s) across 2 project(s)"*) _clast_test_pass "simple fresh: summary line" ;;
  *) _clast_test_fail "simple fresh: summary line"; printf '%s\n' "$out" >&2 ;;
esac
files=$(find "$CLAST_JOURNAL_DIR/transcripts" -type f -name '*.jsonl' | wc -l | tr -d ' ')
assert_eq "2" "$files" "simple fresh: 2 transcript files"
lines=$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')
assert_eq "2" "$lines" "simple fresh: 2 manifest lines"

# --- simple fixture: idempotent re-run ---------------------------------------
out="$("$CLAST_BIN" snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "simple rerun: exits 0"
assert_eq "" "$out" "simple rerun: silent"
lines=$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')
assert_eq "2" "$lines" "simple rerun: manifest still 2 lines"

# --- simple fixture: mtime advance triggers re-capture -----------------------
one_src="$(find "$CLAST_PROJECTS_DIR" -type f -name '*.jsonl' | head -n1)"
touch -d "+1 minute" "$one_src"
out="$("$CLAST_BIN" snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "simple mtime: exits 0"
lines=$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')
assert_eq "3" "$lines" "simple mtime: manifest grew to 3 lines"
teardown_test_journal

# --- simple fixture, --dry-run: no writes ------------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree simple
out="$("$CLAST_BIN" snapshot --dry-run 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "dry-run: exits 0"
assert_file_not_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "dry-run: no manifest"
assert_file_not_exists "$CLAST_JOURNAL_DIR/transcripts" "dry-run: no transcripts dir"

# --dry-run --json still emits captured[]
out="$("$CLAST_BIN" --json snapshot --dry-run 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "dry-run --json: exits 0"
assert_eq "2" "$(jq '.captured | length' <<<"$out")" "dry-run --json: captured length 2"
assert_file_not_exists "$CLAST_JOURNAL_DIR/.manifest.jsonl" "dry-run --json: still no manifest"
teardown_test_journal

# --- multi-project: day-cutoff bucket math -----------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree_from multi-project/projects-tree
export CLAST_NOW_EPOCH; CLAST_NOW_EPOCH="$(date -d "2026-05-30T05:00:00Z" +%s)"
export CLAST_DAY_CUTOFF=04:00
# Freeze TZ so the cutoff math (local-date format) is deterministic across
# developer / CI timezones.
export TZ=UTC
out="$("$CLAST_BIN" --json snapshot 2>/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "multi: partial-failure exit (notes.jsonl errors)"
# With TZ=UTC and 04:00 cutoff: uuid-2's 2026-05-30T01:30Z first-line
# timestamp shifts back into the 2026-05-29 bucket; uuid-3's 10:00Z lands
# in 2026-05-30. uuid-2's exact path is the strongest assertion.
uuid2_day="$(jq -r '.captured[] | select(.session_id == "22222222-2222-4222-8222-222222222222") | .day_bucket' <<<"$out")"
assert_eq "2026-05-29" "$uuid2_day" "multi: uuid-2 cutoff-shifted into 2026-05-29 bucket"
uuid3_day="$(jq -r '.captured[] | select(.session_id == "33333333-3333-4333-8333-333333333333") | .day_bucket' <<<"$out")"
assert_eq "2026-05-30" "$uuid3_day" "multi: uuid-3 stays in 2026-05-30 bucket"
assert_eq "3" "$(jq '.captured | length' <<<"$out")" "multi: 3 valid captures"
assert_eq "1" "$(jq '.errors | length' <<<"$out")" "multi: 1 error (notes.jsonl)"
err_reason="$(jq -r '.errors[0].reason' <<<"$out")"
case "$err_reason" in
  *uuid*) _clast_test_pass "multi: error reason mentions uuid" ;;
  *) _clast_test_fail "multi: error reason mentions uuid (got: $err_reason)" ;;
esac
unset CLAST_NOW_EPOCH CLAST_DAY_CUTOFF TZ
teardown_test_journal

# --- multi-project: --include-segment limits scope ---------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree_from multi-project/projects-tree
out="$("$CLAST_BIN" --json snapshot --include-segment -tmp-proj-xesapps 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "include-segment: exits 0 (scratch + notes filtered)"
assert_eq "2" "$(jq '.captured | length' <<<"$out")" "include-segment: 2 captures"
assert_eq "0" "$(jq '.errors | length' <<<"$out")" "include-segment: 0 errors"
segs="$(jq -r '.captured[].snapshot' <<<"$out" | grep -c xesapps || true)"
assert_eq "2" "$segs" "include-segment: both captures under xesapps segment"
teardown_test_journal

# --- multi-project: --since skips older files --------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree_from multi-project/projects-tree
# Set distinct mtimes so --since filters by source mtime, not first-line ts.
touch -d "2026-05-29T12:00:00Z" "$CLAST_PROJECTS_DIR/-tmp-proj-xesapps/11111111-1111-4111-8111-111111111111.jsonl"
touch -d "2026-05-30T05:00:00Z" "$CLAST_PROJECTS_DIR/-tmp-proj-xesapps/22222222-2222-4222-8222-222222222222.jsonl"
touch -d "2026-05-30T11:00:00Z" "$CLAST_PROJECTS_DIR/-tmp-proj-scratch/33333333-3333-4333-8333-333333333333.jsonl"
touch -d "2026-05-30T11:00:00Z" "$CLAST_PROJECTS_DIR/-tmp-proj-scratch/notes.jsonl"
out="$("$CLAST_BIN" --json snapshot --since 2026-05-30T00:00:00Z 2>/dev/null)" && rc=$? || rc=$?
# notes.jsonl mtime is after --since, so it still errors → partial failure.
assert_eq "1" "$rc" "since: partial-failure exit"
assert_eq "2" "$(jq '.captured | length' <<<"$out")" "since: 2 captures (uuid-2, uuid-3)"
assert_eq "1" "$(jq '.skipped' <<<"$out")" "since: 1 skipped (uuid-1 below threshold)"
teardown_test_journal

# --- multi-project: slug grouping via registry -------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree_from multi-project/projects-tree
# Seed registry so segment -tmp-proj-xesapps resolves to slug "xesapps"
# via the /tmp/proj-xesapps candidate.
mkdir -p "$CLAST_JOURNAL_DIR"
jq -cn '{path:"/tmp/proj-xesapps",slug:"xesapps",first_seen:"2026-05-01",aliases:[]}' \
  >"$CLAST_JOURNAL_DIR/projects.json"
out="$("$CLAST_BIN" snapshot 2>/dev/null)" && rc=$? || rc=$?
# Errors → exit 1, but summary still printed.
assert_eq "1" "$rc" "grouping: partial-failure exit (notes.jsonl)"
case "$out" in
  *"xesapps: 2 session(s)"*) _clast_test_pass "grouping: xesapps slug labeled" ;;
  *) _clast_test_fail "grouping: xesapps slug labeled"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"-tmp-proj-scratch: 1 session(s)"*) _clast_test_pass "grouping: unregistered segment labeled" ;;
  *) _clast_test_fail "grouping: unregistered segment labeled"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- --help exits 0 ----------------------------------------------------------
out="$("$CLAST_BIN" snapshot --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "--help: exits 0"
case "$out" in
  *snapshot*--dry-run*) _clast_test_pass "--help: mentions snapshot and --dry-run" ;;
  *) _clast_test_fail "--help: mentions snapshot and --dry-run" ;;
esac

# --- unknown flag exits 2 ----------------------------------------------------
stderr="$("$CLAST_BIN" snapshot --no-such-flag 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "unknown flag: exits 2"
case "$stderr" in
  *--no-such-flag*) _clast_test_pass "unknown flag: stderr mentions it" ;;
  *) _clast_test_fail "unknown flag: stderr mentions it (got: $stderr)" ;;
esac

# --- --include-segment rejects non-segment value -----------------------------
stderr="$("$CLAST_BIN" snapshot --include-segment foo 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "include-segment bad value: exits 2"
case "$stderr" in
  *"must start with"*) _clast_test_pass "include-segment bad value: stderr explains" ;;
  *) _clast_test_fail "include-segment bad value: stderr explains (got: $stderr)" ;;
esac

# --- Corrupt-manifest abort: documented future hook (task 3) -----------------
# clast_manifest_iterate currently swallows malformed lines via fromjson?
# (manifest-lib), so the exit-4 path is unreachable today. Asserting the
# silent-tolerance behavior we DO have keeps this future hook honest.
setup_test_journal >/dev/null
make_fixture_projects_tree simple
printf '{ this is not json\n' >"$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" --json snapshot 2>/dev/null)" && rc=$? || rc=$?
case "$rc" in
  0|1|4) _clast_test_pass "corrupt manifest: known exit codes (got $rc)" ;;
  *) _clast_test_fail "corrupt manifest: unexpected exit ($rc)" ;;
esac
teardown_test_journal

clast_test_summary
