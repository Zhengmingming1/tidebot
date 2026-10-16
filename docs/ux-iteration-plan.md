# tidebot 交互与 UI 迭代方案

> 写在前面：v0.1.x 的页面"能用"，但离"好用"还差得远。这份文档把每个独立功能拆开，对照了几款同类开源项目的**真实源码**（本地浅克隆后逐一阅读），把值得借的做法落成 tidebot 的具体改动、优先级和验收标准。
>
> 参考来源只写我真的打开读过的文件。Dify、n8n、Windmill、LiteLLM、Coolify、Portainer 这一轮**没有读源码**，所以下文不引用它们；后面如果要补，再单开一节。

## 0. 研究范围

| 项目 | 读过的文件（相对仓库根） | 主要借鉴 |
|---|---|---|
| healthchecks/healthchecks | `static/js/update-timeout-modal.js`、`static/js/adaptive-setinterval.js`、`static/js/search.js`、`hc/front/views.py`（cron_preview / tz）、`templates/accounts/login_totp.html` | 调度编辑器"简单 / Cron"双模式 + 服务端预览后续触发时间、时区选择、页面不可见时暂停轮询、搜索丢弃过期结果 |
| jhuckaby/Cronicle | `htdocs/js/pages/Schedule.class.js`、`Home.class.js`、`JobDetails.class.js` | 按 timing 结构推断"每小时/每天/每周/每月/自定义/按需"模式；首页"正在运行 + 即将运行"两栏 |
| louislam/uptime-kuma | `src/components/Confirm.vue`、`HeartbeatBar.vue`、`TwoFADialog.vue`、`src/util-frontend.js`（toast 超时） | 通用确认框（危险按钮样式可配）、心跳条 + tooltip、2FA 二维码 + 验证码确认后才启用、成功/失败 toast 分别配置停留时间 |
| farion1231/cc-switch | `src/components/providers/ProviderList.tsx`、`ProviderCard.tsx`、`AddProviderDialog.tsx`、`EditProviderDialog.tsx`、`forms/hooks/useApiKeyState.ts`、`useBaseUrlState.ts`、`useModelState.ts`、`src/config/claudeProviderPresets.ts`、`src-tauri/src/tray.rs` | 预设驱动的供应商表单（websiteUrl / apiKeyUrl / 分类）、卡片列表 + 拖拽排序 + "当前使用"标识 + 一键切换、托盘菜单分区（问题区 → 反馈行 → 主操作 → 退出） |
| CherryHQ/cherry-studio | `src/renderer/pages/settings/ProviderSettings/`：`ConnectionSettings/ApiKey.tsx`、`ApiHost.tsx`、`ProviderApiKeyListDrawer.tsx`、`ModelList/ModelList.tsx`、`ModelCheckDialog.tsx`、`ApiKeyCheckResults.tsx`；`components/ModelSelector/useModelListKeyboardNav.ts` | 左列表右详情的设置布局；Key 输入框带显示/隐藏、失焦提交；Base URL 有"恢复默认"；模型列表分组 + 逐个检测连通性；模型选择器键盘导航 |
| siteboon/claudecodeui | `src/modules/command-palette/CommandPalette.tsx`、`src/modules/skills/ProviderSkills.tsx`、`src/modules/mcp/McpServerFormModal.tsx`、`src/modules/plugins/PluginIcon.tsx` | ⌘/Ctrl+K 命令面板；技能拖拽上传（dropzone + 拖拽高亮 + 文件类型错误提示）；MCP 表单弹窗 |
| winfunc/opcode（原 Claudia） | `src/components/AgentRunOutputViewer.tsx`、`TimelineNavigator.tsx`、`UsageDashboard.tsx`、`MCPAddServer.tsx`、`CheckpointSettings.tsx` | 日志"贴底才自动滚动"+ 回到底部按钮 + 全屏 + 复制为 JSONL/Markdown；检查点时间线（diff / fork / restore）；用量看板 7d/30d/全部 + 分 tab 懒加载并缓存 |
| cline/cline | `apps/vscode/webview-ui/src/components/history/HistoryView.tsx`、`settings/ApiOptions.tsx`、`chat/auto-approve-menu/AutoApproveModal.tsx` | 历史列表 Fuse 模糊搜索 + 排序（最新/最旧/最贵/最多 token）+ 收藏筛选；权限分项开关 |
| open-webui/open-webui | `src/lib/components/common/ConfirmDialog.svelte`、`common/Valves.svelte`、`workspace/Tools/ToolkitEditor.svelte`、`workspace/Knowledge/KnowledgeBase/*` | 确认框可带输入（输入名字才能删）；Valves：用 JSON Schema 自动生成插件配置表单（required / enum→select / multiselect） |

