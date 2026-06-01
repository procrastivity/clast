#!/usr/bin/env bash
# test-doctor.sh — `clast doctor` integration suite.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-doctor"

CLAST_BIN="$PWD/bin/clast"
export TZ=UTC
FROZEN_EPOCH=$(date -u -d "2026-05-30T12:00:00Z" +%s)
export CLAST_NOW_EPOCH="$FROZEN_EPOCH"

_seed_full() {
  setup_test_journal >/dev/null
  make_fixture_journal_seed_from "multi-project/journal-seed"
}

# --- all-clean (strip the dddddddd row) ------------------------------------
_seed_full
grep -v 'dddddddd' "$CLAST_JOURNAL_DIR/.manifest.jsonl" \
  > "$CLAST_JOURNAL_DIR/.manifest.jsonl.new"
mv "$CLAST_JOURNAL_DIR/.manifest.jsonl.new" "$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "doctor all-clean: exit 0"
nfails="$(printf '%s\n' "$out" | grep -c '^!' || true)"
ncrit="$(printf '%s\n' "$out" | grep -c '^✗' || true)"
assert_eq "0" "$nfails" "doctor all-clean: no warn lines"
assert_eq "0" "$ncrit" "doctor all-clean: no critical lines"
teardown_test_journal

# --- missing snapshot (the dddddddd row in seed) ---------------------------
_seed_full
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor missing-snapshot: exit 1"
case "$out" in
  *"! Missing snapshots: 1"*"dddddddd"*) _clast_test_pass "doctor missing-snapshot: dddddddd listed" ;;
  *) _clast_test_fail "doctor missing-snapshot: dddddddd listed"; printf '%s\n' "$out" >&2 ;;
esac
json="$("$CLAST_BIN" --json doctor 2>/dev/null || true)"
sev="$(jq -r '.findings[] | select(.check=="missing_snapshots") | .severity' <<<"$json")"
assert_eq "warn" "$sev" "doctor missing-snapshot --json: severity warn"
item="$(jq -r '.findings[] | select(.check=="missing_snapshots") | .items[0]' <<<"$json")"
case "$item" in
  *"dddddddd"*) _clast_test_pass "doctor missing-snapshot --json: item path" ;;
  *) _clast_test_fail "doctor missing-snapshot --json: item path" ;;
esac
teardown_test_journal

# --- orphan snapshot detection ---------------------------------------------
_seed_full
mkdir -p "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan"
: > "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor orphan: exit 1"
case "$out" in
  *"! Orphan snapshots: 1"*"ffffffff"*) _clast_test_pass "doctor orphan: ffffffff listed" ;;
  *) _clast_test_fail "doctor orphan: ffffffff listed"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- orphan removal under --fix --yes --------------------------------------
_seed_full
orphan_path="$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
mkdir -p "$(dirname "$orphan_path")"
: > "$orphan_path"
out="$("$CLAST_BIN" doctor --fix --yes 2>&1)" && rc=$? || rc=$?
# Missing-snapshot warn (dddddddd) survives; exit 1.
assert_eq "1" "$rc" "doctor --fix --yes: exit 1 (missing warn survives)"
assert_file_not_exists "$orphan_path" "doctor --fix --yes: orphan removed"
case "$out" in
  *"removed 1 orphan snapshot(s)"*) _clast_test_pass "doctor --fix --yes: fix summary" ;;
  *) _clast_test_fail "doctor --fix --yes: fix summary"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- --fix without --yes and no TTY → exit 2 -------------------------------
_seed_full
orphan_path="$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
mkdir -p "$(dirname "$orphan_path")"
: > "$orphan_path"
( "$CLAST_BIN" doctor --fix </dev/null ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "doctor --fix no-tty: exit 2"
assert_file_exists "$orphan_path" "doctor --fix no-tty: orphan untouched"
teardown_test_journal

# --- critical manifest without --fix → exit 4 ------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree "corrupt-manifest"
manifest_before_mtime="$(stat -c %Y "$CLAST_JOURNAL_DIR/.manifest.jsonl" 2>/dev/null || stat -f %m "$CLAST_JOURNAL_DIR/.manifest.jsonl")"
manifest_before_hash="$(jq -sR . <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | jq -r length)"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "4" "$rc" "doctor corrupt: exit 4"
case "$out" in
  *"✗ Manifest"*"unparseable"*) _clast_test_pass "doctor corrupt: critical manifest line" ;;
  *) _clast_test_fail "doctor corrupt: critical manifest line"; printf '%s\n' "$out" >&2 ;;
