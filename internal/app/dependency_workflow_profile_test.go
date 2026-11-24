package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func bashStatus(snap dependencySnapshot) string {
	for _, r := range snap.Requirements {
		if r.Requirement.Name == "Bash" {
			return r.Status
		}
	}
	return ""
}

func hasFinding(snap dependencySnapshot, dep, level string) bool {
	for _, f := range snap.Findings {
		if strings.Contains(f.Dependency, dep) && (level == "" || f.Level == level) {
			return true
		}
	}
	return false
}

// The check describes how the workflow's nodes really run: its own permission
// level and folder, not a fixed read-only scratch folder.
func TestDependencyWorkflowProfileFollowsDefinition(t *testing.T) {
	sandboxDeps(t, true)
	a := newTestApp(t)
	ctx := context.Background()
	sk := importPreflightSkill(t, a, "wf-profile", "run a command", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Environment: []skillstore.RuntimeRequirement{{Kind: "tool", Name: "Bash"}}})
	node := []workflow.Node{{ID: "A", Kind: "skill", Prompt: "x", SkillBindings: []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow"}}}}
	dir := t.TempDir()
	for _, tc := range []struct {
		mode, dir, wantMode, wantKind string
		restricted, denies            bool
	}{
		{"", "", "read", "workflow_scratch", true, true},
		{"read", dir, "read", "workflow_selected", true, true},
		{"edit", dir, "edit", "workflow_selected", true, false},
		{"exec", "", "exec", "workflow_shared", false, false},
		{"exec", dir, "exec", "workflow_selected", false, false},
	} {
		def := workflow.Definition{Name: "wf", RunMode: tc.mode, WorkspaceDir: tc.dir, Nodes: node}
		draft, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &def})
		if err != nil {
			t.Fatal(err)
		}
		w, err := a.Workflows.Save(ctx, "", 0, def)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow", ID: w.ID})
		if err != nil {
			t.Fatal(err)
		}
		for name, snap := range map[string]dependencySnapshot{"draft": draft, "saved": saved} {
			p := snap.RuntimeProfile
			if p.Mode != tc.wantMode || p.WorkspaceKind != tc.wantKind || p.Workspace != tc.dir || p.Restricted != tc.restricted || (len(p.DeniedTools) > 0) != tc.denies {
				t.Fatalf("%s %+v: profile %+v", name, tc, p)
			}
			if got := hasFinding(snap, webui.DepTitleSandbox, ""); got != (tc.wantMode == "exec") {
				t.Fatalf("%s %+v: sandbox finding %v", name, tc, got)
			}
			if got := hasFinding(snap, webui.DepTitleWorkspace, "available"); got != (tc.dir != "") {
				t.Fatalf("%s %+v: workspace finding %v %+v", name, tc, got, snap.Findings)
			}
			if got := bashStatus(snap); (got == "blocked") != tc.denies || got == "" {
				t.Fatalf("%s %+v: Bash %q", name, tc, got)
			}
		}
	}
	// a folder the workflow run would refuse is reported as blocked
	def := workflow.Definition{Name: "bad", RunMode: "exec", WorkspaceDir: a.Cfg.HomeDir, Nodes: node}
	snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &def})
	if err != nil || !hasFinding(snap, webui.DepTitleWorkspace, "blocked") {
		t.Fatalf("tidebot's own folder not blocked: %+v %v", snap.Findings, err)
	}
}

