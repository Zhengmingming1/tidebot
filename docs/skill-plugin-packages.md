# 导入包含多个 Skill 的 Claude 插件包

“技能 → 导入技能”可接收含多个标准 `skills/<name>/SKILL.md` 和 `.claude-plugin/plugin.json` 的插件包。ZIP、`.skill`、`.tar.gz`、`.tgz` 与文件夹使用同一处理方式；允许一个外包装目录。

例如：

```text
trunk-merge/
  .claude-plugin/plugin.json
  commands/trunk-merge.md
  skills/query-task-branch/SKILL.md
  skills/query-task-change-file/SKILL.md
  skills/query-task-stage/SKILL.md
  scripts/trunk_merge.py
  scripts/skills/...
```

Tidebot 将它保存为名为 `trunk-merge` 的一个完整技能包。原文件保留相对目录与内容，新增根 `SKILL.md` 汇总命令和辅助技能的说明。预览显示包名、技能数、命令数、默认入口以及全部文件；同名技能需要明确选择创建新版本。包随版本一起导出，重新导入可恢复内容。

运行时使用现有技能入口，例如 `/tidebot:trunk-merge`。包内原有 `/query-task-*` 名称由聚合入口解释为内部操作，不会各自注册为全局命令。要单独查询，可在运行输入里说明所需包内命令或辅助技能。

默认入口优先采用与插件同名的命令；否则采用唯一命令。若有多个命令且没有同名入口，或包里只有辅助技能，根据用户请求选择操作；意图不明确时询问用户，不按文件名顺序擅自执行。

## 资源路径与工作目录

运行副本保留整个插件目录结构。`${CLAUDE_PLUGIN_ROOT}` 指向此技能包根目录；原辅助技能的 `${CLAUDE_SKILL_DIR}` 指向其原 `skills/<name>` 目录。占位符只在运行副本的 Markdown 中展开，已保存版本与导出文件保持原内容。

命令中的插件根相对路径从包根解析；辅助技能的普通相对资源从其原技能目录解析。文档明确写明“插件根目录”的路径仍从包根解析。执行脚本时使用绝对脚本路径，工作目录保持为用户工作区，以保留工作区配置、仓库与输出文件的位置。

## 支持范围

此入口适配标准 `commands/`、`skills/` 和共享文件。manifest 自定义组件路径会明确报错，不会静默漏掉组件。普通单 Skill 仍沿用单技能导入；没有 manifest 的多个 Skill 仍需选择其中一个目录。

hooks、MCP、agents、LSP 和 monitors 仅作为原文件保留，不在此导入方式中启用。原命令/辅助技能 frontmatter 的 `model`、`context`、`allowed-tools` 不会改变 Tidebot 的服务商、会话模式或权限；参数定义作为指令内容保留。

所有命令和辅助技能说明都计入生成的根指令，累计不得超过 256 KiB。原解压文件数、单文件大小和总大小限制继续生效。导入过程不会执行脚本、安装依赖或调用外部接口。

`trunk-merge` 这类包运行前仍需要自身要求的 Python、requests、Git、工作区配置和外部服务权限。某个包内部的缺文件、协议不一致或配置查找约束，需要由包维护者处理；导入与资源恢复成功不代表真实业务调用已经通过。

## 验证

常规测试覆盖 ZIP、tar 与文件夹导入、版本冲突、默认入口、指令预算、资源作用域、单 Skill 兼容与前端转义。可使用未改动的外部附件运行额外验收：

```sh
TIDEBOT_PLUGIN_ACCEPTANCE_ZIP=/absolute/path/trunk-merge.zip \
  go test ./internal/webui -run TestRealTrunkMergePluginAcceptance -v
```

此验收核对原始文件逐字节存储、运行目录资源、入口指令和导出到全新数据库后的文件一致性；不会执行该包的业务脚本或调用研发云。
