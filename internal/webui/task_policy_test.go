package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
)

func TestTaskFormPolicy(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	ps := &policy.Store{DB: c.s.deps.DB}
	base := map[string]any{"name": "Nightly", "prompt": "check", "provider": "anthropic", "cron": "0 2 * * *", "enabled": true, "timeout_minutes": 30}

	// Created without a policy: defaults, and the form shows them.
	_, a := c.json(http.MethodPost, "/api/tasks", base)
	aid := a["id"].(string)
	_, form := c.json(http.MethodGet, "/api/tasks/"+aid+"/form", nil)
	d := policy.Default(aid)
	pf := form["form"].(map[string]any)["policy"].(map[string]any)
	if pf["retry_max"].(float64) != float64(d.RetryMax) || pf["catchup"] != d.Catchup || pf["catchup_grace_minutes"].(float64) != float64(d.CatchupGraceMinutes) || pf["notify_success"] != false {
		t.Fatalf("default policy %v", pf)
	}
	if form["fail_streak"].(float64) != 0 || form["breaker_at"].(float64) != policy.BreakerThreshold {
		t.Fatalf("streak %v %v", form["fail_streak"], form["breaker_at"])
	}

	// Created with one.
	withPol := func(p map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["policy"] = p
		return m
	}
	code, b := c.json(http.MethodPost, "/api/tasks", withPol(map[string]any{"retry_max": 0, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true}))
	if code != 201 {
		t.Fatalf("create %d %v", code, b)
	}
	bid := b["id"].(string)
	if p := ps.Get(ctx, bid); p.RetryMax != 0 || p.Catchup != "skip" || p.CatchupGraceMinutes != 60 || !p.NotifySuccess {
		t.Fatalf("created policy %+v", p)
	}

	// Edit keeps priority, tools and the failure count; leaving policy out keeps it all.
	p := ps.Get(ctx, aid)
	p.Priority, p.AllowedTools = "urgent", "Read"
	if err := ps.Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := ps.RecordOutcome(ctx, aid, false); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := c.json(http.MethodPut, "/api/tasks/"+aid, withPol(map[string]any{"retry_max": 5, "catchup": "run_all", "catchup_grace_minutes": 4320, "notify_success": true})); code != 200 {
		t.Fatalf("update %d %v", code, out)
	}
	got := ps.Get(ctx, aid)
	if got.RetryMax != 5 || got.Catchup != "run_all" || got.CatchupGraceMinutes != 4320 || !got.NotifySuccess ||
		got.Priority != "urgent" || got.AllowedTools != "Read" || got.ConsecutiveFailures != 2 {
		t.Fatalf("updated policy %+v", got)
	}
	_, form = c.json(http.MethodGet, "/api/tasks/"+aid+"/form", nil)
	if form["fail_streak"].(float64) != 2 {
		t.Fatalf("streak %v", form["fail_streak"])
	}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+aid, base); code != 200 {
		t.Fatal("update without policy")
	}
	if again := ps.Get(ctx, aid); again != got {
		t.Fatalf("policy changed without policy field: %+v", again)
	}

	// Bad values are a 400 and change nothing.
	for _, bad := range []map[string]any{
		{"retry_max": 6, "catchup": "run_once", "catchup_grace_minutes": 60},
		{"retry_max": -1, "catchup": "run_once", "catchup_grace_minutes": 60},
		{"retry_max": 1, "catchup": "run_once'; DROP TABLE task_policies; --", "catchup_grace_minutes": 60},
		{"retry_max": 1, "catchup": "", "catchup_grace_minutes": 60},
		{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 0},
		{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 7*24*60 + 1},
	} {
		if code, out := c.json(http.MethodPut, "/api/tasks/"+aid, withPol(bad)); code != 400 {
			b, _ := json.Marshal(bad)
			t.Fatalf("%s: %d %v", b, code, out)
		}
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks", withPol(map[string]any{"retry_max": 9, "catchup": "skip", "catchup_grace_minutes": 60})); code != 400 {
		t.Fatal("bad create accepted")
	}
	if again := ps.Get(ctx, aid); again != got {
		t.Fatalf("bad update changed policy: %+v", again)
	}
}

func TestTaskFormPolicyUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	d := policy.Default("x")
	for _, want := range []string{
		`function policyFieldsHtml(p, streak, brkAt) {`,
		`wsFieldHtml(tf.workspace || "") + policyFieldsHtml(pol, streak, brkAt) +`,
		`wirePolicyFields(root, f);`,
		`policy: readPolicyFields(f), tags: tgs.value() };`,
		`f.catchup_grace_minutes.disabled = f.catchup.value === "skip";`,
		// new-task defaults mirror policy.Default
		`{ retry_max: ` + itoa(d.RetryMax) + `, catchup: "` + d.Catchup + `", catchup_grace_minutes: ` + itoa(d.CatchupGraceMinutes) + `, notify_success: false, notify_answer: ` + strconv.FormatBool(d.NotifyAnswer) + `, notify_changed: ` + strconv.FormatBool(d.NotifyChanged) + `, priority: "` + d.Priority + `" }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	i := strings.Index(src, "var GRACE_MIN = ")
	j := strings.Index(src, "function taskFormModal(")
	if i < 0 || j < i {
		t.Fatal("policy block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt("} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("policy fields use %s", bad)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{".tf-pol {", ".tf-pol-sum {"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, _ := os.ReadFile("static/i18n/" + lang + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"tf.pol", "tf.pol_tip", "tf.retry", "tf.retry_tip", "tf.retry_0", "tf.retry_n", "tf.catchup", "tf.catchup_tip",
			"tf.cu_skip", "tf.cu_once", "tf.cu_all", "tf.grace", "tf.grace_tip", "tf.grace_m", "tf.grace_h", "tf.grace_d",
			"tf.notify_ok", "tf.notify_ok_tip", "tf.streak", "tf.sum_retry", "tf.sum_noretry", "tf.sum_skip", "tf.sum_once", "tf.sum_all", "tf.sum_notify"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", lang, k)
			}
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
