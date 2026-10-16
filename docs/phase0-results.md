# 阶段 0 实测结果

> 测量环境与数字均为本机实跑，不是估算。测量时间：2026-10-16 10:21（上海时间，UTC+8）。

## 环境

| 项 | 值 |
|---|---|
| OS | Linux x86_64 |
| Go | go1.24.4（`CGO_ENABLED=0`，`-trimpath -ldflags="-s -w"`） |
| SQLite 驱动 | `modernc.org/sqlite v1.37.0`（纯 Go） |
| Claude 二进制 | 仓库内 `testdata/mock-claude/claude`（未调用真实 API） |
| WebUI | 关闭（`--no-webui`） |

## 二进制体积

| 产物 | 字节 | MiB |
|---|---:|---:|
| `bin/agentd` | 7 139 512 | **6.81** |
| `bin/agentctl` | 7 147 704 | **6.82** |

对照最终方案目标「安装包 < 20 MB（建议值）」：仅核心两二进制合计约 13.6 MiB，尚未含桌面外壳（Wails/Tauri）与前端资源；现阶段达标空间充足。

## 空闲常驻内存（RSS）

| 项 | 值 |
|---|---|
| 进程 | `agentd --no-webui`（已加载 SQLite、种子 Provider、调度器 tick 循环） |
| 稳定等待 | 启动后 sleep 2s |
| `VmRSS` | **8332 KB ≈ 8.14 MiB** |

对照目标「核心空闲 < 50 MB（建议值）」：**达标**（约 16% 的预算）。

原始记录见本地 `measure-out/metrics.txt`（已 gitignore，可用 `./scripts/measure.sh` 复现）。

## 功能冒烟（mock）

- `agentctl run --prompt "smoke"` → `status=succeeded`，解析到 `total_cost_usd=0.00123`、token in/out、session_id
- `agentctl runs` 列出落库记录
- `agentctl providers list` 展示全部内置预设与官方文档来源链接
- `go test ./...`、`go vet ./...` 通过（见 CI）

## 本阶段刻意未测（留待后续）

- 真实 `claude` CLI + 真实第三方 Key 的端到端调用
- Wails/Tauri 托盘、安装包体积、进程内桥接
- WebUI 空闲监听开销（阶段 0 未实现 HTTP）
- Windows / macOS 交叉编译产物体积与 RSS
- `--plugin-dir`、`--resume`、钩子回调等完整 CLI 旗标矩阵（Runner 已预留 ExtraArgs，契约样例待录制）

## 复现命令

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/agentd ./cmd/agentd
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/agentctl ./cmd/agentctl
./scripts/measure.sh
```

## 真实 Claude Code + Kimi Coding Plan E2E（2026-10-16 10:29–10:30 上海时间）

> 密钥仅通过环境变量 `KIMI_API_KEY` 注入子进程，**未**写入仓库、日志或 SQLite。

| 项 | 值 |
|---|---|
| Claude Code | **2.1.301**（`@anthropic-ai/claude-code`，Node v22.23.3） |
| Provider | `kimi-coding` |
| Base URL | `https://api.kimi.com/coding/` |
| Auth | `ANTHROPIC_API_KEY` ← `env:KIMI_API_KEY` |
| Model | `k3-256k` |
| 文档来源 | https://www.kimi.com/code/docs/third-party-tools/claude-code |

### 手动 `agentctl run`

| 项 | 值 |
|---|---|
| Prompt | 要求只回复一个英文词 `pong`，禁用工具 |
| Flags | `--bare --permission-mode dontAsk --permission-prompts none --restricted --max-budget-usd 0.05 --effort low` |
| Status | **succeeded** |
| Result | `pong` |
| Cost | **$0.00413** |
| Tokens | in **611** / out **43** |
| Duration | ~4.2 s（wall） / result.duration_ms 3508 |
| Session | UUID（Claude Code 2.x） |

### 定时任务触发一次

| 项 | 值 |
|---|---|
| Trigger | cron（强制 `next_run_at` 到期后由 `agentd` 调度） |
| Status | **succeeded** |
| Cost | **$0.001866** |
| Tokens | in **97** / out **45** |
| Duration | ~9.9 s wall |

### 运行时 RSS（真实采样）

