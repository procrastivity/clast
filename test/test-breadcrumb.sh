#!/usr/bin/env bash
# test-breadcrumb.sh — `clast breadcrumb` integration suite.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-breadcrumb"

CLAST_BIN="$PWD/bin/clast"
export TZ=UTC
FROZEN_EPOCH="$(date -u -d '2026-05-30T14:23:00Z' +%s)"
LATER_EPOCH="$(date -u -d '2026-05-30T16:07:00Z' +%s)"

_seed_registry() {
  make_fixture_journal_seed_from "multi-project/journal-seed"
}

_seed_registry_for_pwd() {
  local project_path
  project_path="$(realpath -m "$CLAST_PROJECTS_DIR/-tmp-proj-xesapps")"
  mkdir -p "$CLAST_JOURNAL_DIR"
  jq -cn --arg path "$project_path" --arg slug "xesapps" \
    '{path:$path, slug:$slug, remote:"git@example.com:xes/xesapps.git", first_seen:"2026-05-01", aliases:[]}' \
    >"$CLAST_JOURNAL_DIR/projects.json"
}

_assert_contains() {
  local haystack="$1" needle="$2" msg="$3"
  case "$haystack" in
    *"$needle"*) _clast_test_pass "$msg" ;;
    *) _clast_test_fail "$msg"; printf '%s\n' "$haystack" >&2 ;;
  esac
}

# --- first write and append, scoped via --project ---------------------------
setup_test_journal >/dev/null
_seed_registry
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --project xesapps 'check migration before deploy' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "scoped first write: exits 0"
assert_eq "" "$out" "scoped first write: stdout silent"
file="$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md"
assert_file_exists "$file" "scoped first write: file exists"
expected=$'---\ndate: 2026-05-30\nproject: xesapps\n---\n\n- 14:23 — check migration before deploy\n'
assert_eq "$expected" "$(cat "$file")"$'\n' "scoped first write: exact file bytes"

out="$(CLAST_NOW_EPOCH="$LATER_EPOCH" "$CLAST_BIN" breadcrumb --project xesapps figure out why EXPLAIN differs in CI 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "scoped append: exits 0"
expected=$'---\ndate: 2026-05-30\nproject: xesapps\n---\n\n- 14:23 — check migration before deploy\n- 16:07 — figure out why EXPLAIN differs in CI\n'
assert_eq "$expected" "$(cat "$file")"$'\n' "scoped append: exact file bytes"
assert_eq "2" "$(grep -c '^- ' "$file")" "scoped append: two breadcrumb lines"
assert_eq "1" "$(tail -c 1 "$file" | wc -l | tr -d ' ')" "scoped append: file ends with newline"
teardown_test_journal

# --- first write, global ----------------------------------------------------
setup_test_journal >/dev/null
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global 'remember global note' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "global first write: exits 0"
file="$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md"
assert_file_exists "$file" "global first write: file exists"
_assert_contains "$(cat "$file")" "project: _global" "global first write: frontmatter project"
teardown_test_journal

# --- first write, resolved from pwd -----------------------------------------
setup_test_journal >/dev/null
make_fixture_projects_tree_from multi-project/projects-tree
_seed_registry_for_pwd
pushd "$CLAST_PROJECTS_DIR/-tmp-proj-xesapps" >/dev/null || exit 1
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb 'no flag' 2>/dev/null)" && rc=$? || rc=$?
popd >/dev/null || exit 1
assert_eq "0" "$rc" "pwd-resolved write: exits 0"
assert_file_exists "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md" "pwd-resolved write: xesapps file"
teardown_test_journal

# --- unresolved pwd, default and JSON ---------------------------------------
setup_test_journal >/dev/null
err="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb 'x' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "unresolved pwd: exits 1"
_assert_contains "$err" "--project SLUG or --global" "unresolved pwd: stderr names fallbacks"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json breadcrumb 'x' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "1" "$rc" "unresolved pwd --json: exits 1"
assert_eq "1" "$(jq -r '.code' <<<"$out")" "unresolved pwd --json: code=1"
_assert_contains "$(jq -r '.error' <<<"$out")" "--project SLUG or --global" "unresolved pwd --json: error names fallbacks"
teardown_test_journal

# --- write argument errors --------------------------------------------------
setup_test_journal >/dev/null
err="$("$CLAST_BIN" breadcrumb --project xesapps --global 'x' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "project/global mutual exclusion: exits 2"
_assert_contains "$err" "mutually exclusive" "project/global mutual exclusion: message"

err="$("$CLAST_BIN" breadcrumb --global '' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "empty text: exits 2"
_assert_contains "$err" "missing required argument <TEXT>" "empty text: message"

out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global remember to bump the cache version 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "multi-word text: exits 0"
_assert_contains "$(cat "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md")" "- 14:23 — remember to bump the cache version" "multi-word text: joined"

rm -f "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md"
err="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global $'line1\nline2' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "embedded newline: exits 2"
_assert_contains "$err" "single line" "embedded newline: message"
assert_file_not_exists "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md" "embedded newline: no file"

out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global --date 2026-05-22 'historic note' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "date override: exits 0"
assert_file_exists "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-22-_global.md" "date override: historic file"
assert_file_not_exists "$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-_global.md" "date override: no today file"

err="$("$CLAST_BIN" breadcrumb --global --date foo 'x' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "invalid date: exits 2"

err="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --verbose breadcrumb --global 'x' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "verbose write: exits 0"
_assert_contains "$err" "breadcrumbs/2026-05-30-_global.md" "verbose write: path"
_assert_contains "$err" "(1 lines)" "verbose write: line count"

