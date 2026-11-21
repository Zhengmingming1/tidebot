package webui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Settings › Data: the size tooltip says which database rows are trimmed automatically,
// and the last cleanup (tooltip and toast) says how many went.
func TestStorageRowPruneUI(t *testing.T) {
	b := []byte(appJSSource(t))
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`(v.last.deleted_rows > 0 ? "\n" + T("st.rows", { n: v.last.deleted_rows }) : "")`,
		`tip(T("st.where") + "\n" + T("st.tidy") + "\n"`,
		`if (rows) parts.push(T("st.rows", { n: rows }))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !strings.Contains(m["st.rows"], "{n}") || m["st.tidy"] == "" {
			t.Errorf("%s: st.rows / st.tidy missing", f)
		}
		if strings.Contains(m["st.where"], "only") || strings.Contains(m["st.where"], "只删除") {
			t.Errorf("%s: st.where still says cleanup touches only files", f)
		}
	}
}

// The storage size tooltip is several lines and its card is the first on the page, so it
// opens downward instead of past the top of the window.
func TestStorageTotalTipOpensDown(t *testing.T) {
	b := []byte(cssSource(t))
	if !strings.Contains(string(b), ".st-total [data-tip]:hover::after, .st-total [data-tip]:focus-visible::after { left: auto; right: -4px; transform: none; top: calc(100% + 6px); bottom: auto; }") {
		t.Error("st-total tooltip should open below")
	}
}

// Settings › Data usage bar includes session folders and Claude Code transcripts.
func TestStorageSessionsUsageUI(t *testing.T) {
	b := []byte(appJSSource(t))
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var ST_PARTS = ["logs", "artifacts", "sessions", "capabilities", "database", "backups"];`,
		`T("st.sessions_tip", { f: x.folders || 0, t: x.transcripts || 0 })`,
		`T("st.capabilities_tip", { tools: fmtBytes(x.tools || 0), env: fmtBytes(x.environments || 0), cache: fmtBytes(x.cache || 0), snap: fmtBytes(x.snapshots || 0) })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css := []byte(cssSource(t))
	if !strings.Contains(string(css), ".s-sessions { background: var(--hue-teal); }") {
		t.Error("styles.css lacks .s-sessions")
	}
	if !strings.Contains(string(css), ".s-capabilities { background: var(--hue-violet); }") {
		t.Error("styles.css lacks .s-capabilities")
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if m["st.p_sessions"] == "" || !strings.Contains(m["st.sessions_tip"], "{f}") || !strings.Contains(m["st.sessions_tip"], "{t}") {
			t.Errorf("%s: st.p_sessions / st.sessions_tip missing", f)
		}
		if m["st.p_capabilities"] == "" || !strings.Contains(m["st.capabilities_tip"], "{tools}") || !strings.Contains(m["st.capabilities_tip"], "{env}") || !strings.Contains(m["st.capabilities_tip"], "{cache}") || !strings.Contains(m["st.capabilities_tip"], "{snap}") {
			t.Errorf("%s: st.p_capabilities / st.capabilities_tip missing", f)
		}
		where, confirm := m["st.where"], m["st.clean_confirm"]
		if f == "static/i18n/en.json" {
			for _, w := range []string{"session", "transcript", "scratch", "draft", "extension"} {
				if !strings.Contains(where, w) && !strings.Contains(confirm, w) {
					t.Errorf("%s: st.where/st.clean_confirm missing %q: where=%q confirm=%q", f, w, where, confirm)
				}
			}
		} else {
			for _, w := range []string{"会话", "转录", "临时", "草稿", "扩展"} {
				if !strings.Contains(where, w) && !strings.Contains(confirm, w) {
					t.Errorf("%s: st.where/st.clean_confirm missing %q: where=%q confirm=%q", f, w, where, confirm)
				}
			}
		}
	}
}

// Unused empty chats are part of Settings › Data cleanup: preview, confirm, and
// the log-days subtitle all mention them; the button is enabled when only
// sessions (no files) would be removed.
func TestStorageEmptyChatCleanupUI(t *testing.T) {
	b := []byte(appJSSource(t))
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var sess = pv.sessions || 0, rows = pv.rows || 0;`,
		`T("st.clean_sub_sessions", args)`,
		`T("st.clean_confirm_sessions", args)`,
		`cl.disabled = !(pv.files || sess || rows);`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{"st.log_days_sub", "st.where", "st.clean_confirm", "st.clean_confirm_both", "st.clean_confirm_sessions", "st.clean_sub_both", "st.clean_sub_sessions", "st.clean_title_sessions"} {
			if m[k] == "" {
				t.Errorf("%s: missing %s", f, k)
			}
		}
		if f == "static/i18n/en.json" {
			if !strings.Contains(m["st.log_days_sub"], "empty chat") {
				t.Errorf("%s: st.log_days_sub should mention empty chats: %q", f, m["st.log_days_sub"])
			}
		} else if !strings.Contains(m["st.log_days_sub"], "空会话") {
			t.Errorf("%s: st.log_days_sub should mention empty chats: %q", f, m["st.log_days_sub"])
		}
	}
}

