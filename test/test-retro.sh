#!/usr/bin/env bash
# test-retro.sh — exercises clast-retro-lib.bash (Round 1, step-01:
# front-matter index). Unit style: sources the lib and calls
# clast_retro_index directly against a fixture-seeded temp journal.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-retro-lib.bash
source lib/clast/clast-retro-lib.bash
_CLAST_TEST_NAME="test-retro"

export TZ=UTC

_seed_entries() {
  setup_test_journal >/dev/null
  make_fixture_entries_seed_from "retro/entries-seed"
}

# --- missing entries dir → [] ----------------------------------------------
setup_test_journal >/dev/null
out="$(clast_retro_index)" && rc=$? || rc=$?
assert_eq "0" "$rc" "missing entries dir: exits 0"
assert_eq "[]" "$out" "missing entries dir: empty array"
teardown_test_journal

# --- empty entries dir → [] ------------------------------------------------
setup_test_journal >/dev/null
mkdir -p "$CLAST_JOURNAL_DIR/entries"
assert_eq "[]" "$(clast_retro_index)" "empty entries dir: empty array"
teardown_test_journal

# --- one record per *.md file ----------------------------------------------
_seed_entries
out="$(clast_retro_index)"
assert_eq "4" "$(jq 'length' <<<"$out")" "index: one record per entry"

# --- every record carries the five contract keys ---------------------------
for k in path session_id project_path snapshot_path curated_source_mtime; do
  all_present="$(jq --arg k "$k" 'all(.[]; has($k))' <<<"$out")"
  assert_eq "true" "$all_present" "index: every record has $k"
done

# Records carry no fields beyond the contract (exactly five keys each).
max_keys="$(jq '[.[] | keys | length] | max' <<<"$out")"
assert_eq "5" "$max_keys" "index: records carry exactly the contract keys"

# --- divergent-day entry: fields extracted, snapshot_path kept raw ----------
rec="$(jq -c '.[] | select(.session_id == "1a9a8397-0a83-4389-b310-93a54b8bd474")' <<<"$out")"
assert_eq "/home/bsimensen/Workspaces/dev/xesapps" \
  "$(jq -r '.project_path' <<<"$rec")" "divergent: project_path"
assert_eq "transcripts/2026-06-24/-home-bsimensen-Workspaces-dev-xesapps/1a9a8397-0a83-4389-b310-93a54b8bd474.jsonl" \
  "$(jq -r '.snapshot_path' <<<"$rec")" "divergent: snapshot_path kept raw (work day 06-24, not filename 06-29)"
# curated_source_mtime is unquoted — the JSON string has no surrounding quotes.
assert_eq "2026-06-24T14:30:54Z" \
  "$(jq -r '.curated_source_mtime' <<<"$rec")" "divergent: curated_source_mtime unquoted"

# --- project_path: null → JSON null ----------------------------------------
rec="$(jq -c '.[] | select(.session_id == "33333333-3333-4333-8333-333333333333")' <<<"$out")"
assert_eq "null" "$(jq -c '.project_path' <<<"$rec")" "null-project: project_path is JSON null"
# snapshot_path / mtime still populated on the same record.
assert_eq "false" "$(jq '.snapshot_path == null' <<<"$rec")" "null-project: snapshot_path still present"

# --- missing curated_source_mtime → JSON null ------------------------------
rec="$(jq -c '.[] | select(.session_id == "22222222-2222-4222-8222-222222222222")' <<<"$out")"
assert_eq "null" "$(jq -c '.curated_source_mtime' <<<"$rec")" "missing-mtime: curated_source_mtime is JSON null"
assert_eq "false" "$(jq '.snapshot_path == null' <<<"$rec")" "missing-mtime: snapshot_path still present"

# --- missing snapshot_path → JSON null -------------------------------------
rec="$(jq -c '.[] | select(.session_id == "44444444-4444-4444-8444-444444444444")' <<<"$out")"
assert_eq "null" "$(jq -c '.snapshot_path' <<<"$rec")" "missing-snapshot: snapshot_path is JSON null"
assert_eq "false" "$(jq '.curated_source_mtime == null' <<<"$rec")" "missing-snapshot: curated_source_mtime still present"

# --- deterministic: sorted by path, byte-identical across runs -------------
sorted="$(jq -c 'map(.path) == (map(.path) | sort)' <<<"$out")"
assert_eq "true" "$sorted" "index: records sorted by path"
run1="$(clast_retro_index)"
run2="$(clast_retro_index)"
assert_eq "$run1" "$run2" "index: byte-identical across runs"
teardown_test_journal

clast_test_summary
