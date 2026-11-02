package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// This is an instruction-body limit, not a model context-window claim. Wrappers,
// the task prompt, tool results and model-specific tokenization are additional.
const skillContextMaxBytes = 256 << 10
const contextEstimateMethod = "仅 SKILL.md 正文粗估：ASCII 约 4 字符/token，非 ASCII 约 1 字符/token；不是模型实际 token 用量，不含任务提示词、包装和工具结果"

func estimateSkillTokens(data []byte) int {
	ascii, other := 0, 0
	for len(data) > 0 {
		// Most instruction text is ASCII. Avoid decoding every ASCII byte;
		// DecodeRune still handles multibyte and malformed UTF-8 consistently.
		if data[0] < utf8.RuneSelf {
			ascii++
			data = data[1:]
			continue
		}
		_, n := utf8.DecodeRune(data)
		other++
		data = data[n:]
	}
	return (ascii+3)/4 + other
}

func (a *App) dependencyPreflight(ctx context.Context, req webui.DependencyCheckRequest) (webui.DependencyPreflightReport, error) {
	snapshot, err := a.collectDependencySnapshot(ctx, req)
	if err != nil {
		return webui.DependencyPreflightReport{}, err
	}
	return snapshot.preflight(), nil
}

func (s dependencySnapshot) preflight() webui.DependencyPreflightReport {
	return webui.DependencyPreflightReport{Truncated: s.Truncated, RuntimeProfile: s.RuntimeProfile, ContextUsage: s.ContextUsage, Requirements: s.Requirements, Findings: s.Findings}
}

func initialDependencyProfile(req webui.DependencyCheckRequest) webui.DependencyRuntimeProfile {
	p := webui.DependencyRuntimeProfile{Provider: req.Provider, Mode: "unselected", WorkspaceKind: "unselected", Restricted: true, PythonEnvironment: req.PythonEnvironment,
		Notes: "允许工具列表表示预授权；没有匹配规则不等于禁用。未执行命令、脚本、导入包或联网，未验证服务连通性和凭据有效性。"}
	if req.PythonEnvironment != "" {
		p.Notes += "Python 环境仅用于元数据核对，不会自动激活；实际任务需要显式使用该环境。"
	}
	if req.Kind == "workflow" || req.Kind == "workflow_draft" {
		p.Mode, p.WorkspaceKind = "read", "workflow_scratch"
		p.DeniedTools = append([]string{}, readOnlyDeny...)
	} else if req.Kind == "draft" && req.Execution == "task" {
		p.Mode, p.WorkspaceKind = "task", "task_scratch"
		p.Workspace = guard.ExpandHome(req.Workspace)
		if p.Workspace != "" {
			p.WorkspaceKind = "task_selected"
		}
		p.AllowedTools = append([]string{}, req.AllowedTools...)
		p.Notes += "任务草稿核对目标预授权规则；此检测不会保存或更改执行策略。"
	} else if req.Kind == "draft" && len(req.SkillBindings) == 0 && req.Workspace == "" || req.Kind == "button" {
		p.Mode, p.WorkspaceKind = "quick", "scratch"
	} else if req.Kind == "draft" || req.Kind == "button" || req.Workspace != "" || req.Mode != "" {
		p.Mode, p.WorkspaceKind = "read", "scratch"
		p.Workspace = guard.ExpandHome(req.Workspace)
		if p.Workspace != "" {
			p.WorkspaceKind = "selected"
			if req.Mode == "edit" {
				p.Mode = "edit"
			}
		}
		if p.Mode != "edit" {
			p.DeniedTools = append([]string{}, readOnlyDeny...)
		}
	}
	return p
}

