#!/usr/bin/env bash
# Build the desktop shell (cmd/tidebot-desktop, Wails v3 beta, needs Go >= 1.25 and cgo).
# Usage: scripts/build-desktop.sh [linux|windows|darwin] [out_dir]
# Only linux is tested. Wails needs the native toolchain of the target, so build each OS on
# that OS (see docs/ci/desktop.yml); cross-compiling cgo + webview from Linux is not supported.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OS="${1:-$(go env GOOS)}"
OUT="${2:-$ROOT/bin}"
mkdir -p "$OUT"
cd "$ROOT/cmd/tidebot-desktop"
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.3}" CGO_ENABLED=1
LDF="-s -w"
case "$OS" in
  linux)   # deps: libgtk-3-dev libwebkit2gtk-4.1-dev (gtk3 tag; drop it for gtk4/webkitgtk-6.0)
    go build -tags gtk3 -trimpath -ldflags "$LDF" -o "$OUT/tidebot-desktop" . ;;
  windows) # UNTESTED. Needs WebView2 runtime on the target (preinstalled on Win10/11).
    go build -trimpath -ldflags "$LDF -H windowsgui" -o "$OUT/tidebot-desktop.exe" . ;;
  darwin)  # UNTESTED. Universal binary; wrap in .app via `wails3 package` or docs/release.md.
    GOARCH=arm64 go build -trimpath -ldflags "$LDF" -o "$OUT/tidebot-desktop-arm64" .
    GOARCH=amd64 go build -trimpath -ldflags "$LDF" -o "$OUT/tidebot-desktop-amd64" .
    lipo -create -output "$OUT/tidebot-desktop" "$OUT/tidebot-desktop-arm64" "$OUT/tidebot-desktop-amd64" ;;
  *) echo "unknown OS $OS" >&2; exit 2 ;;
esac
ls -l "$OUT"/tidebot-desktop*
