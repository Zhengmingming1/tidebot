package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// TestErrInfoCatalogTranslated: every error code has zh-CN and en text whose
// placeholders are values the code actually carries, and no wfe.* key is orphaned.
func TestErrInfoCatalogTranslated(t *testing.T) {
	codes := map[string]map[string]bool{}
	for _, p := range errPatterns {
		if codes[p.code] != nil {
			t.Errorf("duplicate code %s", p.code)
		}
		names := map[string]bool{}
		for _, n := range p.names {
			names[n] = true
		}
		if got := p.re.NumSubexp(); got != len(p.names) {
			t.Errorf("%s: %d groups, %d names", p.code, got, len(p.names))
		}
		codes[p.code] = names
	}
	for _, code := range repositoryErrorCodes {
		if codes[code] != nil {
			t.Errorf("duplicate typed error %s", code)
		}
		codes[code] = map[string]bool{}
	}
	ph := regexp.MustCompile(`\{(\w+)\}`)
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		dict := map[string]string{}
		if err := json.Unmarshal(raw, &dict); err != nil {
			t.Fatal(err)
		}
		for code, names := range codes {
			text, ok := dict["wfe."+code]
			if !ok {
				t.Errorf("%s: missing wfe.%s", lang, code)
				continue
			}
			for _, m := range ph.FindAllStringSubmatch(text, -1) {
				if !names[m[1]] {
					t.Errorf("%s wfe.%s uses {%s}, which the code doesn't carry", lang, code, m[1])
				}
			}
		}
		for k := range dict {
			if c, ok := strings.CutPrefix(k, "wfe."); ok && codes[c] == nil {
				t.Errorf("%s: %s has no server code", lang, k)
			}
		}
	}
}

