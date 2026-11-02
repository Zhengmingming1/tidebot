package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

const dependencyReportFixture = `{"summary":"需要确认 Python 包和认证配置。","findings":[{"dependency":"requests","kind":"package","level":"available","reason":"脚本导入此包","evidence":"helper.py: import requests","suggestion":"在实际运行环境确认是否安装"}]}`

func TestDependencySnapshotBoundsAndSecrets(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	for p, content := range map[string]string{
		"SKILL.md":            "---\nname: dependency-example\ndescription: example\n---\nUse helper.py; API_KEY=inside-prompt-secret",
		"helper.py":           "import requests\nconfig = {'api_key': 'inside-code-secret'}\nTOKEN = \"inside-token-secret\"\n",
		"requirements.txt":    "requests>=2\n",
		"credentials.py":      "private-credential-file",
		"deepwiki_token.json": "private-token-file",
		".env":                "private-env-file",
	} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sk, err := a.Skills.ImportDir(ctx, "dependency-example", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "skill", ID: sk.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw := dependencyPrompt(snap)
	for _, secret := range []string{"inside-prompt-secret", "inside-code-secret", "inside-token-secret", "private-credential-file", "private-token-file", "private-env-file"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	if !strings.Contains(raw, "import requests") || !strings.Contains(raw, "requirements.txt") || len(snap.Materials) != 3 || len(snap.SkillBindings) != 1 || snap.SkillBindings[0].VersionID != sk.CurrentVersion.ID {
		t.Fatalf("snapshot %+v", snap)
	}
	// The checker reads only stored blobs, never the original folder.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err = a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "skill", ID: sk.ID}); err != nil {
		t.Fatal(err)
	}
	c := dependencyCollector{snapshot: dependencySnapshot{Materials: []dependencyMaterial{}}}
	for i := 0; i < 10; i++ {
		c.material("large", strings.Repeat("中", 20000))
	}
	if !c.snapshot.Truncated || c.bytes > dependencyMaterialLimit {
		t.Fatalf("unbounded snapshot %+v", c)
	}
}

func TestDependencyCollectAllTargetsAndInvalidReferences(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	first, second := importTestSkill(t, a, "primary-dependency"), importTestSkill(t, a, "aux-dependency")
	bindings := []tasksvc.SkillBinding{{SkillID: first, Mode: "follow", Role: "primary"}, {SkillID: second, Mode: "follow", Role: "auxiliary"}}
	set, err := a.Skills.SaveSet(ctx, "", "Combo", "", bindings)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "Check task", Prompt: "Produce an answer"})
	if err != nil {
		t.Fatal(err)
	}
	spec := *task.Spec
	spec.SkillBindings = []tasksvc.SkillBinding{{SkillID: "missing_skill", Mode: "follow"}}
	if _, err = a.Tasks.UpdateSpec(ctx, task.Task.ID, spec, "test", "fixture"); err != nil {
		t.Fatal(err)
	}
	buttons, _ := json.Marshal([]map[string]string{{"id": "dependency-button", "task_id": task.Task.ID, "prompt": "also use a web service"}})
	if err = a.Auth.SetSetting(ctx, "buttons", string(buttons)); err != nil {
		t.Fatal(err)
	}
	definition := workflow.Definition{Name: "Check workflow", Nodes: []workflow.Node{{ID: "A", Kind: "skill", Prompt: "Inspect input", SkillBindings: bindings}}}
	w, err := a.Workflows.Save(ctx, "", 0, definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []webui.DependencyCheckRequest{
		{Kind: "skill_set", ID: set.ID}, {Kind: "task", ID: task.Task.ID}, {Kind: "button", ID: "dependency-button"}, {Kind: "workflow", ID: w.ID},
		{Kind: "draft", Prompt: "draft input", SkillBindings: bindings},
	} {
		snap, err := a.collectDependencySnapshot(ctx, req)
		if err != nil || len(snap.Materials) == 0 {
			t.Fatalf("%s: %+v %v", req.Kind, snap, err)
		}
		if req.Kind == "task" || req.Kind == "button" {
			found := false
			for _, f := range snap.Findings {
				if strings.Contains(f.Dependency, "missing_skill") && f.Level == "missing" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing binding lost: %+v", snap)
			}
		}
	}
	definition.Nodes[0].Needs = []workflow.Dependency{{NodeID: "A"}}
	beforeJoin := definition.Nodes[0].Join
	snap, err := a.collectDependencySnapshot(ctx, webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &definition})
	if err != nil || len(snap.Findings) == 0 || snap.Findings[0].Kind != "workflow" || snap.Findings[0].Level != "missing" {
		t.Fatalf("invalid graph should be a finding: %+v %v", snap, err)
	}
	if definition.Nodes[0].Join != beforeJoin {
		t.Fatal("validation mutated caller's draft")
	}
}

