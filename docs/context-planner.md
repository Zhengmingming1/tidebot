# Context Planner：实施计划与使用说明

## 范围与分阶段计划

基于 `70ce071` 实施第一阶段确定性 MVP；不新增模型调用，不改变数据库表结构。

| 阶段 | 交付 | 验收 |
| --- | --- | --- |
| 1. 规划内核 | `internal/contextplan`：版本化估算器、作用域检查、不可拆分材料、版本/哈希/来源去重、稳定排序、预算及选择决策 | 同输入同计划 ID；并发执行一致；未授权材料的 loader 不执行；必需指令超预算明确失败 |
| 2. Skill 接入 | 从已冻结、验证归属的绑定读取元数据，再读取 SKILL.md；此阶段不读取 references/scripts；已有依赖验证继续生效 | 外来版本拒绝；正文总量限制；版本哈希校验；主技能与已绑定辅助技能完整保留 |
| 3. DAG 接入 | 节点显式选择祖先输出；重复模板引用只装载一份；完整、规则摘要、原文引用三种模式；可选材料按优先级进入剩余预算 | 非祖先引用拒绝；分支跳过明确标记；摘要保留失败线索、行号、原文哈希和引用；原始输出不被改写 |
| 4. 运行与审计 | 统一执行前预算检查；现有 operation_log 追加计划；运行详情展示选择结果；节点编辑器提供开关和限额 | 审计不可修改、正文不进入审计；持久化失败阻止启动；API 认证不变；三系统 CI 与 Race 检查 |

“任务感知”由节点目标、明确的 Skill 绑定、祖先引用和优先级驱动，不依赖关键词猜测应该丢弃哪些技能。一个节点不读取整个工作流其他节点的技能库；所有已绑定技能均视为必需。若某辅助技能不再需要，应通过已有绑定编辑器从该节点移除，并重新通过依赖校验。

## 预算策略

默认初始输入上限为 65,536 个估算 Token，系统预留 4,096、输出预留 4,096、后续工具结果预留 8,192。窗口大小默认未知，不根据模型名称猜测容量。

已配置窗口时：

```text
可装载预算 = min(输入上限, 窗口 - 输出预留 - 工具预留) - 系统预留
```

窗口未知时，只实施 `输入上限 - 系统预留`，审计标记 `model_window_unknown_input_cap_only`。预算为 Token **估算**，不是供应商硬限制保证。计数器版本为 `ascii4-unicode1/v1`，与依赖预检共享：ASCII 约 4 字符/Token，非 ASCII 约 1 字符/Token。现有 256 KiB Skill 正文及 DAG prompt 字节限制仍然有效。

例如窗口 32,768、输入上限 65,536，使用上述预留值时，可装载预算为 16,384。预算先容纳完整任务及必需 Skill，再选择可选上游材料。必需材料放不下时直接报错，不截断安全规则。可选材料省略后，Prompt 明确标记未装入初始上下文。

全局策略通过已有 IPC 或受认证/CSRF 保护的 RPC 设置：

```sh
agentctl call context.policy.get '{}'
agentctl call context.policy.set '{"window_tokens":32768,"max_input_tokens":65536,"output_reserve":4096,"tool_reserve":8192,"system_reserve":4096}'
```

HTTP 对应 `POST /api/rpc/context.policy.get` 和 `POST /api/rpc/context.policy.set`。零值表示继承/使用默认值，不表示关闭预留。节点的非零字段覆盖全局对应字段；只调整输入上限会保留全局窗口和其他预留。存储策略损坏或读取失败时拒绝启动，不回退为更宽松的默认策略。

## 工作流节点示例

在节点编辑器开启“规划上游上下文”，选择加载方式和输入/摘要上限。既有工作流未开启时保留原有内联模板语义；所有运行仍经过统一初始预算检查。

```json
{
  "id": "repair",
  "kind": "skill",
  "prompt": "修复 {{steps.review.output}} 中的问题，然后核对 {{steps.tests.output}}。",
  "skill_bindings": [{"skill_id": "repair_skill", "mode": "follow"}],
  "needs": [{"node_id": "review"}, {"node_id": "tests"}],
  "context": {
    "budget": {"max_input_tokens": 32768},
    "mode": "summary",
    "max_tokens": 2048,
    "sources": [
      {"node_id": "review", "mode": "full", "priority": 100},
      {"node_id": "tests", "mode": "summary", "max_tokens": 1024, "optional": true, "priority": 80}
    ]
  }
}
```

`sources` 是 API 的精细控制项，UI 提供节点默认模式和限额。模板引用自动进入候选集，`sources` 可覆盖它们或明确加入其他祖先。全部来源必须属于此图的祖先；不会搜索其他运行、Memory、工作区或密钥库。来源的 `optional` 默认 false，不能因为预算小就静默丢弃。

