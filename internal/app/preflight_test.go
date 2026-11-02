package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func importPreflightSkill(t *testing.T, a *App, name, body string, m skillstore.SkillManifest) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test\n---\n"+body), 0600); err != nil {
		t.Fatal(err)
	}
	if m.Format != "" {
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, "tidebot.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	sk, err := a.Skills.ImportDir(context.Background(), name, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	return sk.ID
}

func TestPreflightWorkflowEnvironmentAndPermissionAreSeparate(t *testing.T) {
	a := newTestApp(t)
	envName := "TIDEBOT_PREFLIGHT_TEST_SECRET"
	secret := "do-not-expose-this-value"
	t.Setenv(envName, secret)
	id := importPreflightSkill(t, a, "preflight-runtime", "run a command", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Environment: []skillstore.RuntimeRequirement{
		{Kind: "command", Name: "sh"}, {Kind: "tool", Name: "Bash"}, {Kind: "tool", Name: "Read"}, {Kind: "python_package", Name: "requests"}, {Kind: "env", Name: envName}, {Kind: "file", Name: "scripts/missing.py"},
	}})
	req := webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &workflow.Definition{Name: "example", Nodes: []workflow.Node{{ID: "step1", Name: "Step", Prompt: "test", SkillBindings: []tasksvc.SkillBinding{{SkillID: id}}}}}}
	report, err := a.dependencyPreflight(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, r := range report.Requirements {
		statuses[r.Requirement.Name] = r.Status
	}
	if statuses["sh"] != "present" || statuses["Bash"] != "blocked" || statuses["Read"] != "present" || statuses["requests"] != "unchecked" || statuses[envName] != "present" || statuses["scripts/missing.py"] != "missing" {
		t.Fatalf("statuses %v", statuses)
	}
	if report.RuntimeProfile.Mode != "read" || report.RuntimeProfile.WorkspaceKind != "workflow_scratch" {
		t.Fatalf("profile %+v", report.RuntimeProfile)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), secret) {
		t.Fatal("environment secret leaked")
	}
	foundShell := false
	for _, f := range report.Findings {
		if f.Level == "blocked" && strings.Contains(f.Dependency, "执行权限") {
			foundShell = true
		}
	}
	if !foundShell {
		t.Fatal("installed command hid the blocked shell requirement")
	}
	var runs int
	if err := a.DB.QueryRow("SELECT count(*) FROM runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("preflight launched a run: %d %v", runs, err)
	}
}

func TestPreflightPythonUsesOnlySelectedDistributionMetadata(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "lib", "python3.12", "site-packages")
	dist := filepath.Join(site, "Demo_Package-1.2.3.dist-info")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "pyvenv.cfg"), []byte("home = ignored\n"), 0600)
	os.WriteFile(filepath.Join(dist, "METADATA"), []byte("Metadata-Version: 2.1\nName: Demo-Package\nVersion: 1.2.3\n\n"), 0600)
	// Neither this module nor a python executable should ever be read or executed.
	os.WriteFile(filepath.Join(site, "demo_package.py"), []byte("raise Exception('must not import')\n"), 0600)
	for _, tc := range []struct{ root, name, version, status string }{
		{"", "demo-package", "", "unchecked"}, {root, "demo.package", "", "present"}, {root, "demo-package", "==1.2.3", "present"}, {root, "demo-package", "2.0.0", "missing"}, {root, "demo-package", ">=1.0", "unchecked"}, {root, "absent", "", "missing"},
	} {
		status, _ := inspectPythonDistribution(tc.root, tc.name, tc.version)
		if status != tc.status {
			t.Fatalf("%+v got %s", tc, status)
		}
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "METADATA"), []byte("Name: another\nVersion: 1\n"), 0600)
	if err := os.Symlink(outside, filepath.Join(site, "another-1.dist-info")); err != nil {
		t.Fatal(err)
	}
	status, _ := inspectPythonDistribution(root, "another", "")
	if status != "unchecked" {
		t.Fatalf("symlinked metadata treated as definitive: %s", status)
	}
}

