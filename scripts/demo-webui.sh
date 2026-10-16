#!/usr/bin/env bash
# Start an isolated agentd (mock Claude) with seeded demo data for WebUI screenshots.
# Usage: scripts/demo-webui.sh [home_dir] [port]   — prints the URL; password demo-pass-99.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
H="${1:-/tmp/tidebot-demo}"
PORT="${2:-17421}"
BIN="${BIN:-/tmp/tidebot-demo-bin}"
mkdir -p "$BIN"
(cd "$ROOT" && go build -o "$BIN/agentd" ./cmd/agentd && go build -o "$BIN/agentctl" ./cmd/agentctl)
[[ -f "$H/agentd.pid" ]] && kill "$(cat "$H/agentd.pid")" 2>/dev/null || true
rm -rf "$H"; mkdir -p "$H/data" "$H/ws"
cat > "$H/config.yaml" <<YAML
home_dir: $H
data_dir: $H/data
db_path: $H/data/tidebot.db
ipc_path: $H/agentd.sock
webui_enabled: true
webui_addr: 127.0.0.1:$PORT
claude_bin: $ROOT/testdata/mock-claude/claude
default_provider: kimi-coding
max_concurrency: 2
default_timeout: 5m
log_level: warn
YAML
export TIDEBOT_CONFIG="$H/config.yaml" TIDEBOT_MOCK_RANDOM_COST=1 TIDEBOT_MOCK_STEPS=8 TIDEBOT_MOCK_STEP_SLEEP=0.4
unset TIDEBOT_HOME TIDEBOT_DB TIDEBOT_IPC TIDEBOT_CLAUDE_BIN TIDEBOT_DEFAULT_PROVIDER TIDEBOT_AMEND_AI
nohup "$BIN/agentd" -config "$H/config.yaml" >"$H/agentd.log" 2>&1 &
echo $! > "$H/agentd.pid"
for _ in $(seq 50); do [[ -S "$H/agentd.sock" ]] && break; sleep 0.1; done
ctl() { "$BIN/agentctl" "$@"; }
ctl auth set-password --password demo-pass-99 >/dev/null
tid() { ctl tasks create --name "$1" --prompt "$2" --cron "$3" --workspace "$H/ws" | python3 -c 'import json,sys; print(json.load(sys.stdin)["task"]["ID"])'; }
T1=$(tid "Daily Report" "Generate today's summary of repository activity" "0 9 * * *")
T2=$(tid "Sync Issues" "Pull the latest issues from trackers and update the board" "30 11 * * *")
T3=$(tid "Clean Logs" "Archive log files older than 7 days" "0 18 * * *")
T4=$(tid "Weekly Digest" "Summarize the week's merged PRs and incidents" "0 8 * * 1")
T5=$(tid "Backup & sync workspace" "Back up local project files and sync the archive to cloud storage" "0 3 * * 1-5")

# Amendment thread + branched version tree on the backup task.
first=$(ctl amend propose --task "$T5" --text "Exclude node_modules, .git, tmp and *.log; only run on weekdays (Mon-Fri); back up project files to a zip archive" | python3 -c 'import json,sys; print(",".join(i["id"] for i in json.load(sys.stdin)))')
for i in ${first//,/ }; do ctl amend confirm --id "$i" >/dev/null; done
V1=$(ctl tasks versions --id "$T5" | python3 -c 'import json,sys; v=json.load(sys.stdin)["versions"]; print(min(v,key=lambda x:x["VersionNo"])["ID"])')
sec=$(ctl amend propose --task "$T5" --text "Compress archives with zstd" | python3 -c 'import json,sys; print(",".join(i["id"] for i in json.load(sys.stdin)))')
for i in ${sec//,/ }; do ctl amend confirm --id "$i" >/dev/null; done
ctl tasks rollback --id "$T5" --version "$V1" --reason "try a different approach" >/dev/null
third=$(ctl amend propose --task "$T5" --text "Sync the archive to the Backblaze B2 bucket" | python3 -c 'import json,sys; print(",".join(i["id"] for i in json.load(sys.stdin)))')
for i in ${third//,/ }; do ctl amend confirm --id "$i" >/dev/null; done
ctl amend propose --task "$T5" --text "Also keep backups for 10 days (not 7) only this run" >/dev/null

# A skill.
mkdir -p "$H/skill/repo-summary"
printf -- '---\nname: repo-summary\ndescription: Summarize repository activity\n---\nUse git log to summarize.\n' > "$H/skill/repo-summary/SKILL.md"
ctl skills import --name repo-summary --dir "$H/skill/repo-summary" >/dev/null

# Buttons + a tiny task budget (to demo a breach) via the WebUI API.
J="$H/cookies"; B="http://127.0.0.1:$PORT"
CSRF=$(curl -s -c "$J" -H 'Content-Type: application/json' -d '{"password":"demo-pass-99"}' "$B/api/login" | python3 -c 'import json,sys; print(json.load(sys.stdin)["csrf_token"])')
post() { curl -s -b "$J" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X "$1" -d "$3" "$B$2" >/dev/null; }
post PUT /api/buttons "[{\"id\":\"daily-report\",\"label\":\"Daily Report\",\"description\":\"Generate today's summary\",\"icon\":\"doc\",\"task_id\":\"$T1\"},{\"id\":\"sync-issues\",\"label\":\"Sync Issues\",\"description\":\"Pull latest from trackers\",\"icon\":\"sync\",\"task_id\":\"$T2\"},{\"id\":\"weekly-digest\",\"label\":\"Weekly Digest\",\"description\":\"Summarize the week\",\"icon\":\"pie\",\"task_id\":\"$T4\"},{\"id\":\"clean-logs\",\"label\":\"Clean Logs\",\"description\":\"Archive old log data\",\"icon\":\"trash\",\"task_id\":\"$T3\"}]"
post POST /api/budgets "{\"scope\":\"task\",\"scope_id\":\"$T3\",\"period\":\"day\",\"max_usd\":0.01}"

# Runs (mock Claude streams ~3s each).
for t in "$T1" "$T2" "$T4" "$T5" "$T1" "$T5"; do post POST "/api/tasks/$t/run" '{}'; sleep 0.3; done
post POST /api/runs/quick '{"prompt":"List TODO comments in the repo"}'
sleep 12
post POST "/api/tasks/$T3/run" '{}'; sleep 4; post POST "/api/tasks/$T3/run" '{}'
echo "T5=$T5"
echo "URL=$B  (password demo-pass-99)"
