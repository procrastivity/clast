#!/usr/bin/env bash
# test-stats.sh — `clast stats` integration suite.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-stats"

CLAST_BIN="$PWD/bin/clast-plumbing"
export TZ=UTC
FROZEN_EPOCH=$(date -u -d "2026-05-30T12:00:00Z" +%s)
export CLAST_NOW_EPOCH="$FROZEN_EPOCH"

_seed_journal() {
  setup_test_journal >/dev/null
  make_fixture_journal_seed_from "multi-project/journal-seed"
}

# --- default (today) -------------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" stats 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "stats default: exits 0"
case "$out" in
  *"Window:      2026-05-30 (today)"*) _clast_test_pass "stats default: window today" ;;
  *) _clast_test_fail "stats default: window today"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"Projects:    2"*) _clast_test_pass "stats default: projects=2" ;;
  *) _clast_test_fail "stats default: projects=2"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"Sessions:    2"*) _clast_test_pass "stats default: sessions=2" ;;
  *) _clast_test_fail "stats default: sessions=2"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"Curated:     0 of 2 sessions (0%)"*) _clast_test_pass "stats default: curated 0/2" ;;
  *) _clast_test_fail "stats default: curated 0/2"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"Breadcrumbs: 0 across 0 projects"*) _clast_test_pass "stats default: breadcrumbs 0/0" ;;
  *) _clast_test_fail "stats default: breadcrumbs 0/0"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- --json default --------------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" --json stats 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "stats --json: exits 0"
assert_eq "today" "$(jq -r '.window.label' <<<"$out")" "stats --json: window.label=today"
assert_eq "2026-05-30" "$(jq -r '.window.start' <<<"$out")" "stats --json: window.start"
assert_eq "2026-05-30" "$(jq -r '.window.end' <<<"$out")" "stats --json: window.end"
assert_eq "2" "$(jq -r '.projects' <<<"$out")" "stats --json: projects=2"
assert_eq "2" "$(jq -r '.sessions' <<<"$out")" "stats --json: sessions=2"
assert_eq "8" "$(jq -r '.messages_approx' <<<"$out")" "stats --json: messages_approx=8"
assert_eq "800" "$(jq -r '.bytes' <<<"$out")" "stats --json: bytes=800"
assert_eq "0" "$(jq -r '.curated' <<<"$out")" "stats --json: curated=0"
assert_eq "0" "$(jq -r '.breadcrumbs' <<<"$out")" "stats --json: breadcrumbs=0"
teardown_test_journal

# --- --day yesterday --------------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" stats --day yesterday 2>/dev/null)"
case "$out" in
  *"Window:      2026-05-29 (yesterday)"*) _clast_test_pass "stats --day yesterday: window" ;;
  *) _clast_test_fail "stats --day yesterday: window"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"Sessions:    1"*) _clast_test_pass "stats --day yesterday: sessions=1" ;;
  *) _clast_test_fail "stats --day yesterday: sessions=1" ;;
esac
case "$out" in
  *"Projects:    1"*) _clast_test_pass "stats --day yesterday: projects=1" ;;
  *) _clast_test_fail "stats --day yesterday: projects=1" ;;
esac
teardown_test_journal

# --- --day arbitrary ISO ----------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" --json stats --day 2026-05-22 2>/dev/null)"
assert_eq "1" "$(jq -r '.sessions' <<<"$out")" "stats --day 2026-05-22: sessions=1"
assert_eq "1" "$(jq -r '.projects' <<<"$out")" "stats --day 2026-05-22: projects=1"
teardown_test_journal

# --- --since/--until multi-day (explicit) -----------------------------------
_seed_journal
out="$("$CLAST_BIN" stats --since 2026-05-15 --until 2026-05-30 2>/dev/null)"
case "$out" in
  *"Window:      2026-05-15..2026-05-30"*) _clast_test_pass "stats explicit window: header" ;;
  *) _clast_test_fail "stats explicit window: header"; printf '%s\n' "$out" >&2 ;;
esac
# No "(through today)" suffix when --until is explicit.
case "$out" in
  *"(through today)"*) _clast_test_fail "stats explicit --until: no through-today suffix" ;;
  *) _clast_test_pass "stats explicit --until: no through-today suffix" ;;
esac
case "$out" in
  *"Sessions:    5"*) _clast_test_pass "stats explicit window: sessions=5" ;;
  *) _clast_test_fail "stats explicit window: sessions=5" ;;
esac
teardown_test_journal

# --- --since only -----------------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" stats --since 2026-05-29 2>/dev/null)"
case "$out" in
  *"Window:      2026-05-29..2026-05-30 (through today)"*)
    _clast_test_pass "stats --since only: through-today suffix" ;;
  *) _clast_test_fail "stats --since only: through-today suffix"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- --day + --since (exit 2) -----------------------------------------------