func (c *dependencyCollector) draftTaskPolicy(id string) {
	// The form edits content, not the persisted permission policy. Mirror
	// applyGuard's priority without replacing the draft prompt or bindings.
	p := &c.snapshot.RuntimeProfile
	p.AllowedTools = nil
	t, err := c.a.Tasks.Get(c.ctx, id)
	if err != nil || t.Spec == nil {
		c.finding("任务策略", "configuration", "uncertain", "无法核对该任务已保存的工具策略", "未找到任务或有效版本", "保存任务后重新检测")
		return
	}
	if c.a.Policy != nil {
		for _, rule := range strings.Split(c.a.Policy.Get(c.ctx, id).AllowedTools, ",") {
			if rule = strings.TrimSpace(rule); rule != "" {
				p.AllowedTools = append(p.AllowedTools, rule)
			}
		}
	}
	if len(p.AllowedTools) == 0 {
		p.AllowedTools = append([]string{}, t.Spec.AllowedTools...)
	}
	if p.Provider == "" {
		p.Provider = t.Spec.Provider
	}
	p.Notes += "工具预授权来自此任务已保存的策略（优先）或版本配置。"
}

func (c *dependencyCollector) checkWorkspace() {
	p := &c.snapshot.RuntimeProfile
	if p.WorkspaceKind == "selected" {
		home, _ := os.UserHomeDir()
		_, problem := guard.CheckRunDir(p.Workspace, home, c.a.Cfg.HomeDir)
		if problem != "" {
			c.finding("工作目录", "workspace", "blocked", "所选工作目录无法用于此运行", problem, "选择一个存在且允许访问的工作目录")
		} else {
			c.finding("工作目录", "workspace", "available", "所选工作目录存在且通过目录边界检查", "仅校验目录，不读取其中的文件；未验证写权限", "按任务需要确认只读或修改模式")
		}
	} else if p.WorkspaceKind == "task_selected" {
		// Scheduled tasks have a different workspace contract from the run dialog.
		// Do not claim the dialog guard is enforced on this execution path.
		fi, err := os.Stat(p.Workspace)
		if err != nil || !fi.IsDir() || !filepath.IsAbs(p.Workspace) {
			c.finding("任务工作目录", "workspace", "uncertain", "任务工作目录目前无法确认为可用绝对目录", "运行时可能创建目录；检查不会创建或修改目录", "检查任务保存的目录与访问权限")
		} else {
			c.finding("任务工作目录", "workspace", "available", "任务工作目录存在", "未读取目录内容或尝试写入", "运行时仍应用任务保护策略")
		}
	}
}

func (c *dependencyCollector) measureContext(bindings []tasksvc.SkillBinding, origin string) {
	if len(bindings) == 0 {
		return
	}
	usage := webui.DependencyContextUsage{Origin: origin, Members: []webui.DependencyContextMember{}, LimitBytes: skillContextMaxBytes, EstimateOnly: true, EstimateMethod: contextEstimateMethod}
	for i, b := range bindings {
		if i >= 32 {
			break
		}
		one := b
		one.Role = "primary"
		resolved, err := c.a.Skills.ResolveBindings(c.ctx, []tasksvc.SkillBinding{one})
		if err != nil {
			continue
		}
		frozen := resolved[0]
		m, ok := c.contextMembers[frozen.VersionID]
		if !ok {
			var data []byte
			err = c.a.DB.QueryRowContext(c.ctx, `SELECT b.data FROM version_files f JOIN blobs b ON b.sha256=f.sha256 WHERE f.version_id=? AND f.path='SKILL.md'`, frozen.VersionID).Scan(&data)
			if err != nil {
				continue
			}
			m = webui.DependencyContextMember{SkillID: frozen.SkillID, VersionID: frozen.VersionID, Name: dependencyClip(dependencyRedact(frozen.SkillName), 256), Bytes: len(data), EstimatedTokens: estimateSkillTokens(data)}
			c.contextMembers[frozen.VersionID] = m
		}
		m.Role = b.Role
		if m.Role == "" {
			if i == 0 {
				m.Role = "primary"
			} else {
				m.Role = "auxiliary"
			}
		}
		usage.Members = append(usage.Members, m)
		usage.TotalBytes += m.Bytes
		usage.EstimatedTokens += m.EstimatedTokens
	}
	usage.Exceeded = usage.TotalBytes > skillContextMaxBytes
	c.snapshot.ContextUsage = append(c.snapshot.ContextUsage, usage)
	if usage.Exceeded {
		c.finding(origin, "context", "blocked", "技能正文超过单次运行的 256 KiB 上限，运行前将拒绝而不会截断", fmt.Sprintf("%d bytes；约 %d tokens（估算）", usage.TotalBytes, usage.EstimatedTokens), "精简正文，将详细资料放到按需读取的 references 文件，或拆为独立工作流步骤")
	} else if usage.TotalBytes > skillContextMaxBytes*3/4 {
		c.finding(origin, "context", "uncertain", "技能正文接近单次运行的大小上限", fmt.Sprintf("%d / %d bytes；约 %d tokens（估算）", usage.TotalBytes, skillContextMaxBytes, usage.EstimatedTokens), "精简或按需读取辅助资料，为提示词和工具结果保留空间")
	}
}