esac
manifest_after_hash="$(jq -sR . <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | jq -r length)"
assert_eq "$manifest_before_hash" "$manifest_after_hash" "doctor corrupt: manifest unchanged"
: "$manifest_before_mtime"  # silence unused-var warning
teardown_test_journal

# --- critical manifest with --fix → rebuild → exit 0 -----------------------
setup_test_journal >/dev/null
make_fixture_journal_tree "corrupt-manifest"
# Pin snapshot mtimes to the suite's frozen clock so the rebuilt manifest's
# captured_at is deterministic and well clear of the 04:00 day-cutoff window
# (FROZEN_EPOCH is noon UTC). The rebuild derives captured_at from each
# snapshot's file mtime, which is "now" for a freshly-copied fixture — without
# this, day_cutoff_sanity warns (and doctor exits 1) whenever the suite happens
# to run near 04:00 UTC.
find "$CLAST_JOURNAL_DIR/transcripts" -type f -name '*.jsonl' -exec touch -d "@${CLAST_NOW_EPOCH}" {} +
out="$("$CLAST_BIN" doctor --fix 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "doctor corrupt --fix: exit 0"
nlines="$(wc -l <"$CLAST_JOURNAL_DIR/.manifest.jsonl" | tr -d ' ')"
assert_eq "2" "$nlines" "doctor corrupt --fix: manifest has 2 lines"
# Each line is parseable JSON.
parse_ok="$(jq -cR 'fromjson?' "$CLAST_JOURNAL_DIR/.manifest.jsonl" | wc -l | tr -d ' ')"
assert_eq "2" "$parse_ok" "doctor corrupt --fix: 2 parseable lines"
case "$out" in
  *"manifest rebuilt: 2 line(s)"*) _clast_test_pass "doctor corrupt --fix: rebuild log" ;;
  *) _clast_test_fail "doctor corrupt --fix: rebuild log"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- registry duplicate slug → warn ----------------------------------------
setup_test_journal >/dev/null
cat > "$CLAST_JOURNAL_DIR/projects.json" <<'EOF'
{"path":"/tmp/a","slug":"xesapps","first_seen":"2026-05-01","aliases":[]}
{"path":"/tmp/b","slug":"xesapps","first_seen":"2026-05-02","aliases":[]}
EOF
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor dup-slug: exit 1"
case "$out" in
  *"duplicate slug: xesapps"*) _clast_test_pass "doctor dup-slug: finding lists slug" ;;
  *) _clast_test_fail "doctor dup-slug: finding lists slug"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- registry alias collision → warn ---------------------------------------
setup_test_journal >/dev/null
cat > "$CLAST_JOURNAL_DIR/projects.json" <<'EOF'
{"path":"/tmp/a","slug":"alpha","first_seen":"2026-05-01","aliases":["beta"]}
{"path":"/tmp/b","slug":"beta","first_seen":"2026-05-02","aliases":[]}
EOF
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor alias-collision: exit 1"
case "$out" in
  *"alias collision"*) _clast_test_pass "doctor alias-collision: finding present" ;;
  *) _clast_test_fail "doctor alias-collision: finding present"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- registry alias-alias collision (two slugs share the same alias) -------
setup_test_journal >/dev/null
cat > "$CLAST_JOURNAL_DIR/projects.json" <<'EOF'
{"path":"/tmp/a","slug":"alpha","first_seen":"2026-05-01","aliases":["shared-alias"]}
{"path":"/tmp/b","slug":"gamma","first_seen":"2026-05-02","aliases":["shared-alias"]}
EOF
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor alias-alias collision: exit 1"
case "$out" in
  *"share alias shared-alias"*) _clast_test_pass "doctor alias-alias collision: finding present" ;;
  *) _clast_test_fail "doctor alias-alias collision: finding present"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- orphan check tolerates missing manifest -------------------------------
