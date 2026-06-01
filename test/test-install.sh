#!/usr/bin/env bash
# test-install.sh - prefix install/uninstall integration suite.
set -euo pipefail

cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-install"

PREFIX="$(mktemp -d -t clast-install.XXXXXX)"
trap 'rm -rf "$PREFIX"' EXIT

out="$(./install.sh "$PREFIX")" && rc=$? || rc=$?
assert_eq "0" "$rc" "install exits 0"
case "$out" in
  *"Installed clast to $PREFIX"*) _clast_test_pass "install prints prefix" ;;
  *) _clast_test_fail "install prints prefix"; printf '%s\n' "$out" >&2 ;;
esac

if [[ -x "$PREFIX/bin/clast" ]]; then
  _clast_test_pass "installed bin/clast is executable"
else
  _clast_test_fail "installed bin/clast is executable"
fi
assert_file_exists "$PREFIX/lib/clast/clast-lib.bash" "installed clast-lib.bash"
assert_file_exists "$PREFIX/lib/clast/clast-decode-lib.bash" "installed clast-decode-lib.bash"
assert_file_exists "$PREFIX/lib/clast/clast-registry-lib.bash" "installed clast-registry-lib.bash"
assert_file_exists "$PREFIX/lib/clast/clast-manifest-lib.bash" "installed clast-manifest-lib.bash"
assert_file_exists "$PREFIX/lib/clast/clast-subcommands/whereami.bash" "installed whereami subcommand"
assert_file_exists "$PREFIX/share/clast/.claude-plugin/plugin.json" "installed plugin manifest"
assert_file_exists "$PREFIX/share/clast/hooks/hooks.json" "installed hook manifest"
if [[ -x "$PREFIX/share/clast/hooks/snapshot.sh" ]]; then
  _clast_test_pass "installed snapshot hook is executable"
else
  _clast_test_fail "installed snapshot hook is executable"
fi
assert_file_exists "$PREFIX/share/clast/README.md" "installed README"
assert_file_exists "$PREFIX/share/clast/LICENSE" "installed LICENSE"
assert_file_exists "$PREFIX/lib/clast/package.json" "installed package metadata"

unset CLAST_LIB
expected_version="$(jq -r '.version' package.json)"
version_err="$PREFIX/version.err"
out="$("$PREFIX/bin/clast" --version 2>"$version_err")" && rc=$? || rc=$?
assert_eq "0" "$rc" "installed clast --version exits 0"
assert_eq "clast $expected_version" "$out" "installed clast --version prints package version"
assert_eq "" "$(cat "$version_err")" "installed clast --version has empty stderr"

out="$(./install.sh "$PREFIX")" && rc=$? || rc=$?
assert_eq "0" "$rc" "second install exits 0"
out="$("$PREFIX/bin/clast" --version 2>"$version_err")" && rc=$? || rc=$?
assert_eq "0" "$rc" "second install clast --version exits 0"
assert_eq "clast $expected_version" "$out" "second install clast --version prints package version"
assert_eq "" "$(cat "$version_err")" "second install clast --version has empty stderr"

obsolete="$PREFIX/lib/clast/clast-subcommands/_obsolete.bash"
printf 'stale\n' >"$obsolete"
out="$(./install.sh "$PREFIX")" && rc=$? || rc=$?
assert_eq "0" "$rc" "install after stale sentinel exits 0"
assert_file_not_exists "$obsolete" "reinstall prunes stale subcommand file"

out="$(./uninstall.sh "$PREFIX")" && rc=$? || rc=$?
assert_eq "0" "$rc" "uninstall exits 0"
assert_file_not_exists "$PREFIX/bin/clast" "uninstall removes bin/clast"
assert_file_not_exists "$PREFIX/lib/clast" "uninstall removes lib/clast"
assert_file_not_exists "$PREFIX/share/clast" "uninstall removes share/clast"
assert_file_exists "$PREFIX/bin" "uninstall leaves bin dir"
assert_file_exists "$PREFIX/lib" "uninstall leaves lib dir"
assert_file_exists "$PREFIX/share" "uninstall leaves share dir"

out="$(./uninstall.sh "$PREFIX")" && rc=$? || rc=$?
assert_eq "0" "$rc" "second uninstall exits 0"

clast_test_summary
