package webui

import (
	"fmt"
	"regexp"
	"strings"
)

// Stable English originals for dependency-check and evaluation preflight text.
// The page translates these (and historical Chinese snapshots) at display time.

const (
	DepNoteScope                   = "The allowed-tools list is pre-authorization; a missing rule does not mean the tool is denied. No commands, scripts, package imports, or network calls were executed, and service reachability and credential validity were not verified."
	DepNotePython                  = "The Python environment is used only to inspect metadata and is not activated automatically; a real task must use that environment explicitly."
	DepNoteDraftPolicy             = "This task draft is checked against the target pre-authorization rules; this inspection does not save or change execution policy."
	DepNoteTaskPolicy              = "Tool pre-authorization comes from this task's saved policy (preferred) or from the version configuration."
	DepNoteEvalTools               = "Evaluation allows only Read/Glob/Grep, and all MCP connectors are disabled."
	DepEstimateMethod              = "Rough estimate from SKILL.md body only: about 4 characters/token for ASCII and 1 character/token for non-ASCII. This is not the model's actual token usage and excludes the task prompt, wrapping, and tool results."
	DepAIUnverifiedMissingPrefix   = "The AI inferred this dependency may be missing; it has not been verified. "
	DepAIUnverifiedAvailablePrefix = "The AI only analyzed static materials and did not verify that this is available in the environment. "
	DepTitleSkillRef               = "Skill reference"
	DepTitleSandbox                = "Command sandbox"
	DepEvidenceToolExecSandbox     = "A command run allows this tool inside the system sandbox: commands write only in the working folder, and protection rules still apply"
	DepEvidenceToolExecNoSandbox   = "A command run allows this tool; this platform has no system sandbox, so only permission rules and the after-run configuration check apply"
	DepTitleTaskPolicy             = "Task policy"
	DepTitleWorkspace              = "Workspace"
	DepTitleTaskWorkspace          = "Task workspace"
	DepExecPermSuffix              = " execution permission"
	DepReasonBadBindings           = "The skill bindings are invalid"
	DepSuggestFixBindings          = "Fix the primary skill, auxiliary skills, and version settings"
	DepReasonSkillUnavailable      = "The referenced skill or version is unavailable"
	DepSuggestReselectSkill        = "Choose an imported skill and a valid version again"
	DepReasonVersionUnreadable     = "The skill version contents could not be read"
	DepSuggestReimport             = "Check or re-import the skill"
	DepReasonNoSkillMD             = "The skill version has no SKILL.md"
	DepSuggestReimportDir          = "Re-import the complete skill directory"
	DepReasonSkillFound            = "The skill and its pinned version were found; this does not mean run dependencies are satisfied"
	DepSuggestCheckStatic          = "Review the static dependency findings below"
	DepReasonSourceUnreadable      = "The skill source file could not be read"
	DepSuggestReimportVersion      = "Re-import this skill version"
	DepReasonTaskMissing           = "The task does not exist or could not be read"
	DepSuggestReselectTask         = "Choose the task again"
	DepReasonNoTaskSpec            = "The task has no parseable version snapshot"
	DepEvidenceNoTaskSpec          = "No task spec was obtained"
	DepSuggestSaveTask             = "Save the task configuration and check again"
	DepReasonBadWorkflow           = "The workflow definition is invalid"
	DepSuggestFixWorkflow          = "Fix the nodes, upstream references, branches, and cycles before running"
	DepReasonSetMissing            = "The skill set does not exist or could not be read"
	DepSuggestReselectSet          = "Choose or save the skill set again"
	DepReasonWFUnavailable         = "The workflow service is unavailable"
	DepSuggestRestart              = "Restart the app and check again"
	DepReasonWFMissing             = "The workflow does not exist or could not be read"
	DepSuggestReselectWF           = "Choose or save the workflow again"
	DepReasonButtonMissing         = "The button does not exist"
	DepEvidenceButtonMissing       = "This id was not found in the button settings"
	DepSuggestReselectButton       = "Choose the button again"
	DepReasonTruncated             = "Inspection materials or results exceeded size or count limits, so coverage is incomplete"
	DepEvidenceLimits              = "At most 128 KiB and 96 source materials; 128 run requirements; 256 findings"
	DepSuggestNarrow               = "Narrow the skill set or inspect skills one at a time; unread portions must not be treated as having no dependencies"
	DepReasonEvalDisabled          = "This dependency is disabled in the evaluation environment"
	DepSuggestEvalRegularRun       = "Evaluation only checks text output in the current read-only environment. Use a regular task run when this dependency is required."
	DepReasonSandboxNone           = "This platform has no system sandbox; commands are protected only by permission rules and post-run configuration checks"
	DepEvidenceSandboxWindows      = "Native Windows does not support Claude Code's system sandbox"
	DepSuggestSandboxTrust         = "Use command execution only on trusted directories and skills"
	DepReasonSandboxOK             = "Commands will run in the system sandbox"
	DepEvidenceSandboxOK           = "Sandbox components are present"
	DepReasonSandboxBroken         = "bubblewrap is installed but this system won't let it start; command execution runs will be refused"
	DepSuggestSandboxUserns        = "Allow unprivileged user namespaces for bwrap (Ubuntu 24.04+: an AppArmor profile for bwrap; containers: a seccomp profile that permits them), or switch to read-only / editable"
	DepSuggestNone                 = "No action needed"
	DepReasonTaskPolicyUnread      = "The task's saved tool policy could not be verified"
	DepEvidenceNoTaskVersion       = "The task or a valid version was not found"
	DepSuggestResaveTask           = "Save the task and check again"
	DepReasonWorkspaceUnusable     = "The selected workspace cannot be used for this run"
	DepSuggestPickWorkspace        = "Choose a workspace that exists and is allowed"
	DepReasonWorkspaceOK           = "The selected workspace exists and passed the directory boundary check"
	DepEvidenceWorkspaceOK         = "Only the directory was checked; files inside it were not read, and write permission was not verified"
	DepSuggestConfirmMode          = "Confirm read-only or edit mode as the task requires"
	DepReasonTaskWsUncertain       = "The task workspace cannot currently be confirmed as a usable absolute directory"
	DepEvidenceTaskWsUncertain     = "The directory may be created at run time; this inspection does not create or modify directories"
	DepSuggestCheckTaskWs          = "Check the directory saved on the task and its access permissions"
	DepReasonTaskWsOK              = "The task workspace exists"
	DepEvidenceTaskWsOK            = "Directory contents were not read and no write was attempted"
	DepSuggestTaskWsPolicy         = "Task protection policy still applies at run time"
	DepReasonContextOver           = "Skill bodies exceed the 256 KiB per-run limit and will be refused before the run rather than truncated"
	DepSuggestContextOver          = "Trim the bodies, move details into references files that are read on demand, or split the work into separate workflow steps"
	DepReasonContextNear           = "Skill bodies are approaching the per-run size limit"
	DepSuggestContextNear          = "Trim or load supporting material on demand, and leave room for the prompt and tool results"
	DepReasonBadManifest           = "The skill dependency manifest is invalid"
	DepSuggestFixManifest          = "Fix tidebot.json and import a new version"
	DepReasonReqPresent            = "The read-only check found evidence of this dependency; this does not mean an end-to-end run will succeed"
	DepReasonReqMissing            = "The read-only check did not find a required dependency"
	DepReasonReqBlocked            = "The target run permissions do not allow this dependency"
	DepReasonReqUnchecked          = "This dependency could not yet be verified"
	DepOptionalPrefix              = "Optional dependency: "
	DepSuggestConfigureReq         = "Configure the dependency for the target run environment; this inspection will not install, execute, or widen permissions"
	DepReasonShellDenied           = "Local script or command execution needs a shell, but the current target mode denies the shell"
	DepSuggestShellDenied          = "Use an authorized MCP tool that can complete the work, or run in an execution mode that supports this capability; do not widen permissions during the inspection"
	DepReasonSkillDepAttention     = "Skill dependencies need attention"
	DepSuggestSkillDepAttention    = "Choose imported, compatible skills from the dependency manifest; optional dependencies can be skipped"
	DepReasonSkillDepNotInSet      = "The required skill is imported, but it has not been added to this skill set"
	DepEvidenceSkillDepDeclared    = "This dependency is declared in tidebot.json; the inspection only proposes candidates and does not add them automatically"
	DepSuggestSkillDepPreview      = "Preview and adopt the dependency additions, then check again"
)

