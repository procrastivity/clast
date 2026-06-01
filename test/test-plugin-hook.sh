#!/usr/bin/env bash
# test-plugin-hook.sh — plugin manifest, hook declaration, and the
# SessionStart snapshot.sh script. Verifies JSON shapes, executable bit,
# fast/silent foreground exit, background launch of `clast snapshot`,
# and the plugin-root > sibling > PATH resolution order.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-plugin-hook"

HOOK="$PWD/hooks/snapshot.sh"

# --- JSON validity ------------------------------------------------------

if jq -e . < .claude-plugin/plugin.json >/dev/null 2>&1; then
  _clast_test_pass ".claude-plugin/plugin.json is valid JSON"
else
  _clast_test_fail ".claude-plugin/plugin.json is valid JSON"
fi

name="$(jq -r '.name' .claude-plugin/plugin.json)"
assert_eq "clast" "$name" "plugin.json .name == \"clast\""

if jq -e . < hooks/hooks.json >/dev/null 2>&1; then
  _clast_test_pass "hooks/hooks.json is valid JSON"
else
  _clast_test_fail "hooks/hooks.json is valid JSON"
fi

event="$(jq -r '.hooks[0].event' hooks/hooks.json)"
assert_eq "SessionStart" "$event" "hooks.json .hooks[0].event == \"SessionStart\""

cmd="$(jq -r '.hooks[0].command' hooks/hooks.json)"
needle="\${CLAUDE_PLUGIN_ROOT}"
case "$cmd" in
  *"$needle"*/hooks/snapshot.sh)
    _clast_test_pass "hooks.json command references \${CLAUDE_PLUGIN_ROOT} and ends in /hooks/snapshot.sh" ;;
  *)
    _clast_test_fail "hooks.json command malformed: $cmd" ;;
esac

# --- Hook is executable -------------------------------------------------

if [ -x "$HOOK" ]; then
  _clast_test_pass "hooks/snapshot.sh is executable"
else
  _clast_test_fail "hooks/snapshot.sh is executable"
fi

# --- Helpers ------------------------------------------------------------

