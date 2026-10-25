package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestTaskExportImport(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: weekly\ndescription: d\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := c.s.deps.Skills.ImportDir(ctx, "weekly", dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(t.TempDir(), "private-folder")
	code, src := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "日报", "prompt": "汇总昨天的新闻", "provider": "anthropic",
		"cron": "0 9 * * *", "enabled": true, "budget_usd": 2.5, "timeout_minutes": 45, "skill_id": sk.ID, "workspace": ws,
		"policy": map[string]any{"retry_max": 1, "catchup": "skip", "catchup_grace_minutes": 60, "notify_success": true, "priority": "urgent"}})
	if code != 201 {
		t.Fatalf("create %d %v", code, src)
	}
	sid := src["id"].(string)
	cur, _ := c.s.deps.Tasks.Get(ctx, sid)
	sp := *cur.Spec
	sp.Constraints = []tasksvc.SpecConstraint{{ID: "c1", Text: "附上来源链接"}}
	sp.AllowedTools = []string{"Read", "WebFetch"}
	if _, err := c.s.deps.Tasks.UpdateSpec(ctx, sid, sp, "user", "amend"); err != nil {
		t.Fatal(err)
	}
	_, other := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "周报", "prompt": "写周报"})

	// export one task: a file without the working folder, ids or history
	rr := c.do(http.MethodGet, "/api/tasks/export?ids="+sid, "", nil)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Disposition"), "tidebot-tasks-") {
		t.Fatalf("export %d %q", rr.Code, rr.Header().Get("Content-Disposition"))
	}
	raw := rr.Body.String()
	if strings.Contains(raw, "private-folder") || strings.Contains(raw, sid) || strings.Contains(raw, "workspace") {
		t.Fatalf("export leaks local data: %s", raw)
	}
	var f taskFile
	if err := json.Unmarshal(rr.Body.Bytes(), &f); err != nil || f.Kind != taskFileKind || f.Version != 1 || len(f.Tasks) != 1 {
		t.Fatalf("file %v %+v", err, f)
	}
	e := f.Tasks[0]
	if e.Name != "日报" || e.Cron != "0 9 * * *" || !e.Enabled || e.BudgetUSD != 2.5 || e.TimeoutMinutes != 45 || e.Provider != "anthropic" ||
		e.Skill == nil || e.Skill.Name != "weekly" || len(e.Constraints) != 1 || len(e.AllowedTools) != 2 || e.Policy == nil || e.Policy.Priority != "urgent" {
		t.Fatalf("entry %+v", e)
	}
	// all tasks
	rr = c.do(http.MethodGet, "/api/tasks/export", "", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &f)
	if len(f.Tasks) != 2 {
		t.Fatalf("all %d", len(f.Tasks))
	}
	if rr := c.do(http.MethodGet, "/api/tasks/export?ids=task_nope", "", nil); rr.Code != 404 {
		t.Fatalf("unknown id %d", rr.Code)
	}

	// preview: nothing written; taken names numbered, also within the file
	var one taskFile
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/tasks/export?ids="+sid, "", nil).Body.Bytes(), &one)
	one.Tasks[0].Provider = "gone-provider"
	one.Tasks = append(one.Tasks, one.Tasks[0], taskFileEntry{Name: "坏的", Prompt: "x", Cron: "every day"}, taskFileEntry{Prompt: "没有名字的任务\n第二行"})
	before, _ := c.s.deps.Tasks.List(ctx)
	code, pv := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": one})
	after, _ := c.s.deps.Tasks.List(ctx)
	if code != 200 || len(after) != len(before) || pv["created"].(float64) != 0 {
		t.Fatalf("preview %d %v", code, pv)
	}
	rows := pv["tasks"].([]any)
	r0, r1, r2, r3 := rows[0].(map[string]any), rows[1].(map[string]any), rows[2].(map[string]any), rows[3].(map[string]any)
	if r0["name"] != "日报 2" || r0["from"] != "日报" || !hasNote(r0, "renamed") || !hasNote(r0, "provider_missing") || hasNote(r0, "skill_missing") {
		t.Fatalf("row0 %v", r0)
	}
	if r1["name"] != "日报 3" || r2["error"] == nil || r3["name"] != "没有名字的任务" || r3["error"] != nil {
		t.Fatalf("rows %v %v %v", r1, r2, r3)
	}

	// apply only the first row: arrives paused, everything else carried over
	c.json(http.MethodDelete, "/api/tasks/"+other["id"].(string), nil)
	code, res := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": one, "apply": true, "pick": []int{0}})
	if code != 200 || res["created"].(float64) != 1 {
		t.Fatalf("apply %d %v", code, res)
	}
	nid := res["tasks"].([]any)[0].(map[string]any)["task_id"].(string)
	nv, _ := c.s.deps.Tasks.Get(ctx, nid)
	if nv.Task.Name != "日报 2" || nv.Task.Status != "paused" || nv.Task.CronExpr != "0 9 * * *" || nv.Spec.Provider != "" ||
		len(nv.Spec.Constraints) != 1 || nv.Spec.Constraints[0].ID == "c1" || len(nv.Spec.AllowedTools) != 2 || nv.Spec.TimeoutMinutes != 45 ||
		len(nv.Spec.SkillBindings) != 1 || nv.Spec.SkillBindings[0].SkillID != sk.ID || nv.Spec.WorkspaceDir != "" {
		t.Fatalf("imported %+v %+v", nv.Task, nv.Spec)
	}
	if p := (&policy.Store{DB: c.s.deps.DB}).Get(ctx, nid); p.Priority != "urgent" || p.RetryMax != 1 || p.Catchup != "skip" || !p.NotifySuccess {
		t.Fatalf("policy %+v", p)
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+nid+"/form", nil)
	if form["form"].(map[string]any)["budget_usd"].(float64) != 2.5 {
		t.Fatalf("budget %v", form)
	}

	// enable=true keeps the file's on/off; a skill unknown here is dropped with a note
	one.Tasks = one.Tasks[:1]
	one.Tasks[0].Skill = &taskFileSkill{ID: "skill_other_machine", Name: "nope"}
	_, res = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": one, "apply": true, "enable": true})
	r := res["tasks"].([]any)[0].(map[string]any)
	if !hasNote(r, "skill_missing") {
		t.Fatalf("skill note %v", r)
	}
	ev, _ := c.s.deps.Tasks.Get(ctx, r["task_id"].(string))
	if ev.Task.Status != "active" || len(ev.Spec.SkillBindings) != 0 || ev.Task.NextRunAt.String == "" {
		t.Fatalf("enabled import %+v", ev.Task)
	}

	// bad files
	for _, bad := range []any{nil, map[string]any{"kind": "x", "version": 1, "tasks": []any{}}, map[string]any{"kind": taskFileKind, "version": 9, "tasks": []any{map[string]any{"name": "a", "prompt": "b"}}},
		map[string]any{"kind": taskFileKind, "version": 1, "tasks": []any{}}} {
		if code, _ := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": bad}); code != 400 {
			t.Fatalf("bad file %v → %d", bad, code)
		}
	}
	// CSRF required
	c2 := *c
	c2.csrf = "wrong"
	if rr := c2.do(http.MethodPost, "/api/tasks/import", "application/json", []byte(`{"file":{}}`)); rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
}

