# Releasing tidebot

Releases are built by GitHub Actions. CI (`.github/workflows/ci.yml`) runs i18n, vet, tests and
a build on Linux, macOS and Windows for every push and PR to `main`. Docs-only and README-only
changes skip it. The matrix itself is `.github/workflows/full-matrix.yml`, and you can run it
by hand with `gh workflow run full-matrix.yml -f ref=<branch|tag|sha>`. Pushing a tag `v*`
runs `.github/workflows/release.yml`. It runs the same matrix first, and no packaging job
starts unless it passes.

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
| `tidebot-desktop-<ver>-Linux-x64.deb` | ubuntu-22.04 | depends on `libgtk-3-0`, `libwebkit2gtk-4.1-0` |
| `tidebot-desktop-<ver>-Linux-x64.AppImage` | ubuntu-22.04 | does **not** bundle GTK/WebKitGTK; the host needs `libwebkit2gtk-4.1` |
| `tidebot-desktop-<ver>-macOS-AppleSilicon-Intel.dmg` | macos | universal (arm64 + x86_64) `tidebot.app`, macOS 11+ |
| `tidebot-desktop-<ver>-Windows-x64-Setup.exe` | windows | NSIS per-user installer (no admin), needs WebView2 (built into Windows 10/11) |
| `tidebot-desktop-<platform>.<ext>` (no version) | – | copies of the four desktop files so `releases/latest/download/...` links stay stable |
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

The source repo is public (AGPL-3.0-only), but the update channel stays separate: the
`publish` job also mirrors each release to
[tidebot-releases](https://github.com/Zhengmingming1/tidebot-releases) repo with `SHA256SUMS`
and a signed `latest.json` (+ `latest.json.sig`, ed25519, built by `tools/relsign`).

Secrets (environment **`release`**, which only deploys from `v*` tags and `main`; no
required reviewer, so the weekly routine can still release unattended):

- `UPDATE_SIGNING_KEY`: base64 ed25519 private key. Its public half is `updater.PublicKey`
  in `internal/updater/manifest.go`; `relsign` refuses to sign if they don't match.
- `RELEASES_TOKEN`: fine-grained PAT, only `tidebot-releases`, Contents read/write.
  Without it the mirror step is skipped with a warning.
- The Apple/Windows signing secrets above belong there too. Only the `desktop-macos`,
  `desktop-windows`, `publish` and `price catalog` jobs declare `environment: release`.

Pull requests (including forks) never see these. CI runs on `pull_request`, not
`pull_request_target`; the default `GITHUB_TOKEN` is read-only; fork PRs from outside
contributors need approval before they run; every third-party action is pinned to a commit
SHA (Dependabot bumps them). Rulesets: only the owner pushes to `main` and creates `v*` tags.
Nobody can force-push or delete `main`, or move or delete a `v*` tag.

Only release-workflow binaries carry `buildinfo.Official=1` (`TIDEBOT_OFFICIAL=1` in
`release.yml`). Anything built from source is notify-only; see [building.md](building.md).
`scripts/e2e-update.sh` runs a full 0.1.6 → 0.1.7 update against a local feed signed with a
throwaway key. CI runs it on Linux.

On the client, agentd / the desktop app check `latest.json` once a day (Settings → About →
Updates, can be turned off). `agentctl update check|apply` does the same by hand. Apply =
download, verify signature + sha256, rename the current files to `*.prev` (one backup), move
the new ones in, restart. The new build has to answer on IPC within 90s or it gets rolled back
(under systemd, after two failed starts). Docker, `.deb`, Homebrew/Nix and `TIDEBOT_MANAGED=1`
installs only get a notification. `TIDEBOT_UPDATE_URL` points the check at a mirror.
Plain GETs only, honours `HTTPS_PROXY`, nothing else is sent.

## Local builds

```bash
scripts/package-server.sh 0.1.0 dist          # all server archives + .deb (needs dpkg-deb)
scripts/package-desktop-linux.sh 0.1.0 dist   # needs Go 1.25, libgtk-3-dev, libwebkit2gtk-4.1-dev, file
scripts/package-desktop-macos.sh 0.1.0 dist   # macOS only
```

## Release notes

`scripts/release-notes.sh <tag> <owner/repo>` writes the "Which file do I download?" table
followed by `docs/release-notes/<tag>.md`. The publish job uses it as the release body.
v0.1.0 used the older file names (`... v010` mode).

A GitHub Pages download page with OS detection was skipped for now. The releases page and the
"Which file do I download?" table cover it.