_seed_journal
( "$CLAST_BIN" stats --day 2026-05-30 --since 2026-05-29 ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "stats --day+--since: exit 2"
teardown_test_journal

# --- --since > --until (exit 2) ---------------------------------------------
_seed_journal
( "$CLAST_BIN" stats --since 2026-05-30 --until 2026-05-29 ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "stats --since>--until: exit 2"
teardown_test_journal

# --- --project xesapps ------------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" --json stats --since 2026-05-15 --until 2026-05-30 --project xesapps 2>/dev/null)"
assert_eq "4" "$(jq -r '.sessions' <<<"$out")" "stats --project xesapps: sessions=4"
assert_eq "1" "$(jq -r '.projects' <<<"$out")" "stats --project xesapps: projects=1"
teardown_test_journal

# --- --project unknown -----------------------------------------------------
_seed_journal
( "$CLAST_BIN" stats --project not-a-real-slug ) >/dev/null 2>&1; rc=$?
assert_eq "1" "$rc" "stats --project unknown: exit 1"
out="$("$CLAST_BIN" --json stats --project not-a-real-slug 2>/dev/null || true)"
assert_eq "1" "$(jq -r '.code' <<<"$out")" "stats --project unknown --json: code=1"
teardown_test_journal

# --- empty window (exit 0) --------------------------------------------------
_seed_journal
out="$("$CLAST_BIN" --json stats --day 2026-01-01 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "stats empty window: exit 0"
assert_eq "0" "$(jq -r '.sessions' <<<"$out")" "stats empty window: sessions=0"
assert_eq "0" "$(jq -r '.bytes' <<<"$out")" "stats empty window: bytes=0"
teardown_test_journal

# --- missing manifest -------------------------------------------------------
setup_test_journal >/dev/null
out="$("$CLAST_BIN" --json stats 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "stats missing manifest: exit 0"
assert_eq "0" "$(jq -r '.sessions' <<<"$out")" "stats missing manifest: sessions=0"
teardown_test_journal

# --- bytes_human rendering --------------------------------------------------
setup_test_journal >/dev/null
printf '%s\n' '{"session_id":"99999999-9999-4999-8999-999999999999","source":"/tmp/x.jsonl","snapshot":"transcripts/2026-05-30/-tmp-x/99999999-9999-4999-8999-999999999999.jsonl","captured_at":"2026-05-30T12:00:00Z","source_mtime":"2026-05-30T11:00:00Z","source_size":1572864,"day_bucket":"2026-05-30"}' \
  > "$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" --json stats 2>/dev/null)"
assert_eq "1572864" "$(jq -r '.bytes' <<<"$out")" "stats bytes_human: bytes raw"
assert_eq "1.5 MB" "$(jq -r '.bytes_human' <<<"$out")" "stats bytes_human: 1.5 MB"
out="$("$CLAST_BIN" stats 2>/dev/null)"
case "$out" in
  *"Bytes:       1.5 MB"*) _clast_test_pass "stats bytes default: 1.5 MB" ;;
  *) _clast_test_fail "stats bytes default: 1.5 MB"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- curated count ----------------------------------------------------------
_seed_journal
make_fixture_entries_seed_from "multi-project/entries-seed"
out="$("$CLAST_BIN" --json stats --since 2026-05-15 --until 2026-05-30 2>/dev/null)"
assert_eq "3" "$(jq -r '.curated' <<<"$out")" "stats curated: 3"
# 3/5 = 60%.
assert_eq "60" "$(jq -r '.curated_pct' <<<"$out")" "stats curated_pct: 60"
teardown_test_journal

# --- breadcrumb count -------------------------------------------------------
_seed_journal
mkdir -p "$CLAST_JOURNAL_DIR/breadcrumbs"
printf 'note\n' > "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md"
printf 'note\n' > "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md"
printf 'note\n' > "$CLAST_JOURNAL_DIR/breadcrumbs/2026-04-01-old.md"
out="$("$CLAST_BIN" --json stats 2>/dev/null)"
assert_eq "2" "$(jq -r '.breadcrumbs' <<<"$out")" "stats breadcrumbs: 2 in window"
assert_eq "2" "$(jq -r '.breadcrumb_projects' <<<"$out")" "stats breadcrumb_projects: 2"
teardown_test_journal

# --- --help / unknown flag --------------------------------------------------
( "$CLAST_BIN" stats --help ) >/dev/null 2>&1; rc=$?
assert_eq "0" "$rc" "stats --help: exit 0"
( "$CLAST_BIN" stats --nope ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "stats --nope: exit 2"
( "$CLAST_BIN" stats junk ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "stats positional: exit 2"

clast_test_summary