## 1. tidebot 现状（读 `internal/webui/static/app.js`、`styles.css` 得出）

整体上是一个 1.2k 行的单文件 SPA，`route()` 按 hash 切页，每页 `api().then(mount(html))`。共性问题：

1. **没有加载态**：全文没有 skeleton/spinner，切页时先空白再"跳"出内容。
2. **确认全靠 `window.confirm()`**：删除任务、回滚版本、删按钮、删供应商、卸插件、删触发器、关 WebUI 共 7 处，样式不受控、没法表达"危险程度"，也没有撤销。
3. **toast 只有一种**：`toast(msg, bad)` 固定 3.2s，错误也会自动消失，没有"撤销"按钮。
4. **整页重绘**：大部分操作成功后直接 `route()`，滚动位置、展开状态、输入都丢。
5. **键盘支持零散**：只有搜索框 `K` 和全局 keydown 一处；列表没有方向键、弹窗没有焦点陷阱。
6. **响应式只有两个断点**（1100px / 800px），没有 `prefers-reduced-motion`、`:focus-visible` 的统一处理。
7. **日志流**（`renderRunDetail`）已经做了"贴底才自动滚动"，但没有回到底部按钮、搜索、按类型过滤、复制/下载，长日志也会卡。

下面逐个功能写。优先级：**P0** 本轮必须、**P1** 下一轮、**P2** 有空再做。

---

## 2. 分功能方案

### 2.1 首页 / 仪表盘
- **现状**：4 个快捷按钮 + "即将运行"时间线 + 状态卡。按钮不足 4 个时用"添加按钮"占位，看着像缺东西；"正在运行"没有单独的位置，要进运行记录才看得到。
- **参考**：Cronicle `Home.class.js` 把首页分成"活动任务"（带进度）和"即将运行"两栏，后者由 worker 计算；uptime-kuma `HeartbeatBar.vue` 用一条小色块条表示最近 N 次结果，hover 出 tooltip。
- **目标**：顶部一行 KPI（今日运行 / 成功率 / 今日花费 / 剩余预算）；左栏"正在运行"（实时耗时、成本、点击进日志），右栏"接下来 24 小时"；每个任务一条最近 20 次的心跳条。快捷按钮只显示已有的，末尾一个"+"。
- **验收**：正在运行的任务 ≤1s 出现在首页；心跳条有 tooltip（时间、状态、耗时）；空状态有一句引导 + 主按钮。**P0**

### 2.2 任务列表 / 详情 / 新建 / 调度编辑器
- **现状**：新建刚从 CLI 搬到网页（另一位同事正在做）；调度靠 `cronHuman()` 把 cron 翻成人话，但看不到"下几次具体几点跑"；列表不能搜索、筛选、批量操作。
- **参考**：
  - healthchecks `update-timeout-modal.js`：两种模式（简单周期 / Cron），Cron 模式输入后把 `schedule + tz` 当 hash，变了才请求 `/checks/cron_preview/`，返回后续触发时间，并带时区下拉（`views.py` 里还会把用户最常用的 3 个时区排前面）。
  - Cronicle `Schedule.class.js` L880 起：从 timing 结构反推当前属于 hourly/daily/weekly/monthly/yearly/custom/demand 哪种模式，编辑旧任务时自动选中对应 tab。
- **目标**：
  - 调度编辑器做成三个 tab：**常用**（每 N 分钟 / 每天 HH:mm / 每周几 / 每月几号）、**Cron**（高级）、**仅手动**；切换时互相换算，换算不了就提示"这个规则只能在 Cron 模式下编辑"。
  - 下方固定显示"接下来 5 次：10/17 周六 09:00 …"（本地时区，后端新增 `scheduler.preview` 接口，输入防抖 300ms，用 hash 去重）。
  - 列表：搜索、按状态/Skill 筛选、列排序、多选后批量暂停/恢复/删除。
  - 详情页 tab：概览 / 需求 / 运行 / 版本；名称和提示词支持就地编辑（点一下变输入框，Enter 保存、Esc 取消）。
