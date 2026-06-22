#!/usr/bin/env bash
# test-registry-cmd.sh — `clast registry` subcommand surface.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-registry-cmd"

CLAST_BIN="$PWD/bin/clast-plumbing"

# --- registry list (human) against the fixture ------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
human="$("$CLAST_BIN" registry list)"
case "$human" in
  *"slug"*"label"*"path"*"remote"*"aliases"*) _clast_test_pass "list: header row present" ;;
  *) _clast_test_fail "list: header row present"; printf '%s\n' "$human" >&2 ;;
esac
case "$human" in
  *xesapps*) _clast_test_pass "list: shows xesapps row" ;;
  *) _clast_test_fail "list: shows xesapps row" ;;
esac
teardown_test_journal

# --- registry list --json ----------------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
arr="$("$CLAST_BIN" registry list --json)"
if jq -e . >/dev/null 2>&1 <<<"$arr"; then
  _clast_test_pass "list --json: valid JSON"
else
  _clast_test_fail "list --json: valid JSON"
fi
assert_eq "3" "$(jq 'length' <<<"$arr")" "list --json: 3 entries"
teardown_test_journal

# --- registry add (human) ----------------------------------------------------
# /tmp/proj-new auto-derives label "tmp" from its parent dir, shown in the
# confirmation line.
setup_test_journal >/dev/null
expected_path="$(realpath -m /tmp/proj-new)"
out="$("$CLAST_BIN" registry add /tmp/proj-new --slug proj-new)" && rc=$? || rc=$?
assert_eq "0" "$rc" "add: exits 0"
assert_eq "registered proj-new (tmp) → $expected_path" "$out" "add: confirmation line with label"
teardown_test_journal

# --- registry add --json -----------------------------------------------------
setup_test_journal >/dev/null
out="$("$CLAST_BIN" registry add /tmp/proj-new --slug proj-new --label demo --json)" && rc=$? || rc=$?
assert_eq "0" "$rc" "add --json: exits 0"
assert_eq "proj-new" "$(jq -r .slug <<<"$out")" "add --json: slug field"
assert_eq "demo" "$(jq -r .label <<<"$out")" "add --json: label field"
assert_eq "$(realpath -m /tmp/proj-new)" "$(jq -r .path <<<"$out")" "add --json: path field"
teardown_test_journal

# --- registry resolve hit ----------------------------------------------------
setup_test_journal >/dev/null
existing="$(realpath -m /tmp/clast-resolve-existing)"
mkdir -p "$CLAST_JOURNAL_DIR"
jq -cn --arg p "$existing" '{path:$p,slug:"hit",first_seen:"2026-01-01",aliases:[]}' \
  >"$CLAST_JOURNAL_DIR/projects.json"
out="$("$CLAST_BIN" registry resolve /tmp/clast-resolve-existing)" && rc=$? || rc=$?
assert_eq "0" "$rc" "resolve hit: exits 0"
assert_eq "hit" "$out" "resolve hit: slug on stdout"
teardown_test_journal

# --- registry resolve miss (human) -------------------------------------------
setup_test_journal >/dev/null
stderr="$("$CLAST_BIN" registry resolve /tmp/nope 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "resolve miss: exits 1"
case "$stderr" in
  *"not registered"*) _clast_test_pass "resolve miss: stderr says 'not registered'" ;;
  *) _clast_test_fail "resolve miss: stderr says 'not registered'"; printf '%s\n' "$stderr" >&2 ;;
esac
teardown_test_journal

# --- registry resolve miss (--json) ------------------------------------------
setup_test_journal >/dev/null
stdout="$("$CLAST_BIN" registry resolve /tmp/nope --json 2>/dev/null)" && rc=$? || rc=$?
stderr="$("$CLAST_BIN" registry resolve /tmp/nope --json 2>&1 1>/dev/null)" || true
assert_eq "1" "$rc" "resolve --json miss: exits 1"
assert_eq '{"error":"not registered"}' "$stdout" "resolve --json miss: error on stdout"
assert_eq "" "$stderr" "resolve --json miss: empty stderr"
teardown_test_journal

# --- registry remove ---------------------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
out="$("$CLAST_BIN" registry remove xesapps)" && rc=$? || rc=$?
assert_eq "0" "$rc" "remove: exits 0"
arr="$("$CLAST_BIN" registry list --json)"
assert_eq "1" "$(jq 'length' <<<"$arr")" "remove: only scratch remains"
assert_eq "scratch" "$(jq -r '.[0].slug' <<<"$arr")" "remove: leftover is scratch"
teardown_test_journal

# --- registry add: --slug / --remote without value exit 2 -------------------
setup_test_journal >/dev/null
stderr="$("$CLAST_BIN" registry add /tmp/x --slug 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "add --slug missing value: exits 2"
case "$stderr" in
  *--slug*requires*) _clast_test_pass "add --slug missing value: stderr" ;;
  *) _clast_test_fail "add --slug missing value: stderr (got: $stderr)" ;;
esac
stderr="$("$CLAST_BIN" registry add /tmp/x --remote 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "add --remote missing value: exits 2"
case "$stderr" in
  *--remote*requires*) _clast_test_pass "add --remote missing value: stderr" ;;
  *) _clast_test_fail "add --remote missing value: stderr (got: $stderr)" ;;
esac
teardown_test_journal

# --- registry with no args ---------------------------------------------------
stderr="$("$CLAST_BIN" registry 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "no-op: exits 2"
case "$stderr" in
  *Usage*) _clast_test_pass "no-op: usage on stderr" ;;
  *) _clast_test_fail "no-op: usage on stderr"; printf '%s\n' "$stderr" >&2 ;;
esac

# --- registry bogus-op -------------------------------------------------------
stderr="$("$CLAST_BIN" registry bogus-op 2>&1 1>/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "bogus-op: exits 2"
case "$stderr" in
  *"unknown op"*) _clast_test_pass "bogus-op: 'unknown op' on stderr" ;;
  *) _clast_test_fail "bogus-op: 'unknown op' on stderr" ;;
esac

clast_test_summary
