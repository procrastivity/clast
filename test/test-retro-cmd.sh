#!/usr/bin/env bash
# test-retro-cmd.sh — `clast-plumbing retro` (Round 1, step-03: command wiring
# + deterministic render). Subprocess style against bin/clast-plumbing with the
# retro-manifest fixtures.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-retro-cmd"

CLAST_BIN="$PWD/bin/clast-plumbing"
# Deterministic bucket math.
export TZ=UTC
export CLAST_DAY_CUTOFF=04:00

_seed() {
  setup_test_journal >/dev/null
  make_fixture_entries_seed_from "retro-manifest/entries-seed"
}

# === --json mode emits the manifest ========================================
_seed
out="$("$CLAST_BIN" --json retro 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "json: exits 0"
for k in from to window days; do
  assert_eq "true" "$(jq --arg k "$k" 'has($k)' <<<"$out")" "json: manifest has $k"
done
assert_eq "work-days" "$(jq -r '.window' <<<"$out")" "json: default window"
# Cross-midnight session resolves to 06-13 with both entries (manifest contract).
sx="$(jq -c '[.days[].projects[].sessions[] | select(.session_id=="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")][0]' <<<"$out")"
assert_eq "2026-06-13" "$(jq -r '.work_day' <<<"$sx")" "json: cross-midnight resolved day"
assert_eq "2" "$(jq '.entries|length' <<<"$sx")" "json: cross-midnight merged entries"
teardown_test_journal

# === human render ==========================================================
_seed
out="$("$CLAST_BIN" retro 2>/dev/null)"

# Header echoes unbounded window.
case "$out" in
  "Retro: (start) -> (end) (work-days)"*) _clast_test_pass "render: header (unbounded)" ;;
  *) _clast_test_fail "render: header (unbounded)"; printf '%s\n' "$out" | head -1 >&2 ;;
esac

# Day headers appear in ascending order, unknown last.
days_order="$(printf '%s\n' "$out" | sed -n 's/^== \(.*\) ==$/\1/p' | paste -sd, -)"
assert_eq "2026-06-13,2026-06-14,2026-06-15,unknown" "$days_order" "render: day headers ordered, unknown last"

# Divergence: the filed-06-20 session renders under work day 06-14, not 06-20.
# (No 2026-06-20 day header exists.)
case "$out" in
  *"== 2026-06-20 =="*) _clast_test_fail "render: no filename-date day header" ;;
  *) _clast_test_pass "render: no filename-date day header (bucketed by work day)" ;;
esac

# Project header + session bullet with short session id.
case "$out" in
  *"[/tmp/projA]"*) _clast_test_pass "render: project header" ;;
  *) _clast_test_fail "render: project header" >&2 ;;
esac
case "$out" in
  *"* Curated days later than the work  (eeeeeeee)"*) _clast_test_pass "render: session bullet (title + short sid)" ;;
  *) _clast_test_fail "render: session bullet (title + short sid)" >&2 ;;
esac

# Null project group renders its own header.
case "$out" in
  *"[(no project)]"*) _clast_test_pass "render: null-project header" ;;
  *) _clast_test_fail "render: null-project header" >&2 ;;
esac

# Merged session shows both entry bodies, each with a filename separator.
case "$out" in
  *"--- 2026-06-12-2350-proja-cross.md ---"*"--- 2026-06-13-0010-proja-cross.md ---"*)
    _clast_test_pass "render: merged session shows both entry separators" ;;
  *) _clast_test_fail "render: merged session shows both entry separators" >&2 ;;
esac
case "$out" in
  *"Started before midnight."*"Finished after midnight."*) _clast_test_pass "render: both merged bodies present" ;;
  *) _clast_test_fail "render: both merged bodies present" >&2 ;;
esac

# Body verbatim, with the duplicate `# Session:` heading trimmed.
case "$out" in
  *"## What shipped"*) _clast_test_pass "render: verbatim body section" ;;
  *) _clast_test_fail "render: verbatim body section" >&2 ;;
esac
teardown_test_journal

# === window scope flag =====================================================
_seed
# file-dates over 06-13 keeps only the entry filed 06-13 → one merged entry.
out="$("$CLAST_BIN" --json retro --from 2026-06-13 --to 2026-06-13 --window file-dates 2>/dev/null)"
assert_eq "file-dates" "$(jq -r '.window' <<<"$out")" "window: file-dates echoed"
sx="$(jq -c '[.days[].projects[].sessions[] | select(.session_id=="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")][0]' <<<"$out")"
assert_eq "1" "$(jq '.entries|length' <<<"$sx")" "window: file-dates keeps only in-range entry"
teardown_test_journal

# === flags: quiet, errors, help ============================================
_seed
out="$("$CLAST_BIN" --quiet retro 2>/dev/null)"
assert_eq "" "$out" "quiet: no stdout"

assert_exit_code 2 "$CLAST_BIN" retro --window bogus
assert_exit_code 2 "$CLAST_BIN" retro --from 2026-06-20 --to 2026-06-10
assert_exit_code 2 "$CLAST_BIN" retro --from not-a-date
assert_exit_code 2 "$CLAST_BIN" retro --bogus
assert_exit_code 2 "$CLAST_BIN" retro stray-positional

# --json error envelope.
err="$("$CLAST_BIN" --json retro --window bogus 2>/dev/null)"
assert_eq "2" "$(jq -r '.code' <<<"$err")" "json error: code 2"

out="$("$CLAST_BIN" retro --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "help: exits 0"
case "$out" in *"Usage: clast retro"*) _clast_test_pass "help: usage text" ;; *) _clast_test_fail "help: usage text" >&2 ;; esac
teardown_test_journal

# === empty corpus ==========================================================
setup_test_journal >/dev/null
out="$("$CLAST_BIN" retro 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "empty: exits 0"
case "$out" in *"(no sessions in range)"*) _clast_test_pass "empty: no-sessions notice" ;; *) _clast_test_fail "empty: no-sessions notice" >&2 ;; esac
assert_eq "[]" "$("$CLAST_BIN" --json retro 2>/dev/null | jq -c '.days')" "empty: json days []"
teardown_test_journal

clast_test_summary
