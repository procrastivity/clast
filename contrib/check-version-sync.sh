#!/usr/bin/env bash
# contrib/check-version-sync.sh - assert package.json, flake.nix, and the
# plugin manifest carry the same version literal.
set -euo pipefail

pkg_version=$(jq -r '.version' package.json)
flake_version=$(grep -E '^[[:space:]]*version = "[^"]+";' flake.nix \
    | head -1 \
    | sed -E 's/.*version = "([^"]+)";.*/\1/')
plugin_version=$(jq -r '.version' .claude-plugin/plugin.json)

if [ "$pkg_version" != "$flake_version" ] || [ "$pkg_version" != "$plugin_version" ]; then
    echo "version mismatch:" >&2
    echo "  package.json:              $pkg_version" >&2
    echo "  flake.nix:                 $flake_version" >&2
    echo "  .claude-plugin/plugin.json: $plugin_version" >&2
    exit 1
fi
echo "version sync: $pkg_version"