// A workflow task is checked as the workflow version it runs, not as a prompt
// task in its own (unused, possibly stale) folder.
func TestDependencyWorkflowTaskChecksItsWorkflow(t *testing.T) {
	sandboxDeps(t, true)
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	sk := importPreflightSkill(t, a, "wf-task-profile", "run a command", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Environment: []skillstore.RuntimeRequirement{{Kind: "tool", Name: "Bash"}}})
	def := workflow.Definition{Name: "wf", RunMode: "exec", WorkspaceDir: dir, Nodes: []workflow.Node{{ID: "A", Kind: "skill", Prompt: "x", SkillBindings: []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow"}}}}}
	w, err := a.Workflows.Save(ctx, "", 0, def)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "wf task", Prompt: "input", WorkflowID: w.ID, WorkflowVersion: w.Version})
	if err != nil {
		t.Fatal(err)
	}
	// a folder left on the row from before (older saves kept it)
	if _, err := a.DB.Exec(`UPDATE tasks SET workspace_dir=? WHERE id=?`, t.TempDir(), task.Task.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "task", ID: task.Task.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := snap.RuntimeProfile
	if p.Mode != "exec" || p.Workspace != dir || p.WorkspaceKind != "workflow_selected" || p.Restricted {
		t.Fatalf("profile %+v", p)
	}
	if bashStatus(snap) != "present" || len(snap.ContextUsage) != 1 || snap.ContextUsage[0].Origin != "node:A" || !hasFinding(snap, webui.DepTitleSandbox, "") || hasFinding(snap, webui.DepTitleTaskWorkspace, "") {
		t.Fatalf("findings %+v", snap.Findings)
	}
	// the version it pins is gone: reported, not silently checked as a prompt
	if _, err := a.DB.Exec(`UPDATE tasks SET workspace_dir='' WHERE id=?`, task.Task.ID); err != nil {
		t.Fatal(err)
	}
	spec := *task.Spec
	spec.WorkflowID = "wf_missing"
	if _, err := a.Tasks.UpdateSpec(ctx, task.Task.ID, spec, "test", "gone"); err != nil {
		t.Fatal(err)
	}
	snap, err = a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "task", ID: task.Task.ID})
	if err != nil || !hasFinding(snap, "wf_missing", "missing") || snap.RuntimeProfile.WorkspaceKind == "task_scratch" && snap.RuntimeProfile.Workspace != "" {
		t.Fatalf("missing workflow: %+v %v", snap, err)
	}
}