func DepSummaryCombined(n int, summary string) string {
	return fmt.Sprintf("Combined %d deterministic checks; %s", n, summary)
}

func DepSkillFallbackTitle(skillID string) string {
	if len(skillID) >= 8 {
		return "Skill …" + skillID[len(skillID)-8:]
	}
	return "Skill …" + skillID
}

func DepTitleWithOrigin(title, origin string) string {
	if origin == "" {
		return title
	}
	return title + " (" + origin + ")"
}

func DepExecPermTitle(title, name string) string {
	return title + " · " + name + DepExecPermSuffix
}

func DepSandboxMissingReason(missing []string) string {
	return "Missing " + strings.Join(missing, ", ") + "; command execution runs will be refused"
}

func DepSandboxMissingEvidence(missing []string) string {
	return "tidebot could not find these on this computer: " + strings.Join(missing, ", ")
}

func DepSandboxBrokenEvidence(msg string) string {
	return "bubblewrap said: " + msg
}

func DepSandboxInstallSuggestion(install string) string {
	return "Run " + install + " in a terminal, or switch to read-only / editable"
}

func DepContextOverEvidence(bytes, tokens int) string {
	return fmt.Sprintf("%d bytes; about %d estimated tokens", bytes, tokens)
}

func DepContextNearEvidence(bytes, limit, tokens int) string {
	return fmt.Sprintf("%d / %d bytes; about %d estimated tokens", bytes, limit, tokens)
}

func DepOptionalReason(reason string) string {
	return DepOptionalPrefix + reason
}

func DepVersionUnverifiedEvidence(evidence, version string) string {
	return evidence + "; version requirement " + version + " was not verified"
}

