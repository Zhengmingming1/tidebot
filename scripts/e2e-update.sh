#!/usr/bin/env bash
# End-to-end self-update check against a local signed feed (no real keys, no network):
#   1. an official-marked 0.1.7 tarball install updates itself to 0.1.8 and restarts;
#   2. a self-built 0.1.7 (no Official mark) only announces 0.1.8 and refuses to apply;
#   3. a dev build announces nothing to install.
# A throwaway ed25519 key signs the feed; the test binaries get its public half via -ldflags.
# Usage: scripts/e2e-update.sh   (Linux/macOS, needs go + python3)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
W="$(mktemp -d)"; trap 'kill $(jobs -p) 2>/dev/null || true; pkill -f -- "-config $W/home/config.yaml" 2>/dev/null || true; rm -rf "$W"' EXIT
BI=github.com/Zhengmingming1/tidebot/internal/buildinfo
UP=github.com/Zhengmingming1/tidebot/internal/updater
PUB="$(go run ./tools/relsign keygen "$W/key")"
OS="$(go env GOOS)"; ARCH="$(go env GOARCH)"
build() { # version official outdir
  for b in agentd agentctl; do
    CGO_ENABLED=0 go build -trimpath -ldflags "-X $BI.Version=$1 -X $BI.Official=$2 -X $UP.PublicKey=$PUB" -o "$3/$b" ./cmd/$b
  done
}
PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')"
name="tidebot-server_0.1.8_${OS}_${ARCH}"
mkdir -p "$W/stage/$name" "$W/dist"
build 0.1.8 1 "$W/stage/$name"
tar -C "$W/stage" -czf "$W/dist/$name.tar.gz" "$name"
echo "- e2e test release" > "$W/notes.md"
RELSIGN_ASSET_BASE="http://127.0.0.1:$PORT" UPDATE_SIGNING_KEY="$(cat "$W/key")" \
  go run -ldflags "-X $UP.PublicKey=$PUB" ./tools/relsign manifest v0.1.8 "$W/dist" "$W/notes.md" >/dev/null
(cd "$W/dist" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >/dev/null 2>&1) &
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$PORT/latest.json" >/dev/null && break; sleep 0.1; done

H="$W/home"
start() { # bindir
  rm -rf "$H"; mkdir -p "$H/data"
  printf 'home_dir: %s\ndata_dir: %s/data\ndb_path: %s/data/tidebot.db\nipc_path: %s/agentd.sock\nwebui_enabled: false\nlog_level: warn\n' "$H" "$H" "$H" "$H" > "$H/config.yaml"
  # Not supervised: without INVOCATION_ID (set for anything under systemd, e.g. CI runners)
  # agentd re-execs itself after an update instead of exiting for systemd to restart it.
  env -u INVOCATION_ID -u TIDEBOT_SUPERVISED TIDEBOT_CONFIG="$H/config.yaml" TIDEBOT_UPDATE_URL="http://127.0.0.1:$PORT" TIDEBOT_MANAGED= \
    nohup "$1/agentd" -config "$H/config.yaml" >"$H/agentd.log" 2>&1 &
  echo $! > "$H/agentd.pid"
  for _ in $(seq 100); do [ -S "$H/agentd.sock" ] && break; sleep 0.1; done
}
ctl() { TIDEBOT_CONFIG="$H/config.yaml" TIDEBOT_LANG=en "$1/agentctl" "${@:2}"; }
stop() { pkill -f -- "-config $H/config.yaml" 2>/dev/null || true; sleep 0.5; } # the updated daemon is a new process
ver() { ctl "$1" update check 2>/dev/null | sed -n 's/^Up to date (\(.*\))\.$/\1/p'; } # version the daemon runs

echo "== 1. official 0.1.7 → 0.1.8"
I="$W/opt/tidebot"; mkdir -p "$I"; build 0.1.7 1 "$I"; start "$I"
ctl "$I" update check
ctl "$I" update apply > "$W/apply.json" || { cat "$W/apply.json"; tail -20 "$H/agentd.log"; exit 1; }
for _ in $(seq 150); do v="$(ver "$I" 2>/dev/null || true)"; [ "$v" = 0.1.8 ] && break; sleep 0.2; done
[ "$(ver "$I")" = 0.1.8 ] || { echo "FAIL: still $(ver "$I")"; cat "$W/apply.json"; tail -20 "$H/agentd.log"; exit 1; }
ctl "$I" version | grep -q 0.1.8 || { echo "FAIL: agentctl not replaced"; exit 1; }
echo "ok: daemon restarted as 0.1.8 in place; files: $(ls "$I" | tr '\n' ' ')"
stop

echo "== 2. self-built 0.1.7 (no Official mark)"
S="$W/selfbuilt"; mkdir -p "$S"; build 0.1.7 "" "$S"; before="$(sha256sum "$S/agentd")"; start "$S"
out="$(ctl "$S" update check)"; echo "$out"
echo "$out" | grep -q "built from source" || { echo "FAIL: no self-built notice"; exit 1; }
if ctl "$S" update apply >/dev/null 2>&1; then echo "FAIL: apply succeeded"; exit 1; fi
[ "$(sha256sum "$S/agentd")" = "$before" ] || { echo "FAIL: binary changed"; exit 1; }
echo "ok: announced only, binary untouched"
stop

echo "== 3. dev build"
D="$W/dev"; mkdir -p "$D"; build dev "" "$D"; start "$D"
out="$(ctl "$D" update check)"; echo "$out"
echo "$out" | grep -q "Development build" || { echo "FAIL: no dev notice"; exit 1; }
stop
echo "e2e update: all good"