| 进程 | 观测 |
|---|---|
| `agentctl`（手动 run） | ~6.8–7.5 MiB |
| `claude` 子进程树（手动 run） | 峰值约 **227 MiB**（232264 KB），稳态约 212–217 MiB |
| `agentd`（调度期间） | **~8.2–9.2 MiB**（8440–9436 KB） |
| `claude`（调度触发的一次） | 峰值约 **221 MiB**（226960 KB） |

对照最终方案「Claude Code 会话约 0.3–1.3 GB」第三方区间：本次短对话实测约 **0.22 GB**，与「短任务偏低端」相符。

### 解析兼容性

- 真实 stream-json 含 `system/ui_invalidate`、`system/thinking_tokens`、assistant thinking 块等；结果事件仍为 `type=result` + `usage` + `total_cost_usd`，现有解析器可用。
- 脱敏样例：`testdata/streamjson/kimi-coding-pong.ndjson`（回归测试 `TestParseRealKimiCodingFixture`）。

## Phase 1b：成本控制 + WebUI（2026-10-16）

测量环境同阶段 0（本机实跑，上海时间 UTC+8）。

### agentd 空闲 RSS：WebUI 开 vs 关

| 模式 | VmRSS |
|------|-------|
| WebUI off (`--no-webui` / `webui_enabled: false`) | **13.88 MiB** (14216 KiB) |
| WebUI on (`webui_enabled: true`，监听 127.0.0.1) | **14.20 MiB** (14544 KiB) |
| 差值 (on − off) | **+0.32 MiB** |

说明：二进制因 `go:embed` 静态资源变大（agentd ~17 MiB）；空闲 RSS 仍接近阶段 0 量级，WebUI 监听额外约 0.32 MiB。

### 测试

`go vet ./...` 与 `go test ./...` 全绿（含 auth / CSRF / budget / webui toggle）。

### 截图（未入库）

保存在 `/workspace/tidebot-shots/*.png`（login / home / tasks / task-detail / skills / runs / spend / settings）。

## 桌面壳（cmd/tidebot-desktop，Wails v3.0.0-beta.30）

2026-10-16 在本机 Linux（Debian 13，WebKitGTK 2.54，Xvfb + xfce4-panel，无 GPU）实测。

| 项 | 数值 |
|----|------|
| 二进制（`-s -w -trimpath`，gtk3 tag） | 17.2 MB（17187824 B），动态链接 GTK3 / WebKitGTK 4.1 |
| 空闲 RSS，窗口打开，60 s 后 | 壳+核心 196 MB + WebKitWebProcess 316 MB + NetworkProcess 62 MB = 575 MB（PSS 338 MB） |
| 空闲 RSS，`--hidden` 启动到托盘，45 s 后 | 184 + 217 + 62 = 463 MB（PSS 238 MB） |

RSS 大头是 WebKit 和 GTK 共享库（软件渲染），不是 Go 核心；同一个核心单跑 agentd 约 14 MB。
真机上有 GPU 和共享库页缓存，数值应更低，但我没测。

选型简记：Wails v2 没有托盘；Tauri v2 功能全，但要 Rust 壳 + Go sidecar 两个进程。
Wails v3 有托盘、单实例、通知和 `AssetOptions.Handler`（进程内桥，不开端口），所以选它，
代价是 beta 和 Go 1.25，所以放在独立的嵌套 module 里，根 module 仍是 Go 1.24、无 cgo。

验证过（Linux）：托盘菜单（打开 / 暂停-恢复 / 按钮预设 / 局域网开关 / 退出）、关窗隐藏到托盘且调度继续、
第二次启动聚焦已有窗口且不抢 IPC socket、运行完成 / 失败 / 超预算通知、局域网开关在未配 TLS 时拒绝并通知、
托盘与 WebUI 跟随系统语言（zh_CN）、XDG autostart 写入/删除（单测）。
未测：Windows、macOS（构建脚本和 docs/ci/desktop.yml 已写）、loopback 桥在 WebView2 上的表现、
睡眠前延后（只留了钩子，Linux 只读电池 sysfs）。
Linux 托盘子菜单在 beta.30 的 DBus 菜单里显示为空，所以 Linux 上预设平铺在菜单里。
