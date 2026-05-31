#!/usr/bin/env bash
# test-registry.sh — exercises clast-registry-lib.bash.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

_CLAST_TEST_NAME="test-registry"
# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash
source lib/clast/clast-decode-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
source lib/clast/clast-registry-lib.bash

# --- clast_registry_path -----------------------------------------------------
setup_test_journal >/dev/null
assert_eq "$CLAST_JOURNAL_DIR/projects.json" "$(clast_registry_path)" "path = journal_dir/projects.json"
teardown_test_journal

# --- list: empty / missing returns [] ---------------------------------------
setup_test_journal >/dev/null
out="$(clast_registry_list_json)"
assert_eq "[]" "$out" "missing registry → []"
teardown_test_journal

# --- list: fixture has 3 valid lines (malformed dropped) --------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
out="$(clast_registry_list_json)"
assert_eq "3" "$(jq 'length' <<<"$out")" "fixture list yields 3 valid lines"
teardown_test_journal

# For resolve tests we build a tiny inline registry whose paths are
# already-canonical, since `realpath -m` differs by platform (on macOS
# `/tmp` → `/private/tmp`, `/home` → `/System/Volumes/Data/home`). The
# fixture's hard-coded `/home/...` paths still exercise list / match_remote
# below, where canonicalization is irrelevant.
_seed_resolve_registry() {
  local p1 p2 alias_path
  p1="$(realpath -m /tmp/clast-resolve-p1)"
  p2="$(realpath -m /tmp/clast-resolve-p2)"
  alias_path="$(realpath -m /tmp/clast-resolve-alias)"
  mkdir -p "$CLAST_JOURNAL_DIR"
  jq -cn --arg p "$p1" --arg a "$alias_path" \
    '{path: $p, slug: "alpha", remote: "R1", first_seen: "2026-01-01", aliases: [$a]}' \
    >"$CLAST_JOURNAL_DIR/projects.json"
  jq -cn --arg p "$p2" \
    '{path: $p, slug: "beta", first_seen: "2026-01-02", aliases: []}' \
    >>"$CLAST_JOURNAL_DIR/projects.json"
  # Trailing malformed line — resolve must tolerate.
  printf '{"path": "/oops", "slug":\n' >>"$CLAST_JOURNAL_DIR/projects.json"
  printf '%s\n' "$p1" "$p2" "$alias_path"
}

# --- resolve by path (hit) --------------------------------------------------
setup_test_journal >/dev/null
mapfile -t _seeded < <(_seed_resolve_registry)
out="$(clast_registry_resolve "${_seeded[0]}")" && rc=$? || rc=$?
assert_eq "alpha" "$out" "resolve by canonical path"
assert_eq "0" "$rc" "resolve by path exits 0"
teardown_test_journal

# --- resolve by alias --------------------------------------------------------
setup_test_journal >/dev/null
mapfile -t _seeded < <(_seed_resolve_registry)
out="$(clast_registry_resolve "${_seeded[2]}")" && rc=$? || rc=$?
assert_eq "alpha" "$out" "resolve by alias"
assert_eq "0" "$rc" "resolve by alias exits 0"
teardown_test_journal

# --- resolve by segment ------------------------------------------------------
setup_test_journal >/dev/null
mapfile -t _seeded < <(_seed_resolve_registry)
segment="$(clast_encode_path "${_seeded[0]}")"
out="$(clast_registry_resolve "$segment")" && rc=$? || rc=$?
assert_eq "alpha" "$out" "resolve by segment"
assert_eq "0" "$rc" "resolve by segment exits 0"
teardown_test_journal

# --- resolve miss ------------------------------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
out="$(clast_registry_resolve /tmp/this-path-is-not-registered)" && rc=$? || rc=$?
assert_eq "" "$out" "resolve miss prints nothing"
assert_eq "1" "$rc" "resolve miss exits 1"
teardown_test_journal

# --- resolve tolerates malformed lines (good entries still resolve) ---------
setup_test_journal >/dev/null
mapfile -t _seeded < <(_seed_resolve_registry)
out="$(clast_registry_resolve "${_seeded[1]}")" && rc=$? || rc=$?
assert_eq "beta" "$out" "resolve succeeds despite trailing malformed line"
teardown_test_journal

# --- match_remote hit / miss / empty -----------------------------------------
setup_test_journal >/dev/null
make_fixture_journal_tree multi-project
out="$(clast_registry_match_remote git@gitlab.xes-inc.com:xes/xesapps.git)" && rc=$? || rc=$?
assert_eq "xesapps" "$out" "match_remote hit"
assert_eq "0" "$rc" "match_remote hit exits 0"
out="$(clast_registry_match_remote git@example.com:nope/nope.git)" && rc=$? || rc=$?
assert_eq "1" "$rc" "match_remote miss exits 1"
assert_eq "" "$out" "match_remote miss prints nothing"
out="$(clast_registry_match_remote "")" && rc=$? || rc=$?
assert_eq "1" "$rc" "match_remote empty arg exits 1"
teardown_test_journal