func TestPreflightContextBudgetIsPerNodeAndCannotTruncate(t *testing.T) {
	a := newTestApp(t)
	id := importPreflightSkill(t, a, "context-body", strings.Repeat("a", 150<<10), skillstore.SkillManifest{})
	nodes := []workflow.Node{}
	for _, nodeID := range []string{"one", "two"} {
		nodes = append(nodes, workflow.Node{ID: nodeID, Name: nodeID, Prompt: "x", SkillBindings: []tasksvc.SkillBinding{{SkillID: id}}})
	}
	report, err := a.dependencyPreflight(context.Background(), webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &workflow.Definition{Name: "test", Nodes: nodes}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ContextUsage) != 2 {
		t.Fatalf("usage %+v", report.ContextUsage)
	}
	for _, usage := range report.ContextUsage {
		if usage.Exceeded || !usage.EstimateOnly || len(usage.Members) != 1 || usage.TotalBytes < 150<<10 || usage.EstimatedTokens == 0 {
			t.Fatalf("usage %+v", usage)
		}
	}
	if estimateSkillTokens([]byte("abcd中文")) != 3 {
		t.Fatal("estimate should distinguish ASCII from Chinese")
	}
	stage := &skillStage{Dir: t.TempDir(), Skills: []skillstore.StagedSkill{{Name: "main", SkillMD: []byte(strings.Repeat("x", skillContextMaxBytes+1))}}}
	for _, explicit := range []bool{false, true} {
		if _, err = stage.CheckedArgs(true, explicit); err == nil || !strings.Contains(err.Error(), "not truncated") {
			t.Fatalf("oversized modern context accepted: %v", err)
		}
	}
	stage.Skills[0].SkillMD = []byte(strings.Repeat("x", 17<<10))
	if _, err = stage.CheckedArgs(false, true); err == nil {
		t.Fatal("legacy single skill silently truncated")
	}
}

func TestPreflightMissingSkillDependencyAndTaskPolicy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	id := importPreflightSkill(t, a, "needs-helper", "body", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Dependencies: []skillstore.SkillDependency{{SkillName: "absent-helper"}}, Environment: []skillstore.RuntimeRequirement{{Kind: "tool", Name: "Bash"}}})
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "preflight task", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	sp := *tv.Spec
	sp.SkillBindings = []tasksvc.SkillBinding{{SkillID: id}}
	sp.AllowedTools = []string{"Bash(git status)"}
	if _, err = a.Tasks.UpdateSpec(ctx, tv.Task.ID, sp, "test", "test"); err != nil {
		t.Fatal(err)
	}
	report, err := a.dependencyPreflight(ctx, webui.DependencyCheckRequest{Kind: "task", ID: tv.Task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Requirements) != 1 || report.Requirements[0].Status != "unchecked" {
		t.Fatalf("specific allowed command incorrectly grants all Bash: %+v", report.Requirements)
	}
	if report.RuntimeProfile.Mode != "task" || len(report.RuntimeProfile.AllowedTools) != 1 {
		t.Fatalf("task policy %+v", report.RuntimeProfile)
	}
	found := false
	for _, f := range report.Findings {
		if f.Level == "missing" && strings.Contains(f.Dependency, "absent-helper") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing manifest dependency was not reported")
	}
}

func TestPreflightDraftAndButtonProfilesMatchExecutionPaths(t *testing.T) {
	quick := initialDependencyProfile(webui.DependencyCheckRequest{Kind: "draft", Prompt: "hello", Mode: "edit"})
	if quick.Mode != "quick" || len(quick.DeniedTools) != 0 {
		t.Fatalf("prompt-only quick profile %+v", quick)
	}
	skill := initialDependencyProfile(webui.DependencyCheckRequest{Kind: "draft", Prompt: "hello", Mode: "edit", SkillBindings: []tasksvc.SkillBinding{{SkillID: "skill_one"}}})
	if skill.Mode != "read" || len(skill.DeniedTools) == 0 {
		t.Fatalf("scratch skill run must remain read-only %+v", skill)
	}
	task := initialDependencyProfile(webui.DependencyCheckRequest{Kind: "draft", Execution: "task", Prompt: "hello", Workspace: "/tmp/example", AllowedTools: []string{"Bash(git status)"}})
	if task.Mode != "task" || len(task.DeniedTools) != 0 || len(task.AllowedTools) != 1 {
		t.Fatalf("task draft policy %+v", task)
	}
	a := newTestApp(t)
	ctx := context.Background()
	id := importPreflightSkill(t, a, "button-runtime", "body", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Environment: []skillstore.RuntimeRequirement{{Kind: "tool", Name: "Bash"}}})
	raw, _ := json.Marshal([]map[string]string{{"id": "button_one", "label": "Button", "prompt": "work", "skill_id": id}})
	if err := a.Auth.SetSetting(ctx, "buttons", string(raw)); err != nil {
		t.Fatal(err)
	}
	report, err := a.dependencyPreflight(ctx, webui.DependencyCheckRequest{Kind: "button", ID: "button_one"})
	if err != nil {
		t.Fatal(err)
	}
	if report.RuntimeProfile.Mode != "quick" || len(report.RuntimeProfile.DeniedTools) != 0 || len(report.Requirements) != 1 || report.Requirements[0].Status != "unchecked" {
		t.Fatalf("button has no explicit read-only deny: %+v", report)
	}
}

