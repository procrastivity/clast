# test/helpers.sh — minimal assertion + fixture harness used by all test
# scripts. Plain bash; no bats. Sourced via `source test/helpers.sh` from
# scripts that have already `cd`'d to the repo root.
# shellcheck shell=bash

# Track test counts so per-script footers can summarize.
_CLAST_TEST_PASS=0
_CLAST_TEST_FAIL=0
_CLAST_TEST_NAME="${_CLAST_TEST_NAME:-tests}"

_clast_test_pass() {
  _CLAST_TEST_PASS=$((_CLAST_TEST_PASS + 1))
  printf '  ok  %s\n' "$1"
}

_clast_test_fail() {
  _CLAST_TEST_FAIL=$((_CLAST_TEST_FAIL + 1))
  printf '  FAIL %s\n' "$1" >&2
}

assert_eq() {
  local expected="$1" actual="$2" msg="${3:-assert_eq}"
  if [[ "$expected" == "$actual" ]]; then
    _clast_test_pass "$msg"
  else
    _clast_test_fail "$msg"
    printf '       expected: %q\n' "$expected" >&2
    printf '       actual:   %q\n' "$actual" >&2
    return 1
  fi
}

assert_file_exists() {
  local path="$1" msg="${2:-file exists: $1}"
  if [[ -e "$path" ]]; then
    _clast_test_pass "$msg"
  else
    _clast_test_fail "$msg"
    return 1
  fi
}

assert_file_not_exists() {
  local path="$1" msg="${2:-file absent: $1}"
  if [[ ! -e "$path" ]]; then
    _clast_test_pass "$msg"
  else
    _clast_test_fail "$msg"
    return 1
  fi
}

# assert_exit_code <expected> <command> [args...]
#   Runs the command in a subshell so `set -e` etc. in the caller don't
#   abort on non-zero. Captures the exit code only; stdout/stderr are
#   passed through.
assert_exit_code() {
  local expected="$1"
  shift
  local actual=0
  ( "$@" ) || actual=$?
  if [[ "$expected" -eq "$actual" ]]; then
    _clast_test_pass "exit $expected from: $*"
  else
    _clast_test_fail "exit $expected from: $*"
    printf '       got: %s\n' "$actual" >&2
    return 1
  fi
}

# setup_test_journal — make a per-test tmpdir, point CLAST_JOURNAL_DIR and
# CLAST_PROJECTS_DIR at subdirs of it, and echo the tmpdir path on stdout.
setup_test_journal() {
  local tmp
  tmp="$(mktemp -d -t clast.test.XXXXXX)"
  mkdir -p "$tmp/journal" "$tmp/projects"
  export CLAST_JOURNAL_DIR="$tmp/journal"
  export CLAST_PROJECTS_DIR="$tmp/projects"
  _CLAST_TEST_TMPDIR="$tmp"
  printf '%s\n' "$tmp"
}

teardown_test_journal() {
  if [[ -n "${_CLAST_TEST_TMPDIR:-}" && -d "$_CLAST_TEST_TMPDIR" ]]; then
    rm -rf "$_CLAST_TEST_TMPDIR"
  fi
  unset _CLAST_TEST_TMPDIR CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR
}

# make_fixture_projects_tree <fixture-name>
#   Copies test/fixtures/<name>/ into $CLAST_PROJECTS_DIR. The fixture
#   directory must exist; setup_test_journal must have been called first.
make_fixture_projects_tree() {
  local name="$1"
  local src="test/fixtures/$name"
  if [[ ! -d "$src" ]]; then
    printf 'make_fixture_projects_tree: missing fixture %q\n' "$src" >&2
    return 1
  fi
  if [[ -z "${CLAST_PROJECTS_DIR:-}" ]]; then
    printf 'make_fixture_projects_tree: setup_test_journal not called\n' >&2
    return 1
  fi
  cp -R "$src"/. "$CLAST_PROJECTS_DIR"/
}

# make_fixture_projects_tree_from <fixture-name>/<subpath>
#   Copies test/fixtures/<name>/<subpath>/ into $CLAST_PROJECTS_DIR. Lets a
#   single fixture directory host multiple roots (e.g. multi-project hosts
#   both projects.json and projects-tree/).
make_fixture_projects_tree_from() {
  local rel="$1"
  local src="test/fixtures/$rel"
  if [[ ! -d "$src" ]]; then
    printf 'make_fixture_projects_tree_from: missing fixture %q\n' "$src" >&2
    return 1
  fi
  if [[ -z "${CLAST_PROJECTS_DIR:-}" ]]; then
    printf 'make_fixture_projects_tree_from: setup_test_journal not called\n' >&2
    return 1
  fi
  cp -R "$src"/. "$CLAST_PROJECTS_DIR"/
}

# make_fixture_journal_seed_from <fixture-name>/<subpath>
#   Copies test/fixtures/<name>/<subpath>/ into $CLAST_JOURNAL_DIR. Mirrors
#   make_fixture_projects_tree_from; lets a single fixture host a
#   pre-populated journal (e.g. multi-project/journal-seed/).
make_fixture_journal_seed_from() {
  local rel="$1"
  local src="test/fixtures/$rel"
  if [[ ! -d "$src" ]]; then
    printf 'make_fixture_journal_seed_from: missing fixture %q\n' "$src" >&2
    return 1
  fi
  if [[ -z "${CLAST_JOURNAL_DIR:-}" ]]; then
    printf 'make_fixture_journal_seed_from: setup_test_journal not called\n' >&2
    return 1
  fi
  cp -R "$src"/. "$CLAST_JOURNAL_DIR"/
}

# make_fixture_entries_seed_from <fixture-name>/<subpath>
#   Copies test/fixtures/<name>/<subpath>/ into $CLAST_JOURNAL_DIR. Mirrors
#   make_fixture_journal_seed_from but reserved for entries-only seeds layered
#   on top of a journal-seed (e.g. multi-project/entries-seed/entries/ lands at
#   $CLAST_JOURNAL_DIR/entries/).
make_fixture_entries_seed_from() {
  local rel="$1"
  local src="test/fixtures/$rel"
  if [[ ! -d "$src" ]]; then
    printf 'make_fixture_entries_seed_from: missing fixture %q\n' "$src" >&2
    return 1
  fi
  if [[ -z "${CLAST_JOURNAL_DIR:-}" ]]; then
    printf 'make_fixture_entries_seed_from: setup_test_journal not called\n' >&2
    return 1
  fi
  cp -R "$src"/. "$CLAST_JOURNAL_DIR"/
}

# make_fixture_journal_tree <fixture-name>
#   Copies test/fixtures/<name>/ into $CLAST_JOURNAL_DIR. The fixture
#   directory must exist; setup_test_journal must have been called first.
make_fixture_journal_tree() {
  local name="$1"
  local src="test/fixtures/$name"
  if [[ ! -d "$src" ]]; then
    printf 'make_fixture_journal_tree: missing fixture %q\n' "$src" >&2
    return 1
  fi
  if [[ -z "${CLAST_JOURNAL_DIR:-}" ]]; then
    printf 'make_fixture_journal_tree: setup_test_journal not called\n' >&2
    return 1
  fi
  cp -R "$src"/. "$CLAST_JOURNAL_DIR"/
}

# clast_test_summary — print pass/fail count and return non-zero on any fail.
clast_test_summary() {
  printf '%s: %d passed, %d failed\n' \
    "$_CLAST_TEST_NAME" "$_CLAST_TEST_PASS" "$_CLAST_TEST_FAIL"
  if (( _CLAST_TEST_FAIL > 0 )); then
    return 1
  fi
  return 0
}