func (c *dependencyCollector) checkManifest(b tasksvc.SkillBinding) {
	manifest, err := c.a.Skills.ReadSkillManifest(c.ctx, b.VersionID)
	if err != nil {
		c.finding(dependencySkillTitle(b, ""), "configuration", "missing", "技能依赖清单无效", err.Error(), "修复 tidebot.json 后导入新版本")
		return
	}
	for _, req := range manifest.Environment {
		if len(c.snapshot.Requirements) >= 128 {
			c.snapshot.Truncated = true
			break
		}
		status, evidence := c.checkRequirement(req, b.VersionID)
		if req.Version != "" && req.Kind != "python_package" && status == "present" {
			status, evidence = "unchecked", evidence+"；未验证版本要求 "+req.Version
		}
		evidence = dependencyClip(dependencyRedact(evidence), 2048)
		c.snapshot.Requirements = append(c.snapshot.Requirements, webui.DependencyRequirementResult{SkillID: b.SkillID, VersionID: b.VersionID, Requirement: req, Status: status, Evidence: evidence})
		level := map[string]string{"present": "available", "missing": "missing", "blocked": "blocked", "unchecked": "uncertain"}[status]
		reason := map[string]string{"present": "只读检查已找到此依赖的证据，不代表端到端运行成功", "missing": "只读检查未找到必需依赖", "blocked": "当前目标运行权限不允许使用此依赖", "unchecked": "尚不能验证此依赖是否可用"}[status]
		if req.Optional {
			reason = "可选依赖：" + reason
			if level == "missing" || level == "blocked" {
				level = "uncertain"
			}
		}
		c.finding(dependencySkillTitle(b, "")+" · "+req.Name, req.Kind, level, reason, evidence, "根据目标运行环境配置依赖；检测不会安装、执行或扩大权限")
		// Executable presence does not imply the model can invoke it. Keep these
		// findings separate so an installed Python is never mistaken for executable
		// Python in a workflow whose shell tools are denied.
		if req.Kind == "command" || req.Kind == "python_package" {
			toolStatus, toolEvidence := c.checkTool("Bash")
			if toolStatus == "blocked" {
				level := "blocked"
				if req.Optional {
					level = "uncertain"
				}
				c.finding(dependencySkillTitle(b, "")+" · "+req.Name+" 执行权限", "tool", level, "本地脚本或命令执行需要 shell，但当前目标模式禁止 shell", toolEvidence, "改用已授权且能完成任务的 MCP 工具，或在支持此能力的执行方式中运行；不要在检测时放开权限")
			}
		}
	}
}

