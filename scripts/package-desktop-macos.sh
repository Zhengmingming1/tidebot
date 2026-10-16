#!/usr/bin/env bash
# Build a universal (arm64+amd64) tidebot.app and wrap it in a .dmg. Runs on macOS only.
# Signing/notarization happen only when APPLE_* env vars are present (see docs/release.md).
# Usage: scripts/package-desktop-macos.sh <version> [out_dir]
set -euo pipefail
VER="${1:?version}"; VER="${VER#v}"
OUT="${2:-dist}"; mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LDF="-s -w -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Version=$VER"
B="$(mktemp -d)"
cd "$ROOT/cmd/tidebot-desktop"
export CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=11.0
GOARCH=arm64 CC="clang -arch arm64" go build -trimpath -ldflags "$LDF" -o "$B/arm64" .
GOARCH=amd64 CC="clang -arch x86_64" go build -trimpath -ldflags "$LDF" -o "$B/amd64" .
APP="$B/tidebot.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
lipo -create -output "$APP/Contents/MacOS/tidebot-desktop" "$B/arm64" "$B/amd64"
lipo -info "$APP/Contents/MacOS/tidebot-desktop"
sed "s/@VERSION@/$VER/g" "$ROOT/packaging/macos/Info.plist" > "$APP/Contents/Info.plist"
ICON="$B/icon.iconset"; mkdir -p "$ICON"
for s in 16 32 128 256 512; do
  sips -z $s $s "$ROOT/cmd/tidebot-desktop/assets/icon.png" --out "$ICON/icon_${s}x${s}.png" >/dev/null
  d=$((s*2)); sips -z $d $d "$ROOT/cmd/tidebot-desktop/assets/icon.png" --out "$ICON/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICON" -o "$APP/Contents/Resources/tidebot.icns"
if [ -n "${APPLE_SIGNING_IDENTITY:-}" ]; then
  codesign --force --deep --options runtime --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$APP"
else
  codesign --force --deep --sign - "$APP"   # ad-hoc; Gatekeeper will still warn
fi
DMG="$OUT/tidebot-desktop-${VER}-macOS-AppleSilicon-Intel.dmg"
mkdir -p "$B/dmg" && cp -R "$APP" "$B/dmg/" && ln -s /Applications "$B/dmg/Applications"
hdiutil create -volname "tidebot" -srcfolder "$B/dmg" -ov -format UDZO "$DMG"
if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_SIGNING_IDENTITY:-}" ]; then
  codesign --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$DMG"
  xcrun notarytool submit "$DMG" --apple-id "$APPLE_ID" --password "$APPLE_APP_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
  xcrun stapler staple "$DMG"
fi
ls -l "$OUT"
