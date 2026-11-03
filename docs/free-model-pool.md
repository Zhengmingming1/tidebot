# Kilo 免费模型池

Tidebot 的官方插件「Kilo 免费模型池」通过 Kilo 公开支持的匿名接口使用免费模型。无需注册 Kilo 账号或填写上游 API Key；这不代表领取、生成或共享提供商密钥，也不提供无限额度或免费的 Claude 模型。

## 安装与使用

1. 打开「插件」的官方目录，安装「Kilo 免费模型池」。
2. 批准插件访问 `ai-gateway.kilo.ai` 的网络权限。
3. 在插件配置中阅读并同意：提示词、对话、工具输入输出等发送内容可能被 Kilo 的上游提供商记录，并用于训练或改进服务。不要发送个人信息或机密内容。
4. 刷新免费模型列表，选择模型并保存。默认模型为 `kilo-auto/free`，其实际模型由 Kilo 从当前免费候选中选择。
5. 创建或编辑任务时，选择服务商 `kilo-free`，再运行任务。已有任务和用户原来的默认服务商不会被安装操作改变。

安装和配置不会自动执行任务、读取项目内容或发送业务提示词。刷新模型列表只读取公开目录；实际运行任务后，任务上下文及执行工具产生的内容才会随模型请求发送。

禁用插件、撤销网络权限或撤回数据使用确认后，本地网关会停止，后续请求立即被阻断，并取消通过该网关进行的请求。已经发送到上游的内容无法撤回。重新启用前需要满足相同的权限与确认条件。

## 免费范围与限制

- 列表从 Kilo 实时目录获取，仅保留明确标记 `isFree=true`、价格字段为零、支持文本输入输出及工具调用的模型。目录短时缓存；过期后刷新失败不会继续采用过期价格判断。
- 请求只能使用插件当前选择的免费模型。免费模型下线、变为收费或不再满足条件时，请求失败，不自动切换到付费模型，也不读取其他服务商的密钥兜底。
- 根据 Kilo 官方匿名访问说明，免费请求按 IP 限流，目前为每小时 200 次；同一出口的其他客户端可能共享额度。上游返回 429 时应等待，不通过切换账号、伪造会话或更换身份绕过限制。实际配额和可用性以 Kilo 为准。
- `kilo-auto/free` 的候选模型会变化。上下文长度、输出上限、工具调用效果、地区可用性及数据保留政策取决于当时模型；免费不等于持续可用或具备付费 Claude 模型的全部能力。
- 单次请求的最大输出预算会按所选模型的实际输出上限收窄，避免客户端默认值超过上游限制。
- Kilo 提醒部分免费模型会记录提示词与输出，用于训练或服务改进；其中 NVIDIA 免费端点限试用，并要求不提交个人或机密数据。应根据实际任务内容决定是否使用。

## 与 WaLiAPI 免费池的关系

研究基于 [WaLiAPI 源码 `3fcc40f0afad2c2223cf90c59ab302a700df7388`](https://github.com/fuzhengwei/WaLiAPI/tree/3fcc40f0afad2c2223cf90c59ab302a700df7388)。其启动逻辑一次性预置 Kilo 与 OpenCode 渠道；Kilo 使用空上游凭据，模型同步筛选 `isFree=true`。用户在本地网关使用的 token 负责网关访问控制，不会创造上游额度。

WaLiAPI 的 OpenCode 适配还包含客户端指纹、工具声明和会话标识伪装。当前 OpenCode 官方说明免费聊天模型需要通过 OpenCode 使用，其他客户端应使用付费模型或 Go。因此本插件不复制该适配，也不把 OpenCode 免费渠道作为通用 API 提供。

Tidebot 使用独立的本机回环网关，将 Anthropic Messages 请求转发至 Kilo 原生接口。网关随机 token 只供本次 Tidebot 进程访问本地服务，不持久化为用户的服务商密钥，不转发给 Kilo。上游请求不携带 `Authorization` 或 `x-api-key`；Kilo 区分匿名访问和无效凭据，随意填写假 Key 反而会被拒绝。

## 协议与验证边界

Kilo 官方 Gateway 文档主要展示 OpenAI Chat Completions，但其[官方网关源码 `83359d88f0cf707600cac6b0025828f792a58779`](https://github.com/Kilo-Org/cloud/blob/83359d88f0cf707600cac6b0025828f792a58779/packages/web-shared/src/lib/ai-gateway/handlers/llm-proxy.ts#L198-L218)同时接受 `/messages`。Tidebot 直接转发该原生协议，保留工具调用与流式事件，不自行把 OpenAI 响应翻译成 Anthropic 响应。

2026-11-03 的上游验证使用无认证、固定且无敏感内容的请求：OpenAI 与 Anthropic Messages 各完成一次虚拟工具调用，以及一次工具结果回传的流式响应；四次均返回 HTTP 200，记录费用为零。Messages 流包含 `message_start` 至 `message_stop`，最终文本为 `OK`。这只证明当时所选路由的基础工具与流式能力，不能替代全部模型、真实项目任务和各客户端版本的端到端验收。

产品验收使用 Claude Code 2.1.280，通过 Tidebot 的真实任务执行路径分别选择 `kilo-auto/free` 和 `liquid/lfm-2.5-2.6b:free`：两次均调用 Read 读取专门创建的测试文件、接收工具结果并返回正确文本；运行成功，账本记录 `cost_source=free`、费用为零并保留输入/输出 token。该验证不包含私人项目内容，也不代表所有免费模型或复杂任务均已验收。

上游已核对的路径不包含 `/messages/count_tokens`，本地网关明确返回不支持，不会将估算冒充官方 token 计数。Claude Code 的扩展能力、模型输出上限、特殊工具和长任务是否可用，仍取决于对应客户端及上游模型。发布测试结果见本版本发布说明和 GitHub Actions。

## 依据

- [Kilo 匿名认证与 Key 规则](https://kilo.ai/docs/gateway/authentication)
- [Kilo 模型、Auto Free 与免费端点数据政策](https://kilo.ai/docs/gateway/models-and-providers)
- [Kilo 用量与限流](https://kilo.ai/docs/gateway/usage-and-billing)
- [Kilo API 与工具调用](https://kilo.ai/docs/gateway/api-reference)
- [Kilo 官方匿名请求校验源码](https://github.com/Kilo-Org/cloud/blob/83359d88f0cf707600cac6b0025828f792a58779/packages/web-shared/src/lib/ai-gateway/handlers/llm-proxy.ts#L730-L746)
- [OpenCode 模型及免费访问范围](https://opencode.ai/v2/docs/console/models/)
- [WaLiAPI 内置渠道](https://github.com/fuzhengwei/WaLiAPI/blob/3fcc40f0afad2c2223cf90c59ab302a700df7388/src-tauri/src/services/builtin_channels.rs#L23-L68)、[Kilo 目录过滤](https://github.com/fuzhengwei/WaLiAPI/blob/3fcc40f0afad2c2223cf90c59ab302a700df7388/src-tauri/src/services/upstream_models.rs#L184-L196)、[OpenCode 适配说明](https://github.com/fuzhengwei/WaLiAPI/blob/3fcc40f0afad2c2223cf90c59ab302a700df7388/src-tauri/src/endpoint_executor/opencode_lane.rs#L1-L25)
