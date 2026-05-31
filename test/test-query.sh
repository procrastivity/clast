#!/usr/bin/env bash
# test-query.sh — `clast projects` / `clast sessions` / `clast show`
# integration suite. Subprocess-style: runs bin/clast against the
# multi-project journal-seed fixture.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-query"

CLAST_BIN="$PWD/bin/clast"
FROZEN_EPOCH=$(date -d "2026-05-30T12:00:00Z" +%s)

_seed_journal() {
  setup_test_journal >/dev/null
  make_fixture_journal_seed_from "multi-project/journal-seed"
}

# === clast projects ==========================================================

# --- defaults to today ------------------------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects default: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "projects default: 2 rows (today)"
slugs="$(jq -r 'sort_by(.segment) | .[].segment' <<<"$out" | tr '\n' ' ')"
assert_eq "-tmp-proj-scratch -tmp-proj-xesapps " "$slugs" "projects default: scratch + xesapps segments"
teardown_test_journal

# --- --day 2026-05-29 -------------------------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects --day 2026-05-29 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects --day 2026-05-29: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "projects --day 2026-05-29: 1 row"
assert_eq "xesapps" "$(jq -r '.[0].slug' <<<"$out")" "projects --day 2026-05-29: slug=xesapps"
assert_eq "1" "$(jq -r '.[0].session_count' <<<"$out")" "projects --day 2026-05-29: session_count=1"
# default mode last_active HH:MM
human="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" projects --day 2026-05-29 2>/dev/null)"
case "$human" in
  *"11:48"*) _clast_test_pass "projects --day 2026-05-29: last_active rendered as HH:MM" ;;
  *) _clast_test_fail "projects --day 2026-05-29: last_active rendered as HH:MM"; printf '%s\n' "$human" >&2 ;;
esac
teardown_test_journal

# --- --since/--until window (multi-day) ------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects --since 2026-05-22 --until 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects --since/--until: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "projects --since/--until: 2 distinct projects"
human="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" projects --since 2026-05-22 --until 2026-05-30 2>/dev/null)"
case "$human" in
  *"2026-05-30 14:30"*) _clast_test_pass "projects multi-day: last_active rendered as YYYY-MM-DD HH:MM" ;;
  *) _clast_test_fail "projects multi-day: last_active rendered as YYYY-MM-DD HH:MM"; printf '%s\n' "$human" >&2 ;;
esac
teardown_test_journal

# --- --day --json schema check ---------------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects --day 2026-05-30 --json: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "projects --day 2026-05-30 --json: length 2"
# Each row has all eight documented keys (slug, path, segment, remote,
# session_count, msg_count_approx, last_active, registered).
if jq -e '
  .[0] as $r
  | ["slug","path","segment","remote","session_count","msg_count_approx","last_active","registered"]
  | all(. as $k | $r | has($k))
' <<<"$out" >/dev/null; then
  _clast_test_pass "projects --json: row has all eight documented keys"
else
  _clast_test_fail "projects --json: row has all eight documented keys"
  jq -r '.[0] | keys_unsorted | join(",")' <<<"$out" >&2
fi
# last_active is full ISO Z
la="$(jq -r '.[0].last_active' <<<"$out")"
case "$la" in
  ????-??-??T??:??:??Z) _clast_test_pass "projects --json: last_active is ISO 8601 Z" ;;
  *) _clast_test_fail "projects --json: last_active is ISO 8601 Z (got: $la)" ;;
esac
teardown_test_journal

# --- --unregistered --------------------------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects --unregistered 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects --unregistered: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "projects --unregistered: 1 row"
assert_eq "-tmp-proj-scratch" "$(jq -r '.[0].segment' <<<"$out")" "projects --unregistered: scratch segment"
assert_eq "false" "$(jq -r '.[0].registered' <<<"$out")" "projects --unregistered: registered=false"
teardown_test_journal

# --- mutual exclusion ------------------------------------------------------
_seed_journal
err="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" projects --day today --since 2026-05-01 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "projects --day + --since: exits 2"
case "$err" in
  *"mutually exclusive"*) _clast_test_pass "projects --day + --since: stderr mentions mutual exclusion" ;;
  *) _clast_test_fail "projects --day + --since: stderr mentions mutual exclusion"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- empty manifest --------------------------------------------------------
setup_test_journal >/dev/null
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects empty: exits 0"
assert_eq "[]" "$out" "projects empty --json: []"
teardown_test_journal

# === clast sessions ==========================================================

# --- default: today (two sessions, sorted by start asc) --------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "sessions default: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "sessions default: 2 rows"
assert_eq "33333333-3333-4333-8333-333333333333" "$(jq -r '.[0].session_id' <<<"$out")" "sessions default: first sorted by start"
assert_eq "22222222-2222-4222-8222-222222222222" "$(jq -r '.[1].session_id' <<<"$out")" "sessions default: second sorted by start"
teardown_test_journal

# --- --project xesapps --since/--until -------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions --project xesapps --since 2026-05-22 --until 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "sessions --project xesapps window: exits 0"
assert_eq "3" "$(jq 'length' <<<"$out")" "sessions --project xesapps window: 3 rows"
# Scratch should be absent.
has_scratch="$(jq -r 'map(select(.segment == "-tmp-proj-scratch")) | length' <<<"$out")"
assert_eq "0" "$has_scratch" "sessions --project xesapps: scratch excluded"
teardown_test_journal

