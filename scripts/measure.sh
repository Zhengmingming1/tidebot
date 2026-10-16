#!/usr/bin/env bash
# Measure agentd idle RSS and binary sizes (Phase 0 baseline). Real measurements only.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${ROOT}/measure-out"
BIN="${ROOT}/bin"
mkdir -p "$OUT"

HOME_DIR="$OUT/home"
rm -rf "$HOME_DIR"
mkdir -p "$HOME_DIR/data"

export TIDEBOT_HOME="$HOME_DIR"
export TIDEBOT_CLAUDE_BIN="$ROOT/testdata/mock-claude/claude"
export TIDEBOT_LOG_LEVEL=warn
export TIDEBOT_MAX_CONCURRENCY=2
# No API key needed with mock.

AGENTD="$BIN/agentd"
AGENTCTL="$BIN/agentctl"
if [[ ! -x "$AGENTD" ]]; then
  echo "missing $AGENTD — run make build first" >&2
  exit 1
fi

# Binary sizes
agentd_size=$(wc -c < "$AGENTD" | tr -d ' ')
agentctl_size=$(wc -c < "$AGENTCTL" | tr -d ' ')
agentd_size_mb=$(awk -v s="$agentd_size" 'BEGIN{printf "%.2f", s/1024/1024}')
agentctl_size_mb=$(awk -v s="$agentctl_size" 'BEGIN{printf "%.2f", s/1024/1024}')

# Start idle daemon
"$AGENTD" --no-webui >"$OUT/agentd.log" 2>&1 &
pid=$!
cleanup() { kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }
trap cleanup EXIT

# Wait until process is up
for i in $(seq 1 50); do
  if kill -0 "$pid" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
sleep 2  # settle

# RSS in kB from /proc (Linux)
if [[ -r /proc/$pid/status ]]; then
  rss_kb=$(awk '/VmRSS:/ {print $2}' /proc/$pid/status)
else
  # fallback ps
  rss_kb=$(ps -o rss= -p "$pid" | tr -d ' ')
fi
rss_mb=$(awk -v s="$rss_kb" 'BEGIN{printf "%.2f", s/1024}')

uname_s=$(uname -s)
uname_m=$(uname -m)
go_ver=$(go env GOVERSION)
date_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
date_shanghai=$(TZ=Asia/Shanghai date +%Y-%m-%dT%H:%M:%S%z)

cat > "$OUT/metrics.txt" << METRICS
measured_at_utc=$date_utc
measured_at_shanghai=$date_shanghai
os=$uname_s
arch=$uname_m
go=$go_ver
agentd_bytes=$agentd_size
agentd_mib=$agentd_size_mb
agentctl_bytes=$agentctl_size
agentctl_mib=$agentctl_size_mb
agentd_pid=$pid
agentd_idle_rss_kb=$rss_kb
agentd_idle_rss_mib=$rss_mb
webui=off
claude_bin=mock
METRICS

echo "Wrote $OUT/metrics.txt"
cat "$OUT/metrics.txt"
