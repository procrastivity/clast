#!/usr/bin/env bash
# test-query.sh — `clast projects` / `clast sessions` / `clast show`
# integration suite. Subprocess-style: runs bin/clast against the
# multi-project journal-seed fixture.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-query"

CLAST_BIN="$PWD/bin/clast-plumbing"
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

# --- shared-slug: each segment's row reports its OWN path/remote -----------
# Regression: rows used to be filled via _clast_projects_path_for_slug which
# returned the FIRST registry path for the slug, collapsing every checkout's
# row onto the first checkout's path. With two registered paths sharing one
# slug, each row must reflect its own segment's registry line.
_seed_journal
# Register a second path under the same slug. Use a distinct remote so the
# rows are also distinguishable by remote, and add a session on the same day
# so the second segment shows up in `projects --day` output.
printf '{"path":"/tmp/proj-xesapps-b","slug":"xesapps","label":"clone-b","remote":"git@example.com:xes/xesapps-b.git","first_seen":"2026-05-01","aliases":[]}\n' \
  >>"$CLAST_JOURNAL_DIR/projects.json"
SID_SHARED="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
shared_snap="transcripts/2026-05-30/-tmp-proj-xesapps-b/$SID_SHARED.jsonl"
shared_abs="$CLAST_JOURNAL_DIR/$shared_snap"
mkdir -p "$(dirname "$shared_abs")"
printf '{"type":"summary","timestamp":"2026-05-30T15:00:00Z","session_id":"%s"}\n' "$SID_SHARED" >"$shared_abs"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps-b/x.jsonl","snapshot":"%s","captured_at":"2026-05-30T15:30:00Z","source_mtime":"2026-05-30T15:25:00Z","source_size":100,"day_bucket":"2026-05-30","msg_count":1,"first_ts":"2026-05-30T15:00:00Z","last_ts":"2026-05-30T15:00:00Z"}\n' \
  "$SID_SHARED" "$shared_snap" >>"$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json projects --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "projects shared-slug: exits 0"
row_a="$(jq -c '.[] | select(.segment == "-tmp-proj-xesapps")' <<<"$out")"
row_b="$(jq -c '.[] | select(.segment == "-tmp-proj-xesapps-b")' <<<"$out")"
assert_eq "xesapps" "$(jq -r '.slug' <<<"$row_a")" "projects shared-slug: row A slug"
assert_eq "xesapps" "$(jq -r '.slug' <<<"$row_b")" "projects shared-slug: row B slug"
assert_eq "/tmp/proj-xesapps" "$(jq -r '.path' <<<"$row_a")" "projects shared-slug: row A path is its own"
assert_eq "/tmp/proj-xesapps-b" "$(jq -r '.path' <<<"$row_b")" "projects shared-slug: row B path is its own (not collapsed to A)"
assert_eq "git@example.com:xes/xesapps.git" "$(jq -r '.remote' <<<"$row_a")" "projects shared-slug: row A remote"
assert_eq "git@example.com:xes/xesapps-b.git" "$(jq -r '.remote' <<<"$row_b")" "projects shared-slug: row B remote"
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

# --- cached metadata (step 21): served from manifest, not the file ---------
# A manifest line carrying msg_count/first_ts/last_ts whose snapshot file is
# ABSENT must still report the cached values. If the reader fell through to
# the file it would report msg_count=0 and start/end=source_mtime.
_seed_journal
SID_CACHE="abcdef01-2345-4678-8abc-def012345678"
manifest_path="$CLAST_JOURNAL_DIR/.manifest.jsonl"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/cache.jsonl","snapshot":"transcripts/2026-05-30/-tmp-proj-xesapps/%s.jsonl","captured_at":"2026-05-30T18:00:00Z","source_mtime":"2026-05-30T17:45:00Z","source_size":800,"day_bucket":"2026-05-30","msg_count":248,"first_ts":"2026-05-30T15:01:22.118Z","last_ts":"2026-05-30T16:30:54.902Z"}\n' \
  "$SID_CACHE" "$SID_CACHE" >>"$manifest_path"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
