#!/usr/bin/env bash
# test-retro-manifest.sh — exercises clast_retro_manifest (Round 1, step-02:
# work-day bucketing + session dedup). Unit style: sources the lib and calls
# the function against a fixture-seeded temp journal.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-retro-lib.bash
source lib/clast/clast-retro-lib.bash
_CLAST_TEST_NAME="test-retro-manifest"

# Deterministic bucket math: UTC + the default 04:00 cutoff.
export TZ=UTC
export CLAST_DAY_CUTOFF=04:00

SID_X="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"  # cross-midnight (06-12 + 06-13)
SID_B="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"  # mtime fallback → 06-15
SID_C="cccccccc-cccc-4ccc-8ccc-cccccccccccc"  # both missing → unknown
SID_E="eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"  # diverge: filed 06-20, work 06-14
# (SID_D dddddddd…, null project on 06-14, is asserted by project grouping below.)

_seed() {
  setup_test_journal >/dev/null
  make_fixture_entries_seed_from "retro-manifest/entries-seed"
}

# session object for a given session_id (searches every day/project).
_session() { jq -c --arg s "$1" '[.days[].projects[].sessions[] | select(.session_id == $s)][0]' <<<"$2"; }

_seed
full="$(clast_retro_manifest)"

# --- envelope --------------------------------------------------------------
assert_eq "work-days" "$(jq -r '.window' <<<"$full")" "default window is work-days"
assert_eq "null" "$(jq -c '.from' <<<"$full")" "unbounded: from null"
assert_eq "null" "$(jq -c '.to' <<<"$full")" "unbounded: to null"

# --- cross-midnight dedup: later day wins, both paths merged ----------------
sx="$(_session "$SID_X" "$full")"
assert_eq "2026-06-13" "$(jq -r '.work_day' <<<"$sx")" "cross-midnight: resolved to later day"
assert_eq "2" "$(jq '.entries | length' <<<"$sx")" "cross-midnight: both entries merged"
assert_eq "true" "$(jq -c '.entries == (.entries | sort)' <<<"$sx")" "cross-midnight: entries sorted"
# The merged session lives under day 06-13, not 06-12.
assert_eq "1" "$(jq '[.days[] | select(.day == "2026-06-13")] | length' <<<"$full")" "06-13 day present"
assert_eq "0" "$(jq '[.days[] | select(.day == "2026-06-12")] | length' <<<"$full")" "06-12 has no leftover (merged up to 06-13)"

# --- mtime fallback --------------------------------------------------------
assert_eq "2026-06-15" "$(jq -r '.work_day' <<<"$(_session "$SID_B" "$full")")" "fallback: work day from curated_source_mtime"

# --- both missing → unknown bucket, never dropped --------------------------
assert_eq "unknown" "$(jq -r '.work_day' <<<"$(_session "$SID_C" "$full")")" "both missing: work day unknown"
assert_eq "1" "$(jq '[.days[] | select(.day == "unknown")] | length' <<<"$full")" "unknown day bucketed"
# unknown sorts last among days.
assert_eq "unknown" "$(jq -r '.days[-1].day' <<<"$full")" "unknown day sorts last"

# --- divergence: bucketed by work day, not filename ------------------------
assert_eq "2026-06-14" "$(jq -r '.work_day' <<<"$(_session "$SID_E" "$full")")" "diverge: work day 06-14 (filed 06-20)"

# --- grouping: null project group sorts last within a day ------------------
proj_paths="$(jq -c '[.days[] | select(.day == "2026-06-14") | .projects[].project_path]' <<<"$full")"
assert_eq '["/tmp/projA",null]' "$proj_paths" "06-14: projA before null project"

