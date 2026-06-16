#!/usr/bin/env bash
# contrib/nix-smoke.sh - verify the nix flake package builds and runs.
set -euo pipefail

if ! command -v nix >/dev/null 2>&1; then
    echo "nix not on PATH; skipping" >&2
    exit 0
fi

echo "==> nix flake check"
nix flake check --no-build

echo "==> nix build .#default"
result=$(nix build --print-out-paths --no-link .#default)
echo "    $result"

echo "==> nix build .#clast (alias)"
nix build --print-out-paths --no-link .#clast >/dev/null

echo "==> $result/bin/clast --version (with CLAST_LIB unset, minimal PATH)"
version_output=$(env -u CLAST_LIB PATH=/usr/bin:/bin "$result/bin/clast" --version)
case "$version_output" in
    clast\ [0-9]*.[0-9]*.[0-9]*)
        printf '%s\n' "$version_output"
        ;;
    *)
        echo "unexpected porcelain version output: $version_output" >&2
        exit 1
        ;;
esac

echo "==> $result/bin/clast-plumbing --version (with CLAST_LIB unset, minimal PATH)"
plumbing_version_output=$(env -u CLAST_LIB PATH=/usr/bin:/bin "$result/bin/clast-plumbing" --version)
case "$plumbing_version_output" in
    clast-plumbing\ [0-9]*.[0-9]*.[0-9]*)
        printf '%s\n' "$plumbing_version_output"
        ;;
    *)
        echo "unexpected plumbing version output: $plumbing_version_output" >&2
        exit 1
        ;;
esac

echo "==> $result/bin/clast-plumbing whereami --help (with CLAST_LIB unset, minimal PATH)"
env -u CLAST_LIB PATH=/usr/bin:/bin "$result/bin/clast-plumbing" whereami --help >/dev/null

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
mkdir -p "$tmpdir/journal/entries"
cat >"$tmpdir/journal/entries/2026-05-30-1430-fixture-smoke.md" <<'EOF'
---
session_id: aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
---

# Session: fixture smoke
EOF

echo "==> snapshot/show fixture smoke (with CLAST_LIB unset, minimal PATH)"
env -u CLAST_LIB \
    PATH=/usr/bin:/bin \
    CLAST_PROJECTS_DIR="$PWD/test/fixtures/simple" \
    CLAST_JOURNAL_DIR="$tmpdir/journal" \
    "$result/bin/clast-plumbing" snapshot >/dev/null
env -u CLAST_LIB \
    PATH=/usr/bin:/bin \
    CLAST_JOURNAL_DIR="$tmpdir/journal" \
    "$result/bin/clast-plumbing" show aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa --full >/dev/null

echo "==> layout assertions"
for path in \
    bin/clast \
    bin/clast-plumbing \
    lib/clast/clast-lib.bash \
    lib/clast/clast-porcelain-lib.bash \
    lib/clast/clast-subcommands/whereami.bash \
    lib/clast/clast-porcelain-subcommands/wake.bash \
    lib/clast/clast-porcelain-subcommands/brief.bash \
    share/clast/.claude-plugin/plugin.json \
    share/clast/hooks/hooks.json \
    share/clast/hooks/snapshot.sh \
    share/clast/README.md \
    share/clast/LICENSE ; do
    test -e "$result/$path" || { echo "MISSING: $path" >&2 ; exit 1 ; }
done
test -x "$result/share/clast/hooks/snapshot.sh" \
    || { echo "snapshot.sh not executable" >&2 ; exit 1 ; }

echo "OK"