row="$(jq -c --arg s "$SID_CACHE" '.[] | select(.session_id == $s)' <<<"$out")"
assert_eq "248" "$(jq -r '.msg_count_approx' <<<"$row")" "cached: msg_count_approx from manifest (file absent)"
assert_eq "2026-05-30T15:01:22.118Z" "$(jq -r '.start' <<<"$row")" "cached: start from cached first_ts"
assert_eq "2026-05-30T16:30:54.902Z" "$(jq -r '.end' <<<"$row")" "cached: end from cached last_ts"
# This synthetic line carries msg_count but NOT the classification counts, and
# its file is absent — the counts can't be determined, so they surface as null
# and substantive falls SAFE to true (wake must never auto-dismiss a session it
# couldn't classify).
assert_eq "null" "$(jq -r '.user_msg_count' <<<"$row")" "cached: user_msg_count null when unknowable"
assert_eq "null" "$(jq -r '.assistant_msg_count' <<<"$row")" "cached: assistant_msg_count null when unknowable"
assert_eq "true" "$(jq -r '.substantive' <<<"$row")" "cached: substantive fail-safe true when counts unknown"
teardown_test_journal

# --- legacy line (no cache fields): falls back to file, start/end=mtime -----
# Same shape but WITHOUT the cache fields and with an absent file: the reader
# must fall back, yielding msg_count=0 and start/end=source_mtime.
_seed_journal
SID_LEGACY="abcdef01-2345-4678-8abc-def0deadbeef"
manifest_path="$CLAST_JOURNAL_DIR/.manifest.jsonl"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/legacy.jsonl","snapshot":"transcripts/2026-05-30/-tmp-proj-xesapps/%s.jsonl","captured_at":"2026-05-30T18:00:00Z","source_mtime":"2026-05-30T17:45:00Z","source_size":800,"day_bucket":"2026-05-30"}\n' \
  "$SID_LEGACY" "$SID_LEGACY" >>"$manifest_path"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
row="$(jq -c --arg s "$SID_LEGACY" '.[] | select(.session_id == $s)' <<<"$out")"
assert_eq "0" "$(jq -r '.msg_count_approx' <<<"$row")" "legacy: msg_count_approx falls back to 0 (file absent)"
assert_eq "2026-05-30T17:45:00Z" "$(jq -r '.start' <<<"$row")" "legacy: start falls back to source_mtime"
assert_eq "2026-05-30T17:45:00Z" "$(jq -r '.end' <<<"$row")" "legacy: end falls back to source_mtime"
assert_eq "null" "$(jq -r '.user_msg_count' <<<"$row")" "legacy: user_msg_count null (file absent)"
assert_eq "true" "$(jq -r '.substantive' <<<"$row")" "legacy: substantive fail-safe true (file absent)"
teardown_test_journal

# --- classification recomputed from the transcript for legacy lines ---------
# The journal-seed manifest predates the cached counts, but the transcript is
# present, so sessions recomputes user/assistant counts on the fly. Session
# 22222222 has 2 real user prompts and 2 assistant replies → substantive.
_seed_journal
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
row="$(jq -c '.[] | select(.session_id == "22222222-2222-4222-8222-222222222222")' <<<"$out")"
assert_eq "2" "$(jq -r '.user_msg_count' <<<"$row")" "recompute: user_msg_count from transcript"
assert_eq "2" "$(jq -r '.assistant_msg_count' <<<"$row")" "recompute: assistant_msg_count from transcript"
assert_eq "true" "$(jq -r '.substantive' <<<"$row")" "recompute: substantive true"
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
# Classification fields (recomputed from the transcript for this legacy line).
assert_eq "2" "$(jq -r '.user_msg_count' <<<"$out")" "show --json: user_msg_count"
assert_eq "2" "$(jq -r '.assistant_msg_count' <<<"$out")" "show --json: assistant_msg_count"
assert_eq "true" "$(jq -r '.substantive' <<<"$out")" "show --json: substantive"

