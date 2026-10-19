package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/i18n"
)

func TestEventToMessageLocalizedAndStructured(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defer i18n.SetLang("en")
	_ = a.Auth.SetSetting(ctx, "language", `"zh-CN"`)
	m, ok := a.EventToMessage(Event{Kind: EventRunFailed, TaskName: "备份", Detail: "exit 1"})
	if !ok || !strings.Contains(m.Title, "运行失败") {
		t.Fatalf("title not localized: %q", m.Title)
	}
	if m.Key != "run_failed" || m.Params["name"] != "备份" || m.Params["detail"] != "exit 1" {
		t.Fatalf("structured %+v", m)
	}
	_ = a.Auth.SetSetting(ctx, "language", `"en"`)
	m, _ = a.EventToMessage(Event{Kind: "weird_event"})
	if m.Key != "generic" || m.Params["kind"] != "weird_event" || m.Params["name"] != "" {
		t.Fatalf("generic %+v", m)
	}
	for _, k := range NotificationKeys {
		if k == "" {
			t.Fatal("empty key")
		}
	}
}

func TestInterruptedRunMessage(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defer i18n.SetLang("en")
	_ = a.Auth.SetSetting(ctx, "language", `"zh-CN"`)
	m, ok := a.EventToMessage(Event{Kind: EventRunFailed, TaskName: "备份", Detail: db.RunInterruptedMsg})
	if !ok || m.Key != "run_interrupted" || m.Event != EventRunFailed || !strings.Contains(m.Title, "运行中断") || strings.Contains(m.Body, "interrupted:") {
		t.Fatalf("interrupted message %+v", m)
	}
	found := false
	for _, k := range NotificationKeys {
		found = found || k == "run_interrupted"
	}
	if !found {
		t.Fatal("run_interrupted missing from NotificationKeys")
	}
}

func TestStartupClosesInterruptedRuns(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := a.Store.InsertRun(ctx, db.Run{ID: "run_orphan", Status: "running", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	a.recoverInterruptedRuns(ctx)
	if n, _ := a.Store.CountActiveRuns(ctx); n != 0 {
		t.Fatalf("orphan still active: %d", n)
	}
	waitFor(t, 3*time.Second, func() bool {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(1) FROM notifications WHERE msg_key='run_interrupted'`).Scan(&n)
		return n == 1
	})
}