- **验收**：不懂 cron 的人 30 秒内能建好"工作日每天 9 点"；非法 cron 在输入框下方立即报错；预览时间和实际调度一致（有单测对照）。**P0**

### 2.3 需求补充（amendments）
- **现状**：AI 结构化 → 用户批量确认的逻辑在后端已经有了，界面只是列表 + 按钮，看不出"这一条会改哪里"。
- **参考**：opcode `TimelineNavigator.tsx` 把每个检查点画成时间线节点，可以看 diff、从某点 fork、恢复；open-webui `ConfirmDialog.svelte` 支持在确认框里带输入。
- **目标**：每条补充显示成卡片：原话 → AI 结构化结果 → 对任务的影响（diff 预览）；标签区分"长期 / 仅下次"；勾选后底部出现固定条"确认 3 条 → 生成 v7"；可以就地改 AI 的结构化结果再确认。合并（consolidation）结果同样用 diff 展示。
- **验收**：确认前能看到完整 diff；"仅下次"的条目用完后在列表里显示为已过期、灰掉。**P1**

### 2.4 运行记录 / 日志 / 实时流
- **现状**：`renderRunDetail` 用 EventSource 追加日志，贴底时自动滚动，这点是对的；但没有回到底部、搜索、过滤、复制，日志行全部塞进 DOM。运行列表不能搜索排序。
- **参考**：opcode `AgentRunOutputViewer.tsx`（L82 起）根据 `distanceFromBottom` 判断是否自动滚动、提供滚动到底部、全屏和复制（JSONL / Markdown）；cline `HistoryView.tsx` 用 Fuse 模糊搜索，排序项有 newest / oldest / mostExpensive / mostTokens，可以只看收藏。healthchecks `adaptive-setinterval.js` 页面不可见时停掉轮询，回来立刻补一次。
- **目标**：
  - 日志视图：按事件类型折叠（assistant 文本 / 工具调用 / 工具结果 / 系统），工具调用默认折叠成一行；顶部有过滤 chips 和搜索（高亮命中，n/N 跳转）；用户往上翻时出现"↓ 有 12 条新日志"浮钮；复制 / 下载 JSONL；全屏。
  - 超过 2000 行只渲染可视区（简单的分段懒渲染即可，不引框架）。
  - 运行列表：搜索、状态筛选、按时间/花费/token 排序，失败的可以一键"重跑"。
  - 所有轮询改成"页面不可见就暂停"。
- **验收**：5 万行日志滚动不卡（Chrome 性能面板无 >50ms 长任务）；断线自动重连并从断点续传（SSE `Last-Event-ID`）。**P0**

### 2.5 技能库 / 导入 / 版本 / diff / 回滚
- **现状**：zip 拖拽导入正由另一位同事实现；版本列表回滚用 `confirm()`，看不到两个版本差在哪。
- **参考**：claudecodeui `ProviderSkills.tsx`：整块区域是 dropzone，拖进来时高亮（`isDragActive`），类型不对给明确错误（`errors.dropMarkdown`），也能点击选文件；opcode `TimelineNavigator.tsx` 的 diff/restore 交互。
- **目标**：
  - 导入：拖入后先出"预览卡"（名称、描述、文件树、大小、校验结果），确认才写入；重名时提供"作为新版本 / 改名"。
  - 版本：左侧版本树（current / canary 指针用彩色标签），右侧并排或行内 diff（只做行级 diff，JS 自己实现 Myers 或后端返回 unified diff）；回滚 = 移动指针，按钮旁写清楚"不会生成新版本"。
  - 回滚后 toast 带"撤销"（再把指针挪回去）。
- **验收**：任意两个版本可比较；回滚 10 秒内可撤销；坏 zip（无 SKILL.md、路径穿越 `../`）给出具体原因。**P0**