// Settings › Data exposes the workflow execution history keep period next to
// the run-log / artifact day controls (default 90 days, 0 = forever).
func TestStorageWorkflowRunDaysUI(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	for _, want := range []string{
		`r("workflow_run_days", T("st.workflow_run_days_sub"), sel("st-wfrun", "workflow_run_days"))`,
		`$("#st-wfrun").innerHTML = stOpts(ST_DAYS, p.workflow_run_days, day);`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if m["st.workflow_run_days"] == "" || m["st.workflow_run_days_sub"] == "" {
			t.Errorf("%s: missing workflow run days strings", f)
		}
		sub := m["st.workflow_run_days_sub"]
		if f == "static/i18n/en.json" {
			for _, w := range []string{"workflow execution history", "node outputs", "90", "forever"} {
				if !strings.Contains(sub, w) {
					t.Errorf("%s: st.workflow_run_days_sub missing %q: %q", f, w, sub)
				}
			}
		} else {
			for _, w := range []string{"工作流", "节点输出", "90", "永久"} {
				if !strings.Contains(sub, w) {
					t.Errorf("%s: st.workflow_run_days_sub missing %q: %q", f, w, sub)
				}
			}
		}
	}
}

// Settings › Data "Clean up now" previews pruneable history rows (workflow
// runs, old notices, etc.), mentions them in the subtitle and confirm, and
// enables the button when only rows would be deleted.
func TestStorageCleanupPreviewRowsUI(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	for _, want := range []string{
		`var sess = pv.sessions || 0, rows = pv.rows || 0;`,
		`cl.disabled = !(pv.files || sess || rows);`,
		`T("st.clean_sub_rows", args)`,
		`T("st.clean_sub_files_rows", args)`,
		`T("st.clean_sub_sessions_rows", args)`,
		`T("st.clean_sub_all", args)`,
		`T("st.clean_confirm_rows", args)`,
		`T("st.clean_confirm_files_rows", args)`,
		`T("st.clean_confirm_sessions_rows", args)`,
		`T("st.clean_confirm_all", args)`,
		`T("st.clean_title_rows")`,
		`T("st.clean_title_data")`,
		`function storageCleanSub(pv)`,
		`function storageCleanConfirm(pv)`,
		`confirmDialog({ title: title, body: body + "\n" + T(keep ? "st.clean_keeps" : "st.clean_keeps0", { k: keep }), ok: T("st.clean_btn"), danger: true })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{
			"st.clean_sub_rows", "st.clean_sub_files_rows", "st.clean_sub_sessions_rows", "st.clean_sub_all",
			"st.clean_confirm_rows", "st.clean_confirm_files_rows", "st.clean_confirm_sessions_rows", "st.clean_confirm_all",
			"st.clean_title_rows", "st.clean_title_data",
		} {
			if m[k] == "" {
				t.Errorf("%s: missing %s", f, k)
			}
			if strings.HasPrefix(k, "st.clean_sub") || strings.HasPrefix(k, "st.clean_confirm") {
				if (strings.Contains(k, "rows") || strings.HasSuffix(k, "_all")) && !strings.Contains(m[k], "{r}") {
					t.Errorf("%s: %s should interpolate {r}: %q", f, k, m[k])
				}
			}
		}
		if f == "static/i18n/en.json" {
			for _, k := range []string{"st.clean_sub_rows", "st.clean_confirm_rows"} {
				for _, w := range []string{"history", "workflow"} {
					if !strings.Contains(m[k], w) {
						t.Errorf("%s: %s missing %q: %q", f, k, w, m[k])
					}
				}
			}
		} else {
			for _, k := range []string{"st.clean_sub_rows", "st.clean_confirm_rows"} {
				for _, w := range []string{"历史记录", "工作流"} {
					if !strings.Contains(m[k], w) {
						t.Errorf("%s: %s missing %q: %q", f, k, w, m[k])
					}
				}
			}
		}
	}
}