setup_test_journal >/dev/null
mkdir -p "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-x"
: > "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-x/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor no-manifest+orphans: exit 1"
case "$out" in
  *"! Orphan snapshots: 1"*"aaaaaaaa"*) _clast_test_pass "doctor no-manifest+orphans: orphan reported" ;;
  *) _clast_test_fail "doctor no-manifest+orphans: orphan reported"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- --fix skips orphan removal when a non-manifest critical persists ------
setup_test_journal >/dev/null
# Valid manifest with one orphan-able transcript.
cat > "$CLAST_JOURNAL_DIR/.manifest.jsonl" <<'EOF'
{"session_id":"77777777-7777-4777-8777-777777777777","source":"/tmp/p/7777.jsonl","snapshot":"transcripts/2026-05-30/-tmp-p/77777777-7777-4777-8777-777777777777.jsonl","captured_at":"2026-05-30T15:00:00Z","source_mtime":"2026-05-30T14:30:00Z","source_size":100,"day_bucket":"2026-05-30"}
EOF
# Unparseable registry → registry_validity critical.
printf 'not valid json at all\n' > "$CLAST_JOURNAL_DIR/projects.json"
# Stray orphan that would normally be removed under --fix --yes.
orphan_path="$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
mkdir -p "$(dirname "$orphan_path")"
: > "$orphan_path"
out="$("$CLAST_BIN" doctor --fix --yes 2>&1)" && rc=$? || rc=$?
assert_eq "4" "$rc" "doctor non-manifest critical + --fix: exit 4 (still critical)"
assert_file_exists "$orphan_path" "doctor non-manifest critical: orphan preserved"
case "$out" in
  *"removed"*"orphan"*) _clast_test_fail "doctor non-manifest critical: no removal summary" ;;
  *) _clast_test_pass "doctor non-manifest critical: no removal summary" ;;
esac
teardown_test_journal

# --- --json --fix without --yes errors (no stdout prompt) -------------------
_seed_full
mkdir -p "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan"
: > "$CLAST_JOURNAL_DIR/transcripts/2026-05-30/-tmp-proj-orphan/ffffffff-ffff-4fff-8fff-ffffffffffff.jsonl"
( "$CLAST_BIN" --json doctor --fix </dev/null ) >/tmp/clast-json-fix-out 2>&1; rc=$?
assert_eq "2" "$rc" "doctor --json --fix no --yes: exit 2"
# Whatever lands on stdout/stderr should NOT include the interactive prompt.
if grep -q 'Remove these' /tmp/clast-json-fix-out; then
  _clast_test_fail "doctor --json --fix no --yes: no prompt leaked"
else
  _clast_test_pass "doctor --json --fix no --yes: no prompt leaked"
fi
rm -f /tmp/clast-json-fix-out
teardown_test_journal

# --- day-bucket mismatch ----------------------------------------------------
setup_test_journal >/dev/null
cat > "$CLAST_JOURNAL_DIR/.manifest.jsonl" <<'EOF'
{"session_id":"77777777-7777-4777-8777-777777777777","source":"/tmp/p/7777.jsonl","snapshot":"transcripts/2026-05-22/-tmp-p/77777777-7777-4777-8777-777777777777.jsonl","captured_at":"2026-05-22T15:00:00Z","source_mtime":"2026-05-22T14:30:00Z","source_size":100,"day_bucket":"2026-05-30"}
EOF
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor day-bucket mismatch: exit 1"
case "$out" in
  *"! Day-bucket consistency"*) _clast_test_pass "doctor day-bucket: warn finding" ;;
  *) _clast_test_fail "doctor day-bucket: warn finding"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- day-cutoff warn (≥6 of 10 lines within ±30min of 04:00 UTC) -----------
