# Building from source

tidebot is GNU AGPL-3.0-only, and anyone can build it:

```bash
go build -o bin/ ./cmd/agentd ./cmd/agentctl           # server + CLI (Go 1.24+)
(cd cmd/tidebot-desktop && go build -tags gtk3 .)     # desktop; Linux needs libgtk-3-dev libwebkit2gtk-4.1-dev
scripts/package-server.sh 0.1.7 dist                  # release-style archives
```

## Self-built copies don't self-update

Official releases come from `.github/workflows/release.yml` and are stamped with
`-X github.com/Zhengmingming1/tidebot/internal/buildinfo.Official=1`. Only those binaries may
replace themselves with a downloaded update. Anything else is **notify-only**: plain
`go build`, `scripts/package-*.sh` without `TIDEBOT_OFFICIAL=1`, or a fork's build.

- With a version stamped (`-X …/buildinfo.Version=0.1.6`), Settings → About and
  `agentctl update check` say a newer official release exists. They never download or swap
  files. The text reads "built from source, … download the official build by hand".
- A `dev` build (no version) only says it's a development build and which official release is
  the latest.

This stops your own build from being silently swapped for official binaries, which would
throw away your changes. The update itself is safe either way: `latest.json` is verified with
ed25519 against `updater.PublicKey`, and each file against the sha256 in that signed manifest.
A copy built from source can't be fed someone else's binaries.

## Forks with their own update channel

Run your own releases repo and key, and set them at link time:

```bash
go run ./tools/relsign keygen my-update.key        # prints the public key; keep the file secret
LDF="-X github.com/Zhengmingming1/tidebot/internal/buildinfo.Official=1 \
     -X github.com/Zhengmingming1/tidebot/internal/buildinfo.SourceURL=https://github.com/you/tidebot \
     -X github.com/Zhengmingming1/tidebot/internal/updater.ReleasesRepo=you/tidebot-releases \
     -X github.com/Zhengmingming1/tidebot/internal/updater.PublicKey=<base64 public key>"
```

`relsign manifest` then signs a `latest.json` that points at your repo. Under the AGPL, a changed
version that you let others use over a network must offer its source. Set `SourceURL` to it,
and the WebUI's Settings → About links that exact version (`/tree/v<version>` or the commit).

## Testing the updater

```bash
scripts/e2e-update.sh
```

This builds an "official" 0.1.6 and a 0.1.7 tarball, signs a local feed with a throwaway key,
and checks three things: 0.1.6 updates in place and restarts as 0.1.7; a self-built 0.1.6 only
announces the update and leaves its files alone; a dev build announces nothing to install.
It never touches the real signing key.
