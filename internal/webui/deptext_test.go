package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDepTextCatalogTranslated: every dependency-text code has zh-CN and en
// copy whose placeholders the code actually carries, and no dep.txt.* key is orphaned.
func TestDepTextCatalogTranslated(t *testing.T) {
	codes := depTextCodes()
	if len(codes) == 0 {
		t.Fatal("no dependency text codes")
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
			text, ok := dict["dep.txt."+code]
			if !ok {
				t.Errorf("%s: missing dep.txt.%s", lang, code)
				continue
			}
			for _, m := range ph.FindAllStringSubmatch(text, -1) {
				if !names[m[1]] {
					t.Errorf("%s dep.txt.%s uses {%s}, which the code doesn't carry", lang, code, m[1])
				}
			}
		}
		for k := range dict {
			if c, ok := strings.CutPrefix(k, "dep.txt."); ok && codes[c] == nil {
				t.Errorf("%s: %s has no server code", lang, k)
			}
		}
	}
}

func TestExplainDepTextEnglishAndHistoricalChinese(t *testing.T) {
	if info := explainDepText(DepAIUnverifiedMissingPrefix + "needs pip"); info == nil || info.Code != "dep_ai_unverified_missing" || info.Params["detail"] != "needs pip" {
		t.Fatalf("english prefix %+v", info)
	}
	if info := explainDepText("AI 推断此依赖可能缺失，尚未实际验证。needs pip"); info == nil || info.Code != "dep_ai_unverified_missing" || info.Params["detail"] != "needs pip" {
		t.Fatalf("chinese prefix %+v", info)
	}
	if info := explainDepText(DepSummaryCombined(3, "check the runtime")); info == nil || info.Code != "dep_summary_combined" || info.Params["n"] != "3" || info.Params["detail"] != "check the runtime" {
		t.Fatalf("english summary %+v", info)
	}
	if info := explainDepText("已结合 3 项确定性检查；请确认运行环境"); info == nil || info.Code != "dep_summary_combined" || info.Params["n"] != "3" || info.Params["detail"] != "请确认运行环境" {
		t.Fatalf("chinese summary %+v", info)
	}
	if info := explainDepText(DepReasonEvalDisabled); info == nil || info.Code != "dep_reason_eval_disabled" {
		t.Fatalf("eval reason %+v", info)
	}
	if info := explainDepText("此依赖在评测环境中被禁用"); info == nil || info.Code != "dep_reason_eval_disabled" {
		t.Fatalf("historical eval reason %+v", info)
	}
	notes := DepNoteScope + " " + DepNoteEvalTools
	got := explainDepNotes(notes)
	if len(got) != 2 || got[0].Code != "dep_note_scope" || got[1].Code != "dep_note_eval_tools" {
		t.Fatalf("english notes %+v", got)
	}
	old := "允许工具列表表示预授权；没有匹配规则不等于禁用。未执行命令、脚本、导入包或联网，未验证服务连通性和凭据有效性。 评测仅允许 Read/Glob/Grep，所有 MCP 关闭。"
	got = explainDepNotes(old)
	if len(got) != 2 || got[0].Code != "dep_note_scope" || got[1].Code != "dep_note_eval_tools" {
		t.Fatalf("chinese notes %+v", got)
	}
	if info := explainDepTitle("Command sandbox"); info == nil || info.Code != "dep_title_sandbox" {
		t.Fatalf("sandbox title %+v", info)
	}
	if info := explainDepTitle("技能 …89abcdef（node:A）"); info == nil || info.Code != "dep_title_skill_id_origin" || info.Params["id"] != "89abcdef" || info.Params["origin"] != "node:A" {
		t.Fatalf("historical skill title %+v", info)
	}
	if info := explainDepTitle(DepSkillFallbackTitle("skill_0123456789abcdef0123456789abcdef") + " (node:A)"); info == nil || info.Code != "dep_title_skill_id_origin" {
		t.Fatalf("english skill title %+v", info)
	}
	if info := explainError("evaluation preflight: Bash: " + DepReasonEvalDisabled); info == nil || info.Code != "ev_preflight" || info.Cause == nil || info.Cause.Code != "dep_reason_eval_disabled" {
		t.Fatalf("preflight error cause %+v", info)
	}
}

