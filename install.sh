#!/usr/bin/env bash
# install.sh - install clast to PREFIX (default /usr/local)
set -euo pipefail

PREFIX="${1:-/usr/local}"
SRC="$(cd "$(dirname "$0")" && pwd)"

echo "Installing clast to $PREFIX ..."

install -d "$PREFIX/bin" "$PREFIX/lib/clast" "$PREFIX/share/clast"

install -m755 "$SRC/bin/clast" "$PREFIX/bin/clast"
install -m755 "$SRC/bin/clast-plumbing" "$PREFIX/bin/clast-plumbing"

# Drop stale files from a prior install before re-copying.
rm -rf "$PREFIX/lib/clast"
install -d "$PREFIX/lib/clast" "$PREFIX/lib/clast/prompts"
cp -R "$SRC/lib/clast/." "$PREFIX/lib/clast/"

# Drop stale files from a prior install before re-copying.
rm -rf \
  "$PREFIX/share/clast/.claude-plugin" \
  "$PREFIX/share/clast/skills" \
  "$PREFIX/share/clast/hooks" \
  "$PREFIX/share/clast/examples" \
  "$PREFIX/share/clast/bin" \
  "$PREFIX/share/clast/lib"
cp -R "$SRC/.claude-plugin" "$PREFIX/share/clast/"
cp -R "$SRC/skills" "$PREFIX/share/clast/"
cp -R "$SRC/hooks" "$PREFIX/share/clast/"
chmod +x "$PREFIX/share/clast/hooks/snapshot.sh"
cp -R "$SRC/examples" "$PREFIX/share/clast/"
# Expose clast-plumbing inside the plugin root so Claude adds it to PATH
# when the plugin is installed from share/clast (see plugin bin/ convention).
install -d "$PREFIX/share/clast/bin"
ln -sf ../../../bin/clast-plumbing "$PREFIX/share/clast/bin/clast-plumbing"
# Copy prompt templates into the plugin root so skills can read them at
# $CLAUDE_PLUGIN_ROOT/lib/clast/prompts/ regardless of whether $PREFIX/bin
# is on PATH.
install -d "$PREFIX/share/clast/lib/clast"
cp -R "$SRC/lib/clast/prompts" "$PREFIX/share/clast/lib/clast/"

install -m644 "$SRC/README.md" "$PREFIX/share/clast/README.md"
install -m644 "$SRC/LICENSE" "$PREFIX/share/clast/LICENSE"
install -m644 "$SRC/package.json" "$PREFIX/lib/clast/package.json"

echo "Installed clast to $PREFIX"
echo "  Porcelain: $PREFIX/bin/clast (wake, brief, retro)"
echo "  Plumbing:  $PREFIX/bin/clast-plumbing"
echo "  Plugin:    $PREFIX/share/clast/.claude-plugin"
echo ""
echo "Add the plugin via:"
echo "  claude plugin install $PREFIX/share/clast"
echo ""
echo "Uninstall with:"
echo "  $SRC/uninstall.sh $PREFIX"
echo ""
"$PREFIX/bin/clast" --version || true
"$PREFIX/bin/clast-plumbing" --version || true
