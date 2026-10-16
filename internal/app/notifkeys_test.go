package app

import (
	"context"
	"strings"
	"testing"

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