### 2.6 技能优化器（审阅 / 灰度）
- **现状**：三种模式在设置里是下拉，候选版本审阅没有专门界面。
- **参考**：cline `AutoApproveModal.tsx` 把每类权限做成独立开关并就地说明；opcode diff 视图。
- **目标**：每个 Skill 详情里有"优化"卡：模式切换（关闭 / 只提建议 / 灰度后确认）配一句说明；候选版本显示 diff + 优化理由 + 灰度期间的成功率/成本对比（当前 vs canary），两个按钮"转正"/"丢弃"。永不自动转正这一点在 UI 上写明。
- **验收**：灰度对比数据至少含运行次数、成功率、平均花费；转正需二次确认。**P1**

### 2.7 按钮
- **现状**：图标选择器和自动 ID 正在做；按钮编辑和首页展示是两套样式。
- **参考**：cc-switch `ProviderList.tsx` 用 `@dnd-kit` 拖拽排序；claudecodeui `PluginIcon.tsx` 统一的图标渲染。
- **目标**：按钮列表可拖拽排序（原生 HTML5 DnD + 键盘上下移动作为无障碍替代）；编辑区右侧实时预览"首页上长这样"；图标选择器可搜索、键盘可选。
- **验收**：排序后首页立即同步；不用鼠标也能完成新建和排序。**P1**

### 2.8 插件市场 / 安装 / 权限
- **现状**：安装时会弹权限审批，但插件配置项没有表单，只能改文件。
- **参考**：open-webui `common/Valves.svelte`：读插件声明的 schema，`required` 标星、`enum` 渲染成 select、`array + multiselect` 渲染多选，自动生成配置表单；claudecodeui `McpServerFormModal.tsx`、opcode `MCPAddServer.tsx`（stdio / SSE 分 tab 添加）。
- **目标**：`plugin.yaml` 增加 `config_schema`（JSON Schema 子集），前端据此生成表单；权限审批弹窗按风险分组（文件系统 / 网络 / 执行命令），高风险项默认不勾选；已安装插件卡片显示"已授予权限"，可以单独撤销。
- **验收**：新插件不写前端代码也能有配置界面；撤销权限后插件下次调用被拒并有清晰报错。**P1**

### 2.9 触发器
- **现状**：触发器只有列表和删除。
- **参考**：healthchecks 的 check 详情把"最近一次 ping、状态、事件日志"放在一起；uptime-kuma 心跳条。
- **目标**：触发器卡片显示类型（定时 / Webhook / 文件变化）、最近一次触发时间和结果；Webhook 类型给出可复制的 URL 和 curl 示例（复制按钮）、"发送测试事件"按钮；启用/停用开关就地切换。
- **验收**：Webhook 一键复制可用；测试事件能在运行记录里看到。**P1**

### 2.10 供应商 / 模型设置
- **现状**：正在改成可编辑；需要对齐 cc-switch / cherry-studio 的完成度。
- **参考**：
  - cc-switch `claudeProviderPresets.ts`：每个预设带 `websiteUrl`、`apiKeyUrl`、分类、模板变量；`AddProviderDialog` 先选预设再填 Key；`ProviderList` 卡片 + "当前使用" + 拖拽排序；`useApiKeyState.ts` 里 Key 和配置双向同步，并注释了"JSON 无效时不回写，避免输入框闪烁"。
  - cherry-studio `ConnectionSettings/ApiKey.tsx`：显示/隐藏、失焦提交；`ApiHost.tsx` 有"恢复默认地址"；`ModelList/ModelCheckDialog.tsx` 逐个模型检测、`ApiKeyCheckResults.tsx` 展示每个 Key 的检测结果。
- **目标**：左侧供应商列表（拖拽排序、默认标记、启用开关），右侧详情：Base URL（带"恢复默认"）、Key（只显示掩码 `sk-…abcd`，可替换不可回显，后端永远不返回明文）、"获取 Key"外链、模型列表（可从接口拉取或手填，单个检测）、"测试连接"显示延迟和错误原因。
- **验收**：Kimi Coding Plan 从选预设到测试通过 ≤4 步；API 响应里搜不到明文 Key（加测试）。**P0**

