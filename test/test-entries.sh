#!/usr/bin/env bash
# test-entries.sh — `clast entries` list/read/write integration suite.
# Subprocess-style: runs bin/clast against the multi-project fixtures.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-entries"

CLAST_BIN="$PWD/bin/clast"
FROZEN_EPOCH=$(date -d "2026-05-30T14:30:00Z" +%s)

# Subprocesses inherit TZ=UTC so local time math (clast_today + HHMM) is
# deterministic across developer machines.
export TZ=UTC

_seed_full() {
  setup_test_journal >/dev/null
  make_fixture_journal_seed_from "multi-project/journal-seed"
  make_fixture_entries_seed_from "multi-project/entries-seed"
}

_seed_manifest_only() {
  setup_test_journal >/dev/null
  make_fixture_journal_seed_from "multi-project/journal-seed"
}

_env_for_write() {
  CLAST_NOW_EPOCH="$FROZEN_EPOCH" \
  CLAST_AUTHOR=test-user \
  CLAST_MACHINE=test-host \
  TZ=UTC \
  "$@"
}

KNOWN_SID="22222222-2222-4222-8222-222222222222"

# === list ===================================================================

# --- default (no flags): 3 rows sorted by date/time desc -------------------
_seed_full
out="$("$CLAST_BIN" entries 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries default: exits 0"
case "$out" in
  *"entry"*"tags"*) _clast_test_pass "entries default: header line" ;;
  *) _clast_test_fail "entries default: header line"; printf '%s\n' "$out" >&2 ;;
esac
lines="$(printf '%s\n' "$out" | grep -c '^20')"
assert_eq "3" "$lines" "entries default: 3 data rows"
# Sort check: first data row is most recent (2026-05-30 14:30).
first_data="$(printf '%s\n' "$out" | grep '^20' | head -n1)"
case "$first_data" in
  "2026-05-30-1430-xesapps-vw-consumer-fields-explain.md"*) _clast_test_pass "entries default: first row is most recent" ;;
  *) _clast_test_fail "entries default: first row is most recent"; printf '%s\n' "$first_data" >&2 ;;
esac
teardown_test_journal

# --- --json schema (10 keys, tags arrays, absolute paths) ------------------
_seed_full
out="$("$CLAST_BIN" --json entries 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --json: exits 0"
assert_eq "3" "$(jq 'length' <<<"$out")" "entries --json: 3 rows"
for k in path date time day_bucket project session_id session_slug branch tags title; do
  present="$(jq --arg k "$k" '.[0] | has($k)' <<<"$out")"
  assert_eq "true" "$present" "entries --json: row has $k"