// TestErrInfoRealErrors runs the real validators, so a reworded Go message that the
// catalog no longer matches fails here instead of showing English in a zh-CN page.
func TestErrInfoRealErrors(t *testing.T) {
	skill := func(id string, mut func(*workflow.Node)) workflow.Node {
		n := workflow.Node{ID: id, Kind: "skill", Prompt: "{{input}}", Join: "all", SkillBindings: []tasksvc.SkillBinding{{SkillID: "s1", Mode: "follow", Role: "primary"}}}
		if mut != nil {
			mut(&n)
		}
		return n
	}
	def := func(mut func(*workflow.Definition)) *workflow.Definition {
		d := &workflow.Definition{Name: "g", MaxParallel: 1, TimeoutMinutes: 10, Nodes: []workflow.Node{skill("a", nil)}}
		mut(d)
		return d
	}
	cases := []struct {
		d      *workflow.Definition
		code   string
		params map[string]string
	}{
		{def(func(d *workflow.Definition) { d.Nodes = nil }), "wf_node_count", map[string]string{"max": "32"}},
		{def(func(d *workflow.Definition) { d.Name = "" }), "wf_name", nil},
		{def(func(d *workflow.Definition) { d.MaxParallel = 9 }), "wf_parallel", nil},
		{def(func(d *workflow.Definition) { d.TimeoutMinutes = 5000 }), "wf_timeout_range", nil},
		{def(func(d *workflow.Definition) { d.Nodes[0].Prompt = "" }), "wf_prompt", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].SkillBindings = nil }), "wf_primary", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].RetryMax = 9 }), "wf_retry", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].Join = "most" }), "wf_join", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].Kind = "loop" }), "wf_kind", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes = append(d.Nodes, skill("a", nil)) }), "wf_node_id", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].Needs = []workflow.Dependency{{NodeID: "zz"}} }), "wf_dependency", map[string]string{"node": "a", "dep": "zz"}},
		{def(func(d *workflow.Definition) {
			d.Nodes[0].Needs = []workflow.Dependency{{NodeID: "b"}}
			d.Nodes = append(d.Nodes, skill("b", func(n *workflow.Node) { n.Needs = []workflow.Dependency{{NodeID: "a"}} }))
		}), "wf_cycle", nil},
		{def(func(d *workflow.Definition) { d.Nodes[0].Prompt = "{{steps.zz.output}}" }), "wf_template_ref", map[string]string{"node": "a", "ref": "steps.zz.output"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].Prompt = "{{input" }), "wf_template", map[string]string{"node": "a"}},
		{def(func(d *workflow.Definition) {
			d.Nodes = append(d.Nodes, skill("b", func(n *workflow.Node) { n.Needs = []workflow.Dependency{{NodeID: "a", Branch: "true"}} }))
		}), "wf_branch_condition", map[string]string{"node": "b"}},
		{def(func(d *workflow.Definition) { d.Nodes[0].Context = &workflow.ContextPolicy{Mode: "everything"} }), "wf_node", map[string]string{"node": "a"}},
	}
	for _, c := range cases {
		err := workflow.Validate(c.d)
		if err == nil {
			t.Errorf("%s: no error", c.code)
			continue
		}
		info := explainError(err.Error())
		if info == nil || info.Code != c.code {
			t.Errorf("%q → %+v, want %s", err, info, c.code)
			continue
		}
		for k, v := range c.params {
			if info.Params[k] != v {
				t.Errorf("%s: param %s = %q, want %q", c.code, k, info.Params[k], v)
			}
		}
	}
	// The wrapped context error is explained too, not left in English.
	if info := explainError(workflow.Validate(cases[len(cases)-1].d).Error()); info.Cause == nil || info.Cause.Code != "wf_context_mode" {
		t.Errorf("context cause %+v", info)
	}

	for _, s := range []error{workflow.ErrNotFound, workflow.ErrConflict, workflow.ErrNotActive, workflow.ErrClosing, workflow.ErrCanceled, workflow.ErrInterrupted, skillstore.ErrSetConflict, context.Canceled, context.DeadlineExceeded} {
		if explainError(s.Error()) == nil {
			t.Errorf("sentinel %q has no code", s)
		}
	}
	setErrs := []error{}
	_, err := tasksvc.NormalizeBindings([]tasksvc.SkillBinding{{SkillID: "a", Role: "primary"}, {SkillID: "b", Role: "primary"}})
	setErrs = append(setErrs, err)
	_, err = tasksvc.NormalizeBindings([]tasksvc.SkillBinding{{SkillID: "a", Mode: "pinned"}})
	setErrs = append(setErrs, err)
	_, err = tasksvc.NormalizeBindings([]tasksvc.SkillBinding{{SkillID: "a"}, {SkillID: "a"}})
	setErrs = append(setErrs, err)
	setErrs = append(setErrs,
		skillstore.ValidateSkillSetDetails(skillstore.SkillSetDetails{TestCases: []skillstore.EvalCase{{Name: "", Input: "x"}}}),
		skillstore.ValidateSkillSetDetails(skillstore.SkillSetDetails{TestCases: []skillstore.EvalCase{{Name: "n", Input: ""}}}),
		skillstore.ValidateSkillSetDetails(skillstore.SkillSetDetails{TestCases: make([]skillstore.EvalCase, 6)}),
		skillstore.ValidateSkillSetDetails(skillstore.SkillSetDetails{Description: strings.Repeat("x", 5000)}),
	)
	for i, err := range setErrs {
		if err == nil {
			t.Errorf("set case %d: no error", i)
		} else if info := explainError(err.Error()); info == nil || !strings.HasPrefix(info.Code, "ss_") {
			t.Errorf("set case %d: %q → %+v", i, err, info)
		}
	}

	// Stored execution messages, including nested "node X: node run failed: …".
	info := explainError("node a: node run failed: claude exited 1")
	if info == nil || info.Code != "wf_node" || info.Params["node"] != "a" || info.Cause == nil || info.Cause.Code != "wf_child_run" ||
		info.Cause.Params["status"] != "failed" || info.Cause.Params["detail"] != "claude exited 1" || info.Cause.Cause != nil {
		t.Errorf("nested %+v / %+v", info, info.Cause)
	}
	for _, m := range []string{"workflow stopped before this node started", "dependency or branch not selected", "output a is unavailable (failed)", "node output exceeds 128 KB; summarize it before passing downstream"} {
		if explainError(m) == nil {
			t.Errorf("stored %q has no code", m)
		}
	}
	// Skill-set evaluations, dependency checks and stable run messages.
	for _, c := range []struct{ msg, code string }{
		{"Tidebot stopped before this evaluation completed; it was not restarted.", "ev_interrupted"},
		{"Provider configuration changed during evaluation; run a fresh comparison.", "ev_provider_changed"},
		{"Evaluation budget exhausted.", "ev_budget_exhausted"},
		{"A comparison run did not succeed; remaining cases were not executed.", "ev_run_failed"},
		{"evaluation budget must be between 0.02 and 5 USD", "ev_budget_range"},
		{"comparison requires primary and auxiliary skills", "ev_two_skills"},
		{"save at least one test case with non-empty assertions before evaluating", "ev_no_cases"},
		{"another skill package evaluation is running; finish or cancel it first", "ev_busy"},
		{"evaluation is not running", "ev_not_running"},
		{"evaluation skill context exceeds its limit", "ev_context_limit"},
		{"evaluation dependency curl is missing: not on PATH", "ev_dependency"},
		{"evaluation preflight: curl: required tool missing", "ev_preflight"},
		{"evaluations unavailable", "ev_unavailable"},
		{"evaluation not found", "ev_not_found"},
		{"dependency report is invalid", "dep_report_invalid"},
		{"dependency check snapshot is unreadable", "dep_snapshot_unreadable"},
		{"provide a prompt or skills to analyze", "dep_need_input"},
		{"dependency checking is unavailable", "dep_unavailable"},
		{"dependency preflight is unavailable", "dep_unavailable"},
		{"stopped: by user", "run_stopped"},
		{"interrupted: tidebot stopped before this run finished", "run_interrupted"},
		{"interrupted: quit: tidebot was quitting and stopped this run", "run_quit"},
		{"skipped: task paused", "run_skip_paused"},
	} {
		info := explainError(c.msg)
		if info == nil || info.Code != c.code {
			t.Errorf("%q → %+v, want %s", c.msg, info, c.code)
		}
	}
	dep := explainError("evaluation dependency curl is blocked: Evaluation permits only Read/Glob/Grep and disables all MCP connectors.")
	if dep == nil || dep.Params["name"] != "curl" || dep.Params["status"] != "blocked" {
		t.Errorf("ev_dependency params %+v", dep)
	}
	for _, m := range []string{"", "claude exited 1", "node", strings.Repeat("node a: ", 600)} {
		if info := explainError(m); info != nil && info.Code != "wf_node" {
			t.Errorf("unknown %q → %+v", m[:min(len(m), 20)], info)
		}
	}
}

