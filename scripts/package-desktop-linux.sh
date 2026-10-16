#!/usr/bin/env bash
# Build the Linux desktop app and pack it as AppImage + .deb.
# Usage: scripts/package-desktop-linux.sh <version> [out_dir]
# The AppImage does NOT bundle GTK/WebKitGTK; the host needs libwebkit2gtk-4.1 (see docs/release.md).
set -euo pipefail
VER="${1:?version}"; VER="${VER#v}"
OUT="${2:-dist}"; mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARCH="$(dpkg --print-architecture)"
LDF="-s -w -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Version=$VER"
(cd "$ROOT/cmd/tidebot-desktop" && CGO_ENABLED=1 go build -tags gtk3 -trimpath -ldflags "$LDF" -o "$OUT/.tidebot-desktop" .)
# .deb
deb="$(mktemp -d)"
install -Dm755 "$OUT/.tidebot-desktop" "$deb/usr/bin/tidebot-desktop"
install -Dm644 "$ROOT/packaging/linux/tidebot-desktop.desktop" "$deb/usr/share/applications/tidebot-desktop.desktop"
install -Dm644 "$ROOT/cmd/tidebot-desktop/assets/icon.png" "$deb/usr/share/icons/hicolor/256x256/apps/tidebot.png"
mkdir -p "$deb/DEBIAN"
cat > "$deb/DEBIAN/control" <<CTL
Package: tidebot-desktop
Version: $VER
Architecture: $ARCH
Maintainer: Zhengmingming1 <noreply@github.com>
Depends: libgtk-3-0, libwebkit2gtk-4.1-0
Section: utils
Priority: optional
Homepage: https://github.com/Zhengmingming1/tidebot
Description: tidebot desktop app (tray + window, embeds the scheduler)
 Needs the claude CLI installed separately.
CTL
dpkg-deb --root-owner-group --build "$deb" "$OUT/tidebot-desktop-${VER}-Linux-x64.deb" >/dev/null
# AppImage
app="$(mktemp -d)/tidebot.AppDir"
install -Dm755 "$OUT/.tidebot-desktop" "$app/usr/bin/tidebot-desktop"
cp "$ROOT/packaging/linux/tidebot-desktop.desktop" "$app/tidebot-desktop.desktop"
cp "$ROOT/cmd/tidebot-desktop/assets/icon.png" "$app/tidebot.png"
printf '#!/bin/sh\nexec "$(dirname "$(readlink -f "$0")")/usr/bin/tidebot-desktop" "$@"\n' > "$app/AppRun"; chmod +x "$app/AppRun"
TOOL="${APPIMAGETOOL:-}"
if [ -z "$TOOL" ]; then
  case "$ARCH" in amd64) A=x86_64;; arm64) A=aarch64;; esac
  TOOL="$(mktemp -d)/appimagetool"
  curl -fsSL -o "$TOOL" "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-$A.AppImage"
  chmod +x "$TOOL"
fi
case "$ARCH" in amd64) A=x86_64;; arm64) A=aarch64;; esac
ARCH=$A APPIMAGE_EXTRACT_AND_RUN=1 "$TOOL" --no-appstream "$app" "$OUT/tidebot-desktop-${VER}-Linux-x64.AppImage"
rm -f "$OUT/.tidebot-desktop"
ls -l "$OUT"
