#!/usr/bin/env bash
# Print release notes: a bilingual "which file" table, then docs/release-notes/<tag>.md.
# Usage: scripts/release-notes.sh <tag> <owner/repo> [names: new|v010]
set -euo pipefail
TAG="$1"; REPO="$2"; NAMES="${3:-new}"; V="${TAG#v}"
IMG="ghcr.io/$(echo "${REPO%%/*}" | tr '[:upper:]' '[:lower:]')/tidebot:${V}"
U="https://github.com/$REPO/releases/download/$TAG"
if [ "$NAMES" = v010 ]; then
  MAC="tidebot-desktop_${V}_macos_universal.dmg"; WIN="tidebot-desktop_${V}_windows_amd64_setup.exe"
  AI="tidebot-desktop_${V}_x86_64.AppImage"; DDEB="tidebot-desktop_${V}_amd64.deb"
else
  MAC="tidebot-desktop-${V}-macOS-AppleSilicon-Intel.dmg"; WIN="tidebot-desktop-${V}-Windows-x64-Setup.exe"
  AI="tidebot-desktop-${V}-Linux-x64.AppImage"; DDEB="tidebot-desktop-${V}-Linux-x64.deb"
fi
S="tidebot-server_${V}"
l() { echo "[\`$1\`]($U/$1)"; }
cat <<MD
## Which file do I download? / 下载哪个？

| Your computer / 你的电脑 | Download / 下载 |
|---|---|
| **macOS, Apple Silicon (M1–M4) or Intel** / 苹果芯片或 Intel 的 Mac | $(l "$MAC") (one file for both / 两种芯片通用) |
| **Windows 10/11, x64** | $(l "$WIN") |
| **Linux desktop, x64** / Linux 桌面 | $(l "$AI") or / 或 $(l "$DDEB") (Debian/Ubuntu) |
| Server, Linux x64 / 服务器 | $(l "${S}_linux_amd64.tar.gz") or / 或 $(l "${S}_amd64.deb") |
| Server, Linux ARM64 | $(l "${S}_linux_arm64.tar.gz") or / 或 $(l "${S}_arm64.deb") |
| Server, macOS (Apple Silicon / Intel) | $(l "${S}_darwin_arm64.tar.gz") / $(l "${S}_darwin_amd64.tar.gz") |
| Server, Windows x64 / ARM64 | $(l "${S}_windows_amd64.zip") / $(l "${S}_windows_arm64.zip") |
| Docker | \`docker pull $IMG\` |

Most people want the **desktop** row for their OS. "Server" is only for running tidebot headless.
大多数人下载自己系统那一行的**桌面版**就行；"服务器"版是给无界面部署用的。

### First open (the apps are not signed yet) / 第一次打开（目前未签名）

- **macOS**: right-click the app → Open → Open. Or in Terminal: \`xattr -dr com.apple.quarantine /Applications/tidebot.app\`
  macOS：在应用上右键 → 打开 → 打开；或在终端执行上面的 \`xattr\` 命令。
- **Windows**: if SmartScreen says "Windows protected your PC", click **More info → Run anyway**.
  Windows：弹出"Windows 已保护你的电脑"时，点 **更多信息 → 仍要运行**。

Checksums / 校验：\`checksums.txt\` (sha256).

---

MD
[ -f "docs/release-notes/${TAG}.md" ] && cat "docs/release-notes/${TAG}.md" || echo "tidebot ${TAG}"
