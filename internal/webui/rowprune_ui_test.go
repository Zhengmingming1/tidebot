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
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`(v.last.deleted_rows > 0 ? "\n" + T("st.rows", { n: v.last.deleted_rows }) : "")`,
		`tip(T("st.where") + "\n" + T("st.tidy") + "\n"`,
		`(r.report.deleted_rows > 0 ? " · " + T("st.rows", { n: r.report.deleted_rows }) : "")`,
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
	b, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), ".st-total [data-tip]:hover::after, .st-total [data-tip]:focus-visible::after { left: auto; right: -4px; transform: none; top: calc(100% + 6px); bottom: auto; }") {
		t.Error("st-total tooltip should open below")
	}
}

// Settings › Data usage bar includes session folders and Claude Code transcripts.
func TestStorageSessionsUsageUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var ST_PARTS = ["logs", "artifacts", "sessions", "database", "backups"];`,
		`T("st.sessions_tip", { f: x.folders || 0, t: x.transcripts || 0 })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
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
