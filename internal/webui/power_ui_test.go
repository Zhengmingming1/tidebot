package webui

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// TestPowerUI: Settings › General has the power card (mode select wired to
// power.settings, reading pill, waiting notice), the task form edits the priority,
// and a run held back for power is explained in both languages. The page's pattern
// must match the message the core stores ("skipped: power saving: <why> <percent>").
func TestPowerUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`powerCardHtml() +`,
		`powerWire();`,
		`var PWR_MODES = ["off", "low", "battery"];`,
		`rpc("power.settings", { mode: sel.value })`,
		`rpc("power.get", {})`,
		`var TF_PRIOS = ["urgent", "normal", "background"];`,
		`priority: f.priority.value`,
		`T("rd.err_skip_power_" + pw[1]`,
		`battery: '<rect`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	i, j := strings.Index(src, "var PWR_MODES"), strings.Index(src, "// ---- notification channels (Settings")
	if i < 0 || j < i {
		t.Fatal("power block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt("} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("power block uses %s", bad)
		}
	}
	m := regexp.MustCompile(`var pw = /(\^skipped: power saving: [^/]*)/\.exec\(msg\)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("power pattern not found in runErr")
	}
	re := regexp.MustCompile(m[1])
	for msg, want := range map[string][2]string{
		"skipped: power saving: low 15":     {"low", "15"},
		"skipped: power saving: battery -1": {"battery", "-1"},
		"skipped: power saving: saver 100":  {"saver", "100"},
		"skipped: power saving: weird 5":    {},
		"skipped: disk full: 1/2":           {},
	} {
		got := re.FindStringSubmatch(msg)
		if want[0] == "" {
			if got != nil {
				t.Errorf("%q matched", msg)
			}
			continue
		}
		if got == nil || got[1] != want[0] || got[2] != want[1] {
			t.Errorf("%q → %v", msg, got)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".pwr-now {") {
		t.Error("styles.css lacks .pwr-now")
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	keys := []string{"pwr.title", "pwr.mode", "pwr.m_off", "pwr.m_low", "pwr.m_battery", "pwr.recommended", "pwr.sub_off", "pwr.sub_low", "pwr.sub_battery",
		"pwr.ac", "pwr.battery", "pwr.saver", "pwr.no_battery", "pwr.unknown", "pwr.now_tip", "pwr.waiting", "pwr.waiting_tip",
		"tf.prio", "tf.pr_urgent", "tf.pr_normal", "tf.pr_background", "tf.prio_tip", "tf.sum_pr_urgent", "tf.sum_pr_background",
		"rd.err_skip_power_battery", "rd.err_skip_power_low", "rd.err_skip_power_saver"}
	for _, k := range keys {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
	for _, k := range []string{"rd.err_skip_power_battery", "rd.err_skip_power_low", "rd.err_skip_power_saver"} {
		if !strings.Contains(en[k], "{p}") || !strings.Contains(zh[k], "{p}") {
			t.Errorf("%s lacks {p}", k)
		}
	}
	for _, k := range []string{"pwr.sub_low", "pwr.waiting"} {
		ph := map[string]string{"pwr.sub_low": "{n}", "pwr.waiting": "{n}"}[k]
		if !strings.Contains(en[k], ph) || !strings.Contains(zh[k], ph) {
			t.Errorf("%s lacks %s", k, ph)
		}
	}
	// Short enough for the select at 1024 px.
	for _, k := range []string{"pwr.m_low", "pwr.m_battery", "pwr.ac", "pwr.battery", "pwr.saver"} {
		if len([]rune(zh[k])) > 8 || len(en[k]) > 26 {
			t.Errorf("%s too long", k)
		}
	}
}

// The task form saves the priority; an older page without it keeps the task's.
func TestTaskFormPriority(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	ps := &policy.Store{DB: c.s.deps.DB}
	pol := func(prio string) map[string]any {
		p := map[string]any{"retry_max": 1, "catchup": "run_once", "catchup_grace_minutes": 360, "notify_success": false}
		if prio != "-" {
			p["priority"] = prio
		}
		return map[string]any{"name": "Nightly", "prompt": "check", "provider": "anthropic", "cron": "0 2 * * *", "enabled": true, "timeout_minutes": 30, "policy": p}
	}
	code, a := c.json(http.MethodPost, "/api/tasks", pol("background"))
	if code != 201 {
		t.Fatalf("create %d %v", code, a)
	}
	id := a["id"].(string)
	if ps.Get(ctx, id).Priority != "background" {
		t.Fatal("priority not saved")
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if form["form"].(map[string]any)["policy"].(map[string]any)["priority"] != "background" {
		t.Fatalf("form lacks priority: %v", form["form"])
	}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, pol("-")); code != 200 || ps.Get(ctx, id).Priority != "background" {
		t.Fatal("update without priority changed it")
	}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, pol("urgent")); code != 200 || ps.Get(ctx, id).Priority != "urgent" {
		t.Fatal("urgent not saved")
	}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, pol("asap")); code != 400 || ps.Get(ctx, id).Priority != "urgent" {
		t.Fatal("bad priority accepted")
	}
}