# --- --project unknown-slug -------------------------------------------------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions --project unknown-slug 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "sessions --project unknown-slug: exits 0"
assert_eq "[]" "$out" "sessions --project unknown-slug: []"
teardown_test_journal

# --- --json schema (documented fields, curated false, branch null) --------
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions 2>/dev/null)"
assert_eq "false" "$(jq -r '.[0].curated' <<<"$out")" "sessions --json: curated=false"
assert_eq "null" "$(jq -r '.[0].branch' <<<"$out")" "sessions --json: branch=null"
for k in session_id project segment branch start end msg_count_approx snapshot_path day_bucket curated; do
  present="$(jq --arg k "$k" '.[0] | has($k)' <<<"$out")"
  assert_eq "true" "$present" "sessions --json: row has $k"
done
teardown_test_journal

# === clast show =============================================================

# --- known session (default) ----------------------------------------------
_seed_journal
out="$("$CLAST_BIN" show 22222222-2222-4222-8222-222222222222 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "show known: exits 0"
case "$out" in
  *"session_id:"*"22222222-2222-4222-8222-222222222222"*) _clast_test_pass "show known: session_id line" ;;
  *) _clast_test_fail "show known: session_id line"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"project:"*"xesapps"*) _clast_test_pass "show known: project line" ;;
  *) _clast_test_fail "show known: project line" ;;
esac
case "$out" in
  *"snapshot:"*) _clast_test_pass "show known: snapshot line" ;;
  *) _clast_test_fail "show known: snapshot line" ;;
esac
case "$out" in
  *"first_prompt:"*"first xesapps prompt"*) _clast_test_pass "show known: first_prompt line" ;;
  *) _clast_test_fail "show known: first_prompt line" ;;
esac
case "$out" in
  *"last_prompt:"*"last xesapps prompt"*) _clast_test_pass "show known: last_prompt line" ;;
  *) _clast_test_fail "show known: last_prompt line" ;;
esac

# --- --full --turns 1 ------------------------------------------------------
out="$("$CLAST_BIN" show 22222222-2222-4222-8222-222222222222 --full --turns 1 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "show --full --turns 1: exits 0"
case "$out" in
  *"## First 1 turns"*) _clast_test_pass "show --full: First N turns section" ;;
  *) _clast_test_fail "show --full: First N turns section" ;;
esac
case "$out" in
  *"## Last 1 turns"*) _clast_test_pass "show --full: Last N turns section" ;;
  *) _clast_test_fail "show --full: Last N turns section" ;;
esac

# --- --json --full --turns 1 -----------------------------------------------
out="$("$CLAST_BIN" --json show 22222222-2222-4222-8222-222222222222 --full --turns 1 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "show --json --full: exits 0"
if jq -e . >/dev/null 2>&1 <<<"$out"; then
  _clast_test_pass "show --json --full: valid JSON"
else
  _clast_test_fail "show --json --full: valid JSON"
fi
assert_eq "1" "$(jq '.first_turns | length' <<<"$out")" "show --json --full: first_turns length 1"
assert_eq "1" "$(jq '.last_turns | length' <<<"$out")" "show --json --full: last_turns length 1"
assert_eq "user" "$(jq -r '.first_turns[0].role' <<<"$out")" "show --json --full: first turn role"
teardown_test_journal

# --- unknown UUID ----------------------------------------------------------
_seed_journal
err="$("$CLAST_BIN" show 00000000-0000-0000-0000-000000000000 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "show unknown: exits 1"
case "$err" in
  *"not found in manifest"*) _clast_test_pass "show unknown: stderr mentions not found" ;;
  *) _clast_test_fail "show unknown: stderr mentions not found"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- orphan (manifest line exists, file missing) ---------------------------
_seed_journal
err="$("$CLAST_BIN" show dddddddd-dddd-4ddd-8ddd-dddddddddddd 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "show orphan: exits 1"
case "$err" in
  *"clast doctor"*) _clast_test_pass "show orphan: stderr mentions clast doctor" ;;
  *) _clast_test_fail "show orphan: stderr mentions clast doctor"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- bad UUID format -------------------------------------------------------
_seed_journal
err="$("$CLAST_BIN" show not-a-uuid 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "show bad UUID: exits 2"
case "$err" in
  *"UUID"*) _clast_test_pass "show bad UUID: stderr mentions UUID" ;;
  *) _clast_test_fail "show bad UUID: stderr mentions UUID"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- --help / unknown flag for each subcommand -----------------------------
for sub in projects sessions show; do
  out="$("$CLAST_BIN" "$sub" --help 2>/dev/null)" && rc=$? || rc=$?
  assert_eq "0" "$rc" "$sub --help: exits 0"
  case "$out" in
    *"$sub"*) _clast_test_pass "$sub --help: usage mentions subcommand" ;;
    *) _clast_test_fail "$sub --help: usage mentions subcommand" ;;
  esac
done

for sub in projects sessions show; do
  err="$("$CLAST_BIN" "$sub" --bogus 2>&1 >/dev/null)" && rc=$? || rc=$?
  assert_eq "2" "$rc" "$sub --bogus: exits 2"
  case "$err" in
    *"$sub"*) _clast_test_pass "$sub --bogus: stderr mentions subcommand" ;;
    *) _clast_test_fail "$sub --bogus: stderr mentions subcommand"; printf '%s\n' "$err" >&2 ;;
  esac
done

clast_test_summary
