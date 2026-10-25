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

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

func lastOf(t *testing.T, s *Service, chID string) *Delivery {
	t.Helper()
	list, err := s.Summaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == chID {
			return c.Last
		}
	}
	t.Fatalf("channel %s not listed", chID)
	return nil
}

// TestChannelLastDelivery: real sends and tests of the saved setup are kept on the row
// (state, error, failures in a row, last success); unsaved form tests are not; changing
// the address forgets the old result; nothing secret ends up in the stored error.
func TestChannelLastDelivery(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var mu sync.Mutex
	reply := `{"errcode":0,"errmsg":"ok"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		out := reply
		mu.Unlock()
		_, _ = io.WriteString(w, out)
	}))
	defer srv.Close()
	setReply := func(s string) { mu.Lock(); reply = s; mu.Unlock() }
	s := New(sqlDB, secrets.NewMemory())
	ctx := context.Background()
	hook := srv.URL + "/robot/send?access_token=SECRETTOKEN"
	c, err := s.Save(ctx, "", Input{Kind: "dingtalk", Name: "钉钉", URL: hook, Groups: []string{"failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := lastOf(t, s, c.ID); d != nil {
		t.Fatalf("new channel has a result: %+v", d)
	}

	// a real delivery that works
	if st, err := s.Dispatch(ctx, Message{Event: "run_failed", Title: "x"}, ""); err != nil || st != "delivered" {
		t.Fatalf("dispatch: %s %v", st, err)
	}
	d := lastOf(t, s, c.ID)
	if d == nil || !d.OK || d.Streak != 0 || d.At == "" || d.OKAt != d.At || d.Error != "" {
		t.Fatalf("after ok: %+v", d)
	}
	okAt := d.OKAt

	// two refusals in a row: streak counts, last success is kept
	setReply(`{"errcode":310000,"errmsg":"keywords not in content"}`)
	for i := 0; i < 2; i++ {
		if _, err := s.Dispatch(ctx, Message{Event: "run_failed", Title: "x"}, ""); err == nil {
			t.Fatal("refusal not reported")
		}
	}
	d = lastOf(t, s, c.ID)
	if d.OK || d.Streak != 2 || d.OKAt != okAt || !strings.Contains(d.Error, "310000") {
		t.Fatalf("after 2 failures: %+v", d)
	}

	// a test of unsaved form values (a new URL) does not touch the row
	setReply(`{"errcode":0}`)
	if err := s.Test(ctx, c.ID, Input{Kind: "dingtalk", Name: "钉钉", URL: srv.URL + "/other"}, Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if d = lastOf(t, s, c.ID); d.OK || d.Streak != 2 {
		t.Fatalf("form test changed the row: %+v", d)
	}
	// a test of the saved setup (list button) does, and clears the streak
	if err := s.Test(ctx, c.ID, Input{Kind: "dingtalk", Name: "钉钉"}, Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if d = lastOf(t, s, c.ID); !d.OK || d.Streak != 0 {
		t.Fatalf("saved test not recorded: %+v", d)
	}

	// renaming / regrouping keeps it, a new address forgets it
	if _, err := s.Save(ctx, c.ID, Input{Kind: "dingtalk", Name: "运维群", Groups: []string{"failed", "budget"}}); err != nil {
		t.Fatal(err)
	}
	if lastOf(t, s, c.ID) == nil {
		t.Fatal("rename dropped the result")
	}
	if _, err := s.Save(ctx, c.ID, Input{Kind: "dingtalk", Name: "运维群", URL: srv.URL + "/new", Groups: []string{"failed"}}); err != nil {
		t.Fatal(err)
	}
	if d := lastOf(t, s, c.ID); d != nil {
		t.Fatalf("new address kept the old result: %+v", d)
	}

	// unsaved channels (no id) never write anything
	s.record(ctx, "", s.Now(), errors.New("x"))

	// stored errors never carry a URL token or a bot token
	var raw string
	_ = sqlDB.QueryRow(`SELECT last_error FROM notify_channels WHERE id=?`, c.ID).Scan(&raw)
	if strings.Contains(raw, "SECRETTOKEN") {
		t.Fatal("token stored")
	}
}

func TestCleanErr(t *testing.T) {
	for in, want := range map[string]string{
		`Post "https://api.telegram.org/bot123456789:AAHxxxxxxxxxxxxxxxxxxxxxxxxx/sendMessage": dial tcp: timeout`: `Post "api.telegram.org": dial tcp: timeout`,
		`Post "https://oapi.dingtalk.com/robot/send?access_token=abc": EOF`:                                        `Post "oapi.dingtalk.com": EOF`,
		`smtp: dial smtp://me:pw@mail.example.com:465/x failed`:                                                    `smtp: dial mail.example.com:465 failed`,
		"token 123456789:AAHxxxxxxxxxxxxxxxxxxxxxxxxx rejected":                                                    "token *** rejected",
		"http 502\n\n  bad   gateway":                                                                              "http 502 bad gateway",
	} {
		if got := cleanErr(errors.New(in)); got != want {
			t.Errorf("cleanErr(%q) = %q, want %q", in, got, want)
		}
	}
	if cleanErr(nil) != "" {
		t.Error("nil")
	}
	if got := cleanErr(errors.New(strings.Repeat("é", 300))); len([]rune(got)) != 201 {
		t.Errorf("not cut: %d", len([]rune(got)))
	}
}
