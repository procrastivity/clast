#!/usr/bin/env bash
# test-wake-auto.sh — `clast wake --auto` non-interactive curation.
#
# Function-level: sources the porcelain subcommand and shadows both the LLM
# (clast_porcelain_llm_chat) and the bareword `clast-plumbing` with stubs, so
# the auto path runs with no tty, no network, and no real journal internals.
# --auto is the only wake path that is testable without an interactive
# terminal (the interactive flow reads choices from /dev/tty).
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-wake-auto"

export TZ=UTC
# Resolve prompt templates from the in-repo copy (CI has no installed prompts,
# no CLAST_LIB) so a draft generation does not error on missing templates.
export CLAST_LIB="$PWD/lib/clast"
# Satisfy preflight_llm; the real curl call is stubbed away below.
export CLAST_LLM_BASE_URL="http://stub" CLAST_LLM_API_KEY="x" CLAST_LLM_MODEL="stub"

# shellcheck source=lib/clast/clast-porcelain-lib.bash
source lib/clast/clast-porcelain-lib.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/wake.bash
source lib/clast/clast-porcelain-subcommands/wake.bash

# --- Stubs -----------------------------------------------------------------

# The draft the model "generates" — a title heading + a tags trailer so the
# accept path exercises title/tag extraction. Set LLM_RC=1 to simulate failure.
LLM_RC=0
clast_porcelain_llm_chat() {
  (( LLM_RC != 0 )) && return "$LLM_RC"
  printf '# Session: Auto Curated Thing\n\n## What shipped\n- did the thing\n\nSuggested tags: alpha, beta\n'
}

# Records every `entries write` (one per auto-accept).
WRITELOG=""
# The session set `clast-plumbing sessions --since` returns.
SESSIONS_JSON=""

# Shadow the bareword external command wake shells out to. Handles only the
# invocations the --auto path makes.
clast-plumbing() {
  [[ "${1:-}" == "--json" ]] && shift   # drop the global flag; irrelevant to the stub
  case "${1:-}" in
    snapshot) return 0 ;;
    sessions)
      [[ "${2:-}" == "--since" ]] && { printf '%s' "$SESSIONS_JSON"; return 0; }
      return 0 ;;  # `sessions dismiss ...` — no-op
    show)
      printf '{"first_turns":[{"role":"user","text":"hi"}],"last_turns":[{"role":"assistant","text":"done"}]}'
      return 0 ;;
    breadcrumb) return 0 ;;  # no breadcrumbs
    whereami)   printf 'journal_dir: %s\n' "$CLAST_JOURNAL_DIR"; return 0 ;;
    entries)
      cat >/dev/null                      # consume the body piped on stdin
      printf 'wrote\n' >>"$WRITELOG"
      printf 'wrote entry (session %s)\n' "${4:-?}"
      return 0 ;;
    *) return 0 ;;
  esac
}

_writes() { wc -l <"$WRITELOG" | tr -d ' '; }

# Two uncurated, substantive sessions on the same day.
_seed() {
  setup_test_journal >/dev/null
  WRITELOG="$_CLAST_TEST_TMPDIR/writes.log"; : >"$WRITELOG"
  SESSIONS_JSON='[
    {"session_id":"11111111-1111-4111-8111-111111111111","project":"projA","branch":"main","start":"2026-07-06T10:00:00Z","end":"2026-07-06T11:00:00Z","msg_count_approx":10,"snapshot_path":"transcripts/2026-07-06/x/s1.jsonl","day_bucket":"2026-07-06","curated":false,"stale":false,"substantive":true,"dismissed":false},
    {"session_id":"22222222-2222-4222-8222-222222222222","project":"projB","branch":"main","start":"2026-07-06T12:00:00Z","end":"2026-07-06T13:00:00Z","msg_count_approx":20,"snapshot_path":"transcripts/2026-07-06/y/s2.jsonl","day_bucket":"2026-07-06","curated":false,"stale":false,"substantive":true,"dismissed":false}
  ]'
}

# === auto-accepts every draft, no prompting, no tty ========================
_seed
LLM_RC=0
out="$(clast_cmd_wake --auto </dev/null 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "auto: exits 0"
assert_eq "2" "$(_writes)" "auto: wrote an entry for every session (2)"
case "$out" in *"Auto mode:"*) _clast_test_pass "auto: announces auto mode" ;; *) _clast_test_fail "auto: announces auto mode"; printf '%s\n' "$out" >&2 ;; esac
case "$out" in *"Curated: 2 session(s)"*) _clast_test_pass "auto: summary counts both curated" ;; *) _clast_test_fail "auto: summary counts both curated"; printf '%s\n' "$out" >&2 ;; esac
case "$out" in *"Model time:"*) _clast_test_pass "auto: reports model time" ;; *) _clast_test_fail "auto: reports model time" >&2 ;; esac
# The interactive menu must never appear in auto mode.
case "$out" in *"[a] Accept"*) _clast_test_fail "auto: must not print the interactive menu" ;; *) _clast_test_pass "auto: no interactive menu" ;; esac
teardown_test_journal

# === a failed draft is skipped, not written, and does not abort ============
_seed
LLM_RC=1
out="$(clast_cmd_wake --auto </dev/null 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "auto fail: still exits 0"
assert_eq "0" "$(_writes)" "auto fail: nothing written"
case "$out" in *"LLM call failed"*) _clast_test_pass "auto fail: warns on failure" ;; *) _clast_test_fail "auto fail: warns on failure" >&2 ;; esac
case "$out" in *"Skipped: 2 session(s)"*) _clast_test_pass "auto fail: both skipped" ;; *) _clast_test_fail "auto fail: both skipped"; printf '%s\n' "$out" >&2 ;; esac
teardown_test_journal

# === without --auto and no tty, wake refuses (die) =========================
# clast_porcelain_die calls exit, so run in a subshell to not kill the test.
_seed
( clast_cmd_wake </dev/null ) >/dev/null 2>&1 && rc=$? || rc=$?
assert_eq "1" "$rc" "no --auto + no tty: refuses with exit 1"
teardown_test_journal

# === arg validation ========================================================
out="$(clast_cmd_wake --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "help: exits 0"
case "$out" in *"Usage: clast wake"*) _clast_test_pass "help: usage text" ;; *) _clast_test_fail "help: usage text" >&2 ;; esac
case "$out" in *"--auto"*) _clast_test_pass "help: documents --auto" ;; *) _clast_test_fail "help: documents --auto" >&2 ;; esac
assert_exit_code 2 clast_cmd_wake --bogus

clast_test_summary
