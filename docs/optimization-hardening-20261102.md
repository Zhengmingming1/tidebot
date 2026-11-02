# 2026-11-02 全量优化实施记录

本轮以 `b0b2dc7` 为基线，沿用 `chore/optimization-hardening-20261102` 分支。
不新增数据库迁移，不移除已有接口；新增工作流重试配置默认为关闭。

## 六项交付与验证

| 范围 | 实际修改 | 回归验证 |
| --- | --- | --- |
| 执行可靠性 | daemon 恢复失败时拒绝启动并释放 IPC；后台任务取消与等待；调度使用条件更新原子领取到期任务，避免旧快照重复派发；任务锁按 App 管理并回收；关闭时取消策略重试；DAG 节点可选瞬态错误重试、取消退避、panic 转失败、晚到回调保护 | daemon 恢复故障注入；16 个并发调度领取；调度停止/重启；恢复幂等；瞬态/永久错误、重试耗尽、取消及 panic；用户停止与退出原因保持 |
| 代码模块化 | Go WebUI API 从 2,055 行拆为路由入口及 9 个职责模块；前端源文件拆为 25 个功能文件，通过确定顺序生成原有 `static/app.js` | 全量 API/前端回归；生成文件一致性、JavaScript 语法、双语键检查；Windows/macOS/Linux 构建 |
| 并发安全 | MCP 在 EOF 后等待处理器退出，关闭待返回调用；处理器并发上限与过载响应；插件并发启动合并；策略统计用原子 SQL 更新；执行指标使用原子计数；IPC listener 访问同步 | 原始 MCP 修复 `-race -count=20`；MCP/策略并发回归在 CI 连续 10 次；全仓库 Race Detector；并发插件启动与指标读取 |
| Skill / DAG | 绑定版本元数据用单次关联查询；复用已解析版本；依赖索引、延迟加载及哈希复用；上下文只构造一次；ASCII Token 估计快路径；DAG 展开前验证总字节数并一次分配 | Skill 导入、绑定、依赖及固定版本回归；预算边界、混合文本 Token 估计；DAG fan-in 与超限输入测试及基准 |
| 可观测性与性能 | 新增执行队列、耗时、失败、取消、跳过、拒绝、持久化失败及工作流重试指标；沿用 metrics 鉴权；启动/结束状态写入失败显式报错；缺失结果、超长 stdout、超长 stderr 的子进程处理 | metrics 鉴权与并发读取；数据库写失败注入；跨平台测试子进程覆盖输出异常、密钥遮蔽及取消；3 项基准进入 CI |
| 安全加固 | KV/日志 API 检查插件授权；撤销 secret 权限后停止 MCP 环境注入；插件日志/错误及运行错误遮蔽密钥；secret 名校验；Skill 路径校验覆盖 Windows 保留名和 ADS；导入及 staging 使用根目录约束；本地导入拒绝符号链接与非普通文件；归档数量/大小限制 | 插件权限与密钥撤销回归；日志泄露回归；路径穿越、目标符号链接、目录项数量回归；全历史 gitleaks |

前端拆分保留原有共享闭包、加载顺序、资源 URL 和 CSP；它是可维护的源文件拆分，不是独立 ES module 的迁移。

## 验证记录

- 原始 `b0b2dc7` MCP 测试：`go test -race -count=20 ./internal/plugin/official` 通过。
- 六组优化代码 `5ea3c18`：[CI 41458695434](https://github.com/Zhengmingming1/tidebot/actions/runs/41458695434) 的 10 个任务全部通过：Linux Go 1.26/1.27、macOS Go 1.26、Windows Go 1.26 的 vet/全量测试/核心构建，三系统桌面 vet/test/build，Linux 全量 Race Detector，前端一致性/i18n/gofmt/gitleaks，以及基准。Linux 签名更新端到端测试通过。
- 本地全量普通测试通过，但该托管环境无法创建 Unix socket，且 `/proc` PID 映射不一致；仅本地运行排除了相应 IPC 与子进程 RSS 测试。上述原生三系统 CI 没有增加排除项。
- 本地全量 Race 检查暴露了“插入运行后、标记 running 前取消”被误记为 skipped 的时序问题，已修复，并用确定性 `OnStart` 取消测试覆盖。`TestUserCancelIsNotQuit` 与新增测试连续 10 次 Race 回归通过。
- 本地全量 Race 的 WebUI 包在共享计算环境中达到默认 10 分钟超时，堆栈停在测试数据库初始化；原生 CI 同一代码的完整 Race 测试通过。没有通过跳过测试或放宽 CI 超时来处理。
- 最终取消修复及本记录提交后，重新运行同一完整 CI 矩阵；只有最终代码全部通过才更新 `main`。最终运行可在 [Actions](https://github.com/Zhengmingming1/tidebot/actions/workflows/ci.yml) 按提交 SHA 查询。

## 性能对比

基线为 `b0b2dc7`，仅将本轮相同的 benchmark 测试文件放入基线工作树。
Linux amd64、Go 1.26.10、Intel Xeon Platinum 8573C、`GOMAXPROCS=2`；基线与优化后串行执行，各 3 次，取中位数。

```sh
GOMAXPROCS=2 go test -run '^$' \
  -bench 'BenchmarkResolveBindings16|BenchmarkRender' -benchmem -count=3 \
  ./internal/skillstore ./internal/workflow
```

| 基准 | 基线时间/op | 优化后时间/op | 基线 B/op | 优化后 B/op | 分配次数/op（前 → 后） |
| --- | ---: | ---: | ---: | ---: | ---: |
| 16 个 Skill 绑定解析 | 1.493 ms | 0.881 ms | 92,528 | 14,953 | 2,534 → 406 |
| 3 × 64 KiB DAG 输入汇聚 | 0.444 ms | 0.178 ms | 748,057 | 205,777 | 6 → 8 |
| 拒绝 1,024 次引用的超限展开 | 703.171 ms | 3.025 µs | 847,834,784 | 144 | 113 → 7 |

绑定解析与汇聚时间中位数分别下降约 41% 和 60%，分配字节分别下降约 84% 和 72%。汇聚增加两次很小的分配，但消除了较大的中间字符串。超限展开现在在分配结果字符串前返回错误。

共享 CPU 会造成显著时间波动，这些结果是合成微基准，不代表真实模型请求或完整工作流的端到端延迟。分配量改善比时间倍率更可重复。原始测量见 [optimization-hardening-benchmarks.txt](optimization-hardening-benchmarks.txt)。

## 边界与剩余风险

- 调度领取保证同一到期快照只被成功领取一次；领取与执行入队之间尚无持久化 outbox。进程恰在两者之间崩溃仍可能漏执行，不能宣称外部副作用 exactly-once。工作流恢复保留终态，避免盲目重放；需要补偿的外部操作仍由业务处理。
- DAG 重试默认关闭，只应对可幂等操作开启。错误分类基于现有提供商错误信息，不能判断远端是否已完成副作用。
- 插件授权限制 Host API 和注入的 secret，不是原生可执行文件的操作系统沙箱。网络/文件系统隔离仍取决于部署环境；本轮没有承诺执行不可信二进制的强隔离。
- Token 预算沿用现有估算语义与字节上限，不等同于每个模型的精确 tokenizer；没有对真实供应商请求执行付费压测。
- 执行计数为进程生命周期指标，重启清零；多字段快照分别读取原子计数，不是跨字段事务快照。持久化历史仍在现有数据库中。
- 保留数据库格式与 API 兼容性；新增重试字段为可选。未进行真实用户长时间压力运行或所有第三方插件的兼容认证。