func DepCommandVersionUnchecked(version string) string {
	return "The executable was found on PATH, but no version probe was run, so the required version " + version + " could not be verified"
}

func DepToolModeDenied(mode, deny string) string {
	return "Target run mode " + mode + " denies " + deny + " via --disallowedTools"
}

func DepPythonVersionUnchecked(actual, want string) string {
	return "Distribution metadata is present (version " + actual + "), but version range " + want + " has not been verified"
}

func DepPythonVersionMissing(actual, want string) string {
	return "Distribution metadata version is " + actual + ", which does not satisfy the required version " + want
}

func DepPythonPresent(actual string) string {
	return "Distribution metadata is present in the selected virtual environment (version " + actual + "); the package was not imported and scripts were not verified to run"
}

type depTextPattern struct {
	code  string
	re    *regexp.Regexp
	names []string
}

func depExact(code string, texts ...string) depTextPattern {
	parts := make([]string, 0, len(texts))
	for _, t := range texts {
		if t != "" {
			parts = append(parts, regexp.QuoteMeta(t))
		}
	}
	return depTextPattern{code, regexp.MustCompile(`^(?:` + strings.Join(parts, `|`) + `)$`), nil}
}

func depRE(code, re string, names ...string) depTextPattern {
	return depTextPattern{code, regexp.MustCompile(`^(?:` + re + `)$`), names}
}

// depTextPatterns matches stored English originals and historical Chinese text.
// More specific (prefix / parameterized) entries come first.
var depTextPatterns = []depTextPattern{
	depRE("dep_summary_combined", `Combined (\d+) deterministic checks; (.*)`, "n", "detail"),
	depRE("dep_summary_combined", `已结合 (\d+) 项确定性检查；(.*)`, "n", "detail"),
	depRE("dep_ai_unverified_missing", `The AI inferred this dependency may be missing; it has not been verified\. (.*)`, "detail"),
	depRE("dep_ai_unverified_missing", `AI 推断此依赖可能缺失，尚未实际验证。(.*)`, "detail"),
	depRE("dep_ai_unverified_available", `The AI only analyzed static materials and did not verify that this is available in the environment\. (.*)`, "detail"),
	depRE("dep_ai_unverified_available", `AI 仅分析静态材料，未验证环境可用性。(.*)`, "detail"),
	depRE("dep_optional", `Optional dependency: (.*)`, "detail"),
	depRE("dep_optional", `可选依赖：(.*)`, "detail"),
	depRE("dep_version_unverified", `(.*); version requirement (.+) was not verified`, "detail", "version"),
	depRE("dep_version_unverified", `(.*)；未验证版本要求 (.+)`, "detail", "version"),
	depRE("dep_sandbox_missing", `Missing (.+); command execution runs will be refused`, "missing"),
	depRE("dep_sandbox_missing", `缺少 (.+)，可执行命令的运行会被拒绝`, "missing"),
	depRE("dep_sandbox_missing_ev", `tidebot could not find these on this computer: (.+)`, "missing"),
	depRE("dep_sandbox_missing_ev", `tidebot 在这台电脑上找不到：(.+)`, "missing"),
	depRE("dep_sandbox_install", `Run (.+) in a terminal, or switch to read-only / editable`, "install"),
	depRE("dep_sandbox_broken_ev", `bubblewrap said: (.+)`, "error"),
	depRE("dep_sandbox_install", `在终端运行 (.+)，或改用只读 / 可修改`, "install"),
	depRE("dep_context_over_ev", `(\d+) bytes; about (\d+) estimated tokens`, "bytes", "tokens"),
	depRE("dep_context_over_ev", `(\d+) bytes；约 (\d+) tokens（估算）`, "bytes", "tokens"),
	depRE("dep_context_near_ev", `(\d+) / (\d+) bytes; about (\d+) estimated tokens`, "bytes", "limit", "tokens"),
	depRE("dep_context_near_ev", `(\d+) / (\d+) bytes；约 (\d+) tokens（估算）`, "bytes", "limit", "tokens"),
	depRE("dep_cmd_ver_unchecked", `The executable was found on PATH, but no version probe was run, so the required version (.+) could not be verified`, "version"),
	depRE("dep_cmd_ver_unchecked", `PATH 中找到可执行文件，但未执行版本探测，无法验证要求的版本 (.+)`, "version"),
	depRE("dep_tool_mode_denied", `Target run mode (.+) denies (.+) via --disallowedTools`, "mode", "deny"),
	depRE("dep_tool_mode_denied", `目标运行模式 (.+) 通过 --disallowedTools 禁止 (.+)`, "mode", "deny"),
	depRE("dep_py_ver_unchecked", `Distribution metadata is present \(version (.+)\), but version range (.+) has not been verified`, "actual", "want"),
	depRE("dep_py_ver_unchecked", `存在发行包元数据（版本 (.+)），但尚未验证版本范围 (.+)`, "actual", "want"),
	depRE("dep_py_ver_missing", `Distribution metadata version is (.+), which does not satisfy the required version (.+)`, "actual", "want"),
	depRE("dep_py_ver_missing", `发行包元数据版本为 (.+)，不符合指定版本 (.+)`, "actual", "want"),
	depRE("dep_py_present", `Distribution metadata is present in the selected virtual environment \(version (.+)\); the package was not imported and scripts were not verified to run`, "actual"),
	depRE("dep_py_present", `所选虚拟环境中存在发行包元数据（版本 (.+)）；未导入包或验证脚本可运行`, "actual"),
}

