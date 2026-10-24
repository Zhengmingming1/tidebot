package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/notify"
)

// TestNotifyChannelsFromWebUI: Settings → Notifications can add, test, switch off and remove
// a channel through /api/rpc, and the overview never carries the robot URL or a secret.
func TestNotifyChannelsFromWebUI(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()
	call := func(m string, p any) (any, error) {
		raw, _ := json.Marshal(p)
		return a.CallExt(ctx, m, raw)
	}
	hook := srv.URL + "/robot/send?access_token=TOPSECRET"
	if _, err := call("notify.channel_test", map[string]any{"kind": "wecom", "name": "企业微信", "url": hook}); err != nil || len(got) != 1 {
		t.Fatal("test before saving", err, got)
	}
	if !strings.Contains(got[0], `"msgtype":"text"`) {
		t.Fatal(got[0])
	}
	if _, err := call("notify.channel_save", map[string]any{"kind": "wecom", "name": "企业微信", "url": hook, "groups": []string{}}); err == nil || err.Error() != "notify: groups" {
		t.Fatal("no groups must be refused with a code", err)
	}
	out, err := call("notify.channel_save", map[string]any{"kind": "wecom", "name": "企业微信", "url": hook, "groups": notify.DefaultGroups})
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]string)["id"]
	ov, err := call("notify.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ov)
	if strings.Contains(string(b), "TOPSECRET") || strings.Contains(string(b), "url_ref") || !strings.Contains(string(b), `"host":"127.0.0.1"`) {
		t.Fatalf("overview leaks or lacks host: %s", b)
	}
	// a failed run reaches the channel through the normal notifier path
	m, _ := a.EventToMessage(Event{Kind: EventRunFailed, TaskName: "日报", Detail: "exit 1"})
	if st, err := a.Notify.Dispatch(ctx, m, ""); err != nil || st != "delivered" {
		t.Fatal(st, err)
	}
	if _, err := call("notify.channel_enable", map[string]any{"id": id, "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Notify.Dispatch(ctx, m, ""); st != "logged" {
		t.Fatal("disabled channel", st)
	}
	if _, err := call("notify.channel_remove", map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
	if l, _ := a.Notify.Summaries(ctx); len(l) != 0 {
		t.Fatal(l)
	}
}
