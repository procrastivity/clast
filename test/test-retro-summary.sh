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
# Porcelain render shows the friendly project_name too (HOME=/tmp → ~/projA).
home_out="$(HOME=/tmp clast_cmd_retro 2>/dev/null)"
case "$home_out" in *"[~/projA]"*) _clast_test_pass "render: friendly project_name" ;; *) _clast_test_fail "render: friendly project_name" >&2 ;; esac
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
# Condensed output: the raw --bodies input is stripped, only the summary remains.
assert_eq "false" "$(jq '[.days[].projects[].sessions[] | has("body")] | any' <<<"$js")" "json: no raw body field"
assert_eq "0" "$(_calls)" "json: served from warm cache (no calls)"
teardown_test_journal

# === null session_id: summary fold must not abort, must not collapse ======
# The porcelain consumes `clast-plumbing --json retro --bodies`; id-less entries
# flow through as JSON null. Folding summaries back used to `$sum[null]` → jq
# abort. And two id-less sessions must stay distinct (not collapse to one), each
# summarized and cached under its own file.
setup_test_journal >/dev/null
mkdir -p "$CLAST_JOURNAL_DIR/entries"
CALLLOG="$_CLAST_TEST_TMPDIR/calls.log"; : >"$CALLLOG"
LASTUSER="$_CLAST_TEST_TMPDIR/lastuser.txt"
for s in A B; do
  {
    printf -- '---\ndate: 2026-09-01\ntime: "10:00"\nday_bucket: 2026-09-01\n'
    printf -- 'project: nullsess\nproject_path: /tmp/projNull\n'
    printf -- 'snapshot_path: transcripts/2026-09-01/-tmp-projNull/legacy%s.jsonl\n' "$s"
    printf -- 'machine: m\ncurated_source_mtime: "2026-09-01T10:00:00Z"\n---\n\n'
    printf -- '# Session: Legacy %s\n## What shipped\n- shipped from id-less session %s\n' "$s" "$s"
  } > "$CLAST_JOURNAL_DIR/entries/2026-09-01-1000-nullsess-legacy$s.md"
done
js="$(clast_cmd_retro --from 2026-09-01 --to 2026-09-01 --json 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "null sid: porcelain --json exits 0 (no jq null-index abort)"
assert_eq "2" "$(jq '[.days[].projects[].sessions[] | select(.session_id==null)] | length' <<<"$js")" "null sid: two id-less sessions stay distinct"
assert_eq "0" "$(jq '[.days[].projects[].sessions[] | select(.session_id==null) | .summary | select(. == null)] | length' <<<"$js")" "null sid: every id-less session gets a summary"
# Each id-less session summarized independently (two LLM calls, two cache files).
assert_eq "2" "$(_calls)" "null sid: both id-less sessions summarized (2 calls)"
assert_eq "2" "$(find "$CLAST_JOURNAL_DIR/.retro-summaries" -name '*.json' 2>/dev/null | wc -l | tr -d ' ')" "null sid: a distinct cache file per id-less session"
teardown_test_journal

# === progress output goes to stderr, gated ================================
# CLAST_RETRO_PROGRESS=always forces progress on (tests have no tty). It must
# land on stderr (stdout stays the clean render/JSON) and carry the resolved
# window + a per-session counter.
_seed
progress="$(clast_cmd_retro --from 2026-06-13 --to 2026-06-15 2>&1 >/dev/null)"
assert_eq "" "$progress" "progress: silent by default (no tty)"
progress="$(CLAST_RETRO_PROGRESS=always clast_cmd_retro --from 2026-06-13 --to 2026-06-15 2>&1 >/dev/null)"
case "$progress" in *"window: 2026-06-13 -> 2026-06-15 (work-days)"*) _clast_test_pass "progress: resolved window line" ;; *) _clast_test_fail "progress: resolved window line"; printf '%s\n' "$progress" >&2 ;; esac
case "$progress" in *"resolved 4 session(s) across 3 day(s)"*) _clast_test_pass "progress: session/day counts" ;; *) _clast_test_fail "progress: session/day counts" >&2 ;; esac
case "$progress" in *"[1/4]"*"[4/4]"*) _clast_test_pass "progress: per-session counter" ;; *) _clast_test_fail "progress: per-session counter" >&2 ;; esac
# stdout must remain a clean render even with progress forced on.
clean="$(CLAST_RETRO_PROGRESS=always clast_cmd_retro --from 2026-06-13 --to 2026-06-15 2>/dev/null)"
case "$clean" in *"clast: building work-day manifest"*) _clast_test_fail "progress: must not leak onto stdout" ;; *) _clast_test_pass "progress: stdout stays clean" ;; esac
# CLAST_QUIET wins over the force flag.
progress="$(CLAST_QUIET=1 CLAST_RETRO_PROGRESS=always clast_cmd_retro --from 2026-06-13 --to 2026-06-15 2>&1 >/dev/null)"
assert_eq "" "$progress" "progress: CLAST_QUIET silences it"
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
