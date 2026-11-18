package webui

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// errInfo is a stable code (+ values) for a server error, so the page can show it in
// the reader's language. The English "error" text stays for the CLI, logs and older
// pages; codes the page doesn't know fall back to that text.
type errInfo struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
	// Cause explains the wrapped part of "node X: …"; when nil, Params["detail"]
	// holds the raw inner text.
	Cause *errInfo `json:"cause,omitempty"`
}

type errPattern struct {
	code  string
	re    *regexp.Regexp
	names []string // one per capture group
}

// Matches the English messages of the workflow and skill-composition packages,
// including the ones already stored on past executions. Anchored, first match wins,
// so specific "node X: …" messages come before the generic wrapper.
var errPatterns = func() []errPattern {
	id := `([A-Za-z0-9_-]{1,80})`
	p := func(code, re string, names ...string) errPattern {
		return errPattern{code, regexp.MustCompile(`^(?:` + re + `)$`), names}
	}
	return []errPattern{
		// workflow service
		p("wf_not_found", `workflow not found`),
		p("wf_conflict", `workflow changed; reload before saving`),
		p("wf_not_active", `workflow run is not active`),
		p("wf_closing", `workflow service is stopping`),
		p("wf_stopped", `workflow stopped by user`),
		p("wf_interrupted", `workflow interrupted by shutdown`),
		p("wf_unavailable", `workflows unavailable|workflow executor unavailable`),
		p("wf_recover_busy", `cannot recover workflows while executions are active`),
		p("wf_bad_json", `invalid (?:workflow|run) JSON`),
		p("wf_unknown_provider", `unknown provider`),
		p("wf_input_size", `workflow input exceeds (\d+) KB`, "kb"),
		// workflow validation
		p("wf_name", `workflow name is required \(max 80 characters\)`),
		p("wf_description", `description exceeds 4096 bytes`),
		p("wf_node_count", `workflow needs 1-(\d+) nodes`, "max"),
		p("wf_parallel", `parallelism must be 1-4`),
		p("wf_timeout_range", `timeout must be 0 \(unlimited\) or 1-1440 minutes`),
		p("wf_retry", `node `+id+`: retry_max must be 0-5 and retry_backoff_ms 0-60000`, "node"),
		p("wf_condition_retry", `condition node `+id+` cannot retry`, "node"),
		p("wf_position", `node `+id+`: canvas position must be within 0-10000`, "node"),
		p("wf_node_id", `invalid or duplicate node id: (.{0,80})`, "node"),
		p("wf_node_name", `node `+id+` name exceeds 80 characters`, "node"),
		p("wf_join", `node `+id+`: join must be all or any`, "node"),
		p("wf_prompt", `node `+id+` needs a prompt \(max 32 KB\)`, "node"),
		p("wf_primary", `node `+id+` needs a primary skill`, "node"),
		p("wf_skill_condition", `skill node `+id+` cannot have a condition`, "node"),
		p("wf_condition", `node `+id+` needs a bounded condition`, "node"),
		p("wf_condition_skills", `condition node `+id+` cannot run skills`, "node"),
		p("wf_operator", `node `+id+`: unknown condition operator`, "node"),
		p("wf_kind", `node `+id+`: kind must be skill or condition`, "node"),
		p("wf_dependency", `node `+id+`: missing, self or duplicate dependency (.{0,80})`, "node", "dep"),
		p("wf_branch_select", `node `+id+`: select true or false branch of `+id, "node", "parent"),
		p("wf_branch_condition", `node `+id+`: branch requires a condition node`, "node"),
		p("wf_cycle", `workflow contains a cycle at `+id, "node"),
		p("wf_template", `node `+id+`: malformed template`, "node"),
		p("wf_template_ref", `node `+id+`: template reference "(.{0,200})" must name an ancestor output`, "node", "ref"),
		p("wf_set_source", `node `+id+`: skill set source version does not belong to set`, "node"),
		p("wf_no_skills", `node `+id+` has no skills`, "node"),
		p("wf_context_skill", `context policy requires a skill node`),
		p("wf_context_mode", `invalid or unauthorized context source (.{0,80})|invalid context mode or summary token limit \(256-65536\)`, "node"),
		// workflow execution (also stored on past runs)
		p("wf_prompt_size", `rendered prompt exceeds (\d+) KB`, "kb"),
		p("wf_template_unknown", `unknown template (.{0,200})`, "ref"),
		p("wf_output_missing", `missing output `+id, "node"),
		p("wf_output_unavailable", `output `+id+` is unavailable \((\w+)\)`, "node", "status"),
		p("wf_context_output", `context output `+id+` is unavailable`, "node"),
		p("wf_output_size", `node output exceeds (\d+) KB; summarize it before passing downstream`, "kb"),
		p("wf_unresolved", `workflow has unresolved dependencies`),
		p("wf_not_selected", `dependency or branch not selected`),
		p("wf_not_started", `workflow stopped before this node started`),
		p("wf_canceled", `context canceled`),
		p("wf_deadline", `workflow timeout|context deadline exceeded`),
		p("wf_panic", `workflow executor panicked`),
		p("wf_child_run", `node run (\w+): (.*)`, "status", "detail"),
		p("wf_record_versions", `cannot record skill versions: (.*)`, "detail"),
		// skill composition
		p("ss_not_found", `skill set not found`),
		p("no_rows", `sql: no rows in result set`),
		p("ss_conflict", `skill set changed; reload before saving`),
		p("ss_name", `skill set name is required \(max 80 characters\)`),
		p("ss_two", `a skill set requires at least two skills`),
		p("ss_no_skillmd", `skill `+id+` has no SKILL.md`, "skill"),
		p("ss_dup_command", `duplicate skill command name "(.{0,200})"`, "name"),
		p("ss_unavailable", `skill `+id+` or its selected version is unavailable: (.*)`, "skill", "detail"),
		p("ss_max_skills", `at most 16 skills may be attached`),
		p("ss_skill_id", `invalid or duplicate skill id: (.{0,80})`, "skill"),
		p("ss_mode", `skill mode must be follow or pinned`),
		p("ss_pinned", `pinned skill requires a version`),
		p("ss_primary", `choose exactly one primary skill`),
		p("ss_role", `skill role must be primary or auxiliary`),
		p("ss_deps_attention", `skill dependencies need attention before use`),
		p("ss_deps_review", `required skill dependencies must be reviewed and added before use`),
		p("ss_source", `invalid skill set source|skill set source version does not belong to set|version does not belong to skill set|invalid composition source state`),
		p("ss_details_size", `package description allows 4 KiB; instructions and input/output hints allow 8 KiB each`),
		p("ss_cases_max", `a package allows at most five test cases`),
		p("ss_case_name", `test case (\d+) needs a name \(max 80 characters\)`, "n"),
		p("ss_case_input", `test case (\d+) needs an input \(max 8 KiB\)`, "n"),
		p("ss_case_assertions", `test case (\d+) needs 1–8 assertions`, "n"),
		p("ss_case_assertion_size", `test case (\d+) assertions must be non-empty \(max 1 KiB each\)`, "n"),
		p("ss_case_contradict", `test case (\d+) has contradictory assertions`, "n"),
		p("ss_export_size", `skill set is too large to export`),
		p("ss_archive_size", `skill set archive exceeds 20 MB`),
		p("ss_archive", `skill-set\.json is missing|unsupported skill set archive|invalid skill set members|invalid skill set name|invalid or oversized archive|cannot read archive`),
		p("ss_archive_pick", `choose a skill set ZIP`),
		// skill-set evaluation (stored on past results too)
		p("ev_unavailable", `evaluations unavailable`),
		p("ev_bad_request", `valid set_id, version_id and evaluation options are required`),
		p("ev_not_found", `evaluation not found`),
		p("ev_budget_range", `evaluation budget must be between 0\.02 and 5 USD`),
		p("ev_two_skills", `comparison requires primary and auxiliary skills`),
		p("ev_no_cases", `save at least one test case with non-empty assertions before evaluating`),
		p("ev_busy", `another skill package evaluation is running; finish or cancel it first`),
		p("ev_not_running", `evaluation is not running`),
		p("ev_interrupted", `Tidebot stopped before this evaluation completed; it was not restarted\.`),
		p("ev_provider_changed", `Provider configuration changed during evaluation; run a fresh comparison\.`),
		p("ev_budget_exhausted", `Evaluation budget exhausted\.`),
		p("ev_run_failed", `A comparison run did not succeed; remaining cases were not executed\.`),
		p("ev_context_limit", `evaluation skill context exceeds its limit`),
		p("ev_dependency", `evaluation dependency (.{0,200}) is (\w+): (.*)`, "name", "status", "detail"),
		p("ev_preflight", `evaluation preflight: (.{0,200}): (.*)`, "name", "detail"),
		// dependency check (API refusals and stored status errors)
		p("dep_unavailable", `dependency checking is unavailable|dependency preflight is unavailable`),
		p("dep_bad_input", `invalid dependency check input`),
		p("dep_one_json", `one JSON object is required`),
		p("dep_not_found", `dependency check not found`),
		p("dep_bad_id", `invalid dependency check id`),
		p("dep_read", `cannot read dependency check`),
		p("dep_not_started", `dependency check did not start`),
		p("dep_start_timeout", `dependency check could not start in time`),
		p("dep_target_id", `a valid target id is required`),
		p("dep_draft_id", `invalid task draft id`),
		p("dep_need_input", `provide a prompt or skills to analyze`),
		p("dep_need_workflow", `workflow definition is required`),
		p("dep_kind", `unsupported dependency check kind`),
		p("dep_input_size", `dependency check input is too large`),
		p("dep_snapshot_size", `dependency check snapshot is too large|workflow dependency snapshot is too large`),
		p("dep_runtime_path", `invalid runtime path`),
		p("dep_mode", `runtime mode must be read, edit or exec`),
		p("dep_execution", `execution must be quick or task`),
		p("dep_tools_max", `too many allowed tool rules`),
		p("dep_tool_rule", `invalid allowed tool rule`),
		p("dep_python_venv", `python_environment must be an absolute virtual environment directory`),
		p("dep_buttons", `cannot read buttons`),
		p("dep_snapshot_unreadable", `dependency check snapshot is unreadable`),
		p("dep_report_invalid", `dependency report is invalid`),
		// stable run error_message values (also shown on the run page and in sessions)
		p("run_quit", `interrupted: quit: tidebot was quitting and stopped this run`),
		p("run_interrupted", `interrupted: tidebot stopped before this run finished`),
		p("run_stopped", `stopped: by user`),
		p("run_skip_paused", `skipped: task paused`),
		// generic wrapper last: "node X: <inner>"
		p("wf_node", `node `+id+`: (.*)`, "node", "detail"),
	}
}()