type preflightForbiddenSecrets struct{ t *testing.T }

func (s preflightForbiddenSecrets) Get(string) (string, error) {
	s.t.Fatal("preflight attempted to read a stored secret")
	return "", nil
}
func (s preflightForbiddenSecrets) Set(string, string) error {
	s.t.Fatal("preflight attempted to set a secret")
	return nil
}
func (s preflightForbiddenSecrets) Delete(string) error {
	s.t.Fatal("preflight attempted to delete a secret")
	return nil
}

func TestPreflightMCPChecksApprovalWithoutReadingSecrets(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	manifest := `id: preflight-plugin
name: Preflight
version: 1.0.0
permissions:
  secrets: [token]
contributes:
  connectors:
    - id: query
      mcp:
        command: query.sh
        env:
          TOKEN: '${secret:token}'
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "query.sh"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	v, err := a.Plugins.Stage(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Plugins.Approve(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.ExecContext(ctx, `UPDATE plugins SET enabled=1 WHERE id='preflight-plugin'`); err != nil {
		t.Fatal(err)
	}
	a.Plugins.Secrets = preflightForbiddenSecrets{t: t}
	c := dependencyCollector{a: a, ctx: ctx}
	status, _ := c.checkMCP("preflight-plugin__query")
	if status != "present" {
		t.Fatalf("approved MCP: %s", status)
	}
	if _, err = a.DB.ExecContext(ctx, `UPDATE plugins SET enabled=0 WHERE id='preflight-plugin'`); err != nil {
		t.Fatal(err)
	}
	status, _ = c.checkMCP("query")
	if status != "blocked" {
		t.Fatalf("disabled MCP: %s", status)
	}
	if _, err = a.DB.ExecContext(ctx, `UPDATE plugins SET enabled=1 WHERE id='preflight-plugin'`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.ExecContext(ctx, `UPDATE plugin_versions SET approved_permissions_json='[]' WHERE id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	status, _ = c.checkMCP("query")
	if status != "blocked" {
		t.Fatalf("revoked MCP permission: %s", status)
	}
}

func TestPreflightTaskDraftUsesSavedEffectiveToolPolicy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "draft policy", Prompt: "original"})
	if err != nil {
		t.Fatal(err)
	}
	sp := *tv.Spec
	sp.AllowedTools = []string{"Read"}
	if _, err = a.Tasks.UpdateSpec(ctx, tv.Task.ID, sp, "test", "test"); err != nil {
		t.Fatal(err)
	}
	req := webui.DependencyCheckRequest{Kind: "draft", Execution: "task", ID: tv.Task.ID, Prompt: "unsaved prompt", AllowedTools: []string{"Bash"}}
	report, err := a.dependencyPreflight(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.RuntimeProfile.AllowedTools, ",") != "Read" {
		t.Fatalf("saved spec must be used: %+v", report.RuntimeProfile)
	}
	pol := a.Policy.Get(ctx, tv.Task.ID)
	pol.AllowedTools = "Grep, Bash(git status)"
	if err = a.Policy.Put(ctx, pol); err != nil {
		t.Fatal(err)
	}
	report, err = a.dependencyPreflight(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.RuntimeProfile.AllowedTools, ",") != "Grep,Bash(git status)" {
		t.Fatalf("saved policy must take precedence: %+v", report.RuntimeProfile)
	}
}

func TestPreflightUnprobedRuntimePathAndManagedCredentialsStayUnchecked(t *testing.T) {
	a := newTestApp(t)
	a.Secrets = preflightForbiddenSecrets{t: t}
	c := dependencyCollector{a: a, ctx: context.Background()}
	t.Setenv("ANTHROPIC_API_KEY", "must-not-read-provider-secret")
	for _, req := range []skillstore.RuntimeRequirement{{Kind: "env", Name: "ANTHROPIC_API_KEY"}, {Kind: "command", Name: "tidebot-preflight-command-that-does-not-exist"}} {
		status, evidence := c.checkRequirement(req, "")
		if status != "unchecked" || strings.Contains(evidence, "must-not-read-provider-secret") {
			t.Fatalf("unverified runtime turned definitive: %s %s", status, evidence)
		}
	}
}
