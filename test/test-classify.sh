#!/usr/bin/env bash
# test-classify.sh — exercises clast-classify-lib.bash: the deterministic
# no-op detector that lets wake skip empty / slash-command-only sessions
# without an LLM call.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

_CLAST_TEST_NAME="test-classify"
# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-classify-lib.bash
source lib/clast/clast-classify-lib.bash

FIX="test/fixtures/classify"

# assert_counts <fixture> <expect_user> <expect_assistant> <expect_substantive>
assert_counts() {
  local fixture="$1" eu="$2" ea="$3" esub="$4"
  local u a sub
  IFS=$'\t' read -r u a < <(clast_session_msg_counts "$FIX/$fixture")
  assert_eq "$eu" "$u" "$fixture: user_msg_count"
  assert_eq "$ea" "$a" "$fixture: assistant_msg_count"
  # substantive iff Claude replied (assistant > 0) — NOT gated on user count.
  if [[ "$a" =~ ^[0-9]+$ ]] && (( a > 0 )); then
    sub=true
  else
    sub=false
  fi
  assert_eq "$esub" "$sub" "$fixture: substantive"
}

# --- no-op sessions: no real user prompt (leg 1) ----------------------------
# /clear leaves a caveat (meta) + a <command-name> wrapper user message.
assert_counts "clear-only.jsonl" 0 0 false
# /model leaves only system/local_command lines.
assert_counts "model-only.jsonl" 0 0 false
# Opened and closed immediately — only bookkeeping lines.
assert_counts "blank.jsonl" 0 0 false

# --- no-op session: prompt but no assistant reply (leg 2) -------------------
assert_counts "abandoned.jsonl" 1 0 false

# --- substantive: real prompt + assistant, even tool-only reply -------------
# A tool-only assistant turn (no text) still counts as real work.
assert_counts "tool-only-assistant.jsonl" 1 1 true
# Multi-turn: meta caveat + tool_results are excluded; 2 real prompts, 2 replies.
assert_counts "normal.jsonl" 2 2 true
# Custom slash command (/review): zero prose prompts (only the command wrapper)
# but real assistant work — MUST be substantive, else we'd auto-dismiss real
# sessions. This is the case that makes user-count-gating wrong.
assert_counts "custom-command.jsonl" 0 2 true

# --- missing / unreadable file falls to 0/0 (safe) --------------------------
IFS=$'\t' read -r mu ma < <(clast_session_msg_counts "$FIX/does-not-exist.jsonl")
assert_eq "0" "$mu" "missing file: user_msg_count 0"
assert_eq "0" "$ma" "missing file: assistant_msg_count 0"

# --- empty path arg ---------------------------------------------------------
IFS=$'\t' read -r eu ea < <(clast_session_msg_counts "")
assert_eq "0" "$eu" "empty path: user_msg_count 0"
assert_eq "0" "$ea" "empty path: assistant_msg_count 0"

# --- marker regex is exported for reuse by show.bash ------------------------
if [[ -n "${CLAST_COMMAND_MARKER_RE:-}" ]]; then
  _clast_test_pass "CLAST_COMMAND_MARKER_RE is set"
else
  _clast_test_fail "CLAST_COMMAND_MARKER_RE is set"
fi

clast_test_summary
