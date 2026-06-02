#!/usr/bin/env bash
# contrib/npm-pack-check.sh - verify npm pack --dry-run produces the
# documented file set, without actually publishing.
set -euo pipefail

if ! command -v npm >/dev/null 2>&1; then
    echo "npm not on PATH; skipping" >&2
    exit 0
fi

echo "==> npm pack --dry-run --json"
npm_cache=$(mktemp -d)
trap 'rm -rf "$npm_cache"' EXIT
pack_json=$(npm_config_cache="$npm_cache" npm pack --dry-run --json 2>/dev/null)

# Required top-level paths in the tarball.
required=(
    "bin/clast"
    "lib/clast/clast-lib.bash"
    "lib/clast/clast-decode-lib.bash"
    "lib/clast/clast-manifest-lib.bash"
    "lib/clast/clast-registry-lib.bash"
    "lib/clast/clast-subcommands/whereami.bash"
    "lib/clast/clast-subcommands/snapshot.bash"
    "lib/clast/clast-subcommands/breadcrumb.bash"
    "lib/clast/clast-subcommands/stats.bash"
    "lib/clast/clast-subcommands/doctor.bash"
    ".claude-plugin/plugin.json"
    ".claude-plugin/skills/wakeup/SKILL.md"
    ".claude-plugin/skills/day-wakeup/SKILL.md"
    "hooks/hooks.json"
    "hooks/snapshot.sh"
    "README.md"
    "LICENSE"
    "package.json"
)
for path in "${required[@]}"; do
    if ! jq -e --arg p "$path" '.[0].files[] | select(.path == $p)' \
            <<<"$pack_json" >/dev/null ; then
        echo "MISSING from tarball: $path" >&2
        exit 1
    fi
done

# Forbidden top-level paths - things that should NEVER ship.
forbidden=(
    "test/"
    "docs/"
    ".github/"
    ".envrc"
    "flake.nix"
    "flake.lock"
    "Makefile"
    "install.sh"
    "uninstall.sh"
    "contrib/"
    ".pre-commit-config.yaml"
    "cliff.toml"
)
for prefix in "${forbidden[@]}"; do
    if jq -e --arg p "$prefix" \
            '.[0].files[] | select(.path | startswith($p))' \
            <<<"$pack_json" >/dev/null ; then
        echo "FORBIDDEN in tarball: $prefix*" >&2
        exit 1
    fi
done

# Tarball size sanity: warn if > 1 MiB (the source tree is small; a
# large tarball means something snuck in via the bin/lib trees).
size=$(jq -r '.[0].size' <<<"$pack_json")
if [ "$size" -gt 1048576 ]; then
    echo "WARN: tarball size is $size bytes (> 1 MiB)" >&2
fi

echo "OK ($(jq -r '.[0].files | length' <<<"$pack_json") files, $size bytes)"
