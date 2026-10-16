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