# --- add: new entry, then resolve --------------------------------------------
setup_test_journal >/dev/null
expected_path="$(realpath -m /tmp/proj-x)"
line="$(clast_registry_add /tmp/proj-x --slug proj-x)"
assert_eq "proj-x" "$(jq -r .slug <<<"$line")" "add new: slug field"
assert_eq "$expected_path" "$(jq -r .path <<<"$line")" "add new: path field"
assert_eq "[]" "$(jq -c .aliases <<<"$line")" "add new: aliases empty"
out="$(clast_registry_resolve /tmp/proj-x)"
assert_eq "proj-x" "$out" "resolve after add"
teardown_test_journal

# --- add: remote match overrides --slug to existing entry's slug -------------
setup_test_journal >/dev/null
foo_path="$(realpath -m /tmp/proj-foo)"
foo2_path="$(realpath -m /tmp/proj-foo-2)"
clast_registry_add /tmp/proj-foo --slug foo --remote R >/dev/null
line="$(clast_registry_add /tmp/proj-foo-2 --slug ignored --remote R)"
# Why: docs/cli-contract.md#clast-registry add step 4 — remote match merges
# into the existing slug rather than creating a new one. The caller's
# --slug is overridden on purpose.
assert_eq "foo" "$(jq -r .slug <<<"$line")" "add: remote match overrides --slug"
assert_eq "$foo2_path" "$(jq -r .path <<<"$line")" "add: new path recorded"
# Aliases roll-up: the prior known path for slug=foo lives in aliases.
has_alias="$(jq -r --arg p "$foo_path" '.aliases | index($p) != null' <<<"$line")"
assert_eq "true" "$has_alias" "add: prior path rolled into aliases"
teardown_test_journal

# --- add: rejects empty path -------------------------------------------------
setup_test_journal >/dev/null
err="$(clast_registry_add "" 2>&1)" && rc=$? || rc=$?
assert_eq "2" "$rc" "add empty path exits 2"
case "$err" in
  *required*) _clast_test_pass "add empty path: error message" ;;
  *) _clast_test_fail "add empty path: error message (got: $err)" ;;
esac
teardown_test_journal

# --- add: no remote when --remote omitted and path is not a git repo --------
setup_test_journal >/dev/null
nogit="$(mktemp -d -t clast.registry.nogit.XXXXXX)"
line="$(clast_registry_add "$nogit" --slug nogit)"
has_remote="$(jq -e 'has("remote") | not' <<<"$line" >/dev/null && echo yes || echo no)"
assert_eq "yes" "$has_remote" "add without git: no remote field"
rm -rf "$nogit"
teardown_test_journal

# --- add: --slug / --remote without a value exit 2 (not shell error) --------
setup_test_journal >/dev/null
err="$(clast_registry_add /tmp/x --slug 2>&1)" && rc=$? || rc=$?
assert_eq "2" "$rc" "add --slug without value exits 2"
case "$err" in
  *--slug*requires*) _clast_test_pass "add --slug missing value: error message" ;;
  *) _clast_test_fail "add --slug missing value: error message (got: $err)" ;;
esac
err="$(clast_registry_add /tmp/x --remote 2>&1)" && rc=$? || rc=$?
assert_eq "2" "$rc" "add --remote without value exits 2"
case "$err" in
  *--remote*requires*) _clast_test_pass "add --remote missing value: error message" ;;
  *) _clast_test_fail "add --remote missing value: error message (got: $err)" ;;
esac
teardown_test_journal

# --- remove: by slug ---------------------------------------------------------
setup_test_journal >/dev/null
clast_registry_add /tmp/proj-a --slug foo --remote R >/dev/null
clast_registry_add /tmp/proj-b --slug foo --remote R >/dev/null
assert_exit_code 0 clast_registry_remove foo
# File should have zero entries with slug=foo.
remaining="$(jq -cR 'fromjson? | select(.slug == "foo")' "$(clast_registry_path)" | grep -c . || true)"
assert_eq "0" "$remaining" "remove drops all matching lines"
# Second remove returns 1.
assert_exit_code 1 clast_registry_remove foo
teardown_test_journal

# --- remove never touches transcripts ---------------------------------------
setup_test_journal >/dev/null
mkdir -p "$CLAST_JOURNAL_DIR/transcripts"
sentinel="$CLAST_JOURNAL_DIR/transcripts/.sentinel"
: >"$sentinel"
clast_registry_add /tmp/proj-x --slug proj-x >/dev/null
clast_registry_remove proj-x >/dev/null
assert_file_exists "$sentinel" "remove leaves transcripts/ intact"
teardown_test_journal

# --- double-source guard -----------------------------------------------------
# shellcheck source=lib/clast/clast-registry-lib.bash
source lib/clast/clast-registry-lib.bash
_clast_test_pass "double-source is idempotent"

clast_test_summary
