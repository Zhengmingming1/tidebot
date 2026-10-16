package app

import (
	"context"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/policy"
)

func TestEventToMessage(t *testing.T) {
	a := newTestApp(t)
	i18n.SetLang("zh-CN")
	defer i18n.SetLang("en")
	task, _ := a.EnsureTask(context.Background(), "备份", "p", "", "", "")
	if _, ok := a.EventToMessage(Event{Kind: EventRunFinished, TaskID: task.ID, TaskName: "备份"}); ok {
		t.Fatal("success silent by default")
	}
	_ = a.Policy.Put(context.Background(), policy.Policy{TaskID: task.ID, NotifySuccess: true})
	m, ok := a.EventToMessage(Event{Kind: EventRunFinished, TaskID: task.ID, TaskName: "备份", CostUSD: 0.01})
	if !ok || m.Title != "运行完成：备份" {
		t.Fatal(m.Title)
	}
	m, _ = a.EventToMessage(Event{Kind: EventBreakerTripped, TaskName: "备份"})
	if !m.Urgent || m.Title != "连续失败已暂停：备份" {
		t.Fatal(m)
	}
}
