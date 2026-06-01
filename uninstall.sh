#!/usr/bin/env bash
# uninstall.sh - uninstall clast from PREFIX (default /usr/local)
set -euo pipefail

PREFIX="${1:-/usr/local}"
# shellcheck disable=SC2034
SRC="$(cd "$(dirname "$0")" && pwd)"

rm -f "$PREFIX/bin/clast"
rm -rf "$PREFIX/lib/clast"
rm -rf "$PREFIX/share/clast"

echo "Uninstalled clast from $PREFIX"
