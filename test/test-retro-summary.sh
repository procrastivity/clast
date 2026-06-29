#!/usr/bin/env bash
# test-retro-summary.sh — `clast retro` porcelain summarize pass (Round 2,
# step-04). Function-level: sources the porcelain subcommand and stubs the LLM
# (clast_porcelain_llm_chat) so no network call happens — the established
# porcelain-test approach (cf. test-brief).
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-retro-summary"

# Bareword clast-plumbing must resolve; deterministic bucket math.
export PATH="$PWD/bin:$PATH"
export TZ=UTC
export CLAST_DAY_CUTOFF=04:00
# Satisfy preflight_llm (the real curl call is stubbed away below).
export CLAST_LLM_BASE_URL="http://stub" CLAST_LLM_API_KEY="x" CLAST_LLM_MODEL="stub"

# shellcheck source=lib/clast/clast-porcelain-lib.bash
source lib/clast/clast-porcelain-lib.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/retro.bash
source lib/clast/clast-porcelain-subcommands/retro.bash

# File-based call log + capture of the last user prompt — command substitution
# runs the summarizer in a subshell, so in-memory counters won't survive.
CALLLOG=""
LASTUSER=""
clast_porcelain_llm_chat() {
  printf 'x\n' >>"$CALLLOG"
  printf '%s' "$2" >"$LASTUSER"
  printf -- '- **Shipped:** stub bullet for testing\n'
}

_calls() { wc -l <"$CALLLOG" | tr -d ' '; }

_seed() {
  setup_test_journal >/dev/null
  make_fixture_entries_seed_from "retro-manifest/entries-seed"
  CALLLOG="$_CLAST_TEST_TMPDIR/calls.log"
  LASTUSER="$_CLAST_TEST_TMPDIR/lastuser.txt"
  : >"$CALLLOG"
}

# === cache miss → calls LLM + writes cache ==================================
_seed
out="$(clast_cmd_retro --from 2026-06-13 --to 2026-06-15 2>/dev/null)"
assert_eq "4" "$(_calls)" "miss: LLM called once per session (4)"
cache_dir="$CLAST_JOURNAL_DIR/.retro-summaries"
assert_eq "4" "$(find "$cache_dir" -name '*.json' 2>/dev/null | wc -l | tr -d ' ')" "miss: a cache file per session"
# Cache file shape: fingerprint + summary.
one="$(find "$cache_dir" -name '*.json' | head -1)"
assert_eq "true" "$(jq 'has("fingerprint") and has("summary")' "$one")" "miss: cache file has fingerprint + summary"
# Render shows the summary bullet, not the raw body.
case "$out" in
  *"- **Shipped:** stub bullet for testing"*) _clast_test_pass "render: shows summary bullet" ;;
  *) _clast_test_fail "render: shows summary bullet"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"- Finished after midnight."*) _clast_test_fail "render: raw body must NOT appear" ;;
  *) _clast_test_pass "render: raw body replaced by summary" ;;
esac
# Day/project structure preserved from the manifest.
case "$out" in *"== 2026-06-13 =="*"[/tmp/projA]"*) _clast_test_pass "render: day/project structure" ;; *) _clast_test_fail "render: day/project structure" >&2 ;; esac
teardown_test_journal

# === cache hit → no LLM call ===============================================
_seed
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1   # prime
: >"$CALLLOG"
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1   # second run
assert_eq "0" "$(_calls)" "hit: re-run makes no LLM calls"
teardown_test_journal

# === --refresh → forces re-summarize =======================================
_seed
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1
: >"$CALLLOG"
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 --refresh >/dev/null 2>&1
assert_eq "4" "$(_calls)" "refresh: re-summarizes every session"
teardown_test_journal

# === content edit → only the changed session re-summarizes =================
_seed
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1
printf -- '- another shipped item\n' >> "$CLAST_JOURNAL_DIR/entries/2026-06-16-0900-projb-fallback.md"
: >"$CALLLOG"
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1
assert_eq "1" "$(_calls)" "fingerprint: only the edited session re-summarizes"
teardown_test_journal

# === prompt assembly: body + metadata reach the model ======================
_seed
clast_cmd_retro --from 2026-06-15 --to 2026-06-15 >/dev/null 2>&1   # projB fallback session
case "$(cat "$LASTUSER")" in
  *"Work day: 2026-06-15"*) _clast_test_pass "prompt: work_day filled" ;;
  *) _clast_test_fail "prompt: work_day filled"; cat "$LASTUSER" >&2 ;;
esac
case "$(cat "$LASTUSER")" in
  *"No snapshot_path; work day comes from the mtime."*) _clast_test_pass "prompt: body included" ;;
  *) _clast_test_fail "prompt: body included" >&2 ;;
esac
teardown_test_journal

# === --json emits a summary per session, served from warm cache ============
_seed
clast_cmd_retro --from 2026-06-13 --to 2026-06-15 >/dev/null 2>&1   # prime cache
: >"$CALLLOG"
js="$(clast_cmd_retro --from 2026-06-13 --to 2026-06-15 --json 2>/dev/null)"
n_null="$(jq '[.days[].projects[].sessions[].summary | select(. == null)] | length' <<<"$js")"
assert_eq "0" "$n_null" "json: every session has a summary"
assert_eq "0" "$(_calls)" "json: served from warm cache (no calls)"
teardown_test_journal

# === arg validation ========================================================
_seed
assert_exit_code 2 clast_cmd_retro --window
assert_exit_code 2 clast_cmd_retro --bogus
out="$(clast_cmd_retro --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "help: exits 0"
case "$out" in *"Usage: clast retro"*) _clast_test_pass "help: usage text" ;; *) _clast_test_fail "help: usage text" >&2 ;; esac
teardown_test_journal

clast_test_summary
