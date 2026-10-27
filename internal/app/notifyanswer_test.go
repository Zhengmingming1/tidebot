package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/i18n"
)

// A success notice carries the start of the stored answer (chats, bell) and the whole of it
// (email); the task can turn that off.
func TestSuccessNoticeCarriesAnswer(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	i18n.SetLang("zh-CN")
	defer i18n.SetLang("en")
	task, _ := a.EnsureTask(ctx, "日报", "p", "", "", "")
	if !a.Policy.Get(ctx, task.ID).NotifyAnswer {
		t.Fatal("answer is on by default")
	}
	p := a.Policy.Get(ctx, task.ID)
	p.NotifySuccess = true
	if err := a.Policy.Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	answer := "# 今日要闻\n\n- 第一条\n- 第二条\n\n" + strings.Repeat("细节。", 300)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, prompt, created_at) VALUES('run_a1','succeeded','p','2026-10-28T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SetRunResult(ctx, "run_a1", answer); err != nil {
		t.Fatal(err)
	}
	ev := Event{Kind: EventRunFinished, TaskID: task.ID, TaskName: "日报", RunID: "run_a1", CostUSD: 0.01}
	a.attachAnswer(ctx, &ev)
	if ev.Answer != answer {
		t.Fatalf("answer not attached: %q", ev.Answer)
	}
	m, ok := a.EventToMessage(ev)
	if !ok {
		t.Fatal("success notice on")
	}
	ex := m.Params["excerpt"]
	if !strings.HasPrefix(ex, "今日要闻\n\n• 第一条\n• 第二条") || m.Params["cut"] != "1" || m.Params["run"] != "run_a1" {
		t.Fatalf("params %q", m.Params)
	}
	if !strings.HasPrefix(m.Body, "花费 $0.0100\n\n今日要闻") || !strings.HasSuffix(m.Body, "完整回答见 tidebot 运行详情") {
		t.Fatalf("body %q", m.Body)
	}
	if !strings.HasSuffix(m.LongBody, strings.Repeat("细节。", 300)) || !strings.HasPrefix(m.LongBody, "花费 $0.0100\n\n# 今日要闻") {
		t.Fatal("email gets the whole answer")
	}

	// off for this task: cost only, still a link to the run
	p.NotifyAnswer = false
	_ = a.Policy.Put(ctx, p)
	ev2 := Event{Kind: EventRunFinished, TaskID: task.ID, TaskName: "日报", RunID: "run_a1", CostUSD: 0.01}
	a.attachAnswer(ctx, &ev2)
	m, _ = a.EventToMessage(ev2)
	if ev2.Answer != "" || m.Params["excerpt"] != "" || m.LongBody != "" || m.Body != "花费 $0.0100" || m.Params["run"] != "run_a1" {
		t.Fatalf("off: %q %q", m.Body, m.Params)
	}

	// failures link to their run too
	m, _ = a.EventToMessage(Event{Kind: EventRunFailed, TaskID: task.ID, RunID: "run_f1", Detail: "boom"})
	if m.Params["run"] != "run_f1" {
		t.Fatal(m.Params)
	}
}

// policy.set from a caller that doesn't send notify_answer keeps it.
func TestPolicySetKeepsNotifyAnswer(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	task, _ := a.EnsureTask(ctx, "x", "p", "", "", "")
	if _, err := a.rpcPolicySet(ctx, []byte(`{"task_id":"`+task.ID+`","retry_max":1}`)); err != nil {
		t.Fatal(err)
	}
	if !a.Policy.Get(ctx, task.ID).NotifyAnswer {
		t.Fatal("kept")
	}
	if _, err := a.rpcPolicySet(ctx, []byte(`{"task_id":"`+task.ID+`","notify_answer":false}`)); err != nil {
		t.Fatal(err)
	}
	if a.Policy.Get(ctx, task.ID).NotifyAnswer {
		t.Fatal("set off")
	}
}
