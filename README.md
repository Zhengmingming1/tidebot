# tidebot

轻量级本地常驻 Agent 编排器：以 Claude Code（`claude -p` + stream-json）为执行引擎，支持长期定时任务、Skill 管理、版本指针回退、按钮与插件扩展。

当前进度：**阶段 0（技术验证）** — Go 核心骨架、Claude Runner（含 mock）、多模型 Provider 抽象、最小 cron 调度、`agentctl` CLI。

## 文档

- [最终方案 v3](docs/final-plan.md)
- [功能调研与可行性研究](docs/report.md)
- [模型 Provider 说明（含官方 base URL 来源）](docs/providers.md)
- [阶段 0 实测结果](docs/phase0-results.md)

## 依赖

- Go 1.22+（开发环境为 1.24）
- 可选：本机已安装的 `claude` CLI；无真实 Key 时用仓库内 mock

## 构建

```bash
make build          # 产出 bin/agentd、bin/agentctl
make test           # go vet 由 make ci 覆盖；测试使用 testdata/mock-claude
make measure        # 实测空闲 RSS 与二进制体积 → measure-out/ + 更新 docs/phase0-results.md 前先看脚本输出
```

## 快速试用（mock，无需 API Key）

```bash
export TIDEBOT_HOME=/tmp/tidebot-dev
export TIDEBOT_CLAUDE_BIN="$PWD/testdata/mock-claude/claude"
export TIDEBOT_DEFAULT_PROVIDER=anthropic

make build
./bin/agentctl providers list
./bin/agentctl run --prompt "hello from tidebot"
./bin/agentctl runs
```

接真实第三方模型时设置 Key（示例 Kimi），并选择对应 provider：

```bash
export TIDEBOT_API_KEY_KIMI="sk-..."   # 或 TIDEBOT_SECRET_PROVIDER_KIMI
./bin/agentctl run --prompt "hi" --provider kimi
```

**切勿把真实 Key 写进仓库、配置文件明文或 issue。**

## 守护进程

```bash
./bin/agentd --no-webui
# 另开终端：创建定时任务（重叠策略默认 skip）
./bin/agentctl task create --name demo --prompt "hourly check" --cron "0 * * * *" --provider kimi
```

## 布局

```
cmd/agentd          核心守护进程
cmd/agentctl        CLI
internal/config     配置
internal/db         SQLite + 迁移
internal/runner     Claude Code 子进程与 stream-json
internal/provider   Provider 预设与环境注入
internal/scheduler  cron 调度
internal/secrets    密钥解析（env；钥匙串后续）
testdata/mock-claude  假 claude，供测试与无 Key 联调
```

## 许可证

私有仓库；产品名不使用 “Claude Code”。
