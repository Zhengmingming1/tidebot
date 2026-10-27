package webui

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestChannelDownCenter: the notification center files "channel down" under failures,
// "working again" under other, and reports how many enabled channels are failing (for
// the dot on Settings); disabled channels and channels never used don't count.
func TestChannelDownCenter(t *testing.T) {
	c := login(t)
	db := c.s.deps.DB
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO notifications(id,event,body,created_at,msg_key,msg_params) VALUES
		('d1','channel_down','x',?,'channel_down','{"name":"运维群","n":"3","detail":"errcode 310000"}'),
		('u1','channel_up','x',?,'channel_up','{"name":"运维群"}')`, now, now)
	_, _ = db.Exec(`INSERT INTO notify_channels(id,kind,name,config_json,enabled,created_at,last_at,last_ok) VALUES
		('c1','dingtalk','a','{}',1,?,?,0),('c2','wecom','b','{}',1,?,?,1),('c3','feishu','c','{}',0,?,?,0),('c4','webhook','d','{}',1,?,NULL,0)`,
		now, now, now, now, now, now, now)
	code, out := c.json(http.MethodGet, "/api/notifications/center", nil)
	if code != 200 {
		t.Fatalf("center %d", code)
	}
	groups := map[string]string{}
	for _, x := range out["items"].([]any) {
		m := x.(map[string]any)
		groups[m["id"].(string)] = m["group"].(string)
	}
	if groups["d1"] != "failed" || groups["u1"] != "other" {
		t.Fatalf("groups %v", groups)
	}
	if n, _ := out["channels_failing"].(float64); n != 1 {
		t.Fatalf("channels_failing %v", out["channels_failing"])
	}
}

// TestChannelDownUI: bell entries open the channel list, a dot marks Settings and
// Settings › Notifications while a channel is failing (count in the tooltip only), and
// every string has both languages.
func TestChannelDownUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{
		`n.key === "channel_down" || n.key === "channel_up" ? "#/settings/notifications"`,
		`'<a class="item link-item note'`,
		`state.chFailing = d.channels_failing || 0;`,
		`function paintChannelDot()`,
		`#nav a[data-nav="settings"], .set-nav a[href="#/settings/notifications"]`,
		`noteChannels(o.channels);`,
		`<b class="nav-dot" aria-hidden="true"></b>`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".nav-dot {", ".popover a.link-item.note {", "#nav .nav-dot { position: absolute;"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, k := range []string{"nt.failing_tip", "ntf.channel_down.t", "ntf.channel_down.b", "ntf.channel_up.t", "ntf.channel_up.b", "notif.channel_down", "notif.channel_up"} {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
	for _, d := range []map[string]string{en, zh} {
		if !strings.Contains(d["ntf.channel_down.t"], "{name}") || !strings.Contains(d["ntf.channel_down.t"], "{n}") || !strings.Contains(d["nt.failing_tip"], "{n}") {
			t.Error("placeholders missing")
		}
	}
}
