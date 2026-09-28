#!/usr/bin/env bash
#
# build_dmg.sh — produce a self-contained FindIt.dmg. The app bundles the
# recovery engines (The Sleuth Kit) and the privileged clone helper, so an end
# user installs and runs it with no Homebrew and no other dependencies.
#
# Note: the result is ad-hoc signed, not notarized. Distributing outside your
# own machine needs an Apple Developer ID + notarization (out of scope here).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export PATH="$PATH:$(go env GOPATH)/bin"

APP="build/bin/findit.app"
DMG="build/FindIt.dmg"
VOL="FindIt"

echo "==> Building app (wails)"
wails build

echo "==> Bundling recovery engines into the app"
scripts/bundle_engines.sh "$APP/Contents/Resources/engines" >/dev/null

echo "==> Adding privileged clone helper"
go build -o "$APP/Contents/MacOS/findit-imagecopy" ./cmd/findit-imagecopy
codesign --force --sign - "$APP/Contents/MacOS/findit-imagecopy" 2>/dev/null || true

echo "==> Re-signing the app bundle"
codesign --force --deep --sign - "$APP" 2>/dev/null || true

echo "==> Creating DMG"
rm -f "$DMG"
STAGE="$(mktemp -d)"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -volname "$VOL" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
rm -rf "$STAGE"

echo
echo "==> Built $DMG"
ls -lh "$DMG"
echo "==> App contents:"
ls "$APP/Contents/MacOS" "$APP/Contents/Resources/engines/bin"
