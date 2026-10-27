package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/notify"
)

// TestChannelDownReachesOthers: a channel refusing notify.DownAfter sends in a row puts one
// channel_down event on the bus; routed like any event it lands in the bell (structured,
// in the viewer's language) and on the other channels, never on the failing one. When it
// works again a channel_up event follows.
func TestChannelDownReachesOthers(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defer i18n.SetLang("en")
	_ = a.Auth.SetSetting(ctx, "language", `"zh-CN"`)
	var mu sync.Mutex
	hits := map[string][]string{}
	refuse := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		hits[r.URL.Path] = append(hits[r.URL.Path], string(b))
		if r.URL.Path == "/a" && refuse {
			_, _ = io.WriteString(w, `{"errcode":310000,"errmsg":"keywords not in content"}`)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()
	save := func(name, path string) string {
		c, err := a.Notify.Save(ctx, "", notify.Input{Kind: "dingtalk", Name: name, URL: srv.URL + path + "?access_token=TOPSECRET", Groups: notify.DefaultGroups})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	idA := save("运维群", "/a")
	save("值班", "/b")
	bus := a.Events.Subscribe(16)
	next := func() Event {
		for {
			select {
			case e := <-bus:
				if e.Kind == EventChannelDown || e.Kind == EventChannelUp {
					return e
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no channel event")
			}
		}
	}
	failed, _ := a.EventToMessage(Event{Kind: EventRunFailed, TaskName: "日报", Detail: "exit 1"})
	for i := 0; i < notify.DownAfter; i++ {
		_, _ = a.Notify.Dispatch(ctx, failed, "")
	}
	e := next()
	if e.Kind != EventChannelDown || e.ChannelID != idA || e.TaskName != "运维群" || e.Count != notify.DownAfter || !strings.Contains(e.Detail, "310000") {
		t.Fatalf("down event %+v", e)
	}
	m, ok := a.EventToMessage(e)
	if !ok || m.Key != "channel_down" || m.SkipChannel != idA || m.TaskID != "" || m.Params["name"] != "运维群" || m.Params["n"] != "3" || !strings.Contains(m.Title, "连续 3 次发送失败") {
		t.Fatalf("message %+v", m)
	}
	mu.Lock()
	beforeA, beforeB := len(hits["/a"]), len(hits["/b"])
	mu.Unlock()
	if st, err := a.Notify.Dispatch(ctx, m, ""); err != nil || st != "delivered" {
		t.Fatal(st, err)
	}
	mu.Lock()
	gotA, gotB := len(hits["/a"])-beforeA, len(hits["/b"])-beforeB
	mu.Unlock()
	if gotA != 0 || gotB != 1 {
		t.Fatalf("down notice went to a:%d b:%d", gotA, gotB)
	}
	var key, params string
	if err := a.DB.QueryRow(`SELECT msg_key, msg_params FROM notifications WHERE event='channel_down'`).Scan(&key, &params); err != nil || key != "channel_down" || strings.Contains(params, "TOPSECRET") {
		t.Fatalf("bell row %q %q %v", key, params, err)
	}

	mu.Lock()
	refuse = false
	mu.Unlock()
	_, _ = a.Notify.Dispatch(ctx, failed, "")
	if e := next(); e.Kind != EventChannelUp || e.ChannelID != idA {
		t.Fatalf("up event %+v", e)
	}
	if m, _ := a.EventToMessage(Event{Kind: EventChannelUp, ChannelID: idA, TaskName: "运维群"}); m.Key != "channel_up" || !strings.Contains(m.Title, "已恢复") {
		t.Fatalf("up message %+v", m)
	}
}
