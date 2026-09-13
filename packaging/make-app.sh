#!/usr/bin/env bash
#
# Build DerbyAndAles.app — a self-contained macOS application bundle.
#
# The whole point of this project is that someone other than the author can run
# a race night. So the output must be one thing you double-click, with no Go,
# no Python, no Homebrew, and no Terminal.
#
# Usage: packaging/make-app.sh [version]

set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$ROOT/dist"
APP="$DIST/DerbyAndAles.app"
BIN_NAME="derbyandales"

cd "$ROOT"

echo "==> Building DerbyAndAles.app $VERSION"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# Universal binary, so the same .app runs on Apple Silicon and Intel Macs.
# modernc.org/sqlite and go.bug.st/serial are both cgo-free, so cross-compiling
# needs no toolchain beyond Go itself.
LDFLAGS="-s -w -X main.version=$VERSION"

echo "    arm64..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/$BIN_NAME-arm64" "./cmd/$BIN_NAME"

echo "    amd64..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/$BIN_NAME-amd64" "./cmd/$BIN_NAME"

echo "    merging..."
lipo -create -output "$APP/Contents/MacOS/$BIN_NAME" \
  "$DIST/$BIN_NAME-arm64" "$DIST/$BIN_NAME-amd64"
chmod +x "$APP/Contents/MacOS/$BIN_NAME"
rm -f "$DIST/$BIN_NAME-arm64" "$DIST/$BIN_NAME-amd64"

sed -e "s/__VERSION__/$VERSION/g" \
    "$ROOT/packaging/Info.plist.in" > "$APP/Contents/Info.plist"

if [ -f "$ROOT/packaging/AppIcon.icns" ]; then
  cp "$ROOT/packaging/AppIcon.icns" "$APP/Contents/Resources/"
fi

# Ad-hoc signature. Without this, Gatekeeper is harsher than it needs to be on
# the machine that built it. Proper Developer ID signing and notarization are
# still to do — see packaging/NOTARIZING.md.
if command -v codesign >/dev/null 2>&1; then
  echo "    ad-hoc signing..."
  codesign --force --deep --sign - "$APP" 2>/dev/null || \
    echo "    (ad-hoc signing failed; the app still runs)"
fi

echo
echo "Built $APP"
echo
echo "To run it:  open '$APP'"
echo
echo "On another Mac, macOS will quarantine it because it is not notarized."
echo "Clear that with:"
echo "  xattr -dr com.apple.quarantine '/Applications/DerbyAndAles.app'"
