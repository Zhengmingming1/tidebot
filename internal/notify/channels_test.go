package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

func TestChannelSaveSummaryTest(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var mu sync.Mutex
	var hits []string
	reply := `{"errcode":0,"errmsg":"ok"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, r.URL.String()+" "+string(b))
		out := reply
		mu.Unlock()
		_, _ = io.WriteString(w, out)
	}))
	defer srv.Close()
	sec := secrets.NewMemory()
	s := New(sqlDB, sec)
	s.Now = func() time.Time { return time.Unix(1700000000, 0) }
	ctx := context.Background()
	code := func(err error) string {
		var ce CodeError
		if errors.As(err, &ce) {
			return ce.Code
		}
		return ""
	}

	// validation codes
	for _, tc := range []struct {
		in   Input
		want string
	}{
		{Input{Kind: "sms", Name: "x", Groups: []string{"failed"}}, "kind"},
		{Input{Kind: "dingtalk", Name: " ", URL: srv.URL, Groups: []string{"failed"}}, "name"},
		{Input{Kind: "dingtalk", Name: "d", Groups: []string{"failed"}}, "url_required"},
		{Input{Kind: "dingtalk", Name: "d", URL: "file:///etc/passwd", Groups: []string{"failed"}}, "url_bad"},
		{Input{Kind: "dingtalk", Name: "d", URL: srv.URL, Groups: []string{"nope"}}, "groups"},
		{Input{Kind: "telegram", Name: "t", Config: map[string]string{"chat_id": "1"}, Groups: []string{"failed"}}, "token"},
		{Input{Kind: "email", Name: "m", Config: map[string]string{"host": "smtp.x"}, Groups: []string{"failed"}}, "mail"},
	} {
		if _, err := s.Save(ctx, "", tc.in); code(err) != tc.want {
			t.Fatalf("%+v: got %v want %s", tc.in, err, tc.want)
		}
	}

	hook := srv.URL + "/robot/send?access_token=SECRETTOKEN"
	c, err := s.Save(ctx, "", Input{Kind: "dingtalk", Name: "钉钉", URL: hook, Secret: "SECabc", Groups: []string{"failed", "budget", "failed"},
		Config: map[string]string{"password": "drop me"}})
	if err != nil {
		t.Fatal(err)
	}
	var cj string
	_ = sqlDB.QueryRow(`SELECT config_json FROM notify_channels WHERE id=?`, c.ID).Scan(&cj)
	if strings.Contains(cj, "SECRETTOKEN") || strings.Contains(cj, "SECabc") || strings.Contains(cj, "drop me") {
		t.Fatalf("secret in database: %s", cj)
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM notify_rules WHERE channel_id=?`, c.ID).Scan(&n)
	if n != 3 { // run_failed + budget_breached + budget_warning
		t.Fatalf("rules = %d", n)
	}
	// a per-task rule survives edits
	_, _ = s.AddRule(ctx, "run_failed", "task_1", c.ID)

	list, err := s.Summaries(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	x := list[0]
	if !x.HasURL || !x.HasSecret || x.Host != "127.0.0.1" || strings.Join(x.Groups, ",") != "failed,budget" || x.Tasks != 1 || x.All {
		t.Fatalf("summary %+v", x)
	}

	// dispatch uses the stored URL and signs it
	if st, err := s.Dispatch(ctx, Message{Event: "run_failed", Title: "失败"}, ""); err != nil || st != "delivered" {
		t.Fatal(st, err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0], "access_token=SECRETTOKEN&timestamp=1700000000000&sign=") {
		t.Fatalf("hits %v", hits)
	}

	// edit: blank URL / secret keep the stored ones, groups replace the all-task rules
	if _, err := s.Save(ctx, c.ID, Input{Kind: "dingtalk", Name: "群", Groups: []string{"confirm"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, c.ID, Input{Kind: "wecom", Name: "群", Groups: []string{"confirm"}}); code(err) != "kind_change" {
		t.Fatal(err)
	}
	list, _ = s.Summaries(ctx)
	if list[0].Name != "群" || strings.Join(list[0].Groups, ",") != "confirm" || !list[0].HasURL || !list[0].HasSecret {
		t.Fatalf("after edit %+v", list[0]) // per-task rules don't count as groups
	}
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM notify_rules WHERE channel_id=? AND task_id='task_1'`, c.ID).Scan(&n)
	if n != 1 {
		t.Fatal("per-task rule dropped")
	}

	// test with the saved channel (blank URL) and with an unsaved form
	hits = nil
	if err := s.Test(ctx, c.ID, Input{Kind: "dingtalk", Name: "群"}, Message{Title: "test"}); err != nil || len(hits) != 1 {
		t.Fatal(err, hits)
	}
	if err := s.Test(ctx, "", Input{Kind: "wecom", Name: "w", URL: srv.URL + "/wecom"}, Message{Title: "test"}); err != nil || !strings.Contains(hits[1], "/wecom") {
		t.Fatal(err, hits)
	}
	// a robot that answers 200 but refuses is an error, with its message
	reply = `{"errcode":310000,"errmsg":"keywords not in content"}`
	if err := s.Test(ctx, "", Input{Kind: "dingtalk", Name: "d", URL: srv.URL}, Message{Title: "t"}); err == nil || !strings.Contains(err.Error(), "310000") || !strings.Contains(err.Error(), "keywords") {
		t.Fatal(err)
	}
	reply = `{"code":19021,"msg":"sign match fail"}`
	if err := s.Test(ctx, "", Input{Kind: "feishu", Name: "f", URL: srv.URL, Secret: "k"}, Message{Title: "t"}); err == nil || !strings.Contains(err.Error(), "19021") {
		t.Fatal(err)
	}
	if !strings.Contains(hits[len(hits)-1], `"sign":"`) || !strings.Contains(hits[len(hits)-1], `"timestamp":"1700000000"`) {
		t.Fatal("feishu sign", hits[len(hits)-1])
	}
	// generic webhooks don't get their JSON second-guessed
	if err := s.Test(ctx, "", Input{Kind: "webhook", Name: "h", URL: srv.URL}, Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}

	// enable switch
	if err := s.SetEnabled(ctx, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, "nch_missing", true); code(err) != "not_found" {
		t.Fatal(err)
	}
	hits = nil
	reply = `{"errcode":0}`
	if st, _ := s.Dispatch(ctx, Message{Event: "approval_needed", Title: "x"}, ""); st != "logged" || len(hits) != 0 {
		t.Fatal("disabled channel still delivered", st)
	}

	// older channels: url in config, "*" rule → every group shown on
	old, _ := s.AddChannel(ctx, "feishu", "旧", map[string]string{"url": srv.URL + "/old"}, "")
	_, _ = s.AddRule(ctx, "*", "", old.ID)
	list, _ = s.Summaries(ctx)
	if !list[1].All || len(list[1].Groups) != len(EventGroups) || list[1].Host == "" {
		t.Fatalf("legacy %+v", list[1])
	}
	// saving it from the WebUI with a new URL moves the URL out of the database
	if _, err := s.Save(ctx, old.ID, Input{Kind: "feishu", Name: "旧", URL: srv.URL + "/new", Groups: []string{"failed"}}); err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.QueryRow(`SELECT config_json FROM notify_channels WHERE id=?`, old.ID).Scan(&cj)
	if strings.Contains(cj, "/old") || strings.Contains(cj, "/new") {
		t.Fatal("url left in config", cj)
	}

	// remove drops rules and both secrets
	if err := s.RemoveChannel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if v, _ := sec.Get(urlRef(c.ID)); v != "" {
		t.Fatal("url secret kept")
	}
	if v, _ := sec.Get(secretRef(c.ID)); v != "" {
		t.Fatal("secret kept")
	}
}

func TestSplitAddrs(t *testing.T) {
	got := splitAddrs("a@x.com; Bob <b@y.com>, ,c@z.com")
	if strings.Join(got, "|") != "a@x.com|b@y.com|c@z.com" {
		t.Fatal(got)
	}
}