var depExactPatterns = func() []depTextPattern {
	type pair struct{ code, en, zh string }
	pairs := []pair{
		{"dep_note_scope", DepNoteScope, "允许工具列表表示预授权；没有匹配规则不等于禁用。未执行命令、脚本、导入包或联网，未验证服务连通性和凭据有效性。"},
		{"dep_note_python", DepNotePython, "Python 环境仅用于元数据核对，不会自动激活；实际任务需要显式使用该环境。"},
		{"dep_note_draft_policy", DepNoteDraftPolicy, "任务草稿核对目标预授权规则；此检测不会保存或更改执行策略。"},
		{"dep_note_task_policy", DepNoteTaskPolicy, "工具预授权来自此任务已保存的策略（优先）或版本配置。"},
		{"dep_note_eval_tools", DepNoteEvalTools, "评测仅允许 Read/Glob/Grep，所有 MCP 关闭。"},
		{"dep_estimate_method", DepEstimateMethod, "仅 SKILL.md 正文粗估：ASCII 约 4 字符/token，非 ASCII 约 1 字符/token；不是模型实际 token 用量，不含任务提示词、包装和工具结果"},
		{"dep_reason_bad_bindings", DepReasonBadBindings, "技能绑定无效"},
		{"dep_suggest_fix_bindings", DepSuggestFixBindings, "修正主技能、辅助技能及版本设置"},
		{"dep_reason_skill_unavailable", DepReasonSkillUnavailable, "引用的技能或版本不可用"},
		{"dep_suggest_reselect_skill", DepSuggestReselectSkill, "重新选择已导入的技能和有效版本"},
		{"dep_reason_version_unreadable", DepReasonVersionUnreadable, "无法读取技能版本内容"},
		{"dep_suggest_reimport", DepSuggestReimport, "检查或重新导入技能"},
		{"dep_reason_no_skillmd", DepReasonNoSkillMD, "技能版本缺少 SKILL.md"},
		{"dep_suggest_reimport_dir", DepSuggestReimportDir, "重新导入完整技能目录"},
		{"dep_reason_skill_found", DepReasonSkillFound, "已找到技能及固定版本；不代表运行依赖已满足"},
		{"dep_suggest_check_static", DepSuggestCheckStatic, "检查下列静态依赖分析"},
		{"dep_reason_source_unreadable", DepReasonSourceUnreadable, "无法读取技能源文件"},
		{"dep_suggest_reimport_version", DepSuggestReimportVersion, "重新导入此技能版本"},
		{"dep_reason_task_missing", DepReasonTaskMissing, "任务不存在或无法读取"},
		{"dep_suggest_reselect_task", DepSuggestReselectTask, "重新选择任务"},
		{"dep_reason_no_task_spec", DepReasonNoTaskSpec, "任务没有可解析的版本快照"},
		{"dep_evidence_no_task_spec", DepEvidenceNoTaskSpec, "未获取到任务 Spec"},
		{"dep_suggest_save_task", DepSuggestSaveTask, "保存任务配置后重新检测"},
		{"dep_reason_bad_workflow", DepReasonBadWorkflow, "工作流定义无效"},
		{"dep_suggest_fix_workflow", DepSuggestFixWorkflow, "修复节点、上游引用、分支和环路后再运行"},
		{"dep_reason_set_missing", DepReasonSetMissing, "技能组合不存在或无法读取"},
		{"dep_suggest_reselect_set", DepSuggestReselectSet, "重新选择或保存技能组合"},
		{"dep_reason_wf_unavailable", DepReasonWFUnavailable, "工作流服务不可用"},
		{"dep_suggest_restart", DepSuggestRestart, "重启软件后重新检测"},
		{"dep_reason_wf_missing", DepReasonWFMissing, "工作流不存在或无法读取"},
		{"dep_suggest_reselect_wf", DepSuggestReselectWF, "重新选择或保存工作流"},
		{"dep_reason_button_missing", DepReasonButtonMissing, "按钮不存在"},
		{"dep_evidence_button_missing", DepEvidenceButtonMissing, "按钮设置中未找到此 ID"},
		{"dep_suggest_reselect_button", DepSuggestReselectButton, "重新选择按钮"},
		{"dep_reason_truncated", DepReasonTruncated, "检测材料或结果超过大小、数量限制，分析范围不完整"},
		{"dep_evidence_limits", DepEvidenceLimits, "最多 128 KiB、96 份源材料；128 项运行依赖；256 项检查结果"},
		{"dep_suggest_narrow", DepSuggestNarrow, "缩小技能组合或逐个技能检测；未读取部分不能视作没有依赖"},
		{"dep_reason_eval_disabled", DepReasonEvalDisabled, "此依赖在评测环境中被禁用"},
		{"dep_suggest_eval_regular_run", DepSuggestEvalRegularRun, "评测只验证当前只读环境下的文本输出；需要该依赖时请使用常规任务运行"},
		{"dep_reason_sandbox_none", DepReasonSandboxNone, "这个平台没有系统沙箱，命令只受权限规则和运行后的配置检查保护"},
		{"dep_evidence_sandbox_windows", DepEvidenceSandboxWindows, "原生 Windows 不支持 Claude Code 的系统沙箱"},
		{"dep_suggest_sandbox_trust", DepSuggestSandboxTrust, "只在信任的目录和技能上使用可执行命令"},
		{"dep_reason_sandbox_ok", DepReasonSandboxOK, "命令会在系统沙箱里运行"},
		{"dep_evidence_sandbox_ok", DepEvidenceSandboxOK, "沙箱组件齐全"},
		{"dep_reason_sandbox_broken", DepReasonSandboxBroken, "已安装 bubblewrap，但系统不允许它启动，可执行命令的运行会被拒绝"},
		{"dep_suggest_sandbox_userns", DepSuggestSandboxUserns, "允许 bwrap 使用非特权用户命名空间（Ubuntu 24.04 及以上：为 bwrap 添加 AppArmor 配置；容器：使用允许它的 seccomp 配置），或改用只读 / 可修改"},
		{"dep_suggest_none", DepSuggestNone, "无需处理"},
		{"dep_reason_task_policy_unread", DepReasonTaskPolicyUnread, "无法核对该任务已保存的工具策略"},
		{"dep_evidence_no_task_version", DepEvidenceNoTaskVersion, "未找到任务或有效版本"},
		{"dep_suggest_resave_task", DepSuggestResaveTask, "保存任务后重新检测"},
		{"dep_reason_workspace_unusable", DepReasonWorkspaceUnusable, "所选工作目录无法用于此运行"},
		{"dep_suggest_pick_workspace", DepSuggestPickWorkspace, "选择一个存在且允许访问的工作目录"},
		{"dep_reason_workspace_ok", DepReasonWorkspaceOK, "所选工作目录存在且通过目录边界检查"},
		{"dep_evidence_workspace_ok", DepEvidenceWorkspaceOK, "仅校验目录，不读取其中的文件；未验证写权限"},
		{"dep_suggest_confirm_mode", DepSuggestConfirmMode, "按任务需要确认只读或修改模式"},
		{"dep_reason_task_ws_uncertain", DepReasonTaskWsUncertain, "任务工作目录目前无法确认为可用绝对目录"},
		{"dep_evidence_task_ws_uncertain", DepEvidenceTaskWsUncertain, "运行时可能创建目录；检查不会创建或修改目录"},
		{"dep_suggest_check_task_ws", DepSuggestCheckTaskWs, "检查任务保存的目录与访问权限"},
		{"dep_reason_task_ws_ok", DepReasonTaskWsOK, "任务工作目录存在"},
		{"dep_evidence_task_ws_ok", DepEvidenceTaskWsOK, "未读取目录内容或尝试写入"},
		{"dep_suggest_task_ws_policy", DepSuggestTaskWsPolicy, "运行时仍应用任务保护策略"},
		{"dep_reason_context_over", DepReasonContextOver, "技能正文超过单次运行的 256 KiB 上限，运行前将拒绝而不会截断"},
		{"dep_suggest_context_over", DepSuggestContextOver, "精简正文，将详细资料放到按需读取的 references 文件，或拆为独立工作流步骤"},
		{"dep_reason_context_near", DepReasonContextNear, "技能正文接近单次运行的大小上限"},
		{"dep_suggest_context_near", DepSuggestContextNear, "精简或按需读取辅助资料，为提示词和工具结果保留空间"},
		{"dep_reason_bad_manifest", DepReasonBadManifest, "技能依赖清单无效"},
		{"dep_suggest_fix_manifest", DepSuggestFixManifest, "修复 tidebot.json 后导入新版本"},
		{"dep_reason_req_present", DepReasonReqPresent, "只读检查已找到此依赖的证据，不代表端到端运行成功"},
		{"dep_reason_req_missing", DepReasonReqMissing, "只读检查未找到必需依赖"},
		{"dep_reason_req_blocked", DepReasonReqBlocked, "当前目标运行权限不允许使用此依赖"},
		{"dep_reason_req_unchecked", DepReasonReqUnchecked, "尚不能验证此依赖是否可用"},
		{"dep_suggest_configure_req", DepSuggestConfigureReq, "根据目标运行环境配置依赖；检测不会安装、执行或扩大权限"},
		{"dep_reason_shell_denied", DepReasonShellDenied, "本地脚本或命令执行需要 shell，但当前目标模式禁止 shell"},
		{"dep_suggest_shell_denied", DepSuggestShellDenied, "改用已授权且能完成任务的 MCP 工具，或在支持此能力的执行方式中运行；不要在检测时放开权限"},
		{"dep_reason_skill_dep_attention", DepReasonSkillDepAttention, "技能依赖需要处理"},
		{"dep_suggest_skill_dep_attention", DepSuggestSkillDepAttention, "按依赖清单选择已导入且兼容的技能；可选依赖可以跳过"},
		{"dep_reason_skill_dep_not_in_set", DepReasonSkillDepNotInSet, "依赖技能已导入，但尚未加入本次技能组合"},
		{"dep_evidence_skill_dep_declared", DepEvidenceSkillDepDeclared, "此依赖由 tidebot.json 声明；检测只提出候选，不会自动加入"},
		{"dep_suggest_skill_dep_preview", DepSuggestSkillDepPreview, "预览并采用依赖补全后重新检测"},
		{"dep_ev_cmd_need_name", "A command must be an executable name with no path or arguments", "命令必须为不含路径和参数的可执行文件名称"},
		{"dep_ev_cmd_not_on_path", "The executable was not found on the application process PATH; a real run may use an extended PATH. No shell or version probe was executed, so this is not treated as missing", "应用进程 PATH 中未找到可执行文件；实际运行可能使用扩展 PATH，未执行 shell 或版本探测，不能认定缺失"},
		{"dep_ev_cmd_present", "The executable exists on the application process PATH; it was not run, and its version and run permission were not verified", "应用进程 PATH 中存在可执行文件；未运行、未验证版本和运行权限"},
		{"dep_ev_env_name", "The environment variable name is invalid", "环境变量名无效"},
		{"dep_ev_env_overwritten", "This variable may be overwritten by run isolation or provider configuration; credential values were not read, so the host environment cannot be used to judge the actual child process configuration", "此变量可能由运行隔离或提供商配置重写；未读取凭据值，不能用宿主环境判断实际子进程配置"},
		{"dep_ev_env_mapping", "This variable is part of the provider credential mapping, so the actual child process may inject or remove it; the inspection does not read secrets", "此变量参与提供商凭据映射，实际子进程可能注入或移除；检测不读取密钥"},
		{"dep_ev_env_provider", "The provider's extra environment configuration declares this variable (configured); the value was not saved, returned, or verified", "提供商额外环境配置已声明此变量（configured）；未保存、返回或验证值"},
		{"dep_ev_env_present", "The application process environment declares this variable (configured); the value was not saved, returned, or verified", "应用进程环境中已声明此变量（configured）；未保存、返回或验证值"},
		{"dep_ev_env_missing", "The application process environment does not declare this variable; .env, the secret store, and credential files were not read", "应用进程环境中未声明此变量；未读取 .env、密钥库或凭据文件"},
		{"dep_ev_file_relative", "Only relative file paths inside the skill package are checked", "只检查技能包内部的相对文件路径"},
		{"dep_ev_file_index_unreadable", "The skill version file index could not be read", "无法读取技能版本文件索引"},
		{"dep_ev_file_missing", "This path is not in the skill version file index; no file contents were read", "技能版本的文件索引中不存在此路径；未读取任何文件内容"},
		{"dep_ev_file_present", "This path is in the skill version file index; the file was not read or executed", "技能版本文件索引中存在此路径；未读取或执行文件"},
		{"dep_ev_kind_unchecked", "This kind of dependency has no read-only checker yet", "此类依赖尚无只读检查器"},
		{"dep_ev_tool_mode_unselected", "The target run mode has not been selected, so tool permissions cannot be determined", "尚未选择目标运行模式，不能确定工具权限"},
		{"dep_ev_tool_preauthorized", "The task policy pre-authorizes this tool rule; it is still constrained by the workspace, protection rules, and the actual invocation arguments", "任务策略已预授权此工具规则；仍受工作目录、保护规则和实际调用参数约束"},
		{"dep_ev_tool_readonly", "The target read-only mode does not disable this built-in read tool; file scope is still constrained by the run environment", "目标只读模式未禁用此内置读取工具；文件范围仍受运行环境约束"},
		{"dep_ev_tool_exec_sandbox", DepEvidenceToolExecSandbox, "可执行命令模式在系统沙箱中放开此工具：命令只能写入工作目录，仍受保护规则约束"},
		{"dep_ev_tool_exec_nosandbox", DepEvidenceToolExecNoSandbox, "可执行命令模式放开此工具；此平台没有系统沙箱，只受权限规则和运行后的配置检查保护"},
		{"dep_ev_tool_unproven", "The target run does not explicitly deny this tool, but there is no rule that proves this invocation is authorized", "目标运行未显式禁用此工具，但没有可证明此调用已获授权的规则"},
		{"dep_ev_mcp_svc", "The plugin configuration service is unavailable", "插件配置服务不可用"},
		{"dep_ev_mcp_meta", "Plugin authorization metadata could not be read", "无法读取插件授权元数据"},
		{"dep_ev_mcp_missing", "The corresponding MCP was not found in the current version of the installed plugin; credentials were not read and the service was not contacted", "已安装插件的当前版本中未找到对应 MCP；未读取凭据或连接服务"},
		{"dep_ev_mcp_ambiguous", "Multiple plugins provide an MCP with this name; specify it as plugin-id__connector-id", "多个插件提供同名 MCP，请用 plugin-id__connector-id 指定"},
		{"dep_ev_mcp_disabled", "The plugin that provides this MCP is not currently enabled", "MCP 所属插件当前未启用"},
		{"dep_ev_mcp_unauthorized", "The plugin that provides this MCP still has unauthorized or revoked permissions", "MCP 所属插件仍有未获授权或已撤销的权限"},
		{"dep_ev_mcp_no_entry", "The MCP is authorized, but this process has no usable built-in plugin entry", "MCP 已授权，但此进程没有可用的内置插件入口"},
		{"dep_ev_mcp_entry_missing", "The MCP is authorized, but its plugin entry file is missing or is not a regular file; the entry was not executed", "MCP 已授权，但其插件入口文件不存在或不是普通文件；未执行入口"},
		{"dep_ev_mcp_present", "The plugin is enabled and every permission declared by the current version is authorized; secret configuration, process startup, and service reachability were not verified", "插件已启用且当前版本所声明权限均已授权；未验证密钥配置、进程启动或服务连通性"},
		{"dep_ev_py_no_venv", "No Python virtual environment was selected; the default Python will not be used to import the package and guess whether it is installed", "未选择 Python 虚拟环境；不会用默认 Python 导入包猜测是否安装"},
		{"dep_ev_py_unreadable", "The selected Python environment directory is not readable", "所选 Python 环境目录不可读取"},
		{"dep_ev_py_not_dir", "The selected Python environment is not a directory", "所选 Python 环境不是目录"},
		{"dep_ev_py_no_cfg", "The directory has no regular pyvenv.cfg, so it cannot yet be confirmed as a virtual environment", "目录缺少常规 pyvenv.cfg，尚不能确认为虚拟环境"},
		{"dep_ev_py_list", "Virtual environment package metadata could not be listed", "无法列出虚拟环境包元数据"},
		{"dep_ev_py_marker", "Virtual environment marker metadata could not be read", "无法读取虚拟环境标记元数据"},
		{"dep_ev_py_too_many", "The package directory has more than 4096 entries and was not fully inspected", "包目录超过 4096 项，未完整检查"},
		{"dep_ev_py_ambiguous", "Metadata for multiple distributions with this name was found, so the actual import version cannot be determined", "发现多个同名发行包的元数据，无法确定实际导入版本"},
		{"dep_ev_py_partial", "Virtual environment package metadata could not be fully read; this is not treated as proof that the dependency is not installed", "虚拟环境包元数据未能完整读取；不能据此认定依赖未安装"},
		{"dep_ev_py_missing", "The distribution was not found in the selected virtual environment's dist-info metadata; Python was not executed and the package was not imported", "所选虚拟环境的 dist-info 元数据中未找到发行包；未执行 Python 或导入包"},
	}
	out := make([]depTextPattern, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, depExact(p.code, p.en, p.zh))
	}
	return out
}()

