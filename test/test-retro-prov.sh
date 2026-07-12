#!/usr/bin/env bash
# test-retro-prov.sh — provenance note + interrupted-session handling
# (Round 3, step-06). Covers the manifest (curation_dates / interrupted), both
# renders, and the clast_retro_is_interrupted predicate. Plumbing is exercised
# as a subprocess; the porcelain render is exercised function-level with the
# LLM stubbed (cf. test-retro-summary).
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-retro-lib.bash
source lib/clast/clast-retro-lib.bash
_CLAST_TEST_NAME="test-retro-prov"

CLAST_BIN="$PWD/bin/clast-plumbing"
export PATH="$PWD/bin:$PATH"
export TZ=UTC
export CLAST_DAY_CUTOFF=04:00

_seed() {
  setup_test_journal >/dev/null
  make_fixture_entries_seed_from "retro-prov/entries-seed"
}

# === clast_retro_is_interrupted predicate ==================================
assert_exit_code 0 bash -c 'source lib/clast/clast-lib.bash; source lib/clast/clast-retro-lib.bash
  printf "## Goal\nx\n## Open threads\n- y\n" | clast_retro_is_interrupted'
assert_exit_code 1 bash -c 'source lib/clast/clast-lib.bash; source lib/clast/clast-retro-lib.bash
  printf "## Goal\nx\n## What shipped\n- y\n" | clast_retro_is_interrupted'
assert_exit_code 1 bash -c 'source lib/clast/clast-lib.bash; source lib/clast/clast-retro-lib.bash
  printf "" | clast_retro_is_interrupted'

# === manifest: curation_dates + interrupted ================================
_seed
m="$("$CLAST_BIN" --json retro --bodies 2>/dev/null)"
# Curated-late day 07-01: filed 07-05 → curation_dates differ from the work day.
assert_eq '["2026-07-05"]' "$(jq -c '.days[] | select(.day=="2026-07-01") | .curation_dates' <<<"$m")" "manifest: curated-late day curation_dates = filed date"
# Same-day day 07-02: curation_dates equal the work day.
assert_eq '["2026-07-02"]' "$(jq -c '.days[] | select(.day=="2026-07-02") | .curation_dates' <<<"$m")" "manifest: same-day curation_dates = work day"
# Interrupted flag on the 07-03 session, not on the others.
assert_eq "true"  "$(jq -r '.days[] | select(.day=="2026-07-03") | .projects[0].sessions[0].interrupted' <<<"$m")" "manifest: interrupted session flagged"
assert_eq "false" "$(jq -r '.days[] | select(.day=="2026-07-02") | .projects[0].sessions[0].interrupted' <<<"$m")" "manifest: normal session not flagged"
teardown_test_journal

# === plumbing render: provenance note + [interrupted] ======================
_seed
out="$("$CLAST_BIN" retro 2>/dev/null)"
# Provenance note under the curated-late day (07-01), naming the filed date.
day01="$(awk '/^== 2026-07-01 ==$/{f=1;next} /^== /{f=0} f' <<<"$out")"
case "$day01" in
  *"(filed 2026-07-05; work day reconstructed from session snapshots)"*) _clast_test_pass "render: provenance note on curated-late day" ;;
  *) _clast_test_fail "render: provenance note on curated-late day"; printf '%s\n' "$day01" >&2 ;;
esac
# No provenance note under the same-day day (07-02).
day02="$(awk '/^== 2026-07-02 ==$/{f=1;next} /^== /{f=0} f' <<<"$out")"
case "$day02" in
  *"work day reconstructed"*) _clast_test_fail "render: no note on same-day day" ;;
  *) _clast_test_pass "render: no note on same-day day" ;;
esac
# [interrupted] flag on the 07-03 session bullet; not on the 07-02 one.
case "$out" in *"Started but not finished"*"[interrupted]"*) _clast_test_pass "render: interrupted flagged" ;; *) _clast_test_fail "render: interrupted flagged" >&2 ;; esac
day02_bullet="$(grep -A0 'Curated the same day' <<<"$out")"
case "$day02_bullet" in *"[interrupted]"*) _clast_test_fail "render: normal session not flagged" ;; *) _clast_test_pass "render: normal session not flagged" ;; esac
teardown_test_journal

# === porcelain render: same note + flag (LLM stubbed) ======================
export CLAST_LLM_BASE_URL="http://stub" CLAST_LLM_API_KEY="x" CLAST_LLM_MODEL="stub"
# shellcheck source=lib/clast/clast-porcelain-lib.bash
source lib/clast/clast-porcelain-lib.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/retro.bash
source lib/clast/clast-porcelain-subcommands/retro.bash
clast_porcelain_llm_chat() { printf -- '- **Shipped:** stub\n'; }

_seed
# Use the full corpus so this provenance/interrupted render test is not tied
# to clast retro's rolling default window.
pout="$(clast_cmd_retro --all 2>/dev/null)"
case "$pout" in
  *"(filed 2026-07-05; work day reconstructed from session snapshots)"*) _clast_test_pass "porcelain: provenance note" ;;
  *) _clast_test_fail "porcelain: provenance note"; printf '%s\n' "$pout" >&2 ;;
esac
case "$pout" in *"Started but not finished"*"[interrupted]"*) _clast_test_pass "porcelain: interrupted flagged" ;; *) _clast_test_fail "porcelain: interrupted flagged" >&2 ;; esac
teardown_test_journal

clast_test_summary
