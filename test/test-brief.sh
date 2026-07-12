#!/usr/bin/env bash
# test-brief.sh — non-LLM coverage of the brief porcelain's entry gathering.
# Exercises _clast_brief_gather_entries (pure data assembly via clast-plumbing
# + jq); the LLM synthesis step is not invoked.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-brief"

# gather shells out to the bareword `clast-plumbing`; make it resolvable.
export PATH="$PWD/bin:$PATH"

# Only function definitions are needed; sourcing has no side effects.
# shellcheck source=lib/clast/clast-porcelain-lib.bash
source lib/clast/clast-porcelain-lib.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/brief.bash
source lib/clast/clast-porcelain-subcommands/brief.bash

# _write_entry <basename> <project> <label> <branch> <time> <title>
_write_entry() {
  local name="$1" project="$2" label="$3" branch="$4" time="$5" title="$6"
  local dir="$CLAST_JOURNAL_DIR/entries"
  mkdir -p "$dir"
  local label_line="label: $label"
  [[ "$label" == "null" ]] && label_line="label: null"
  cat > "$dir/$name" <<EOF
---
date: 2026-06-17
time: $time
day_bucket: 2026-06-17
project: $project
project_path: /tmp/x
$label_line
branch: $branch
author: t
tags: []
session_id: 11111111-1111-4111-8111-111111111111
session_slug: ${name%.md}
snapshot_path: transcripts/x
machine: m
curated_source_mtime: "2026-06-17T10:00:00Z"
---

# Session: $title

Body for $title.
EOF
}

# --- multi-workspace: entries grouped under per-label headers ---------------
setup_test_journal >/dev/null
_write_entry 2026-06-17-1200-xesapps-a.md xesapps dev  feat/x 12:00 "Dev newest"
_write_entry 2026-06-17-1100-xesapps-b.md xesapps perf main   11:00 "Perf work"
_write_entry 2026-06-17-0900-xesapps-c.md xesapps dev  feat/x 09:00 "Dev older"
out="$(_clast_brief_gather_entries xesapps)"

case "$out" in
  *"## Workspace: dev"*) _clast_test_pass "gather: dev workspace header present" ;;
  *) _clast_test_fail "gather: dev workspace header present"; printf '%s\n' "$out" >&2 ;;
esac
case "$out" in
  *"## Workspace: perf"*) _clast_test_pass "gather: perf workspace header present" ;;
  *) _clast_test_fail "gather: perf workspace header present"; printf '%s\n' "$out" >&2 ;;
esac
# Branch shown in the header.
case "$out" in
  *"## Workspace: dev (branch: feat/x)"*) _clast_test_pass "gather: header shows branch" ;;
  *) _clast_test_fail "gather: header shows branch"; printf '%s\n' "$out" >&2 ;;
esac
# All three entry bodies present.
for t in "Dev newest" "Dev older" "Perf work"; do
  case "$out" in
    *"$t"*) _clast_test_pass "gather: entry present — $t" ;;
    *) _clast_test_fail "gather: entry present — $t"; printf '%s\n' "$out" >&2 ;;
  esac
done
# dev group (first appearance, newest entry) precedes perf group.
dev_pos="${out%%## Workspace: dev*}"
perf_pos="${out%%## Workspace: perf*}"
if (( ${#dev_pos} < ${#perf_pos} )); then
  _clast_test_pass "gather: dev group precedes perf group"
else
  _clast_test_fail "gather: dev group precedes perf group"
fi
teardown_test_journal

# --- single-workspace: no group headers (strict superset of old output) -----
setup_test_journal >/dev/null
_write_entry 2026-06-17-1200-xesapps-a.md xesapps dev feat/x 12:00 "Only dev"
_write_entry 2026-06-17-0900-xesapps-c.md xesapps dev feat/x 09:00 "Only dev older"
out="$(_clast_brief_gather_entries xesapps)"
case "$out" in
  *"## Workspace:"*) _clast_test_fail "gather single-workspace: must NOT emit headers"; printf '%s\n' "$out" >&2 ;;
  *) _clast_test_pass "gather single-workspace: no headers" ;;
esac
case "$out" in
  *"Only dev"*) _clast_test_pass "gather single-workspace: entry present" ;;
  *) _clast_test_fail "gather single-workspace: entry present"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- busy clone must not starve a quieter clone in the same shared-slug ----