// TestWorkflowAPIErrorInfo: refusals and stored node errors carry error_info, and the
// English "error" stays for the CLI and older pages.
func TestWorkflowAPIErrorInfo(t *testing.T) {
	c := login(t)
	c.s.deps.Workflows = workflow.New(c.s.deps.DB, func(ctx context.Context, n workflow.Node, prompt, provider string, _ func(string)) (string, error) {
		if n.ID == "a" {
			return "", errors.New("node run failed: claude exited 1")
		}
		return prompt, nil
	})
	t.Cleanup(func() {
		c.s.deps.Workflows.BeginShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.s.deps.Workflows.Wait(ctx)
	})
	bind := []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}
	bad := workflow.Definition{Name: "g", Nodes: []workflow.Node{{ID: "a", Kind: "skill", SkillBindings: bind}}}
	code, out := c.json(http.MethodPost, "/api/workflows", bad)
	info, _ := out["error_info"].(map[string]any)
	if code != 400 || out["error"] != "node a needs a prompt (max 32 KB)" || info["code"] != "wf_prompt" || info["params"].(map[string]any)["node"] != "a" {
		t.Fatalf("validation %d %v", code, out)
	}
	if code, out = c.json(http.MethodGet, "/api/workflows/wf_missing", nil); code != 404 || out["error_info"].(map[string]any)["code"] != "wf_not_found" {
		t.Fatalf("missing %d %v", code, out)
	}
	d := workflow.Definition{Name: "g", Nodes: []workflow.Node{
		{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: bind},
		{ID: "b", Kind: "skill", Prompt: "{{steps.a.output}}", SkillBindings: bind, Needs: []workflow.Dependency{{NodeID: "a"}}},
	}}
	code, out = c.json(http.MethodPost, "/api/workflows", d)
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	wid := out["id"].(string)
	code, out = c.json(http.MethodPost, "/api/workflows/"+wid+"/run", map[string]string{"input": "x", "provider": "kimi-coding"})
	if code != 202 {
		t.Fatalf("run %d %v", code, out)
	}
	rid := out["run_id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, out = c.json(http.MethodGet, "/api/workflow-runs/"+rid, nil)
		if out["status"] == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ri, _ := out["error_info"].(map[string]any)
	if out["status"] != "failed" || ri["code"] != "wf_node" || ri["cause"].(map[string]any)["code"] != "wf_child_run" {
		t.Fatalf("run error_info %v", out)
	}
	got := map[string]string{}
	for _, n := range out["nodes"].([]any) {
		n := n.(map[string]any)
		if ni, ok := n["error_info"].(map[string]any); ok {
			got[n["node_id"].(string)] = ni["code"].(string)
		}
		if n["error"] == nil || n["status"] == nil {
			t.Fatalf("node fields lost: %v", n)
		}
	}
	if got["a"] != "wf_child_run" || got["b"] != "wf_not_started" {
		t.Fatalf("node codes %v", got)
	}
	code, list := c.do(http.MethodGet, "/api/workflow-runs?workflow_id="+wid, "", nil).Code, c.do(http.MethodGet, "/api/workflow-runs?workflow_id="+wid, "", nil).Body.String()
	if code != 200 || !strings.Contains(list, `"error_info":{"code":"wf_node"`) {
		t.Fatalf("list %d %s", code, list)
	}
	if code, out = c.json(http.MethodPost, "/api/workflow-runs/"+rid+"/stop", nil); code != 409 || out["error_info"].(map[string]any)["code"] != "wf_not_active" {
		t.Fatalf("stop finished %d %v", code, out)
	}
}