### 2.11 花费 / 预算
- **参考**：opcode `UsageDashboard.tsx`：7d / 30d / 全部 三档时间范围、overview/按模型/按项目等 tab 懒加载，切换范围时有缓存就不显示 loading。
- **目标**：时间范围切换 + 按任务 / 按模型 / 按供应商三个 tab；预算条（已用 / 上限，80% 变黄、100% 变红）；超预算后的行为（暂停 / 只提醒）就地可改。
- **验收**：切换 tab 不闪；预算阈值触发通知。**P1**

### 2.12 登录 / 2FA
- **参考**：uptime-kuma `TwoFADialog.vue`：先显示二维码和密钥，**用户输一次验证码通过才真正启用**；healthchecks `login_totp.html` 的验证码输入框。
- **目标**：登录页单卡片居中，错误信息具体但不泄露"用户是否存在"；TOTP 输入框 `inputmode="numeric" autocomplete="one-time-code"`，输满 6 位自动提交；启用 2FA 走"扫码 → 输入验证 → 展示恢复码（可下载）"三步；连续失败显示剩余等待时间。
- **验收**：未验证通过不会启用 2FA；恢复码只展示一次。**P1**

### 2.13 设置 / 多语言
- **现状**：设置是一长页；语言按钮只在 中/EN 间切换。
- **参考**：cherry-studio 设置页左导航 + 右内容的布局。
- **目标**：设置分组左导航（通用 / 供应商 / 预算 / 安全 / 通知 / 数据与备份 / 关于），URL 可直达 `#/settings/providers`；可自动保存的字段失焦即存并在旁边显示"已保存 ✓"，危险设置（关闭 WebUI、重置）放在最底部"危险区"；i18n 增加缺失 key 检查脚本（CI 里比对 zh-CN 与 en 的 key 集合）。
- **验收**：每个设置项都能通过 URL 定位；两份语言文件 key 完全一致。**P1**

### 2.14 通知 / 搜索
- **参考**：claudecodeui `CommandPalette.tsx`：`(metaKey || ctrlKey) + K` 打开，方向键选择，Enter 执行；healthchecks `search.js` 丢弃过期返回（避免慢请求覆盖新结果）；uptime-kuma `util-frontend.js` 里成功/失败 toast 停留时间分开配置。
- **目标**：顶部搜索升级为命令面板：既能搜任务/技能/运行，也能执行命令（新建任务、跑某个按钮、切语言）；通知中心按"失败 / 需要我确认 / 其他"分组，可全部标为已读；toast 分成功（3s）、错误（不自动消失，需手动关）、可撤销（8s，带按钮）。
- **验收**：只用键盘能完成"打开面板 → 搜任务 → 运行"；待确认的需求补充在通知里有入口。**P0**（toast/确认框部分），命令面板 **P1**

### 2.15 桌面托盘
- **参考**：cc-switch `src-tauri/src/tray.rs` 文件头注释写明了菜单结构：问题区（出问题才出现）→ 反馈行（刚做完需要重启生效的操作）→ 打开主窗口 → 各项切换 → 退出；菜单文案中英文分表。
- **目标**：托盘菜单：问题区（如"agentd 未运行""预算已用完"）→ 正在运行 N 个任务 → 快捷按钮（前 5 个）→ 暂停全部调度（勾选项）→ 打开 tidebot → 退出；图标三态（空闲 / 运行中 / 有错误）。
- **验收**：daemon 挂了托盘 5 秒内显示问题项；菜单语言跟随设置。**P2**

---

## 3. 共享设计系统

现在 `styles.css` 已有颜色、间距（8px 栅格 `--s1..--s6`）、圆角、阴影等 token，方向是对的，需要补齐和收紧：

- **Token**
  - 颜色：保留 sand 主题现有变量；补 `--focus-ring`（accent 40% 透明）、`--overlay`（弹窗遮罩）、`--code-bg`；状态色每种都要有 `-50` 背景 + 正文色，保证对比度 ≥ 4.5:1。
  - 字号：12 / 13 / 14（正文）/ 16 / 20 / 24，行高 1.5（正文）、1.3（标题）；数字列统一 `font-variant-numeric: tabular-nums`。
  - 间距只用 4 的倍数（8px 栅格 + 4px 微调），组件内边距：按钮 8×16、输入框 8×12、卡片 24。
  - 圆角：控件 8、卡片 12、标签/胶囊 999。
  - 动效：`--dur-fast 120ms`、`--dur 200ms`，`cubic-bezier(.2,0,0,1)`；`@media (prefers-reduced-motion: reduce)` 下一律关掉位移动画。