# --- friendly project names (step-05) --------------------------------------
# Unit cases with HOME pinned so tilde/last-two collapse is deterministic.
_friendly() { HOME=/home/tester bash -c '
  source lib/clast/clast-lib.bash; source lib/clast/clast-retro-lib.bash
  clast_retro_friendly_name "$1"' _ "$1"; }
assert_eq "~"            "$(_friendly /home/tester)"                          "friendly: home -> ~"
assert_eq "dev/xesapps"  "$(_friendly /home/tester/Workspaces/dev/xesapps)"  "friendly: deep home -> last two"
# shellcheck disable=SC2088  # literal "~/…" is the expected display string
assert_eq "~/Code/clast" "$(_friendly /home/tester/Code/clast)"              "friendly: 2-comp home -> ~/rest"
# shellcheck disable=SC2088
assert_eq "~/fix"        "$(_friendly /home/tester/fix)"                      "friendly: 1-comp home -> ~/rest"
assert_eq "dev/xesapps"  "$(_friendly -home-tester-Workspaces-dev-xesapps)"  "friendly: encoded segment decoded"
assert_eq "b/c"          "$(_friendly /opt/a/b/c)"                           "friendly: deep non-home -> last two"
assert_eq "/tmp/projA"   "$(_friendly /tmp/projA)"                           "friendly: short non-home -> verbatim"
assert_eq "(no project)" "$(_friendly '')"                                   "friendly: empty -> (no project)"
assert_eq "(no project)" "$(_friendly null)"                                 "friendly: null -> (no project)"

# Manifest carries project_name per group; null group → (no project).
assert_eq "/tmp/projA" "$(jq -r '[.days[].projects[] | select(.project_path=="/tmp/projA")][0].project_name' <<<"$full")" "manifest: project_name for /tmp/projA"
assert_eq "(no project)" "$(jq -r '[.days[].projects[] | select(.project_path==null)][0].project_name' <<<"$full")" "manifest: null group project_name"
# Raw project_path is still present alongside the friendly name.
assert_eq "true" "$(jq '[.days[].projects[] | has("project_path") and has("project_name")] | all' <<<"$full")" "manifest: both project_path and project_name present"

# --- determinism -----------------------------------------------------------
assert_eq "$(clast_retro_manifest)" "$(clast_retro_manifest)" "byte-identical across runs"

# --- work-days windowing keys off work day ---------------------------------
# 06-13 only → just the cross-midnight session.
win="$(clast_retro_manifest --from 2026-06-13 --to 2026-06-13)"
assert_eq "1" "$(jq '[.days[].projects[].sessions[]] | length' <<<"$win")" "work-days 06-13: one session"
assert_eq "$SID_X" "$(jq -r '.days[0].projects[0].sessions[0].session_id' <<<"$win")" "work-days 06-13: the cross-midnight session"
assert_eq "2026-06-13" "$(jq -r '.from' <<<"$win")" "window echoes from"

# SID_E (filed 06-20, work 06-14) IS included by a work-day window over 06-14.
wd="$(clast_retro_manifest --from 2026-06-10 --to 2026-06-15)"
assert_eq "1" "$(jq --arg s "$SID_E" '[.days[].projects[].sessions[] | select(.session_id==$s)] | length' <<<"$wd")" "work-days: diverge included by work day"

# --- bounded work-days drops unknown; unbounded keeps it -------------------
assert_eq "0" "$(jq '[.days[] | select(.day=="unknown")] | length' <<<"$wd")" "bounded work-days: unknown dropped"

# --- file-dates windowing keys off filename date ---------------------------
# Files dated 06-13 only → just A2; the session keeps a single entry.
fd="$(clast_retro_manifest --from 2026-06-13 --to 2026-06-13 --window file-dates)"
assert_eq "file-dates" "$(jq -r '.window' <<<"$fd")" "file-dates: window echoed"
sx_fd="$(_session "$SID_X" "$fd")"
assert_eq "1" "$(jq '.entries | length' <<<"$sx_fd")" "file-dates 06-13: only the in-range entry kept"

# SID_E (filed 06-20) is EXCLUDED by a file-date window ending 06-15...
fd2="$(clast_retro_manifest --from 2026-06-10 --to 2026-06-15 --window file-dates)"
assert_eq "0" "$(jq --arg s "$SID_E" '[.days[].projects[].sessions[] | select(.session_id==$s)] | length' <<<"$fd2")" "file-dates: diverge excluded by filename date"

# --- bad args --------------------------------------------------------------
assert_exit_code 2 clast_retro_manifest --window bogus
assert_exit_code 2 clast_retro_manifest --from not-a-date
assert_exit_code 2 clast_retro_manifest --frobnicate
teardown_test_journal

# --- empty journal → empty days --------------------------------------------
setup_test_journal >/dev/null
empty="$(clast_retro_manifest)"
assert_eq "[]" "$(jq -c '.days' <<<"$empty")" "empty journal: no days"
assert_eq "0" "$(clast_retro_manifest >/dev/null; echo $?)" "empty journal: exit 0"
teardown_test_journal

clast_test_summary