var depNoteFragments = func() []depTextPattern {
	codes := []string{"dep_note_scope", "dep_note_python", "dep_note_draft_policy", "dep_note_task_policy", "dep_note_eval_tools"}
	var out []depTextPattern
	for _, p := range depExactPatterns {
		for _, c := range codes {
			if p.code == c {
				out = append(out, p)
			}
		}
	}
	return out
}()

var depTitlePatterns = []depTextPattern{
	depRE("dep_title_skill_id_origin", `Skill …([0-9a-fA-F]{8}) \((.+)\)`, "id", "origin"),
	depRE("dep_title_skill_id_origin", `技能 …([0-9a-fA-F]{8})（(.+)）`, "id", "origin"),
	depRE("dep_title_skill_id", `Skill …([0-9a-fA-F]{8})`, "id"),
	depRE("dep_title_skill_id", `技能 …([0-9a-fA-F]{8})`, "id"),
	depRE("dep_title_exec_perm", `(.+) · (.+) execution permission`, "name", "dep"),
	depRE("dep_title_exec_perm", `(.+) · (.+) 执行权限`, "name", "dep"),
	depRE("dep_title_skill_ref_origin", `Skill reference \((.+)\)`, "origin"),
	depRE("dep_title_skill_ref_origin", `技能引用（(.+)）`, "origin"),
	depExact("dep_title_skill_ref", DepTitleSkillRef, "技能引用"),
	depExact("dep_title_sandbox", DepTitleSandbox, "命令沙箱"),
	depExact("dep_title_task_policy", DepTitleTaskPolicy, "任务策略"),
	depExact("dep_title_workspace", DepTitleWorkspace, "工作目录"),
	depExact("dep_title_task_workspace", DepTitleTaskWorkspace, "任务工作目录"),
	depRE("dep_title_origin", `(.+) \((.+)\)`, "name", "origin"),
	depRE("dep_title_origin", `(.+)（(.+)）`, "name", "origin"),
}