func TestDependencySkillTitlesRemainReadableAndRedacted(t *testing.T) {
	a := newTestApp(t)
	sid := importTestSkill(t, a, "flow-primary")
	snap, err := a.collectDependencySnapshot(context.Background(), webui.DependencyCheckRequest{Kind: "skill", ID: sid})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Findings) != 1 || snap.Findings[0].Dependency != "flow-primary" || snap.Findings[0].Level != "available" {
		t.Fatalf("resolved skill must use its name: %+v", snap.Findings)
	}
	if len(snap.SkillBindings) != 1 || snap.SkillBindings[0].SkillID != sid || snap.SkillBindings[0].VersionID == "" || strings.Contains(snap.SkillBindings[0].VersionID, "redacted") {
		t.Fatalf("exact snapshot binding was changed: %+v", snap.SkillBindings)
	}
	if snap.Materials[0].Source != "skill:flow-primary/SKILL.md" {
		t.Fatalf("source lost its skill name: %+v", snap.Materials)
	}
	generated := "skill_0123456789abcdef0123456789abcdef"
	if title := dependencySkillTitle(tasksvc.SkillBinding{SkillID: generated}, "node:A"); title != "技能 …89abcdef（node:A）" {
		t.Fatalf("fallback %q", title)
	}
	c := dependencyCollector{}
	c.finding(dependencySkillTitle(tasksvc.SkillBinding{SkillID: generated, SkillName: "token=short-sensitive-value"}, "node:A"), "skill", "missing", "missing", "credentials/token=another-secret", "fix")
	raw, _ := json.Marshal(c.snapshot.Findings)
	if strings.Contains(string(raw), "short-sensitive-value") || strings.Contains(string(raw), "another-secret") || !strings.Contains(string(raw), "redacted") {
		t.Fatalf("display bypassed redaction: %s", raw)
	}
	if dependencySourceFile("credentials/deepwiki_token.json") {
		t.Fatal("credential path became eligible")
	}
}