func hasNote(r map[string]any, n string) bool {
	ns, _ := r["notes"].([]any)
	for _, x := range ns {
		if x == n {
			return true
		}
	}
	return false
}

func TestNumberedName(t *testing.T) {
	taken := map[string]bool{"a": true, "a 2": true}
	if n := numberedName("a", taken); n != "a 3" {
		t.Fatal(n)
	}
	long := strings.Repeat("长", 80)
	if n := numberedName(long, map[string]bool{long: true}); len([]rune(n)) != 80 || !strings.HasSuffix(n, " 2") {
		t.Fatal(n)
	}
}

func TestTaskTransferUI(t *testing.T) {
	src, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	for _, want := range []string{`id="t-import"`, `id="t-export"`, `id="tb-export"`, `"/api/tasks/export"`, `"/api/tasks/import"`, "function taskImportModal", `kind !== "tidebot.tasks"`, "data-noguard data-tx"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, want := range []string{".tx-list", ".tag.warn", ".drop.compact"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css missing %q", want)
		}
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, n := range []string{"renamed", "provider", "skill"} {
		for _, k := range []string{"tx.n_" + n, "tx.n_" + n + "_tip"} {
			if en[k] == "" || zh[k] == "" {
				t.Errorf("missing %s", k)
			}
		}
	}
	// labels on the page head stay short enough for a 1024-wide window
	for _, k := range []string{"tx.import", "tx.export"} {
		if len([]rune(zh[k])) > 4 || len(en[k]) > 8 {
			t.Errorf("label %s too long", k)
		}
	}
}
