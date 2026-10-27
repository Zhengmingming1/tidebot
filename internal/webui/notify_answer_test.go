package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// The task form shows and saves "include the start of the answer"; a form (or an exported
// file) without the field keeps the task's choice.
func TestTaskFormNotifyAnswer(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	ps := &policy.Store{DB: c.s.deps.DB}
	base := map[string]any{"name": "Digest", "prompt": "news", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true, "timeout_minutes": 30}
	with := func(p map[string]any) map[string]any {
		m := map[string]any{"policy": p}
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	code, b := c.json(http.MethodPost, "/api/tasks", with(map[string]any{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true}))
	if code != 201 {
		t.Fatalf("create %d %v", code, b)
	}
	id := b["id"].(string)
	if p := ps.Get(ctx, id); !p.NotifyAnswer || !p.NotifySuccess {
		t.Fatalf("default on: %+v", p)
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if pf := form["form"].(map[string]any)["policy"].(map[string]any); pf["notify_answer"] != true {
		t.Fatalf("form %v", pf)
	}
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, with(map[string]any{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true, "notify_answer": false})); code != 200 {
		t.Fatalf("update %d %v", code, out)
	}
	if ps.Get(ctx, id).NotifyAnswer {
		t.Fatal("turned off")
	}
	_, form = c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if pf := form["form"].(map[string]any)["policy"].(map[string]any); pf["notify_answer"] != false {
		t.Fatalf("form after off %v", pf)
	}
	// an older page without the field keeps it off
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, with(map[string]any{"retry_max": 2, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true})); code != 200 {
		t.Fatal("update without field")
	}
	if p := ps.Get(ctx, id); p.NotifyAnswer || p.RetryMax != 2 {
		t.Fatalf("kept: %+v", p)
	}
}

func TestNotifyAnswerUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`'<label class="check tf-pol-ans"><input type="checkbox" name="notify_answer"' + (p.notify_answer !== false ? " checked" : "")`,
		`notify_answer: f.notify_answer.checked`,
		`T(p.notify_answer === false ? "tf.sum_notify" : "tf.sum_notify_ans")`,
		`var ex = pr.excerpt ? '<p class="nc-ex" title="' + esc(pr.excerpt) + '">' + esc(pr.excerpt) + "</p>" : "";`,
		`/^run_/.test(n.key || "") && /^[\w-]{1,64}$/.test(pr.run || "") ? "#/runs/" + enc(pr.run) : "";`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".notif-center .nc-ex {", "-webkit-line-clamp: 3;", ".tf-pol .tf-pol-checks {"} {
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
		for _, k := range []string{"tf.notify_ans", "tf.notify_ans_tip", "tf.sum_notify_ans"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
