#!/usr/bin/env bash
# contrib/check-version-sync.sh - assert package.json and flake.nix
# carry the same version literal.
set -euo pipefail

pkg_version=$(jq -r '.version' package.json)
flake_version=$(grep -E '^\s*version = "[^"]+";' flake.nix \
    | head -1 \
    | sed -E 's/.*version = "([^"]+)";.*/\1/')

if [ "$pkg_version" != "$flake_version" ]; then
    echo "version mismatch:" >&2
    echo "  package.json: $pkg_version" >&2
    echo "  flake.nix:    $flake_version" >&2
    exit 1
fi
echo "version sync: $pkg_version"