// TestErrInfoTextInPage: the page shows codes in its language, explains the nested
// part, translates a status value and falls back to the server text for unknown codes.
func TestErrInfoTextInPage(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function errInfoText(")
	if start < 0 {
		t.Fatal("errInfoText() not found")
	}
	end := start + strings.Index(js[start:], "\n  }\n") + 4
	if !strings.Contains(js, "data && data.error_info ? errInfoText(data.error_info, data.error)") {
		t.Error("api errors don't use error_info")
	}
	for _, s := range []string{"esc(errInfoText(r.error_info,r.error))", "esc(errInfoText(n.error_info,n.error))"} {
		if !strings.Contains(js, s) {
			t.Errorf("execution page misses %s", s)
		}
	}
	for _, s := range []string{
		"esc(runErrText(x.error_info,x.error))",
		"esc(runErrText(r.error_info,r.error)||T('wf.status_'+r.status))",
		"runErrText(r.error_info, r.error)",
		"runErrText(t.error_info, t.error)",
	} {
		if !strings.Contains(js, s) {
			t.Errorf("page misses %s", s)
		}
	}
	raw, err := os.ReadFile("static/i18n/zh-CN.json")
	if err != nil {
		t.Fatal(err)
	}
	all, small := map[string]string{}, map[string]string{}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	for k, v := range all {
		if strings.HasPrefix(k, "wfe.") || strings.HasPrefix(k, "wf.status_") || strings.HasPrefix(k, "dep.req_") {
			small[k] = v
		}
	}
	zh, _ := json.Marshal(small)
	runNode(t, `
var state = { dict: JSON.parse(DICT) };
function T(key, vars) { var s = state.dict[key]; if (s == null) s = key; if (vars) s = s.replace(/\{(\w+)\}/g, function (m, n) { return vars[n] == null ? m : String(vars[n]); }); return s; }
eval(SECTION);
var bad = [];
function eq(got, want) { if (got !== want) bad.push(JSON.stringify(got) + " != " + JSON.stringify(want)); }
eq(errInfoText({ code: "wf_not_started" }, "workflow stopped before this node started"), "工作流停止时这个步骤还没开始。");
eq(errInfoText({ code: "wf_node_count", params: { max: "32" } }, "x"), "工作流需要 1–32 个步骤。");
eq(errInfoText({ code: "wf_node", params: { node: "a", detail: "node run failed: boom" }, cause: { code: "wf_child_run", params: { status: "failed", detail: "boom" } } }, "raw"), "步骤 a：步骤运行失败：boom");
eq(errInfoText({ code: "wf_node", params: { node: "a", detail: "something new" } }, "raw"), "步骤 a：something new");
eq(errInfoText({ code: "brand_new" }, "server text"), "server text");
eq(errInfoText(null, "server text"), "server text");
eq(errInfoText({ code: "wf_template_ref", params: { node: "a", ref: "{node}" } }, "raw"), "步骤 a 引用了 {node}，但它不是上游步骤的输出。");
eq(errInfoText({ code: "ev_budget_exhausted" }, "Evaluation budget exhausted."), "评测预算已用完。");
eq(errInfoText({ code: "dep_report_invalid" }, "dependency report is invalid"), "依赖检查报告格式不符合要求。");
eq(errInfoText({ code: "ev_dependency", params: { name: "curl", status: "missing", detail: "not on PATH" } }, "raw"), "评测依赖 curl 当前为「缺失」：not on PATH");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"SECTION": js[start:end], "DICT": string(zh)})
}

// BenchmarkExplainError: the worst case is a message no pattern knows (every
// pattern is tried); an execution page explains at most 33 messages.
func BenchmarkExplainError(b *testing.B) {
	for _, m := range []string{"workflow stopped before this node started", "node a: node run failed: claude exited with status 1 after rate limit", strings.Repeat("unknown failure text ", 20)} {
		b.Run(m[:12], func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = explainError(m)
			}
		})
	}
}

// TestSkillSetEvalAPIErrorInfo: start refusals and stored case errors carry error_info.
func TestSkillSetEvalAPIErrorInfo(t *testing.T) {
	c := login(t)
	c.s.deps.StartSkillSetEval = func(req SkillSetEvalRequest) (SkillSetEvaluation, error) {
		return SkillSetEvaluation{}, errors.New("evaluation budget must be between 0.02 and 5 USD")
	}
	c.s.deps.GetSkillSetEval = func(ctx context.Context, id string) (SkillSetEvaluation, error) {
		return SkillSetEvaluation{
			ID:     id,
			Status: "failed",
			Error:  "A comparison run did not succeed; remaining cases were not executed.",
			Cases: []EvalCaseResult{{
				Name:     "sample",
				Baseline: &EvalRunResult{Status: "failed", Error: "comparison requires primary and auxiliary skills"},
			}},
		}, nil
	}
	c.s.deps.ListSkillSetEvals = func(ctx context.Context, id string) ([]SkillSetEvaluation, error) {
		return []SkillSetEvaluation{{ID: "eval_err", SetID: id, Error: "Evaluation budget exhausted."}}, nil
	}
	code, out := c.json(http.MethodPost, "/api/skill-set-evals", map[string]any{"set_id": "set_test", "version_id": "ver_test", "provider": "test", "budget_usd": 0.2})
	info, _ := out["error_info"].(map[string]any)
	if code != 400 || out["error"] != "evaluation budget must be between 0.02 and 5 USD" || info["code"] != "ev_budget_range" {
		t.Fatalf("start %d %v", code, out)
	}
	code, out = c.json(http.MethodGet, "/api/skill-set-evals/eval_err", nil)
	info, _ = out["error_info"].(map[string]any)
	if code != 200 || info["code"] != "ev_run_failed" {
		t.Fatalf("get %d %v", code, out)
	}
	cases, _ := out["cases"].([]any)
	if len(cases) != 1 {
		t.Fatalf("cases %v", out)
	}
	base := cases[0].(map[string]any)["baseline"].(map[string]any)
	bi, _ := base["error_info"].(map[string]any)
	if base["error"] != "comparison requires primary and auxiliary skills" || bi["code"] != "ev_two_skills" {
		t.Fatalf("case error_info %v", base)
	}
	code, list := c.do(http.MethodGet, "/api/skill-set-evals?set_id=set_test", "", nil).Code, c.do(http.MethodGet, "/api/skill-set-evals?set_id=set_test", "", nil).Body.String()
	if code != 200 || !strings.Contains(list, `"error_info":{"code":"ev_budget_exhausted"}`) {
		t.Fatalf("list %d %s", code, list)
	}
	if code, out = c.json(http.MethodGet, "/api/skill-set-evals/@", nil); code != 404 || out["error_info"].(map[string]any)["code"] != "ev_not_found" {
		t.Fatalf("missing %d %v", code, out)
	}
}

// TestDependencyCheckAPIErrorInfo: refusals and stored status errors carry error_info.
func TestDependencyCheckAPIErrorInfo(t *testing.T) {
	c := login(t)
	c.s.deps.StartDependencyCheck = func(req DependencyCheckRequest) (string, error) {
		return "", errors.New("dependency check snapshot is too large")
	}
	c.s.deps.DependencyCheckStatus = func(ctx context.Context, id string) (DependencyCheckStatus, error) {
		return DependencyCheckStatus{RunID: id, ChildRunID: id, Status: "failed", Error: "dependency report is invalid"}, nil
	}
	c.s.deps.DependencyPreflight = func(ctx context.Context, req DependencyCheckRequest) (DependencyPreflightReport, error) {
		return DependencyPreflightReport{}, errors.New("provide a prompt or skills to analyze")
	}
	code, out := c.json(http.MethodPost, "/api/dependencies/check", map[string]any{"kind": "nope"})
	info, _ := out["error_info"].(map[string]any)
	if code != 400 || info["code"] != "dep_kind" {
		t.Fatalf("kind %d %v", code, out)
	}
	code, out = c.json(http.MethodPost, "/api/dependencies/check", map[string]any{"kind": "draft", "prompt": "x"})
	info, _ = out["error_info"].(map[string]any)
	if code != 400 || out["error"] != "dependency check snapshot is too large" || info["code"] != "dep_snapshot_size" {
		t.Fatalf("start %d %v", code, out)
	}
	code, out = c.json(http.MethodGet, "/api/dependencies/check/run_dep_err", nil)
	info, _ = out["error_info"].(map[string]any)
	if code != 200 || out["error"] != "dependency report is invalid" || info["code"] != "dep_report_invalid" {
		t.Fatalf("status %d %v", code, out)
	}
	code, out = c.json(http.MethodPost, "/api/dependencies/preflight", map[string]any{"kind": "draft"})
	info, _ = out["error_info"].(map[string]any)
	if code != 400 || info["code"] != "dep_need_input" {
		t.Fatalf("preflight %d %v", code, out)
	}
}

// TestRunAPIErrorInfo: stored run error_message values carry error_info without a DB migration.
func TestRunAPIErrorInfo(t *testing.T) {
	c := login(t)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.s.deps.DB.Exec(`INSERT INTO runs(id,status,trigger_kind,prompt,error_message,created_at,started_at,finished_at) VALUES('run_ei','canceled','quick','p','stopped: by user',?,?,?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	code, out := c.json(http.MethodGet, "/api/runs/run_ei", nil)
	info, _ := out["error_info"].(map[string]any)
	if code != 200 || out["error"] != "stopped: by user" || info["code"] != "run_stopped" {
		t.Fatalf("run error_info %d %v", code, out)
	}
}
