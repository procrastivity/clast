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
        echo "unexpected version output: $version_output" >&2
        exit 1
        ;;
esac

echo "==> $result/bin/clast whereami --help (with CLAST_LIB unset, minimal PATH)"
env -u CLAST_LIB PATH=/usr/bin:/bin "$result/bin/clast" whereami --help >/dev/null

echo "==> layout assertions"
for path in \
    bin/clast \
    lib/clast/clast-lib.bash \
    lib/clast/clast-subcommands/whereami.bash \
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