- `full`：完整原文；重复引用以 `[upstream:节点ID]` 指向一次加载的材料。
- `summary`：确定性、有损摘录，优先保留含 fail/error/panic/exception 的行及相邻行，再补开头和结尾；保留行号、原文哈希与来源。它不是语义摘要，也不能保证覆盖每个错误。
- `reference`：只提供原文标识、哈希和文件位置，正文不进入初始 Prompt。

原文永久性遵循现有工作流历史保留策略，位于 `workflow_runs` / `workflow_run_nodes` 的现有记录中；运行详情 API `GET /api/workflow-runs/{run_id}` 可追溯。执行期间，当前节点可用的原文写入自己工作目录的 `context/upstream-{node_id}.txt`（0600），节点结束后随临时 stage 清理。模型可按需用原有只读工具读取，既有工具权限不会扩大。只有这个节点已授权的祖先材料会被写入。

## 计划审计

执行前追加 `operation_log`，`entity_type=run`、`action=context.plan`，关联实际 child run ID。写入失败则不启动模型。此表已有禁止修改/删除的触发器，因此无需迁移。运行详情的 `context_plan` 字段和“上下文计划”面板展示：

- 策略与估算器版本、计划 ID、节点/作用域、Provider/Model；
- 生效预算、材料估算量、版本、内容哈希和来源；
- 已选材料、加载模式、可选材料省略与去重原因；
- 估算器及窗口未知的明确警告。

正文、原始 Prompt、凭据、绝对工作目录和完整摘要不写入计划审计。原文仍由既有运行/工作流历史负责。计划 ID 是规范化计划元数据的 SHA-256；同一材料快照、作用域、模型和预算产生同一 ID。不同工作流运行作用域不同，ID 可不同。

版本去重同时考虑类型、不可变版本、内容哈希与来源。不同 Skill 基目录可能包含同名但不同内容的脚本，不能仅因为 SKILL.md 相同就合并。共享依赖应由既有 resolver 归一为同一个绑定；Planner 不删除不同版本或不同来源的指令。

## 验证与边界

测试覆盖规划确定性、并发、预算预留、作用域拒绝、加载前权限检查、版本/哈希去重、快照篡改、规则摘要、取消、DAG 祖先限制、分支跳过、原文保留、运行审计关联、审计无正文、审计失败阻止启动、预算失败阻止启动、Windows 保留文件名以及运行详情认证。基准覆盖 16 个 Skill 的规划开销和三次重复引用的上下文准备。

```sh
go test -race -count=10 ./internal/contextplan
go test -race ./internal/app ./internal/workflow ./internal/skillstore ./internal/webui
go test -run '^$' -bench 'BenchmarkPlan16Skills|BenchmarkContextRepeatedOutput' -benchmem -count=3 ./internal/contextplan ./internal/workflow
python3 scripts/build-webui.py --check
node --check internal/webui/static/app.js
python3 scripts/check-i18n.py
```

CI 在 `feat/context-planner-*` 分支 push 时执行完整矩阵，无需 PR；验证通过才快进 main。

第一版控制 Tidebot 装配的**初始上下文**。Claude Code 内部后续模型调用、动态工具结果和隐藏系统指令由外部 runtime 管理，预留不能代替逐轮硬限制。没有额外付费模型调用，没有引入跨任务 Memory，也没有把插件工具权限当成数据授权。审计为执行前已接受的计划；预算不足而尚未创建 run 的请求返回明确错误，没有虚构 run 审计。现有追加式审计表会增长，沿用应用备份策略；本轮不更改其保留规则。


## 本轮本地测量

Linux amd64，Go 1.26.10，Xeon Platinum 8573C，`GOMAXPROCS=2`，各 3 次取中位数：

| 微基准 | 时间/op | B/op | 说明 |
| --- | ---: | ---: | --- |
| 16 个 Skill 的规划 | 196.842 µs | 92,402 | 包含哈希、估算、决策及计划 ID；不含数据库与 CLI |
| 三次引用同一 64 KiB 输出：旧内联准备 | 108.813 µs | 205,842 | 展开三份正文 |
| 三次引用同一 64 KiB 输出：候选准备 | 5.218 µs | 778 | 保存一份原文引用与模板标记；不含后续规划、审计和最终 Prompt 组装 |

重复引用的原文正文由 192 KiB 降为 64 KiB，另有少量来源/文件/哈希包装。这不是付费模型的 Token 实测。两个准备基准只衡量对应准备步骤，不能把约 21 倍的步骤倍率解释为整个工作流的速度倍率。共享 CPU 下时间会波动。原始结果见 [context-planner-benchmarks.txt](context-planner-benchmarks.txt)。