func TestDependencyCheckAsyncRunAndReport(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	t.Setenv("TIDEBOT_MOCK_RESULT_TEXT", dependencyReportFixture)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	rid, err := a.startDependencyCheck(webui.DependencyCheckRequest{Kind: "draft", Prompt: "Use requests with token='private-value'", SkillBindings: []tasksvc.SkillBinding{{SkillID: "missing_skill", Mode: "follow"}}})
	if err != nil || rid == "" {
		t.Fatalf("start %q %v", rid, err)
	}
	waitRunDone(t, a, rid)
	status, err := a.dependencyCheckStatus(ctx, rid)
	if err != nil || status.Status != "succeeded" || status.Report == nil || status.ChildRunID != rid {
		t.Fatalf("report %+v %v", status, err)
	}
	if len(status.Report.Findings) != 2 || status.Report.Findings[0].Level != "missing" || status.Report.Findings[1].Level != "uncertain" {
		t.Fatalf("AI overrode confirmed facts or fabricated availability: %+v", status.Report)
	}
	var prompt, ws, trigger string
	if err = a.DB.QueryRow(`SELECT prompt,workspace_dir,trigger_kind FROM runs WHERE id=?`, rid).Scan(&prompt, &ws, &trigger); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prompt, dependencySnapshotMarker) || strings.Contains(prompt, "private-value") || trigger != "dependency_check" {
		t.Fatalf("unsafe trace %q", prompt)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if !contains(args, "--tools") || argAfter(args, "--tools") != "" || !contains(args, "--strict-mcp-config") || argAfter(args, "--mcp-config") != `{"mcpServers":{}}` || strings.Contains(string(b), "--plugin-dir") || strings.Contains(string(b), "private-value") {
		t.Fatalf("unsafe args %q", args)
	}
	if argAfter(args, "-p") != "--output-format" {
		t.Fatalf("large snapshot should use stdin: %q", args)
	}
	// The runner publishes its terminal state before RunPromptEx finishes cost
	// accounting and the caller's deferred workspace cleanup.
	waitFor(t, 3*time.Second, func() bool { _, err := os.Stat(ws); return errors.Is(err, os.ErrNotExist) })
	// Results survive application state; malformed model output is an error, never a green report.
	if err = a.Store.SetRunResult(ctx, rid, "not JSON"); err != nil {
		t.Fatal(err)
	}
	status, err = a.dependencyCheckStatus(ctx, rid)
	if err != nil || status.Status != "failed" || status.Error != "报告格式不符合要求" || status.Report != nil {
		t.Fatalf("malformed report %+v %v", status, err)
	}
	if err = a.Store.InsertRun(ctx, db.Run{ID: "run_other", Status: "succeeded", TriggerKind: "quick", Prompt: prompt}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.dependencyCheckStatus(ctx, "run_other"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read unrelated run: %v", err)
	}
	if _, err = a.DB.Exec(`UPDATE runs SET status='canceled' WHERE id=?`, rid); err != nil {
		t.Fatal(err)
	}
	status, err = a.dependencyCheckStatus(ctx, rid)
	if err != nil || status.Status != "canceled" || status.Report != nil {
		t.Fatalf("canceled %+v %v", status, err)
	}
}

func TestDependencyReportValidation(t *testing.T) {
	for _, bad := range []string{"", `{}`, `{"summary":"ok"}`, `{"summary":"ok","findings":null}`, `{"summary":"ok","findings":[{"dependency":"a","kind":"tool","level":"certain","reason":"x","evidence":"x","suggestion":"x"}]}`} {
		if _, err := parseDependencyReport(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if r, err := parseDependencyReport(`{"summary":"仅有静态材料","findings":[]}`); err != nil || r.Findings == nil {
		t.Fatalf("empty findings: %+v %v", r, err)
	}
	missing := strings.ReplaceAll(dependencyReportFixture, `"available"`, `"missing"`)
	if r, err := parseDependencyReport(missing); err != nil || r.Findings[0].Level != "uncertain" {
		t.Fatalf("unverified missing environment dependency reported as fact: %+v %v", r, err)
	}
}

func TestDependencyRedactsAuthorizationWithoutEatingContext(t *testing.T) {
	for _, in := range []string{
		"Authorization: Bearer abc123-short-secret\nRequires requests",
		"Authorization: Basic dXNlcjpwYXNz\nRequires requests",
		`headers={"Authorization": "Bearer abc123-short-secret"}; import requests`,
		`headers={'Authorization': 'Basic dXNlcjpwYXNz'}; import requests`,
	} {
		out := dependencyRedact(in)
		if strings.Contains(out, "abc123-short-secret") || strings.Contains(out, "dXNlcjpwYXNz") || !strings.Contains(out, "requests") {
			t.Fatalf("unsafe redaction: %q", out)
		}
	}
	a := newTestApp(t)
	d := workflow.Definition{Name: "draft", Nodes: []workflow.Node{{ID: "A", Kind: "skill", Prompt: `headers={"Authorization": "Bearer abc123-short-secret"}; import requests`}}}
	snap, err := a.collectDependencySnapshot(context.Background(), webui.DependencyCheckRequest{Kind: "workflow_draft", Definition: &d})
	if err != nil {
		t.Fatal(err)
	}
	if raw := dependencyPrompt(snap); strings.Contains(raw, "abc123-short-secret") || !strings.Contains(raw, "requests") {
		t.Fatalf("nested JSON secret: %s", raw)
	}
}