- **组件（在 app.js 里抽成小函数，不引框架）**
  - `Modal`（焦点陷阱、Esc 关闭、关闭后焦点回到触发元素）、`ConfirmDialog`（普通 / 危险两种，危险操作可要求输入名称，参照 open-webui）
  - `Toast`（success / error / undo 三类，可堆叠，`aria-live="polite"`）
  - `Skeleton`（列表行、卡片两种），`EmptyState`（图标 + 一句话 + 主按钮），`ErrorState`（原因 + 重试）
  - `InlineEdit`、`Tabs`、`SegmentedControl`、`Switch`、`Combobox`（可搜索下拉，键盘导航参照 cherry-studio `useModelListKeyboardNav.ts`）
  - `DataTable`（排序、筛选、多选、批量操作栏）、`DiffView`、`LogView`、`ScheduleEditor`、`CopyField`、`SecretField`（掩码，只写不读）
- **局部刷新**：操作成功后只更新受影响的行/卡片，不再整页 `route()`；保留滚动位置。
- **图标**：继续用内联 SVG（`ic()`），统一 24 网格、1.75 描边；图标选择器和按钮共用同一份图标表；纯图标按钮必须有 `aria-label` 和 tooltip。
- **无障碍**：`:focus-visible` 统一焦点环；所有交互元素可 Tab 到达；弹窗 `role="dialog" aria-modal`；颜色之外再用图标/文字区分状态；最小点击区域 32×32。
- **键盘快捷键**（`?` 弹出说明）：`⌘/Ctrl+K` 命令面板、`N` 新建（当前页语境）、`G T`/`G R`/`G S` 跳转任务/运行/技能、列表内 `J/K` 上下、`Enter` 打开、`Esc` 关闭。

## 4. 分阶段迭代

| 阶段 | 范围 | 说明 |
|---|---|---|
| **I1 地基** | 设计 token 补齐；Modal / ConfirmDialog / Toast(撤销) / Skeleton / Empty / Error 组件；替换全部 7 处 `confirm()`；局部刷新替代整页 `route()`；`:focus-visible`、reduced-motion | 先等另一位同事的四项改动合并，避免冲突；后面所有阶段都依赖这层 |
| **I2 核心链路** | 任务列表（搜索/筛选/批量）+ 详情 tab + 就地编辑；调度编辑器（三模式 + 后端 `scheduler.preview` + 时区）；供应商设置补全（检测、恢复默认、排序） | 用户最常用、抱怨最多的地方 |
| **I3 可观测** | 日志视图（分类折叠、过滤搜索、新日志浮钮、复制下载、懒渲染、SSE 续传）；运行列表排序筛选 + 重跑；首页"正在运行/接下来 24h/心跳条"；轮询页面不可见暂停 | |
| **I4 版本与优化** | 技能导入预览卡；版本树 + DiffView + 回滚撤销；优化器审阅/灰度对比；需求补充 diff 卡片 + 批量确认条 | |
| **I5 扩展** | 插件 `config_schema` 自动表单 + 权限分组/撤销；触发器卡片 + Webhook 复制/测试；按钮拖拽排序 + 实时预览；花费看板 tab + 预算条 | |
| **I6 打磨** | 命令面板 + 快捷键说明；通知中心分组；设置左导航 + 危险区 + i18n key 校验；登录/2FA 三步流程；托盘菜单与三态图标 | |

每个阶段结束都要：截图更新到 README、zh-CN/en 两种语言走查一遍、跑 `go test ./...` 和前端冒烟（Playwright 截图对比 1440 / 1100 / 800 三个宽度）。

## 5. 不变的底线

- Key 只写不读：前端只拿掩码，API 不回传明文；日志里不出现 Key。
- 所有渲染走 `esc()`，diff / 日志 / Markdown 展示也一样，不用 `innerHTML` 拼接未转义的数据。
- SQL 一律参数化；新增的预览、搜索接口都要有输入长度限制。
- 回滚只挪指针、优化器永不自动转正，这些规则在 UI 文案上写清楚。