out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json breadcrumb --global 'x' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "json write: exits 0"
assert_eq "_global" "$(jq -r '.slug' <<<"$out")" "json write: slug"
assert_eq "2026-05-30" "$(jq -r '.date' <<<"$out")" "json write: date"
assert_eq "2" "$(jq -r '.line_count' <<<"$out")" "json write: post-write line count"

out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --json --global 'subcommand json' 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "subcommand --json write: exits 0"
assert_eq "_global" "$(jq -r '.slug' <<<"$out")" "subcommand --json write: slug"
assert_eq "3" "$(jq -r '.line_count' <<<"$out")" "subcommand --json write: line count"

escape_target="/tmp/clast-escaped.md"
rm -f "$escape_target"
err="$("$CLAST_BIN" breadcrumb --project '../../../../../tmp/clast-escaped' 'x' 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "unsafe slug: exits 2"
_assert_contains "$err" "invalid project slug" "unsafe slug: message"
assert_file_not_exists "$escape_target" "unsafe slug: no escaped file"

err="$("$CLAST_BIN" breadcrumb --bogus foo 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "unknown flag: exits 2"
_assert_contains "$err" "unknown flag '--bogus'" "unknown flag: message"
teardown_test_journal

# --- read existing and missing ---------------------------------------------
setup_test_journal >/dev/null
_seed_registry
CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --project xesapps 'read me' >/dev/null 2>/dev/null
file="$CLAST_JOURNAL_DIR/breadcrumbs/2026-05-30-xesapps.md"
out="$("$CLAST_BIN" breadcrumb --read --project xesapps --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "read existing: exits 0"
assert_eq "$(cat "$file")" "$out" "read existing: cats file"
out="$("$CLAST_BIN" --json breadcrumb --read --project xesapps --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "read existing --json: exits 0"
assert_eq "true" "$(jq -r '.exists' <<<"$out")" "read existing --json: exists"
assert_eq "$(cat "$file")" "$(jq -r '.content' <<<"$out")" "read existing --json: content"
out="$("$CLAST_BIN" breadcrumb --read --json --project xesapps --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "read existing subcommand --json: exits 0"
assert_eq "true" "$(jq -r '.exists' <<<"$out")" "read existing subcommand --json: exists"

out="$("$CLAST_BIN" breadcrumb --read --project xesapps --day 2026-05-22 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "read missing: exits 0"
assert_eq "" "$out" "read missing: empty stdout"
out="$("$CLAST_BIN" --json breadcrumb --read --project xesapps --day 2026-05-22 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "read missing --json: exits 0"
assert_eq "false" "$(jq -r '.exists' <<<"$out")" "read missing --json: exists=false"
assert_eq "" "$(jq -r '.content' <<<"$out")" "read missing --json: empty content"
teardown_test_journal

# --- list empty and populated ----------------------------------------------
setup_test_journal >/dev/null
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --list 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "list empty: exits 0"
assert_eq "1" "$(wc -l <<<"$out" | tr -d ' ')" "list empty: header only"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" --json breadcrumb --list 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "list empty --json: exits 0"
assert_eq "[]" "$(jq -c . <<<"$out")" "list empty --json: []"
out="$(CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --list --json 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "list empty subcommand --json: exits 0"
assert_eq "[]" "$(jq -c . <<<"$out")" "list empty subcommand --json: []"
teardown_test_journal

setup_test_journal >/dev/null
_seed_registry
CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --project xesapps 'one' >/dev/null 2>/dev/null
CLAST_NOW_EPOCH="$LATER_EPOCH" "$CLAST_BIN" breadcrumb --project xesapps 'two' >/dev/null 2>/dev/null
CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global 'global one' >/dev/null 2>/dev/null
CLAST_NOW_EPOCH="$FROZEN_EPOCH" "$CLAST_BIN" breadcrumb --global --date 2026-05-22 'old global' >/dev/null 2>/dev/null

out="$("$CLAST_BIN" --json breadcrumb --list --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "list populated --json: exits 0"
assert_eq "2" "$(jq 'length' <<<"$out")" "list populated --json: two rows"
assert_eq "1" "$(jq -r '.[] | select(.project == "_global") | .line_count' <<<"$out")" "list populated --json: global count"
assert_eq "2" "$(jq -r '.[] | select(.project == "xesapps") | .line_count' <<<"$out")" "list populated --json: xesapps count"

human="$("$CLAST_BIN" breadcrumb --list --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "list populated default: exits 0"
_assert_contains "$human" "(global)" "list populated default: renders global"
_assert_contains "$human" "xesapps" "list populated default: renders xesapps"
_assert_contains "$human" "          2" "list populated default: right column count"

out="$("$CLAST_BIN" --json breadcrumb --list --day 2026-05-30 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$(jq '[.[] | select(.path | contains("2026-05-22"))] | length' <<<"$out")" "list ignores other days: no historic rows"
teardown_test_journal

# --- mode/help errors -------------------------------------------------------
setup_test_journal >/dev/null
err="$("$CLAST_BIN" breadcrumb --read --list 2>&1 >/dev/null)" && rc=$? || rc=$?
assert_eq "2" "$rc" "read/list mutual exclusion: exits 2"
_assert_contains "$err" "--read and --list are mutually exclusive" "read/list mutual exclusion: message"

out="$("$CLAST_BIN" breadcrumb --help 2>/dev/null)" && rc=$? || rc=$?
assert_eq "0" "$rc" "help: exits 0"
_assert_contains "$out" "Usage:" "help: usage"
teardown_test_journal

unset TZ
clast_test_summary
