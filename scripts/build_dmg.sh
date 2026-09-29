#!/usr/bin/env bash
#
# build_dmg.sh — produce a self-contained FindIt.dmg. The app bundles the
# recovery engines (The Sleuth Kit) and the privileged helpers, so an end user
# installs and runs it with no Homebrew and no other dependencies.
#
# Signing (optional environment):
#   SIGN_IDENTITY   "Developer ID Application: Name (TEAMID)"; default "-" (ad-hoc)
#   NOTARY_PROFILE  keychain profile from `xcrun notarytool store-credentials`;
#                   when set (with a real identity) the DMG is notarized and stapled.
#
# Ad-hoc builds run on the machine that built them; on other Macs Gatekeeper
# blocks them until the user allows them in System Settings → Privacy & Security.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export PATH="$PATH:$(go env GOPATH)/bin"

APP="build/bin/FindIt.app"
DMG="build/FindIt.dmg"
VOL="FindIt"
IDENTITY="${SIGN_IDENTITY:--}"

sign() {
  if [[ "$IDENTITY" == "-" ]]; then
    codesign --force --sign - "$@"
  else
    codesign --force --options runtime --timestamp --sign "$IDENTITY" "$@"
  fi
}

echo "==> Building app (wails)"
wails build -clean -trimpath

echo "==> Bundling recovery engines into the app"
scripts/bundle_engines.sh "$APP/Contents/Resources/engines" >/dev/null

echo "==> Adding privileged helpers"
for h in findit-imagecopy findit-devopen; do
  go build -trimpath -ldflags "-s -w" -o "$APP/Contents/MacOS/$h" "./cmd/$h"
done

echo "==> Signing (inside-out: engines, helpers, then the app)"
find "$APP/Contents/Resources/engines/bin" "$APP/Contents/Resources/engines/lib" -type f -print0 |
  while IFS= read -r -d '' f; do sign "$f"; done
sign "$APP/Contents/MacOS/findit-imagecopy" "$APP/Contents/MacOS/findit-devopen"
sign "$APP"
codesign --verify --deep --strict "$APP"

echo "==> Creating DMG"
rm -f "$DMG"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
# hdiutil occasionally fails with "Resource busy" (common on CI runners); retry.
for attempt in 1 2 3; do
  hdiutil create -volname "$VOL" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null && break
  echo "hdiutil failed (attempt $attempt); retrying…" >&2
  sleep 5
done
[[ -f "$DMG" ]] || { echo "could not create $DMG" >&2; exit 1; }

if [[ "$IDENTITY" != "-" ]]; then
  sign "$DMG"
  if [[ -n "${NOTARY_PROFILE:-}" ]]; then
    echo "==> Notarizing"
    xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
    xcrun stapler staple "$DMG"
  fi
fi

echo
echo "==> Built $DMG ($([[ "$IDENTITY" == "-" ]] && echo "ad-hoc signed" || echo "signed: $IDENTITY"))"
ls -lh "$DMG"
