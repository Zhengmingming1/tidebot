# Releasing tidebot

Releases are built by GitHub Actions. CI (`.github/workflows/ci.yml`) runs vet, tests and a build
on Linux, macOS and Windows for every push to `main`. Pushing a tag `v*` runs
`.github/workflows/release.yml`.

## Cutting a release

```bash
# write docs/release-notes/vX.Y.Z.md first (used as the release body)
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
gh run watch                       # wait for the release workflow
gh release view v0.1.0             # check the assets
```

To rebuild an existing tag (after fixing the workflow), run the workflow by hand with
`gh workflow run release.yml -f tag=v0.1.0`. Assets with the same name are replaced.

## What gets built

| Asset | Built on | Notes |
|---|---|---|
| `tidebot-server_<ver>_<os>_<arch>.tar.gz` / `.zip` | ubuntu | agentd + agentctl, pure Go (no cgo), linux/darwin/windows × amd64/arm64 |
| `tidebot-server_<ver>_<arch>.deb` | ubuntu | installs `/usr/bin/agentd`, `/usr/bin/agentctl` and a systemd *user* unit |
| `tidebot-desktop_<ver>_amd64.deb` | ubuntu-22.04 | depends on `libgtk-3-0`, `libwebkit2gtk-4.1-0` |
| `tidebot-desktop_<ver>_x86_64.AppImage` | ubuntu-22.04 | does **not** bundle GTK/WebKitGTK; the host needs `libwebkit2gtk-4.1` |
| `tidebot-desktop_<ver>_macos_universal.dmg` | macos | universal (arm64 + x86_64) `tidebot.app`, macOS 11+ |
| `tidebot-desktop_<ver>_windows_amd64_setup.exe` | windows | NSIS per-user installer (no admin), needs WebView2 (built into Windows 10/11) |
| `checksums.txt` | – | sha256 of every asset; `agentctl call update.apply` refuses to update without it |
| `ghcr.io/zhengmingming1/tidebot:<ver>` and `:latest` | ubuntu (buildx) | linux/amd64 + linux/arm64 |

The Docker image has agentd and agentctl but **not** the `claude` CLI. Add it yourself:

```dockerfile
FROM ghcr.io/zhengmingming1/tidebot:0.1.0
USER root
RUN npm install -g @anthropic-ai/claude-code
USER tidebot
```

```bash
docker run -d -p 127.0.0.1:7420:7420 -v tidebot:/home/tidebot/.tidebot \
  -e KIMI_API_KEY ghcr.io/zhengmingming1/tidebot:0.1.0
```

The container serves the WebUI over plain HTTP on 7420. Put a TLS proxy in front if you
expose it beyond localhost.

## Signing (optional)

Without these secrets the jobs still succeed: macOS gets an ad-hoc signature (Gatekeeper will
warn, right-click → Open), Windows binaries are unsigned (SmartScreen will warn).

| Secret | Used for |
|---|---|
| `APPLE_CERT_P12_BASE64`, `APPLE_CERT_PASSWORD` | Developer ID Application certificate, imported into a temp keychain |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Your Name (TEAMID)` |
| `APPLE_ID`, `APPLE_APP_PASSWORD`, `APPLE_TEAM_ID` | notarization with `notarytool` + stapling (all four Apple values needed) |
| `WINDOWS_CERT_PFX_BASE64`, `WINDOWS_CERT_PASSWORD` | Authenticode signing of the exe files and the installer via `signtool` |

Set them with `gh secret set NAME < file`. They are only read inside the steps that need them
and never echoed.

## Self-update

`agentctl call update.check` looks at the latest GitHub release; `update.apply` downloads the
server archive for this OS/arch, checks it against `checksums.txt`, swaps `agentd`/`agentctl` in
place and keeps the old ones as `*.prev`. Restart agentd afterwards. It is disabled when
`TIDEBOT_MANAGED=1` (the .deb unit and the Docker image set it; use apt / docker pull there).
While the repository is private, unauthenticated update checks get a 404, so self-update only
works once the repo is public.

## Local builds

```bash
scripts/package-server.sh 0.1.0 dist          # all server archives + .deb (needs dpkg-deb)
scripts/package-desktop-linux.sh 0.1.0 dist   # needs Go 1.25, libgtk-3-dev, libwebkit2gtk-4.1-dev, file
scripts/package-desktop-macos.sh 0.1.0 dist   # macOS only
```