func TestAnnotateDependencyReportAndPreflight(t *testing.T) {
	report := annotateReport(&DependencyReport{
		Summary: DepSummaryCombined(1, "confirm Python"),
		Findings: []DependencyFinding{{
			Dependency: DepTitleSandbox,
			Kind:       "runtime",
			Level:      "blocked",
			Reason:     DepAIUnverifiedMissingPrefix + "curl is required",
			Evidence:   "SKILL.md",
			Suggestion: DepSuggestEvalRegularRun,
		}},
	})
	if report.SummaryInfo == nil || report.SummaryInfo.Code != "dep_summary_combined" || report.SummaryInfo.Params["n"] != "1" {
		t.Fatalf("summary info %+v", report.SummaryInfo)
	}
	f := report.Findings[0]
	if f.DependencyInfo == nil || f.DependencyInfo.Code != "dep_title_sandbox" || f.ReasonInfo == nil || f.ReasonInfo.Code != "dep_ai_unverified_missing" || f.ReasonInfo.Params["detail"] != "curl is required" || f.SuggestionInfo == nil || f.SuggestionInfo.Code != "dep_suggest_eval_regular_run" {
		t.Fatalf("finding info %+v", f)
	}
	pf := annotatePreflight(DependencyPreflightReport{
		RuntimeProfile: DependencyRuntimeProfile{Notes: DepNoteScope + " " + DepNoteEvalTools},
		ContextUsage:   []DependencyContextUsage{{EstimateMethod: DepEstimateMethod}},
		Requirements:   []DependencyRequirementResult{{Evidence: "The environment variable name is invalid"}},
		Findings:       []DependencyFinding{{Dependency: "命令沙箱", Reason: "此依赖在评测环境中被禁用", Suggestion: "评测只验证当前只读环境下的文本输出；需要该依赖时请使用常规任务运行"}},
	})
	if len(pf.RuntimeProfile.NotesInfo) != 2 || pf.RuntimeProfile.NotesInfo[1].Code != "dep_note_eval_tools" {
		t.Fatalf("notes info %+v", pf.RuntimeProfile.NotesInfo)
	}
	if pf.ContextUsage[0].EstimateMethodInfo == nil || pf.ContextUsage[0].EstimateMethodInfo.Code != "dep_estimate_method" {
		t.Fatalf("estimate info %+v", pf.ContextUsage[0].EstimateMethodInfo)
	}
	if pf.Requirements[0].EvidenceInfo == nil || pf.Requirements[0].EvidenceInfo.Code != "dep_ev_env_name" {
		t.Fatalf("requirement evidence %+v", pf.Requirements[0].EvidenceInfo)
	}
	if pf.Findings[0].DependencyInfo.Code != "dep_title_sandbox" || pf.Findings[0].ReasonInfo.Code != "dep_reason_eval_disabled" {
		t.Fatalf("historical finding %+v", pf.Findings[0])
	}
}

func TestDependencyCheckAPILocalizesReport(t *testing.T) {
	c := login(t)
	c.s.deps.DependencyCheckStatus = func(ctx context.Context, id string) (DependencyCheckStatus, error) {
		return DependencyCheckStatus{
			RunID:          id,
			Status:         "succeeded",
			Report:         &DependencyReport{Summary: DepSummaryCombined(2, "confirm the runtime"), Findings: []DependencyFinding{{Dependency: DepTitleSandbox, Kind: "runtime", Level: "uncertain", Reason: DepAIUnverifiedMissingPrefix + "bwrap", Evidence: "host", Suggestion: DepSuggestSandboxTrust}}},
			StaticFindings: []DependencyFinding{{Dependency: "命令沙箱", Kind: "runtime", Level: "blocked", Reason: "缺少 bwrap，可执行命令的运行会被拒绝", Evidence: "tidebot 在这台电脑上找不到：bwrap", Suggestion: "在终端运行 something，或改用只读 / 可修改"}},
		}, nil
	}
	code, out := c.json(http.MethodGet, "/api/dependencies/check/run_dep_i18n", nil)
	if code != 200 {
		t.Fatalf("status %d %v", code, out)
	}
	report := out["report"].(map[string]any)
	info := report["summary_info"].(map[string]any)
	if info["code"] != "dep_summary_combined" {
		t.Fatalf("summary_info %v", report)
	}
	findings := report["findings"].([]any)
	reason := findings[0].(map[string]any)["reason_info"].(map[string]any)
	if reason["code"] != "dep_ai_unverified_missing" {
		t.Fatalf("reason_info %v", findings[0])
	}
	static := out["static_findings"].([]any)[0].(map[string]any)
	if static["dependency_info"].(map[string]any)["code"] != "dep_title_sandbox" || static["reason_info"].(map[string]any)["code"] != "dep_sandbox_missing" {
		t.Fatalf("static %v", static)
	}
}