# Regression: the gather used to apply --limit 30 to the global entries query,
# so a clone with >30 newer entries pushed the current clone out of the window
# entirely. The dropped global cap plus per_group/total_cap is enough; the
# current_label hoist below guarantees ordering on top of that.
setup_test_journal >/dev/null
# 31 newer entries in workspace "busy" — past the old global cap.
for i in $(seq -w 1 31); do
  hh=$(( 10#$i / 2 + 8 ))
  mm=$(( 10#$i % 60 ))
  _write_entry "2026-06-17-${i}-xesapps-b.md" xesapps busy main "$(printf '%02d:%02d' "$hh" "$mm")" "Busy $i"
done
# Single older entry in workspace "active". Without the fix, it never surfaces.
_write_entry 2026-06-17-0001-xesapps-a.md xesapps active feat/a 00:01 "Active sole"
out="$(_clast_brief_gather_entries xesapps active)"
case "$out" in
  *"Active sole"*) _clast_test_pass "gather busy+active: active entry surfaces despite 31 newer busy entries" ;;
  *) _clast_test_fail "gather busy+active: active entry surfaces despite 31 newer busy entries"; printf '%s\n' "$out" >&2 ;;
esac
active_pos="${out%%## Workspace: active*}"
busy_pos="${out%%## Workspace: busy*}"
if (( ${#active_pos} < ${#busy_pos} )); then
  _clast_test_pass "gather busy+active: current_label 'active' hoisted before busy"
else
  _clast_test_fail "gather busy+active: current_label 'active' hoisted before busy"
fi
# Without current_label, newest-first-appearance order applies: busy precedes active.
out_no_hoist="$(_clast_brief_gather_entries xesapps)"
active_pos="${out_no_hoist%%## Workspace: active*}"
busy_pos="${out_no_hoist%%## Workspace: busy*}"
if (( ${#busy_pos} < ${#active_pos} )); then
  _clast_test_pass "gather busy+active (no current_label): newest workspace precedes older"
else
  _clast_test_fail "gather busy+active (no current_label): newest workspace precedes older"
fi
teardown_test_journal

# --- entries with no label fall back to branch grouping ---------------------
setup_test_journal >/dev/null
_write_entry 2026-06-17-1200-xesapps-a.md xesapps null feat/aaa 12:00 "Branch A"
_write_entry 2026-06-17-1100-xesapps-b.md xesapps null feat/bbb 11:00 "Branch B"
out="$(_clast_brief_gather_entries xesapps)"
case "$out" in
  *"## Workspace: feat/aaa"*) _clast_test_pass "gather: falls back to branch as group key" ;;
  *) _clast_test_fail "gather: falls back to branch as group key"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# === arg validation ========================================================
export CLAST_LLM_BASE_URL="http://stub" CLAST_LLM_API_KEY="x" CLAST_LLM_MODEL="stub"
assert_exit_code 2 clast_cmd_brief --bogus
out="$(clast_cmd_brief --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "help: exits 0"
case "$out" in *"Usage: clast brief"*) _clast_test_pass "help: usage text" ;; *) _clast_test_fail "help: usage text"; printf '%s\n' "$out" >&2 ;; esac
out="$(clast_cmd_brief -- xesapps 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "dashdash: exits 0"
case "$out" in *"Briefing for project: xesapps"*) _clast_test_pass "dashdash: positional slug preserved" ;; *) _clast_test_fail "dashdash: positional slug preserved"; printf '%s\n' "$out" >&2 ;; esac

clast_test_summary
