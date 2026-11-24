package app

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// This is an instruction-body limit, not a model context-window claim. Wrappers,
// the task prompt, tool results and model-specific tokenization are additional.
const skillContextMaxBytes = 256 << 10
const contextEstimateMethod = webui.DepEstimateMethod

func estimateSkillTokens(data []byte) int { return contextplan.Estimate(data) }

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
		Notes: webui.DepNoteScope}
	if req.PythonEnvironment != "" {
		p.Notes += " " + webui.DepNotePython
	}
	if req.Kind == "workflow" || req.Kind == "workflow_draft" {
		// a saved workflow's own level and folder are applied once it is loaded
		def := workflow.Definition{}
		if req.Kind == "workflow_draft" && req.Definition != nil {
			def = *req.Definition
		}
		applyWorkflowProfile(&p, def)
	} else if req.Kind == "draft" && req.Execution == "task" {
		p.Mode, p.WorkspaceKind = "task", "task_scratch"
		p.Workspace = guard.ExpandHome(req.Workspace)
		if p.Workspace != "" {
			p.WorkspaceKind = "task_selected"
		}
		p.AllowedTools = append([]string{}, req.AllowedTools...)
		p.Notes += " " + webui.DepNoteDraftPolicy
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
	if req.Mode == "exec" && p.Mode != "task" {
		// 可执行命令: no --restricted, no read-only deny; a scratch folder without one
		p.Mode, p.Restricted, p.DeniedTools = "exec", false, nil
		if p.WorkspaceKind == "unselected" {
			p.WorkspaceKind = "scratch"
		}
	}
	return p
}

// commandRun: the target runs at 可执行命令 (applyExecSandbox): a workflow or
// run at exec, or a prompt task (saved, or the form's choice) whose level is exec.
func (c *dependencyCollector) commandRun() bool {
	switch c.snapshot.RuntimeProfile.Mode {
	case "exec":
		return true
	case "task":
		return c.req.Mode == "exec" || c.req.Kind == "task" && c.taskRunMode(c.req.ID) == "exec"
	}
	return false
}

func (c *dependencyCollector) taskRunMode(id string) string {
	t, err := c.a.Tasks.Get(c.ctx, id)
	if err != nil || t.Spec == nil || t.Spec.WorkflowID != "" {
		return "" // a workflow task runs at the workflow's level (in the profile)
	}
	return t.Spec.RunMode
}

// applyWorkflowProfile describes how a workflow's nodes run, as runWorkflowNode
// does: every node at the workflow's permission level ("" = read, as saved before
// the three levels), in its folder; without one, read-only nodes get an empty
// folder each and edit / exec nodes share a scratch folder per run.
func applyWorkflowProfile(p *webui.DependencyRuntimeProfile, d workflow.Definition) {
	mode := d.RunMode
	if mode != "edit" && mode != "exec" {
		mode = "read"
	}
	p.Mode, p.AllowedTools = mode, nil
	p.Workspace = guard.ExpandHome(strings.TrimSpace(d.WorkspaceDir))
	switch {
	case p.Workspace != "":
		p.WorkspaceKind = "workflow_selected"
	case mode == "read":
		p.WorkspaceKind = "workflow_scratch"
	default:
		p.WorkspaceKind = "workflow_shared"
	}
	p.Restricted, p.DeniedTools = mode != "exec", nil
	if mode == "read" {
		p.DeniedTools = append([]string{}, readOnlyDeny...)
	}
}

// checkSandbox: a command run needs the system sandbox where one is expected
// (Linux: bubblewrap + socat; macOS: Seatbelt). Without it the run is refused.
func (c *dependencyCollector) checkSandbox() {
	st := c.a.SandboxStatus()
	switch st.Status {
	case guard.SandboxMissing:
		c.finding(webui.DepTitleSandbox, "runtime", "blocked", webui.DepSandboxMissingReason(st.Missing), webui.DepSandboxMissingEvidence(st.Missing), webui.DepSandboxInstallSuggestion(st.Install))
	case guard.SandboxNone:
		c.finding(webui.DepTitleSandbox, "runtime", "uncertain", webui.DepReasonSandboxNone, webui.DepEvidenceSandboxWindows, webui.DepSuggestSandboxTrust)
	default:
		c.finding(webui.DepTitleSandbox, "runtime", "available", webui.DepReasonSandboxOK, webui.DepEvidenceSandboxOK, webui.DepSuggestNone)
	}
}