func matchDepPattern(p depTextPattern, msg string) *errInfo {
	m := p.re.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	info := &errInfo{Code: p.code}
	for i, name := range p.names {
		if i+1 < len(m) && m[i+1] != "" {
			if info.Params == nil {
				info.Params = map[string]string{}
			}
			info.Params[name] = m[i+1]
		}
	}
	if d := info.Params["detail"]; d != "" {
		info.Cause = explainDepText(d)
	}
	return info
}

// explainDepText maps a known English or historical Chinese phrase to a code.
func explainDepText(msg string) *errInfo {
	msg = strings.TrimSpace(msg)
	if msg == "" || len(msg) > 4096 {
		return nil
	}
	for _, p := range depTextPatterns {
		if info := matchDepPattern(p, msg); info != nil {
			return info
		}
	}
	for _, p := range depExactPatterns {
		if info := matchDepPattern(p, msg); info != nil {
			return info
		}
	}
	return nil
}

func explainDepTitle(msg string) *errInfo {
	msg = strings.TrimSpace(msg)
	if msg == "" || len(msg) > 4096 {
		return nil
	}
	for _, p := range depTitlePatterns {
		if info := matchDepPattern(p, msg); info != nil {
			return info
		}
	}
	return nil
}

func explainDepNotes(s string) []*errInfo {
	rest := strings.TrimSpace(s)
	if rest == "" {
		return nil
	}
	var out []*errInfo
	for rest != "" {
		var best *errInfo
		bestLen := 0
		for _, p := range depNoteFragments {
			// Each exact pattern is anchored; try prefixes by testing every known text
			// against the start of the remainder via the pattern's alternatives.
			for _, alt := range noteAlternatives(p) {
				if strings.HasPrefix(rest, alt) && len(alt) > bestLen {
					best = &errInfo{Code: p.code}
					bestLen = len(alt)
				}
			}
		}
		if best == nil || bestLen == 0 {
			return nil
		}
		out = append(out, best)
		rest = strings.TrimSpace(rest[bestLen:])
	}
	return out
}

