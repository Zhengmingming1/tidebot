#!/usr/bin/env bash
# Cross-compile agentd/agentctl and pack them as release archives (+ .deb for linux).
# Usage: scripts/package-server.sh <version> [out_dir]
set -euo pipefail
VER="${1:?version like 0.1.0}"; VER="${VER#v}"
OUT="${2:-dist}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
LDF="-s -w -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Version=$VER -X github.com/Zhengmingming1/tidebot/internal/buildinfo.Commit=$COMMIT"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"; arch="${target#*/}"; ext=""; [ "$os" = windows ] && ext=".exe"
  name="tidebot-server_${VER}_${os}_${arch}"
  stage="$(mktemp -d)/$name"; mkdir -p "$stage"
  for b in agentd agentctl; do
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDF" -o "$stage/$b$ext" ./cmd/$b)
  done
  cp "$ROOT/README.md" "$ROOT/README.en.md" "$stage/"
  if [ "$os" = windows ]; then
    (cd "$(dirname "$stage")" && if command -v zip >/dev/null; then zip -qr "$OUT/$name.zip" "$name"; else python3 -m zipfile -c "$OUT/$name.zip" "$name"; fi)
  else
    tar -C "$(dirname "$stage")" -czf "$OUT/$name.tar.gz" "$name"
  fi
  if [ "$os" = linux ]; then
    deb="$(mktemp -d)"
    install -Dm755 "$stage/agentd" "$deb/usr/bin/agentd"
    install -Dm755 "$stage/agentctl" "$deb/usr/bin/agentctl"
    install -Dm644 "$ROOT/packaging/linux/tidebot.service" "$deb/usr/lib/systemd/user/tidebot.service"
    mkdir -p "$deb/DEBIAN"
    cat > "$deb/DEBIAN/control" <<CTL
Package: tidebot-server
Version: $VER
Architecture: $arch
Maintainer: Zhengmingming1 <noreply@github.com>
Section: utils
Priority: optional
Homepage: https://github.com/Zhengmingming1/tidebot
Description: tidebot scheduler daemon (agentd) and CLI (agentctl)
 Runs Claude Code tasks on a schedule. Needs the claude CLI installed separately.
CTL
    dpkg-deb --root-owner-group --build "$deb" "$OUT/tidebot-server_${VER}_${arch}.deb" >/dev/null
  fi
done
ls -l "$OUT"