func TestSkillSetEvalAPILocalizesPreflight(t *testing.T) {
	c := login(t)
	c.s.deps.GetSkillSetEval = func(ctx context.Context, id string) (SkillSetEvaluation, error) {
		return SkillSetEvaluation{
			ID:     id,
			Status: "succeeded",
			Preflight: &DependencyPreflightReport{
				RuntimeProfile: DependencyRuntimeProfile{Notes: "允许工具列表表示预授权；没有匹配规则不等于禁用。未执行命令、脚本、导入包或联网，未验证服务连通性和凭据有效性。 评测仅允许 Read/Glob/Grep，所有 MCP 关闭。"},
				Findings:       []DependencyFinding{{Dependency: "curl", Kind: "tool", Level: "uncertain", Reason: DepReasonEvalDisabled, Suggestion: DepSuggestEvalRegularRun}},
			},
		}, nil
	}
	code, out := c.json(http.MethodGet, "/api/skill-set-evals/eval_dep_i18n", nil)
	if code != 200 {
		t.Fatalf("get %d %v", code, out)
	}
	pf := out["preflight"].(map[string]any)
	profile := pf["runtime_profile"].(map[string]any)
	notes, _ := profile["notes_info"].([]any)
	if len(notes) != 2 || notes[1].(map[string]any)["code"] != "dep_note_eval_tools" {
		t.Fatalf("notes_info %v", profile)
	}
	finding := pf["findings"].([]any)[0].(map[string]any)
	if finding["reason_info"].(map[string]any)["code"] != "dep_reason_eval_disabled" {
		t.Fatalf("finding %v", finding)
	}
}

func TestDepTextInPage(t *testing.T) {
	js := appJS(t)
	for _, s := range []string{
		"depText(f.reason_info,f.reason)",
		"depText(f.suggestion_info,f.suggestion)",
		"depText(r.report.summary_info,r.report.summary)",
		"depNotesText(p.notes_info,p.notes||'')",
		"r.preflight?preflightHTML(r.preflight):''",
	} {
		if !strings.Contains(js, s) {
			t.Errorf("page misses %s", s)
		}
	}
	start := strings.Index(js, "\n  function errInfoText(")
	if start < 0 {
		t.Fatal("errInfoText() not found")
	}
	end := strings.Index(js[start:], "\n  function loadLang(")
	if end < 0 {
		t.Fatal("helpers not followed by loadLang")
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
		if strings.HasPrefix(k, "wfe.") || strings.HasPrefix(k, "dep.txt.") || strings.HasPrefix(k, "wf.status_") || strings.HasPrefix(k, "dep.req_") {
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
eq(depText({ code: "dep_summary_combined", params: { n: "2", detail: "确认运行环境" } }, "raw"), "已结合 2 项确定性检查；确认运行环境");
eq(depText({ code: "dep_ai_unverified_missing", params: { detail: "needs curl" } }, "raw"), "AI 推断此依赖可能缺失，尚未实际验证。needs curl");
eq(depText({ code: "dep_reason_eval_disabled" }, "raw"), "此依赖在评测环境中被禁用");
eq(depNotesText([{ code: "dep_note_eval_tools" }], "raw"), "评测仅允许 Read/Glob/Grep，所有 MCP 关闭。");
eq(depText({ code: "dep_title_sandbox" }, "Command sandbox"), "命令沙箱");
eq(errInfoText({ code: "ev_preflight", params: { name: "Bash", detail: "This dependency is disabled in the evaluation environment" }, cause: { code: "dep_reason_eval_disabled" } }, "raw"), "评测前检查未通过（Bash）：此依赖在评测环境中被禁用");
if (bad.length) { console.error(bad.join("\\n")); process.exitCode = 1; }
`, map[string]string{"SECTION": js[start : start+end], "DICT": string(zh)})
}
