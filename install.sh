#!/usr/bin/env bash
# install.sh - install clast to PREFIX (default /usr/local)
set -euo pipefail

PREFIX="${1:-/usr/local}"
SRC="$(cd "$(dirname "$0")" && pwd)"

mkdir -p "$PREFIX/bin" "$PREFIX/lib/clast" "$PREFIX/share/clast"

install -m755 "$SRC/bin/clast" "$PREFIX/bin/clast"

# Drop stale files from a prior install before re-copying.
rm -rf "$PREFIX/lib/clast"
mkdir -p "$PREFIX/lib/clast"
cp -R "$SRC/lib/clast/." "$PREFIX/lib/clast/"

# Drop stale files from a prior install before re-copying.
rm -rf \
  "$PREFIX/share/clast/.claude-plugin" \
  "$PREFIX/share/clast/hooks" \
  "$PREFIX/share/clast/examples"
cp -R "$SRC/.claude-plugin" "$PREFIX/share/clast/"
cp -R "$SRC/hooks" "$PREFIX/share/clast/"
chmod +x "$PREFIX/share/clast/hooks/snapshot.sh"
cp -R "$SRC/examples" "$PREFIX/share/clast/"

install -m644 "$SRC/README.md" "$PREFIX/share/clast/README.md"
install -m644 "$SRC/LICENSE" "$PREFIX/share/clast/LICENSE"
install -m644 "$SRC/package.json" "$PREFIX/lib/clast/package.json"

echo "Installed clast to $PREFIX"
echo "  Binary: $PREFIX/bin/clast"
echo "  Plugin: $PREFIX/share/clast/.claude-plugin"
echo ""
echo "Add the plugin via:"
echo "  claude plugin install $PREFIX/share/clast"
echo ""
echo "Uninstall with:"
echo "  $SRC/uninstall.sh $PREFIX"