# --- large multi-line session: must not SIGPIPE under pipefail (regression) -
# A session whose concatenated user-message text exceeds the 64KB pipe buffer
# across many lines used to abort `show` with exit 141 (SIGPIPE) via a
# `printf "$user_msgs" | head -n1` pipeline. See show.bash first_prompt/last_prompt.
big_uuid="99999999-9999-4999-8999-999999999999"
big_snap="transcripts/2026-05-30/-tmp-proj-xesapps/$big_uuid.jsonl"
big_abs="$CLAST_JOURNAL_DIR/$big_snap"
mkdir -p "$(dirname "$big_abs")"
big_pad="$(printf 'x%.0s' {1..300})"   # ~300-byte payload per message
{
  printf '{"type":"summary","timestamp":"2026-05-30T13:00:00Z","session_id":"%s"}\n' "$big_uuid"
  for (( bn=1; bn<=300; bn++ )); do    # 300 lines × ~300 bytes ≫ 64KB
    printf '{"type":"user","timestamp":"2026-05-30T13:00:00Z","content":"msg %d %s"}\n' "$bn" "$big_pad"
  done
} >"$big_abs"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/9999.jsonl","snapshot":"%s","captured_at":"2026-05-30T13:30:00Z","source_mtime":"2026-05-30T13:25:00Z","source_size":99999,"day_bucket":"2026-05-30"}\n' \
  "$big_uuid" "$big_snap" >>"$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" --json show "$big_uuid" --full --turns 1 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "show large multi-line session: exits 0 (no SIGPIPE)"
case "$(jq -r '.first_prompt // empty' <<<"$out")" in
  "msg 1 "*) _clast_test_pass "show large session: first_prompt is first user message" ;;
  *) _clast_test_fail "show large session: first_prompt is first user message"; printf '%s\n' "$out" >&2 ;;
esac

# --- session with a >128KB turn: must not blow ARG_MAX (regression) ---------
# `--full` grafts turn arrays onto the JSON. Passing a >128KB turn array as a
# jq --argjson argv value used to fail with exit 126 "Argument list too long"
# (MAX_ARG_STRLEN), even well under total ARG_MAX. show.bash now feeds the
# arrays via stdin. The huge turn must be the LAST turn so --turns 1 keeps it.
# NB: the >128KB blob is streamed straight to disk and show's output is read
# back from a file — never held in a shell variable, which would itself bust
# MAX_ARG_STRLEN on the next exec (the env string would exceed the cap).
huge_uuid="88888888-8888-4888-8888-888888888888"
huge_snap="transcripts/2026-05-30/-tmp-proj-xesapps/$huge_uuid.jsonl"
huge_abs="$CLAST_JOURNAL_DIR/$huge_snap"
huge_out="$CLAST_JOURNAL_DIR/../huge-show.json"
mkdir -p "$(dirname "$huge_abs")"
{
  printf '{"type":"summary","timestamp":"2026-05-30T13:00:00Z","session_id":"%s"}\n' "$huge_uuid"
  printf '{"type":"user","timestamp":"2026-05-30T13:00:00Z","content":"small first prompt"}\n'
  # 200KB all-ASCII 'x' (needs no JSON escaping), streamed inline — never a var.
  printf '{"type":"assistant","timestamp":"2026-05-30T13:01:00Z","content":"'
  head -c 200000 /dev/zero | tr '\0' x
  printf '"}\n'
} >"$huge_abs"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/8888.jsonl","snapshot":"%s","captured_at":"2026-05-30T13:30:00Z","source_mtime":"2026-05-30T13:25:00Z","source_size":200500,"day_bucket":"2026-05-30"}\n' \
  "$huge_uuid" "$huge_snap" >>"$CLAST_JOURNAL_DIR/.manifest.jsonl"
"$CLAST_BIN" --json show "$huge_uuid" --full --turns 1 >"$huge_out" 2>/dev/null && rc=$? || rc=$?
assert_eq "0" "$rc" "show >128KB turn: exits 0 (no ARG_MAX overflow)"
assert_eq "1" "$(jq '.last_turns | length' "$huge_out")" "show >128KB turn: last_turns length 1"
assert_eq "200000" "$(jq -r '.last_turns[0].text | length' "$huge_out")" "show >128KB turn: huge turn text intact"
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