setup_test_journal >/dev/null
{
  for i in 1 2 3 4 5 6; do
    printf '{"session_id":"4444444%d-4444-4444-8444-444444444444","source":"/t/x.jsonl","snapshot":"transcripts/2026-05-30/-t/4444444%d-4444-4444-8444-444444444444.jsonl","captured_at":"2026-05-30T04:00:0%dZ","source_mtime":"2026-05-30T04:00:00Z","source_size":1,"day_bucket":"2026-05-30"}\n' "$i" "$i" "$i"
  done
  for i in 1 2 3 4; do
    printf '{"session_id":"5555555%d-5555-4555-8555-555555555555","source":"/t/x.jsonl","snapshot":"transcripts/2026-05-30/-t/5555555%d-5555-4555-8555-555555555555.jsonl","captured_at":"2026-05-30T1%d:00:00Z","source_mtime":"2026-05-30T12:00:00Z","source_size":1,"day_bucket":"2026-05-30"}\n' "$i" "$i" "$i"
  done
} > "$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
assert_eq "1" "$rc" "doctor day-cutoff warn: exit 1"
case "$out" in
  *"! Day-cutoff sanity"*) _clast_test_pass "doctor day-cutoff: warn finding" ;;
  *) _clast_test_fail "doctor day-cutoff: warn finding"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- day-cutoff no-warn (uniform distribution) -----------------------------
setup_test_journal >/dev/null
{
  for h in 06 08 10 12 14 16 18 20 22; do
    printf '{"session_id":"%s%s%s-6666-4666-8666-666666666666","source":"/t/x.jsonl","snapshot":"transcripts/2026-05-30/-t/%s%s%s-6666-4666-8666-666666666666.jsonl","captured_at":"2026-05-30T%s:00:00Z","source_mtime":"2026-05-30T11:00:00Z","source_size":1,"day_bucket":"2026-05-30"}\n' "$h" "$h" "$h" "$h" "$h" "$h" "$h"
  done
} > "$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" doctor 2>&1)" && rc=$? || rc=$?
# Session IDs all share format and pass schema; manifest is otherwise clean.
assert_eq "1" "$rc" "doctor day-cutoff uniform: exit 1 (missing snapshots warn)"
# Day-cutoff finding itself is ok.
json="$("$CLAST_BIN" --json doctor 2>/dev/null)"
sev="$(jq -r '.findings[] | select(.check=="day_cutoff_sanity") | .severity' <<<"$json")"
assert_eq "ok" "$sev" "doctor day-cutoff uniform: severity ok"
teardown_test_journal

# --- --json all-clean shape -------------------------------------------------
_seed_full
grep -v 'dddddddd' "$CLAST_JOURNAL_DIR/.manifest.jsonl" \
  > "$CLAST_JOURNAL_DIR/.manifest.jsonl.new"
mv "$CLAST_JOURNAL_DIR/.manifest.jsonl.new" "$CLAST_JOURNAL_DIR/.manifest.jsonl"
out="$("$CLAST_BIN" --json doctor 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "doctor --json clean: exit 0"
assert_eq "6" "$(jq '.findings | length' <<<"$out")" "doctor --json clean: 6 findings"
assert_eq "0" "$(jq -r '.exit_code' <<<"$out")" "doctor --json clean: exit_code=0"
assert_eq "[]" "$(jq -c '.fixed' <<<"$out")" "doctor --json clean: fixed empty"
# Canonical order.
order="$(jq -r '[.findings[].check] | @csv' <<<"$out")"
expected='"manifest_validity","registry_validity","orphan_snapshots","missing_snapshots","day_bucket_consistency","day_cutoff_sanity"'
assert_eq "$expected" "$order" "doctor --json clean: canonical order"
teardown_test_journal

# --- --help / unknown flag / positional ------------------------------------
( "$CLAST_BIN" doctor --help ) >/dev/null 2>&1; rc=$?
assert_eq "0" "$rc" "doctor --help: exit 0"
( "$CLAST_BIN" doctor --nope ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "doctor --nope: exit 2"
( "$CLAST_BIN" doctor junk ) >/dev/null 2>&1; rc=$?
assert_eq "2" "$rc" "doctor positional: exit 2"

clast_test_summary
