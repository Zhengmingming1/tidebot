package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// The task form shows and saves "only when the answer changed"; a form (or an exported file)
// without the field keeps the task's choice; the export carries it.
func TestTaskFormNotifyChanged(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	ps := &policy.Store{DB: c.s.deps.DB}
	base := map[string]any{"name": "Watch", "prompt": "check the page", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true, "timeout_minutes": 30}
	with := func(p map[string]any) map[string]any {
		m := map[string]any{"policy": p}
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	code, b := c.json(http.MethodPost, "/api/tasks", with(map[string]any{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true, "notify_changed": true}))
	if code != 201 {
		t.Fatalf("create %d %v", code, b)
	}
	id := b["id"].(string)
	if p := ps.Get(ctx, id); !p.NotifyChanged || !p.NotifyAnswer {
		t.Fatalf("saved: %+v", p)
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if pf := form["form"].(map[string]any)["policy"].(map[string]any); pf["notify_changed"] != true {
		t.Fatalf("form %v", pf)
	}
	// an older page without the field keeps it on
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, with(map[string]any{"retry_max": 2, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true})); code != 200 {
		t.Fatal("update without field")
	}
	if p := ps.Get(ctx, id); !p.NotifyChanged || p.RetryMax != 2 {
		t.Fatalf("kept: %+v", p)
	}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, with(map[string]any{"retry_max": 2, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true, "notify_changed": false})); code != 200 || ps.Get(ctx, id).NotifyChanged {
		t.Fatal("turned off")
	}
}

// The run detail names the earlier run with the same answer (and when it ran); a dangling
// id (that run was cleaned up) shows nothing.
func TestRunAnswerSameAPI(t *testing.T) {
	c := login(t)
	now := time.Now()
	seedRun(t, c, "as1", "digest", "succeeded", 0.01, now.Add(-time.Hour))
	seedRun(t, c, "as2", "digest", "succeeded", 0.01, now)
	seedRun(t, c, "as3", "digest", "succeeded", 0.01, now)
	db := c.s.deps.DB
	_, _ = db.Exec(`UPDATE runs SET result_text='same', answer_same_as='as1' WHERE id='as2'`)
	_, _ = db.Exec(`UPDATE runs SET result_text='same', answer_same_as='gone' WHERE id='as3'`)
	get := func(id string) map[string]any {
		rec := c.do(http.MethodGet, "/api/runs/"+id, "", nil)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	s, ok := get("as2")["answer_same"].(map[string]any)
	if !ok || s["run_id"] != "as1" || s["at"] != now.Add(-time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("as2 %v", get("as2"))
	}
	if _, ok := get("as3")["answer_same"]; ok {
		t.Fatal("dangling id shown")
	}
	if _, ok := get("as1")["answer_same"]; ok {
		t.Fatal("as1 has none")
	}
}

func TestNotifyChangedUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`<input type="checkbox" name="notify_changed"' + (p.notify_changed ? " checked" : "") + (p.notify_success ? "" : " disabled")`,
		`notify_changed: f.notify_changed.checked`,
		`c.disabled = !f.notify_success.checked; c.parentNode.classList.toggle("off", c.disabled);`,
		`if (p.notify_success && p.notify_changed) parts.push(T("tf.sum_changed"));`,
		`'<a class="pill plain sm rd-ans-same" href="#/runs/' + enc(r.answer_same.run_id) + '" data-tip="' + esc(T("rd.ans_same_tip", { t: when(r.answer_same.at) }))`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".tf-pol .tf-pol-ans.off {", ".rd-ans .rd-ans-same {"} {
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
		for _, k := range []string{"tf.notify_changed", "tf.notify_changed_tip", "tf.sum_changed", "rd.ans_same"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["rd.ans_same_tip"], "{t}") {
			t.Errorf("%s rd.ans_same_tip lacks {t}", f)
		}
	}
}
