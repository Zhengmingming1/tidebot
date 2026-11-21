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
		`var ST_PARTS = ["logs", "artifacts", "sessions", "database", "backups"];`,
		`T("st.sessions_tip", { f: x.folders || 0, t: x.transcripts || 0 })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css := []byte(cssSource(t))
	if !strings.Contains(string(css), ".s-sessions { background: var(--hue-teal); }") {
		t.Error("styles.css lacks .s-sessions")
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
		where, confirm := m["st.where"], m["st.clean_confirm"]
		if f == "static/i18n/en.json" {
			for _, w := range []string{"session", "transcript", "scratch", "draft"} {
				if !strings.Contains(where, w) && !strings.Contains(confirm, w) {
					t.Errorf("%s: st.where/st.clean_confirm missing %q: where=%q confirm=%q", f, w, where, confirm)
				}
			}
		} else {
			for _, w := range []string{"会话", "转录", "临时", "草稿"} {
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
		`var sess = pv.sessions || 0;`,
		`T("st.clean_sub_sessions", { c: sess })`,
		`T("st.clean_confirm_sessions", { c: sess })`,
		`cl.disabled = !(pv.files || sess);`,
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