// The check dialog shows T("dep.mode_"+mode) and T("dep.workspace_"+kind): every
// value the profile can take needs a label in both languages (a missing one
// showed the raw key "dep.mode_exec" for the default 可执行命令 level).
func TestDependencyProfileValuesHaveLabels(t *testing.T) {
	kinds := map[string]bool{"unselected": true}
	for _, f := range []string{"preflight.go", "dependency_check.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`WorkspaceKind = (?:"\w+", )?"(\w+)"`).FindAllStringSubmatch(string(src), -1) {
			kinds[m[1]] = true
		}
	}
	for _, k := range []string{"workflow_selected", "workflow_scratch", "workflow_shared", "task_scratch", "task_selected", "scratch", "selected"} {
		if !kinds[k] {
			t.Fatalf("kind %s no longer set; scan missed %v", k, kinds)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile(filepath.Join("..", "webui", "static", "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		dict := map[string]string{}
		if err := json.Unmarshal(raw, &dict); err != nil {
			t.Fatal(err)
		}
		for _, m := range []string{"unselected", "read", "edit", "exec", "task", "quick"} {
			if dict["dep.mode_"+m] == "" {
				t.Errorf("%s: no label dep.mode_%s", lang, m)
			}
		}
		for k := range kinds {
			if dict["dep.workspace_"+k] == "" {
				t.Errorf("%s: no label dep.workspace_%s", lang, k)
			}
		}
	}
}

func reqStatus(snap dependencySnapshot, name string) (string, string) {
	for _, r := range snap.Requirements {
		if r.Requirement.Name == name {
			return r.Status, r.Evidence
		}
	}
	return "", ""
}

// 可执行命令 adds Bash (PowerShell on Windows), WebFetch and WebSearch to the
// run (applyExecSandbox): the check reports them available where the run can
// start, instead of "unchecked", and blocked when the sandbox is missing.
func TestDependencyExecModeToolsAvailable(t *testing.T) {
	sandboxDeps(t, true)
	a := newTestApp(t)
	ctx := context.Background()
	tools := []skillstore.RuntimeRequirement{{Kind: "tool", Name: "Bash"}, {Kind: "tool", Name: "WebFetch"}, {Kind: "tool", Name: "Write"}, {Kind: "command", Name: "go"}}
	sk := importPreflightSkill(t, a, "exec-tools", "run commands", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Environment: tools})
	bind := []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow"}}
	wantEv := webui.DepEvidenceToolExecSandbox
	if runtime.GOOS == "windows" {
		wantEv = webui.DepEvidenceToolExecNoSandbox
	}
	task, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "exec task", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	spec := *task.Spec
	spec.RunMode, spec.SkillBindings = "exec", bind
	if _, err := a.Tasks.UpdateSpec(ctx, task.Task.ID, spec, "test", "exec"); err != nil {
		t.Fatal(err)
	}
	execReqs := map[string]webui.DependencyCheckRequest{
		"run":        {Kind: "draft", Mode: "exec", SkillBindings: bind},
		"task form":  {Kind: "draft", Execution: "task", Mode: "exec", SkillBindings: bind},
		"saved task": {Kind: "task", ID: task.Task.ID},
	}
	for name, req := range execReqs {
		snap, err := a.collectDependencySnapshot(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range []string{"Bash", "WebFetch"} {
			if st, ev := reqStatus(snap, tool); st != "present" || ev != wantEv {
				t.Fatalf("%s: %s %q %q", name, tool, st, ev)
			}
		}
		// a tool the run doesn't add stays unproven
		if st, _ := reqStatus(snap, "Write"); st != "unchecked" {
			t.Fatalf("%s: Write %q", name, st)
		}
		if !hasFinding(snap, webui.DepTitleSandbox, "") {
			t.Fatalf("%s: no sandbox finding", name)
		}
	}
	// other levels are unchanged: the task form at edit leaves Bash unproven,
	// a read-only run denies it
	for name, want := range map[string]struct {
		req  webui.DependencyCheckRequest
		bash string
	}{
		"task form edit": {webui.DependencyCheckRequest{Kind: "draft", Execution: "task", Mode: "edit", SkillBindings: bind}, "unchecked"},
		"read run":       {webui.DependencyCheckRequest{Kind: "draft", Mode: "read", Workspace: t.TempDir(), SkillBindings: bind}, "blocked"},
	} {
		snap, err := a.collectDependencySnapshot(ctx, want.req)
		if err != nil {
			t.Fatal(err)
		}
		if st, _ := reqStatus(snap, "Bash"); st != want.bash || hasFinding(snap, webui.DepTitleSandbox, "") {
			t.Fatalf("%s: Bash %q findings %+v", name, st, snap.Findings)
		}
	}
	if runtime.GOOS == "windows" {
		return // no system sandbox to miss
	}
	// the sandbox is missing: the run is refused, so Bash is blocked, with one
	// sandbox finding and no "shell denied by the mode" advice
	sandboxDeps(t, false)
	snap, err := a.collectDependencySnapshot(ctx, execReqs["run"])
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := reqStatus(snap, "Bash"); st != "blocked" || !hasFinding(snap, webui.DepTitleSandbox, "blocked") || hasFinding(snap, webui.DepExecPermSuffix, "") {
		t.Fatalf("sandbox missing: Bash %q findings %+v", st, snap.Findings)
	}
}

// The task form checks a workflow task before it is saved: the version picked
// there (pinned, or 0 = following the current one) with the input typed there,
// not the saved task and not always the workflow's current version.
func TestDependencyWorkflowVersionFromTaskForm(t *testing.T) {
	sandboxDeps(t, true)
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	sk := importPreflightSkill(t, a, "wf-form-version", "run a command", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat})
	node := []workflow.Node{{ID: "A", Kind: "skill", Prompt: "x", SkillBindings: []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow"}}}}
	v1, err := a.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "wf", RunMode: "exec", WorkspaceDir: dir, Nodes: node})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := a.Workflows.Save(ctx, v1.ID, v1.Version, workflow.Definition{Name: "wf", RunMode: "read", Nodes: node})
	if err != nil || v2.Version == v1.Version {
		t.Fatalf("second version: %+v %v", v2, err)
	}
	for _, tc := range []struct {
		version          int
		wantMode, wantWS string
	}{{v1.Version, "exec", dir}, {0, "read", ""}, {v2.Version, "read", ""}} {
		snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow", ID: v1.ID, Version: tc.version, Prompt: "input for the run"})
		if err != nil {
			t.Fatal(err)
		}
		if p := snap.RuntimeProfile; p.Mode != tc.wantMode || p.Workspace != tc.wantWS {
			t.Fatalf("version %d: profile %+v", tc.version, p)
		}
		found := false
		for _, m := range snap.Materials {
			found = found || m.Source == "workflow-input" && m.Content == "input for the run"
		}
		if !found {
			t.Fatalf("version %d: input not in materials %+v", tc.version, snap.Materials)
		}
	}
	snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow", ID: v1.ID, Version: 99})
	if err != nil || len(snap.Findings) == 0 || snap.Findings[0].Kind != "workflow" || snap.Findings[0].Level != "missing" || !strings.Contains(snap.Findings[0].Evidence, "version 99") {
		t.Fatalf("missing version: %+v %v", snap.Findings, err)
	}
	for _, m := range snap.Materials {
		if m.Source == "workflow-input" {
			t.Fatalf("empty input added: %+v", snap.Materials)
		}
	}
	if _, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "task", ID: "t1", Version: 1}); err == nil {
		t.Fatal("version accepted for a task check")
	}
}
