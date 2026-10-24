package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/notify"
)

// TestNotifyChannelsUI: channels are managed in Settings → Notifications (no agentctl hint),
// every error code, kind and event group the server knows has text in both languages, and
// the page never asks the browser for a native dialog.
func TestNotifyChannelsUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{`rpc("notify.channel_save"`, `rpc("notify.channel_test"`, `"notify.channel_enable"`, `"notify.channel_remove"`, `confirmDialog({ title: T("nt.del_title"`} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	if strings.Contains(src, "agentctl notify add") {
		t.Error("channels still point at the command line")
	}
	goSrc, err := os.ReadFile("../notify/channels.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := regexp.MustCompile(`CodeError\{"(\w+)"\}`).FindAllStringSubmatch(string(goSrc), -1)
	if len(codes) < 8 {
		t.Fatalf("found %d codes", len(codes))
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	need := []string{}
	for _, c := range codes {
		need = append(need, "nt.err."+c[1])
	}
	for _, k := range notify.Kinds {
		need = append(need, "nt.k."+k, "nt.h."+k)
	}
	for _, g := range notify.EventGroups {
		need = append(need, "nt.g."+g.ID, "nt.gt."+g.ID, "nt.gs."+g.ID)
		if !strings.Contains(src, `"`+g.ID+`"`) {
			t.Errorf("group %s missing from NT_GROUPS", g.ID)
		}
	}
	for _, k := range need {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
}
