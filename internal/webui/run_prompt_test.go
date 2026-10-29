package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestRunPromptFull: lists carry a 300-byte preview cut on a character boundary (a Chinese
// prompt used to end in U+FFFD); the run detail and "rerun" use the whole prompt — rerunning
// a long quick run used to start it with only the preview plus "…".
func TestRunPromptFull(t *testing.T) {
	c := login(t)
	long := strings.Repeat("整理今天的新闻，", 30) + "\n第二行 ✓ 😀\n<b>结束</b>" // ~760 bytes, 3-byte runes straddle 300
	seedRun(t, c, "rp1", long, "succeeded", 0.01, time.Now())

	var item map[string]any
	for _, r := range listRuns(t, c, "") {
		if r["id"] == "rp1" {
			item = r
		}
	}
	p, _ := item["prompt"].(string)
	if !utf8.ValidString(p) || strings.ContainsRune(p, utf8.RuneError) || !strings.HasSuffix(p, "…") || len(p) > 300+len("…") || !strings.HasPrefix(long, strings.TrimSuffix(p, "…")) {
		t.Fatalf("list preview %q", p)
	}

	rec := c.do(http.MethodGet, "/api/runs/rp1", "", nil)
	var d map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if rec.Code != 200 || d["prompt"] != long {
		t.Fatalf("detail %d %q", rec.Code, d["prompt"])
	}

	var got string
	c.s.deps.QuickRun = func(prompt, prov string) (string, error) { got = prompt; return "rp2", nil }
	if code, out := c.json(http.MethodPost, "/api/runs/rp1/rerun", map[string]any{}); code != 202 || out["run_id"] != "rp2" {
		t.Fatalf("rerun %d %v", code, out)
	}
	if got != long {
		t.Fatalf("rerun prompt %d bytes, want %d", len(got), len(long))
	}
}

// TestRunPromptUI: the details card shows the whole prompt folded to four lines, "show all"
// only when it overflows, copy of the full text, edit/save-as-task fed from the detail, both
// languages, no native dialogs, escaped output.
func TestRunPromptUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`"</dd>" + runPromptHtml(r) + (r.error`,
		`bindRunPrompt(r);`,
		`<div class="rd-prompt" id="rd-prompt">' + esc(p) + "</div>"`,
		`copyText(r.prompt || "").catch(fail)`,
		`box.scrollHeight <= box.clientHeight + 1`,
		`quickRunModal({ prompt: r.prompt, provider: r.provider })`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	i, j := strings.Index(src, "  function runPromptHtml(r) {"), strings.Index(src, "  function ansMode(r) {")
	if i < 0 || j < i {
		t.Fatal("prompt block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML"} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("prompt block uses %s", bad)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".rd-prompt { width: 100%; max-height: 80px;", ".rd-prompt.open { max-height: min(320px, 40vh); overflow: auto; overscroll-behavior: contain; }", "white-space: pre-wrap"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"rd.prompt_meta", "rd.prompt_more", "rd.prompt_less", "rd.prompt_copy"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