func noteAlternatives(p depTextPattern) []string {
	// Recover the exact texts from the alternation built by depExact.
	src := p.re.String()
	src = strings.TrimPrefix(src, "^(?:")
	src = strings.TrimSuffix(src, ")$")
	var alts []string
	for _, part := range strings.Split(src, "|") {
		unquoted, err := unquoteMeta(part)
		if err == nil && unquoted != "" {
			alts = append(alts, unquoted)
		}
	}
	return alts
}

func unquoteMeta(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 >= len(s) {
				return "", fmt.Errorf("trailing escape")
			}
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func annotateFinding(f DependencyFinding) DependencyFinding {
	f.DependencyInfo = explainDepTitle(f.Dependency)
	f.ReasonInfo = explainDepText(f.Reason)
	f.EvidenceInfo = explainDepText(f.Evidence)
	f.SuggestionInfo = explainDepText(f.Suggestion)
	return f
}

func annotateFindings(items []DependencyFinding) []DependencyFinding {
	if len(items) == 0 {
		return items
	}
	out := make([]DependencyFinding, len(items))
	for i, f := range items {
		out[i] = annotateFinding(f)
	}
	return out
}

func annotateReport(r *DependencyReport) *DependencyReport {
	if r == nil {
		return nil
	}
	out := *r
	out.SummaryInfo = explainDepText(out.Summary)
	out.Findings = annotateFindings(out.Findings)
	return &out
}

func annotatePreflight(r DependencyPreflightReport) DependencyPreflightReport {
	r.RuntimeProfile.NotesInfo = explainDepNotes(r.RuntimeProfile.Notes)
	if len(r.ContextUsage) > 0 {
		usage := make([]DependencyContextUsage, len(r.ContextUsage))
		for i, u := range r.ContextUsage {
			usage[i] = u
			usage[i].EstimateMethodInfo = explainDepText(u.EstimateMethod)
		}
		r.ContextUsage = usage
	}
	if len(r.Requirements) > 0 {
		reqs := make([]DependencyRequirementResult, len(r.Requirements))
		for i, x := range r.Requirements {
			reqs[i] = x
			reqs[i].EvidenceInfo = explainDepText(x.Evidence)
		}
		r.Requirements = reqs
	}
	r.Findings = annotateFindings(r.Findings)
	return r
}

func annotateDependencyStatus(out DependencyCheckStatus) DependencyCheckStatus {
	out.Report = annotateReport(out.Report)
	out.StaticFindings = annotateFindings(out.StaticFindings)
	if out.Preflight != nil {
		pf := annotatePreflight(*out.Preflight)
		out.Preflight = &pf
	}
	return out
}

// depTextCodes is the set of codes the page can translate, used by catalog tests.
func depTextCodes() map[string]map[string]bool {
	codes := map[string]map[string]bool{}
	add := func(p depTextPattern) {
		names := map[string]bool{}
		for _, n := range p.names {
			names[n] = true
		}
		if existing := codes[p.code]; existing != nil {
			for n := range names {
				existing[n] = true
			}
			return
		}
		codes[p.code] = names
	}
	for _, p := range depTextPatterns {
		add(p)
	}
	for _, p := range depExactPatterns {
		add(p)
	}
	for _, p := range depTitlePatterns {
		add(p)
	}
	return codes
}