done
xes_tags="$(jq -c 'map(select(.session_slug == "vw-consumer-fields-explain")) | .[0].tags' <<<"$out")"
assert_eq '["mysql","optimization","eav"]' "$xes_tags" "entries --json: xesapps 2026-05-30 tags"
path0="$(jq -r '.[0].path' <<<"$out")"
case "$path0" in
  /*) _clast_test_pass "entries --json: path is absolute" ;;
  *) _clast_test_fail "entries --json: path is absolute"; printf '%s\n' "$path0" >&2 ;;
esac
teardown_test_journal

# --- --day 2026-05-30 ------------------------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --day 2026-05-30: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "entries --day 2026-05-30: 2 rows"
teardown_test_journal

# --- --since/--until window ------------------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --since 2026-05-22 --until 2026-05-29 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --since/--until: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "entries --since/--until: 1 row"
assert_eq "old-thread" "$(jq -r '.[0].session_slug' <<<"$out")" "entries --since/--until: old-thread"
teardown_test_journal

# --- --project xesapps -----------------------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --project xesapps 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --project xesapps: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "entries --project xesapps: 2 rows"
teardown_test_journal

# --- --tag mysql -----------------------------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --tag mysql 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --tag mysql: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "entries --tag mysql: 1 row"
teardown_test_journal

# --- --tag mysql --tag optimization (intersection) -------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --tag mysql --tag optimization 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --tag intersection: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "entries --tag intersection: 1 row"
teardown_test_journal

# --- --tag mysql --tag does-not-exist --------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --tag mysql --tag does-not-exist 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --tag no-match: exits 0"
assert_eq "0" "$(jq 'length' <<<"$out")" "entries --tag no-match: 0 rows"
teardown_test_journal

# --- --limit 1 -------------------------------------------------------------
_seed_full
out="$("$CLAST_BIN" --json entries --limit 1 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --limit 1: exits 0"
assert_eq "1" "$(jq 'length' <<<"$out")" "entries --limit 1: 1 row"
assert_eq "vw-consumer-fields-explain" "$(jq -r '.[0].session_slug' <<<"$out")" "entries --limit 1: most recent"
teardown_test_journal

# --- --day combined with --since (mutual exclusion) ------------------------
_seed_full
err="$("$CLAST_BIN" entries --day 2026-05-30 --since 2026-05-22 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "entries --day + --since: exits 2"
case "$err" in
  *"mutually exclusive"*) _clast_test_pass "entries --day + --since: stderr mentions mutual exclusion" ;;
  *) _clast_test_fail "entries --day + --since: stderr mentions mutual exclusion"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- empty journal: default header only / --json [] ------------------------
setup_test_journal >/dev/null
out="$("$CLAST_BIN" entries 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries empty: exits 0"
case "$out" in
  *"entry"*"tags"*) _clast_test_pass "entries empty: header line printed" ;;
  *) _clast_test_fail "entries empty: header line printed" ;;
esac
out="$("$CLAST_BIN" --json entries 2>/dev/null)"
assert_eq "[]" "$out" "entries empty --json: []"
teardown_test_journal

# === read ===================================================================

# --- read by basename ------------------------------------------------------
_seed_full
expected="$(cat "$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-vw-consumer-fields-explain.md")"
out="$("$CLAST_BIN" entries read 2026-05-30-1430-xesapps-vw-consumer-fields-explain.md 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries read basename: exits 0"
assert_eq "$expected" "$out" "entries read basename: content byte-for-byte"
teardown_test_journal

# --- read by absolute path -------------------------------------------------
_seed_full
abs="$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-vw-consumer-fields-explain.md"
out="$("$CLAST_BIN" entries read "$abs" 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries read absolute: exits 0"
assert_eq "$expected" "$out" "entries read absolute: content"
teardown_test_journal

# --- read missing ----------------------------------------------------------
_seed_full
err="$("$CLAST_BIN" entries read no-such-entry.md 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "entries read missing: exits 1"
case "$err" in
  *"not found"*) _clast_test_pass "entries read missing: stderr mentions not found" ;;
  *) _clast_test_fail "entries read missing: stderr mentions not found"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# === write ==================================================================

# --- write --body-stdin (happy path) ---------------------------------------
_seed_manifest_only
out="$(printf 'Hello\n' | _env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug new-slug --body-stdin 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries write stdin: exits 0"
assert_eq "Wrote entries/2026-05-30-1430-xesapps-new-slug.md" "$out" "entries write stdin: stdout journal-relative"
target="$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-new-slug.md"
assert_file_exists "$target" "entries write stdin: file exists"

content="$(cat "$target")"
# Frontmatter key-order assertion: extract keys (text before colon) from the
# frontmatter block, in order, and compare to the documented sequence.
fm_keys="$(awk 'BEGIN{in_fm=0;seen=0} /^---$/{if(!seen){in_fm=1;seen=1;next} if(in_fm)exit} in_fm{sub(/:.*$/,""); print}' <<<"$content" | paste -sd, -)"
expected_keys="date,time,day_bucket,project,project_path,project_remote,branch,author,tags,session_id,session_slug,snapshot_path,machine,curated_source_mtime"
assert_eq "$expected_keys" "$fm_keys" "entries write stdin: frontmatter key order"
case "$content" in
  *"author: test-user"*) _clast_test_pass "entries write stdin: author=test-user" ;;
  *) _clast_test_fail "entries write stdin: author=test-user" ;;
esac
case "$content" in
  *"machine: test-host"*) _clast_test_pass "entries write stdin: machine=test-host" ;;
  *) _clast_test_fail "entries write stdin: machine=test-host" ;;
esac
case "$content" in
  *"project: xesapps"*) _clast_test_pass "entries write stdin: project=xesapps" ;;
  *) _clast_test_fail "entries write stdin: project=xesapps" ;;
esac
case "$content" in
  *"project_path: /tmp/proj-xesapps"*) _clast_test_pass "entries write stdin: project_path from registry" ;;
  *) _clast_test_fail "entries write stdin: project_path from registry" ;;
esac
case "$content" in
  *"project_remote:"*"example.com"*) _clast_test_pass "entries write stdin: project_remote from registry" ;;
  *) _clast_test_fail "entries write stdin: project_remote from registry" ;;
esac
case "$content" in
  *"branch: feat/consumer-fields"*) _clast_test_pass "entries write stdin: branch from snapshot gitBranch" ;;
  *) _clast_test_fail "entries write stdin: branch from snapshot gitBranch"; printf '%s\n' "$content" >&2 ;;
esac
case "$content" in
  *"session_id: $KNOWN_SID"*) _clast_test_pass "entries write stdin: session_id" ;;
  *) _clast_test_fail "entries write stdin: session_id" ;;
esac
case "$content" in
  *"session_slug: new-slug"*) _clast_test_pass "entries write stdin: session_slug" ;;
  *) _clast_test_fail "entries write stdin: session_slug" ;;
esac
case "$content" in
  *"tags: []"*) _clast_test_pass "entries write stdin: tags empty" ;;
  *) _clast_test_fail "entries write stdin: tags empty" ;;
esac
case "$content" in
  *"snapshot_path: transcripts/2026-05-30/-tmp-proj-xesapps/22222222"*) _clast_test_pass "entries write stdin: snapshot_path from manifest" ;;
  *) _clast_test_fail "entries write stdin: snapshot_path from manifest" ;;
esac
case "$content" in
  *'curated_source_mtime: "2026-05-30T14:30:30Z"'*) _clast_test_pass "entries write stdin: curated_source_mtime from manifest" ;;
  *) _clast_test_fail "entries write stdin: curated_source_mtime from manifest" ;;
esac
# Body assertion: everything after the second `---\n\n` must equal exactly "Hello\n".
body="$(awk 'BEGIN{seen=0;past=0;body=0} /^---$/{if(!seen){seen=1;next} if(!past){past=1;next}} past{if(!body){if($0~/^[[:space:]]*$/)next; body=1} print}' <<<"$content")"
assert_eq "Hello" "$body" "entries write stdin: body == Hello"

# --- cross-check: sessions probe sees the curated entry --------------------
sess_out="$("$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
curated="$(jq -r --arg s "$KNOWN_SID" '.[] | select(.session_id == $s) | .curated' <<<"$sess_out")"
assert_eq "true" "$curated" "sessions curated probe finds entries-step write"
teardown_test_journal

# --- write --tags --title --body-from --------------------------------------
_seed_manifest_only
body_file="$(mktemp)"
printf 'Body content from file.\n' >"$body_file"
out="$(_env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug tagged-slug --tags mysql,perf \
  --title "Long Title" --body-from "$body_file" 2>/dev/null)" && rc=$? || rc=$?
rm -f "$body_file"
assert_eq "0" "$rc" "entries write tags+title: exits 0"
target="$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-tagged-slug.md"
assert_file_exists "$target" "entries write tags+title: file exists"
content="$(cat "$target")"
case "$content" in
  *"tags: [mysql, perf]"*) _clast_test_pass "entries write tags+title: tags inline array" ;;
  *) _clast_test_fail "entries write tags+title: tags inline array"; printf '%s\n' "$content" >&2 ;;
esac
body="$(awk 'BEGIN{seen=0;past=0;body=0} /^---$/{if(!seen){seen=1;next} if(!past){past=1;next}} past{if(!body){if($0~/^[[:space:]]*$/)next; body=1} print}' <<<"$content")"
case "$body" in
  "# Session: Long Title"*"Body content from file."*) _clast_test_pass "entries write tags+title: body has H1 + content" ;;
  *) _clast_test_fail "entries write tags+title: body has H1 + content"; printf '%s\n' "$body" >&2 ;;
esac
teardown_test_journal

# --- collision suffixing ---------------------------------------------------
_seed_manifest_only
for i in 1 2 3; do
  printf 'body %d\n' "$i" | _env_for_write "$CLAST_BIN" entries write \
    --session "$KNOWN_SID" --slug dup-slug --body-stdin >/dev/null
done
for suffix in '' '-2' '-3'; do
  f="$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-dup-slug${suffix}.md"
  assert_file_exists "$f" "collision: dup-slug${suffix}.md exists"
done
teardown_test_journal

# --- missing --session -----------------------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write --slug s --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write missing --session: exits 2"
case "$err" in
  *"--session"*) _clast_test_pass "write missing --session: stderr mentions --session" ;;
  *) _clast_test_fail "write missing --session: stderr mentions --session"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- missing --slug --------------------------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write --session "$KNOWN_SID" --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write missing --slug: exits 2"
case "$err" in
  *"--slug"*) _clast_test_pass "write missing --slug: stderr mentions --slug" ;;
  *) _clast_test_fail "write missing --slug: stderr mentions --slug"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- --body-from + --body-stdin (mutual exclusion) -------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug s --body-from /dev/null --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write body mutual exclusion: exits 2"
case "$err" in
  *"mutually exclusive"*) _clast_test_pass "write body mutual exclusion: stderr mentions exclusion" ;;
  *) _clast_test_fail "write body mutual exclusion: stderr mentions exclusion" ;;
esac
teardown_test_journal

# --- neither body flag -----------------------------------------------------
_seed_manifest_only
err="$(_env_for_write "$CLAST_BIN" entries write --session "$KNOWN_SID" --slug s 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write missing body flag: exits 2"
teardown_test_journal

# --- unknown session UUID --------------------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write \
  --session 00000000-0000-0000-0000-000000000000 --slug s --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "write unknown session: exits 1"
case "$err" in
  *"not found in manifest"*) _clast_test_pass "write unknown session: stderr mentions not found in manifest" ;;
  *) _clast_test_fail "write unknown session: stderr mentions not found in manifest"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- bad session UUID format -----------------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write --session not-a-uuid --slug s --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write bad UUID: exits 2"
teardown_test_journal

# --- bad slug (uppercase / underscore) -------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write --session "$KNOWN_SID" --slug BAD_SLUG --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write bad slug: exits 2"
teardown_test_journal

# --- bad tag ---------------------------------------------------------------
_seed_manifest_only
err="$(printf 'x' | _env_for_write "$CLAST_BIN" entries write --session "$KNOWN_SID" --slug s --tags 'mysql,BAD_TAG' --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "write bad tag: exits 2"
teardown_test_journal

# --- mixed-case tags auto-lowercased --------------------------------------
_seed_manifest_only
out="$(printf 'body' | _env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug lc-slug --tags 'ADRs,Phase-0' \
  --body-stdin 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "write mixed-case tags: exits 0"
target="$CLAST_JOURNAL_DIR/entries/2026-05-30-1430-xesapps-lc-slug.md"
assert_file_exists "$target" "write mixed-case tags: file exists"
content="$(cat "$target")"
case "$content" in
  *"tags: [adrs, phase-0]"*) _clast_test_pass "write mixed-case tags: lowercased in frontmatter" ;;
  *) _clast_test_fail "write mixed-case tags: lowercased in frontmatter"; printf '%s\n' "$content" >&2 ;;
esac
teardown_test_journal

# --- empty body ------------------------------------------------------------
_seed_manifest_only
err="$(printf '' | _env_for_write "$CLAST_BIN" entries write --session "$KNOWN_SID" --slug s --body-stdin 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "write empty body: exits 1"
case "$err" in
  *"body is empty"*) _clast_test_pass "write empty body: stderr mentions body is empty" ;;
  *) _clast_test_fail "write empty body: stderr mentions body is empty"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- --json success --------------------------------------------------------
_seed_manifest_only
out="$(printf 'Hello\n' | _env_for_write "$CLAST_BIN" --json entries write \
  --session "$KNOWN_SID" --slug json-slug --body-stdin 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "write --json success: exits 0"
if jq -e '.path' >/dev/null 2>&1 <<<"$out"; then
  _clast_test_pass "write --json success: stdout has .path"
else
  _clast_test_fail "write --json success: stdout has .path"; printf '%s\n' "$out" >&2
fi
path_val="$(jq -r '.path' <<<"$out")"
case "$path_val" in
  /*) _clast_test_pass "write --json success: path absolute" ;;
  *) _clast_test_fail "write --json success: path absolute" ;;
esac
teardown_test_journal

# --- --json error ----------------------------------------------------------
_seed_manifest_only
out="$(printf 'x' | _env_for_write "$CLAST_BIN" --json entries write \
  --session 00000000-0000-0000-0000-000000000000 --slug s --body-stdin 2>/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "write --json error: exits 1"
err_msg="$(jq -r '.error // empty' <<<"$out")"
err_code="$(jq -r '.code // empty' <<<"$out")"
case "$err_msg" in
  *"not found in manifest"*) _clast_test_pass "write --json error: error field present" ;;
  *) _clast_test_fail "write --json error: error field present"; printf '%s\n' "$out" >&2 ;;
esac
assert_eq "1" "$err_code" "write --json error: code=1"
teardown_test_journal

# === stale detection ========================================================

# --- stale=false when mtime matches (freshly curated) ---------------------
_seed_manifest_only
printf 'Freshly curated session.\n' | _env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug stale-fresh --body-stdin >/dev/null 2>&1
sess_out="$("$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
stale_val="$(jq -r --arg s "$KNOWN_SID" '.[] | select(.session_id == $s) | .stale' <<<"$sess_out")"
assert_eq "false" "$stale_val" "stale detection: false when mtime matches"
teardown_test_journal

# --- stale=true when manifest mtime changes after curation ----------------
_seed_manifest_only
printf 'Will become stale.\n' | _env_for_write "$CLAST_BIN" entries write \
  --session "$KNOWN_SID" --slug stale-updated --body-stdin >/dev/null 2>&1
# Simulate re-snapshot with a newer mtime by appending a new manifest line.
manifest_path="$CLAST_JOURNAL_DIR/.manifest.jsonl"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/2222.jsonl","snapshot":"transcripts/2026-05-30/-tmp-proj-xesapps/%s.jsonl","captured_at":"2026-05-30T18:00:00Z","source_mtime":"2026-05-30T17:45:00Z","source_size":800,"day_bucket":"2026-05-30"}\n' \
  "$KNOWN_SID" "$KNOWN_SID" >>"$manifest_path"
sess_out="$("$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
stale_val="$(jq -r --arg s "$KNOWN_SID" '.[] | select(.session_id == $s) | .stale' <<<"$sess_out")"
assert_eq "true" "$stale_val" "stale detection: true when mtime differs after curation"
curated_val="$(jq -r --arg s "$KNOWN_SID" '.[] | select(.session_id == $s) | .curated' <<<"$sess_out")"
assert_eq "true" "$curated_val" "stale detection: still curated=true"
teardown_test_journal

# --- stale=false for legacy entries without curated_source_mtime ----------
_seed_full
# The seed entries don't have curated_source_mtime; append a new manifest line
# with a different mtime to test the conservative fallback.
manifest_path="$CLAST_JOURNAL_DIR/.manifest.jsonl"
printf '{"session_id":"%s","source":"/tmp/proj-xesapps/2222.jsonl","snapshot":"transcripts/2026-05-30/-tmp-proj-xesapps/%s.jsonl","captured_at":"2026-05-30T18:00:00Z","source_mtime":"2026-05-30T17:45:00Z","source_size":800,"day_bucket":"2026-05-30"}\n' \
  "$KNOWN_SID" "$KNOWN_SID" >>"$manifest_path"
sess_out="$("$CLAST_BIN" --json sessions --day 2026-05-30 2>/dev/null)"
stale_val="$(jq -r --arg s "$KNOWN_SID" '.[] | select(.session_id == $s) | .stale' <<<"$sess_out")"
assert_eq "false" "$stale_val" "stale detection: false for legacy entry without curated_source_mtime"
teardown_test_journal

# === misc ===================================================================

# --- unknown subcommand ----------------------------------------------------
_seed_full
err="$("$CLAST_BIN" entries unknown 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "entries unknown: exits 2"
case "$err" in
  *"unknown subcommand"*) _clast_test_pass "entries unknown: stderr mentions unknown subcommand" ;;
  *) _clast_test_fail "entries unknown: stderr mentions unknown subcommand"; printf '%s\n' "$err" >&2 ;;
esac
teardown_test_journal

# --- --help paths ----------------------------------------------------------
out="$("$CLAST_BIN" entries --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries --help: exits 0"
out="$("$CLAST_BIN" entries write --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "entries write --help: exits 0"

clast_test_summary