func (c *dependencyCollector) checkSkillDependencies(bindings []tasksvc.SkillBinding, origin string) {
	preview, err := c.a.Skills.PreviewDependencies(c.ctx, bindings)
	if err != nil {
		return
	} // bindings() reports unavailable exact references itself.
	for _, issue := range preview.Issues {
		level := "missing"
		if issue.Optional {
			level = "uncertain"
		}
		c.finding(issue.SkillName+"（"+origin+"）", "skill", level, "技能依赖需要处理", issue.Message, "按依赖清单选择已导入且兼容的技能；可选依赖可以跳过")
	}
	for _, addition := range preview.Additions {
		c.finding(dependencySkillTitle(addition, origin), "skill", "missing", "依赖技能已导入，但尚未加入本次技能组合", "此依赖由 tidebot.json 声明；检测只提出候选，不会自动加入", "预览并采用依赖补全后重新检测")
	}
}

var preflightCommandName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)
var preflightEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (c *dependencyCollector) checkRequirement(req skillstore.RuntimeRequirement, versionID string) (string, string) {
	switch req.Kind {
	case "command":
		if !preflightCommandName.MatchString(req.Name) {
			return "unchecked", "命令必须为不含路径和参数的可执行文件名称"
		}
		_, err := exec.LookPath(req.Name)
		if err != nil {
			return "unchecked", "应用进程 PATH 中未找到可执行文件；实际运行可能使用扩展 PATH，未执行 shell 或版本探测，不能认定缺失"
		}
		if req.Version != "" {
			return "unchecked", "PATH 中找到可执行文件，但未执行版本探测，无法验证要求的版本 " + req.Version
		}
		return "present", "应用进程 PATH 中存在可执行文件；未运行、未验证版本和运行权限"
	case "python_package":
		return inspectPythonDistribution(c.snapshot.RuntimeProfile.PythonEnvironment, req.Name, req.Version)
	case "env":
		if !preflightEnvName.MatchString(req.Name) {
			return "unchecked", "环境变量名无效"
		}
		if strings.HasPrefix(req.Name, "ANTHROPIC_") || strings.HasPrefix(req.Name, "CLAUDE_") || req.Name == "TIDEBOT_IPC" || req.Name == "TIDEBOT_TASK_ID" || req.Name == "TIDEBOT_GUARD_WORKSPACE" {
			return "unchecked", "此变量可能由运行隔离或提供商配置重写；未读取凭据值，不能用宿主环境判断实际子进程配置"
		}
		if prof, err := c.a.ResolveProfile(c.snapshot.RuntimeProfile.Provider); err == nil {
			if req.Name == prof.AuthEnv || req.Name == provider.NormalizeSecretEnv(prof.SecretEnv) {
				return "unchecked", "此变量参与提供商凭据映射，实际子进程可能注入或移除；检测不读取密钥"
			}
			if _, configured := prof.ExtraEnv[req.Name]; configured {
				return "present", "提供商额外环境配置已声明此变量（configured）；未保存、返回或验证值"
			}
		}
		// Only existence is observed. No value is retained, interpolated or emitted.
		if _, ok := os.LookupEnv(req.Name); ok {
			return "present", "应用进程环境中已声明此变量（configured）；未保存、返回或验证值"
		}
		return "missing", "应用进程环境中未声明此变量；未读取 .env、密钥库或凭据文件"
	case "tool":
		return c.checkTool(req.Name)
	case "mcp":
		return c.checkMCP(req.Name)
	case "file":
		name := filepath.ToSlash(req.Name)
		if filepath.IsAbs(req.Name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.Contains(name, "\\") {
			return "unchecked", "只检查技能包内部的相对文件路径"
		}
		var count int
		err := c.a.DB.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM version_files WHERE version_id=? AND path=?`, versionID, name).Scan(&count)
		if err != nil {
			return "unchecked", "无法读取技能版本文件索引"
		}
		if count == 0 {
			return "missing", "技能版本的文件索引中不存在此路径；未读取任何文件内容"
		}
		return "present", "技能版本文件索引中存在此路径；未读取或执行文件"
	default:
		return "unchecked", "此类依赖尚无只读检查器"
	}
}

func (c *dependencyCollector) checkTool(name string) (string, string) {
	p := c.snapshot.RuntimeProfile
	base := strings.SplitN(name, "(", 2)[0]
	for _, deny := range p.DeniedTools {
		if strings.EqualFold(base, deny) {
			return "blocked", "目标运行模式 " + p.Mode + " 通过 --disallowedTools 禁止 " + deny
		}
	}
	if p.Mode == "unselected" {
		return "unchecked", "尚未选择目标运行模式，不能确定工具权限"
	}
	for _, allowed := range p.AllowedTools {
		if allowed == base || allowed == name {
			return "present", "任务策略已预授权此工具规则；仍受工作目录、保护规则和实际调用参数约束"
		}
	}
	if p.Mode == "read" && (base == "Read" || base == "Glob" || base == "Grep") {
		return "present", "目标只读模式未禁用此内置读取工具；文件范围仍受运行环境约束"
	}
	return "unchecked", "目标运行未显式禁用此工具，但没有可证明此调用已获授权的规则"
}

func (c *dependencyCollector) checkMCP(name string) (string, string) {
	if c.a.Plugins == nil {
		return "unchecked", "插件配置服务不可用"
	}
	list, err := c.a.Plugins.List(c.ctx)
	if err != nil {
		return "unchecked", "无法读取插件授权元数据"
	}
	type match struct {
		p         plugin.Info
		connector plugin.ConnectorDecl
	}
	var matches []match
	for _, p := range list {
		if p.Current == nil || p.Current.Manifest == nil {
			continue
		}
		for _, connector := range p.Current.Manifest.Contributes.Connectors {
			if name == p.ID+"__"+connector.ID || name == connector.ID {
				matches = append(matches, match{p: p, connector: connector})
			}
		}
	}
	if len(matches) == 0 {
		return "missing", "已安装插件的当前版本中未找到对应 MCP；未读取凭据或连接服务"
	}
	if len(matches) > 1 {
		return "unchecked", "多个插件提供同名 MCP，请用 plugin-id__connector-id 指定"
	}
	p := matches[0].p
	if !p.Enabled {
		return "blocked", "MCP 所属插件当前未启用"
	}
	if p.Current.Approved == nil || len(plugin.NewPermissions(p.Current.Approved, p.Current.Requested)) > 0 {
		return "blocked", "MCP 所属插件仍有未获授权或已撤销的权限"
	}
	connector := matches[0].connector
	if connector.MCP.Builtin != "" {
		if plugin.BuiltinExe == "" {
			return "missing", "MCP 已授权，但此进程没有可用的内置插件入口"
		}
	} else if info, err := os.Stat(filepath.Join(p.Current.Dir, connector.MCP.Command)); err != nil || !info.Mode().IsRegular() {
		return "missing", "MCP 已授权，但其插件入口文件不存在或不是普通文件；未执行入口"
	}
	return "present", "插件已启用且当前版本所声明权限均已授权；未验证密钥配置、进程启动或服务连通性"
}

func canonicalPythonName(name string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(name))
}

// inspectPythonDistribution never executes a venv binary or imports package code.
// Only regular, bounded .dist-info/METADATA files inside an explicitly selected
// venv are read. An unselected or ambiguous environment remains unchecked.
func inspectPythonDistribution(venv, name, want string) (string, string) {
	if venv == "" {
		return "unchecked", "未选择 Python 虚拟环境；不会用默认 Python 导入包猜测是否安装"
	}
	root, err := filepath.EvalSymlinks(venv)
	if err != nil || !filepath.IsAbs(root) {
		return "unchecked", "所选 Python 环境目录不可读取"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "unchecked", "所选 Python 环境不是目录"
	}
	// An explicit venv must have its metadata marker; never scan a whole arbitrary directory.
	marker, err := os.Lstat(filepath.Join(root, "pyvenv.cfg"))
	if err != nil || !marker.Mode().IsRegular() || marker.Size() > 64<<10 {
		return "unchecked", "目录缺少常规 pyvenv.cfg，尚不能确认为虚拟环境"
	}
	paths := []string{filepath.Join(root, "Lib", "site-packages")}
	libs, err := filepath.Glob(filepath.Join(root, "lib", "python*", "site-packages"))
	if err != nil {
		return "unchecked", "无法列出虚拟环境包元数据"
	}
	paths = append(paths, libs...)
	found := []string{}
	readable := false
	incomplete := false
	// A venv may extend sys.path through site metadata. We never evaluate those
	// paths or run their hooks; absence in the local dist-info index is then not
	// proof of a missing runtime dependency.
	config, err := os.ReadFile(filepath.Join(root, "pyvenv.cfg"))
	if err != nil {
		return "unchecked", "无法读取虚拟环境标记元数据"
	}
	for _, line := range strings.Split(string(config), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "include-system-site-packages") && strings.EqualFold(strings.TrimSpace(value), "true") {
			incomplete = true
		}
	}
	for _, site := range paths {
		real, err := filepath.EvalSymlinks(site)
		if err != nil {
			continue
		}
		if !guard.Within(real, root) {
			incomplete = true
			continue
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			incomplete = true
			continue
		}
		readable = true
		if len(entries) > 4096 {
			return "unchecked", "包目录超过 4096 项，未完整检查"
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".pth") || strings.HasSuffix(entry.Name(), ".egg-info") || entry.Name() == "sitecustomize.py" || entry.Name() == "usercustomize.py" {
				incomplete = true
			}
			if entry.Type()&os.ModeSymlink != 0 && strings.HasSuffix(entry.Name(), ".dist-info") {
				incomplete = true
				continue
			}
			if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dist-info") {
				continue
			}
			metadata := filepath.Join(real, entry.Name(), "METADATA")
			fi, err := os.Lstat(metadata)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() > 256<<10 {
				incomplete = true
				continue
			}
			f, err := os.Open(metadata)
			if err != nil {
				incomplete = true
				continue
			}
			scan := bufio.NewScanner(io.LimitReader(f, 256<<10))
			pkg, version := "", ""
			for scan.Scan() {
				line := scan.Text()
				if line == "" {
					break
				}
				if strings.HasPrefix(line, "Name: ") {
					pkg = strings.TrimSpace(strings.TrimPrefix(line, "Name: "))
				}
				if strings.HasPrefix(line, "Version: ") {
					version = strings.TrimSpace(strings.TrimPrefix(line, "Version: "))
				}
			}
			readErr := scan.Err()
			f.Close()
			if readErr != nil {
				incomplete = true
				continue
			}
			if canonicalPythonName(pkg) == canonicalPythonName(name) {
				found = append(found, version)
			}
		}
	}
	if len(found) > 1 {
		return "unchecked", "发现多个同名发行包的元数据，无法确定实际导入版本"
	}
	if len(found) == 1 {
		actual := found[0]
		if want != "" {
			exact := strings.TrimPrefix(want, "==")
			if !regexp.MustCompile(`^[0-9][0-9A-Za-z.+_-]*$`).MatchString(exact) {
				return "unchecked", "存在发行包元数据（版本 " + actual + "），但尚未验证版本范围 " + want
			}
			if actual != exact {
				return "missing", "发行包元数据版本为 " + actual + "，不符合指定版本 " + want
			}
		}
		return "present", "所选虚拟环境中存在发行包元数据（版本 " + actual + "）；未导入包或验证脚本可运行"
	}
	if !readable || incomplete {
		return "unchecked", "虚拟环境包元数据未能完整读取；不能据此认定依赖未安装"
	}
	return "missing", "所选虚拟环境的 dist-info 元数据中未找到发行包；未执行 Python 或导入包"
}