func (c *dependencyCollector) draftTaskPolicy(id string) {
	// The form edits content, not the persisted permission policy. Mirror
	// applyGuard's priority without replacing the draft prompt or bindings.
	p := &c.snapshot.RuntimeProfile
	p.AllowedTools = nil
	t, err := c.a.Tasks.Get(c.ctx, id)
	if err != nil || t.Spec == nil {
		c.finding(webui.DepTitleTaskPolicy, "configuration", "uncertain", webui.DepReasonTaskPolicyUnread, webui.DepEvidenceNoTaskVersion, webui.DepSuggestResaveTask)
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
	p.Notes += " " + webui.DepNoteTaskPolicy
}

func (c *dependencyCollector) checkWorkspace() {
	p := &c.snapshot.RuntimeProfile
	if p.WorkspaceKind == "selected" || p.WorkspaceKind == "workflow_selected" {
		// a workflow run refuses its folder the same way (prepareWorkflow)
		home, _ := os.UserHomeDir()
		_, problem := guard.CheckRunDir(p.Workspace, home, c.a.Cfg.HomeDir)
		if problem != "" {
			c.finding(webui.DepTitleWorkspace, "workspace", "blocked", webui.DepReasonWorkspaceUnusable, problem, webui.DepSuggestPickWorkspace)
		} else {
			c.finding(webui.DepTitleWorkspace, "workspace", "available", webui.DepReasonWorkspaceOK, webui.DepEvidenceWorkspaceOK, webui.DepSuggestConfirmMode)
		}
	} else if p.WorkspaceKind == "task_selected" {
		// Scheduled tasks have a different workspace contract from the run dialog.
		// Do not claim the dialog guard is enforced on this execution path.
		fi, err := os.Stat(p.Workspace)
		if err != nil || !fi.IsDir() || !filepath.IsAbs(p.Workspace) {
			c.finding(webui.DepTitleTaskWorkspace, "workspace", "uncertain", webui.DepReasonTaskWsUncertain, webui.DepEvidenceTaskWsUncertain, webui.DepSuggestCheckTaskWs)
		} else {
			c.finding(webui.DepTitleTaskWorkspace, "workspace", "available", webui.DepReasonTaskWsOK, webui.DepEvidenceTaskWsOK, webui.DepSuggestTaskWsPolicy)
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
		c.finding(origin, "context", "blocked", webui.DepReasonContextOver, webui.DepContextOverEvidence(usage.TotalBytes, usage.EstimatedTokens), webui.DepSuggestContextOver)
	} else if usage.TotalBytes > skillContextMaxBytes*3/4 {
		c.finding(origin, "context", "uncertain", webui.DepReasonContextNear, webui.DepContextNearEvidence(usage.TotalBytes, skillContextMaxBytes, usage.EstimatedTokens), webui.DepSuggestContextNear)
	}
}

func (c *dependencyCollector) checkManifest(b tasksvc.SkillBinding) {
	manifest, err := c.a.Skills.ReadSkillManifest(c.ctx, b.VersionID)
	if err != nil {
		c.finding(dependencySkillTitle(b, ""), "configuration", "missing", webui.DepReasonBadManifest, err.Error(), webui.DepSuggestFixManifest)
		return
	}
	for _, req := range manifest.Environment {
		if len(c.snapshot.Requirements) >= 128 {
			c.snapshot.Truncated = true
			break
		}
		status, evidence := c.checkRequirement(req, b.VersionID)
		if req.Version != "" && req.Kind != "python_package" && status == "present" {
			status, evidence = "unchecked", webui.DepVersionUnverifiedEvidence(evidence, req.Version)
		}
		evidence = dependencyClip(dependencyRedact(evidence), 2048)
		c.snapshot.Requirements = append(c.snapshot.Requirements, webui.DependencyRequirementResult{SkillID: b.SkillID, VersionID: b.VersionID, Requirement: req, Status: status, Evidence: evidence})
		level := map[string]string{"present": "available", "missing": "missing", "blocked": "blocked", "unchecked": "uncertain"}[status]
		reason := map[string]string{"present": webui.DepReasonReqPresent, "missing": webui.DepReasonReqMissing, "blocked": webui.DepReasonReqBlocked, "unchecked": webui.DepReasonReqUnchecked}[status]
		if req.Optional {
			reason = webui.DepOptionalReason(reason)
			if level == "missing" || level == "blocked" {
				level = "uncertain"
			}
		}
		c.finding(dependencySkillTitle(b, "")+" · "+req.Name, req.Kind, level, reason, evidence, webui.DepSuggestConfigureReq)
		// Executable presence does not imply the model can invoke it. Keep these
		// findings separate so an installed Python is never mistaken for executable
		// Python in a workflow whose shell tools are denied.
		if req.Kind == "command" || req.Kind == "python_package" {
			toolStatus, toolEvidence := c.checkTool("Bash")
			if toolStatus == "blocked" && !c.commandRun() { // a missing sandbox has its own finding
				level := "blocked"
				if req.Optional {
					level = "uncertain"
				}
				c.finding(webui.DepExecPermTitle(dependencySkillTitle(b, ""), req.Name), "tool", level, webui.DepReasonShellDenied, toolEvidence, webui.DepSuggestShellDenied)
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
		c.finding(webui.DepTitleWithOrigin(issue.SkillName, origin), "skill", level, webui.DepReasonSkillDepAttention, issue.Message, webui.DepSuggestSkillDepAttention)
	}
	for _, addition := range preview.Additions {
		c.finding(dependencySkillTitle(addition, origin), "skill", "missing", webui.DepReasonSkillDepNotInSet, webui.DepEvidenceSkillDepDeclared, webui.DepSuggestSkillDepPreview)
	}
}

var preflightCommandName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)
var preflightEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (c *dependencyCollector) checkRequirement(req skillstore.RuntimeRequirement, versionID string) (string, string) {
	switch req.Kind {
	case "command":
		if !preflightCommandName.MatchString(req.Name) {
			return "unchecked", "A command must be an executable name with no path or arguments"
		}
		_, err := exec.LookPath(req.Name)
		if err != nil {
			return "unchecked", "The executable was not found on the application process PATH; a real run may use an extended PATH. No shell or version probe was executed, so this is not treated as missing"
		}
		if req.Version != "" {
			return "unchecked", webui.DepCommandVersionUnchecked(req.Version)
		}
		return "present", "The executable exists on the application process PATH; it was not run, and its version and run permission were not verified"
	case "python_package":
		return inspectPythonDistribution(c.snapshot.RuntimeProfile.PythonEnvironment, req.Name, req.Version)
	case "env":
		if !preflightEnvName.MatchString(req.Name) {
			return "unchecked", "The environment variable name is invalid"
		}
		if strings.HasPrefix(req.Name, "ANTHROPIC_") || strings.HasPrefix(req.Name, "CLAUDE_") || req.Name == "TIDEBOT_IPC" || req.Name == "TIDEBOT_TASK_ID" || req.Name == "TIDEBOT_GUARD_WORKSPACE" {
			return "unchecked", "This variable may be overwritten by run isolation or provider configuration; credential values were not read, so the host environment cannot be used to judge the actual child process configuration"
		}
		if prof, err := c.a.ResolveProfile(c.snapshot.RuntimeProfile.Provider); err == nil {
			if req.Name == prof.AuthEnv || req.Name == provider.NormalizeSecretEnv(prof.SecretEnv) {
				return "unchecked", "This variable is part of the provider credential mapping, so the actual child process may inject or remove it; the inspection does not read secrets"
			}
			if _, configured := prof.ExtraEnv[req.Name]; configured {
				return "present", "The provider's extra environment configuration declares this variable (configured); the value was not saved, returned, or verified"
			}
		}
		// Only existence is observed. No value is retained, interpolated or emitted.
		if _, ok := os.LookupEnv(req.Name); ok {
			return "present", "The application process environment declares this variable (configured); the value was not saved, returned, or verified"
		}
		return "missing", "The application process environment does not declare this variable; .env, the secret store, and credential files were not read"
	case "tool":
		return c.checkTool(req.Name)
	case "mcp":
		return c.checkMCP(req.Name)
	case "file":
		name := filepath.ToSlash(req.Name)
		if filepath.IsAbs(req.Name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.Contains(name, "\\") {
			return "unchecked", "Only relative file paths inside the skill package are checked"
		}
		var count int
		err := c.a.DB.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM version_files WHERE version_id=? AND path=?`, versionID, name).Scan(&count)
		if err != nil {
			return "unchecked", "The skill version file index could not be read"
		}
		if count == 0 {
			return "missing", "This path is not in the skill version file index; no file contents were read"
		}
		return "present", "This path is in the skill version file index; the file was not read or executed"
	default:
		return "unchecked", "This kind of dependency has no read-only checker yet"
	}
}

func (c *dependencyCollector) checkTool(name string) (string, string) {
	p := c.snapshot.RuntimeProfile
	base := strings.SplitN(name, "(", 2)[0]
	for _, deny := range p.DeniedTools {
		if strings.EqualFold(base, deny) {
			return "blocked", webui.DepToolModeDenied(p.Mode, deny)
		}
	}
	if p.Mode == "unselected" {
		return "unchecked", "The target run mode has not been selected, so tool permissions cannot be determined"
	}
	if c.commandRun() {
		for _, t := range execRunTools(runtime.GOOS) {
			if !strings.EqualFold(base, t) {
				continue
			}
			// the run itself adds this tool (applyExecSandbox); it starts only
			// where the sandbox can (Linux/macOS) or there is none (Windows)
			switch st := c.a.SandboxStatus(); st.Status {
			case guard.SandboxMissing:
				return "blocked", webui.DepSandboxMissingReason(st.Missing)
			case guard.SandboxNone:
				return "present", webui.DepEvidenceToolExecNoSandbox
			default:
				return "present", webui.DepEvidenceToolExecSandbox
			}
		}
	}
	for _, allowed := range p.AllowedTools {
		if allowed == base || allowed == name {
			return "present", "The task policy pre-authorizes this tool rule; it is still constrained by the workspace, protection rules, and the actual invocation arguments"
		}
	}
	if p.Mode == "read" && (base == "Read" || base == "Glob" || base == "Grep") {
		return "present", "The target read-only mode does not disable this built-in read tool; file scope is still constrained by the run environment"
	}
	return "unchecked", "The target run does not explicitly deny this tool, but there is no rule that proves this invocation is authorized"
}

func (c *dependencyCollector) checkMCP(name string) (string, string) {
	if c.a.Plugins == nil {
		return "unchecked", "The plugin configuration service is unavailable"
	}
	list, err := c.a.Plugins.List(c.ctx)
	if err != nil {
		return "unchecked", "Plugin authorization metadata could not be read"
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
		return "missing", "The corresponding MCP was not found in the current version of the installed plugin; credentials were not read and the service was not contacted"
	}
	if len(matches) > 1 {
		return "unchecked", "Multiple plugins provide an MCP with this name; specify it as plugin-id__connector-id"
	}
	p := matches[0].p
	if !p.Enabled {
		return "blocked", "The plugin that provides this MCP is not currently enabled"
	}
	if p.Current.Approved == nil || len(plugin.NewPermissions(p.Current.Approved, p.Current.Requested)) > 0 {
		return "blocked", "The plugin that provides this MCP still has unauthorized or revoked permissions"
	}
	connector := matches[0].connector
	if connector.MCP.Builtin != "" {
		if plugin.BuiltinExe == "" {
			return "missing", "The MCP is authorized, but this process has no usable built-in plugin entry"
		}
	} else if info, err := os.Stat(filepath.Join(p.Current.Dir, connector.MCP.Command)); err != nil || !info.Mode().IsRegular() {
		return "missing", "The MCP is authorized, but its plugin entry file is missing or is not a regular file; the entry was not executed"
	}
	return "present", "The plugin is enabled and every permission declared by the current version is authorized; secret configuration, process startup, and service reachability were not verified"
}

func canonicalPythonName(name string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(name))
}

// inspectPythonDistribution never executes a venv binary or imports package code.
// Only regular, bounded .dist-info/METADATA files inside an explicitly selected
// venv are read. An unselected or ambiguous environment remains unchecked.
func inspectPythonDistribution(venv, name, want string) (string, string) {
	if venv == "" {
		return "unchecked", "No Python virtual environment was selected; the default Python will not be used to import the package and guess whether it is installed"
	}
	root, err := filepath.EvalSymlinks(venv)
	if err != nil || !filepath.IsAbs(root) {
		return "unchecked", "The selected Python environment directory is not readable"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "unchecked", "The selected Python environment is not a directory"
	}
	// An explicit venv must have its metadata marker; never scan a whole arbitrary directory.
	marker, err := os.Lstat(filepath.Join(root, "pyvenv.cfg"))
	if err != nil || !marker.Mode().IsRegular() || marker.Size() > 64<<10 {
		return "unchecked", "The directory has no regular pyvenv.cfg, so it cannot yet be confirmed as a virtual environment"
	}
	paths := []string{filepath.Join(root, "Lib", "site-packages")}
	libs, err := filepath.Glob(filepath.Join(root, "lib", "python*", "site-packages"))
	if err != nil {
		return "unchecked", "Virtual environment package metadata could not be listed"
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
		return "unchecked", "Virtual environment marker metadata could not be read"
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
			return "unchecked", "The package directory has more than 4096 entries and was not fully inspected"
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
		return "unchecked", "Metadata for multiple distributions with this name was found, so the actual import version cannot be determined"
	}
	if len(found) == 1 {
		actual := found[0]
		if want != "" {
			exact := strings.TrimPrefix(want, "==")
			if !regexp.MustCompile(`^[0-9][0-9A-Za-z.+_-]*$`).MatchString(exact) {
				return "unchecked", webui.DepPythonVersionUnchecked(actual, want)
			}
			if actual != exact {
				return "missing", webui.DepPythonVersionMissing(actual, want)
			}
		}
		return "present", webui.DepPythonPresent(actual)
	}
	if !readable || incomplete {
		return "unchecked", "Virtual environment package metadata could not be fully read; this is not treated as proof that the dependency is not installed"
	}
	return "missing", "The distribution was not found in the selected virtual environment's dist-info metadata; Python was not executed and the package was not imported"
}