# Wait up to ~2s for $1 to exist and be non-empty.
poll_marker() {
  local marker="$1" _i
  for _i in $(seq 1 20); do
    if [ -s "$marker" ]; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

# --- Foreground exit fast and clean when clast not found ----------------

tmp_nf="$(mktemp -d -t clast.hook.XXXXXX)"
trap 'rm -rf "$tmp_nf"' EXIT
out="$tmp_nf/out"; err="$tmp_nf/err"
# PATH set to an empty dir; CLAUDE_PLUGIN_ROOT unset; the sibling lookup
# resolves to this repo's bin/clast — which DOES exist, so to test the
# not-found path we point the hook at a fake repo root via a tmp HOOK copy.
mkdir -p "$tmp_nf/fake-plugin/hooks" "$tmp_nf/empty"
cp "$HOOK" "$tmp_nf/fake-plugin/hooks/snapshot.sh"
chmod +x "$tmp_nf/fake-plugin/hooks/snapshot.sh"

env -i HOME="$HOME" PATH="$tmp_nf/empty:/usr/bin:/bin" \
  bash "$tmp_nf/fake-plugin/hooks/snapshot.sh" >"$out" 2>"$err"
rc=$?
assert_eq "0" "$rc" "not-found: hook exits 0"
if [ ! -s "$out" ]; then
  _clast_test_pass "not-found: stdout is empty"
else
  _clast_test_fail "not-found: stdout is empty"
  cat "$out" >&2
fi
if [ ! -s "$err" ]; then
  _clast_test_pass "not-found: stderr is empty"
else
  _clast_test_fail "not-found: stderr is empty"
  cat "$err" >&2
fi

# --- Hook backgrounds clast snapshot when found via PATH ----------------

tmp_path="$(mktemp -d -t clast.hook.XXXXXX)"
trap 'rm -rf "$tmp_nf" "$tmp_path"' EXIT
marker="$tmp_path/marker"
mkdir -p "$tmp_path/bin" "$tmp_path/fake-plugin/hooks"
cat >"$tmp_path/bin/clast" <<EOF
#!/usr/bin/env bash
echo "\$@" >> "$marker"
EOF
chmod +x "$tmp_path/bin/clast"
cp "$HOOK" "$tmp_path/fake-plugin/hooks/snapshot.sh"
chmod +x "$tmp_path/fake-plugin/hooks/snapshot.sh"

env -i HOME="$HOME" PATH="$tmp_path/bin:/usr/bin:/bin" \
  bash "$tmp_path/fake-plugin/hooks/snapshot.sh" >"$tmp_path/out" 2>"$tmp_path/err"
rc=$?
assert_eq "0" "$rc" "via-PATH: hook foreground exits 0"

if poll_marker "$marker"; then
  content="$(cat "$marker")"
  assert_eq "snapshot" "$content" "via-PATH: backgrounded clast received \"snapshot\""
else
  _clast_test_fail "via-PATH: marker file never appeared within 2s budget"
fi

# --- Hook prefers ${CLAUDE_PLUGIN_ROOT}/bin/clast over PATH -------------

tmp_pref="$(mktemp -d -t clast.hook.XXXXXX)"
trap 'rm -rf "$tmp_nf" "$tmp_path" "$tmp_pref"' EXIT
marker2="$tmp_pref/marker"
mkdir -p "$tmp_pref/plugin-root/bin" "$tmp_pref/path-bin" "$tmp_pref/fake-plugin/hooks"
cat >"$tmp_pref/plugin-root/bin/clast" <<EOF
#!/usr/bin/env bash
echo "from-root" >> "$marker2"
EOF
chmod +x "$tmp_pref/plugin-root/bin/clast"
cat >"$tmp_pref/path-bin/clast" <<EOF
#!/usr/bin/env bash
echo "from-path" >> "$marker2"
EOF
chmod +x "$tmp_pref/path-bin/clast"
cp "$HOOK" "$tmp_pref/fake-plugin/hooks/snapshot.sh"
chmod +x "$tmp_pref/fake-plugin/hooks/snapshot.sh"

env -i HOME="$HOME" PATH="$tmp_pref/path-bin:/usr/bin:/bin" CLAUDE_PLUGIN_ROOT="$tmp_pref/plugin-root" \
  bash "$tmp_pref/fake-plugin/hooks/snapshot.sh" >"$tmp_pref/out" 2>"$tmp_pref/err"
rc=$?
assert_eq "0" "$rc" "prefers-root: hook foreground exits 0"

if poll_marker "$marker2"; then
  content="$(cat "$marker2")"
  assert_eq "from-root" "$content" "prefers-root: plugin-root clast ran, not PATH clast"
else
  _clast_test_fail "prefers-root: marker file never appeared within 2s budget"
fi

# --- Hook tolerates a non-existent CLAUDE_PLUGIN_ROOT -------------------

tmp_bad="$(mktemp -d -t clast.hook.XXXXXX)"
trap 'rm -rf "$tmp_nf" "$tmp_path" "$tmp_pref" "$tmp_bad"' EXIT
mkdir -p "$tmp_bad/fake-plugin/hooks" "$tmp_bad/empty"
cp "$HOOK" "$tmp_bad/fake-plugin/hooks/snapshot.sh"
chmod +x "$tmp_bad/fake-plugin/hooks/snapshot.sh"

env -i HOME="$HOME" PATH="$tmp_bad/empty:/usr/bin:/bin" \
  CLAUDE_PLUGIN_ROOT="$tmp_bad/does-not-exist" \
  bash "$tmp_bad/fake-plugin/hooks/snapshot.sh" >"$tmp_bad/out" 2>"$tmp_bad/err"
rc=$?
assert_eq "0" "$rc" "bad-root: hook exits 0"
if [ ! -s "$tmp_bad/out" ] && [ ! -s "$tmp_bad/err" ]; then
  _clast_test_pass "bad-root: hook is silent on stdout and stderr"
else
  _clast_test_fail "bad-root: hook is silent on stdout and stderr"
fi

# --- Footer -------------------------------------------------------------

clast_test_summary