// explainError maps a known English message to a code; nil when it isn't known.
func explainError(msg string) *errInfo {
	return explainDepth(strings.TrimSpace(msg), 0)
}

func explainDepth(msg string, depth int) *errInfo {
	if msg == "" || depth > 3 || len(msg) > 4096 {
		return nil
	}
	for _, p := range errPatterns {
		m := p.re.FindStringSubmatch(msg)
		if m == nil {
			continue
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
			info.Cause = explainDepth(d, depth+1)
		}
		return info
	}
	if info := explainDepText(msg); info != nil {
		return info
	}
	return explainClassified(msg)
}

// classifiedRunCodes are display-layer codes for leftover provider text after
// catalog patterns don't match. policy.Classify decides the class; "ok" and
// "unknown" stay uncoded so the raw text is shown. Historical run and
// workflow-node errors are classified the same way, without a DB migration.
var classifiedRunCodes = []string{
	"run_acceptance_failed",
	"run_authentication_failed",
	"run_billing_error",
	"run_budget",
	"run_danger_blocked",
	"run_invalid_request",
	"run_network",
	"run_overloaded",
	"run_process_exit",
	"run_rate_limit",
	"run_server_error",
}

func explainClassified(msg string) *errInfo {
	class, _ := policy.Classify(msg)
	if class == "" || class == "ok" || class == "unknown" {
		return nil
	}
	code := "run_" + class
	for _, known := range classifiedRunCodes {
		if known == code {
			return &errInfo{Code: code}
		}
	}
	return nil
}

// writeErrInfo writes {"error": msg} plus "error_info" when the message is known.
func writeErrInfo(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, withErrInfo(map[string]any{"error": msg}))
}

// withErrInfo adds "error_info" to an error payload whose "error" is known.
func withErrInfo(payload map[string]any) map[string]any {
	if msg, ok := payload["error"].(string); ok {
		if info := explainError(msg); info != nil {
			payload["error_info"] = info
		}
	}
	return payload
}
