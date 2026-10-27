package notify

import (
	"context"
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

// TestChannelDownUp: one "down" notice when a channel reaches DownAfter failures in a row
// (not on every failure after that), one "up" notice when it works again; tests from the
// settings page never raise either; a changed address starts over; a message with
// SkipChannel never goes to that channel; FailingCount follows the last result.
func TestChannelDownUp(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var mu sync.Mutex
	reply, hits := map[string]string{}, map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		hits[r.URL.Path]++
		out := reply[r.URL.Path]
		if out == "" {
			out = `{"errcode":0}`
		}
		_, _ = io.WriteString(w, out)
	}))
	defer srv.Close()
	set := func(path, s string) { mu.Lock(); reply[path] = s; mu.Unlock() }
	hit := func(path string) int { mu.Lock(); defer mu.Unlock(); return hits[path] }

	s := New(sqlDB, secrets.NewMemory())
	var got []Health
	s.OnHealth = func(h Health) { got = append(got, h) }
	ctx := context.Background()
	a, err := s.Save(ctx, "", Input{Kind: "dingtalk", Name: "运维群", URL: srv.URL + "/a?access_token=SECRETTOKEN", Groups: []string{"failed"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(ctx, "", Input{Kind: "wecom", Name: "值班", URL: srv.URL + "/b", Groups: []string{"failed", "system"}})
	if err != nil {
		t.Fatal(err)
	}
	send := func() { _, _ = s.Dispatch(ctx, Message{Event: "run_failed", Title: "x"}, "") }
	failing := func() int {
		n, err := s.FailingCount(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	set("/a", `{"errcode":310000,"errmsg":"keywords not in content"}`)
	for i := 1; i < DownAfter; i++ {
		send()
	}
	if len(got) != 0 || failing() != 1 {
		t.Fatalf("before the threshold: %+v, failing %d", got, failing())
	}
	// tests of the saved setup move the streak but stay quiet
	if err := s.Test(ctx, a.ID, Input{Kind: "dingtalk", Name: "运维群"}, Message{Title: "t"}); err == nil {
		t.Fatal("test refusal not reported")
	}
	if len(got) != 0 {
		t.Fatalf("a test raised a notice: %+v", got)
	}
	send() // the streak is past the threshold now: the next real failure tells
	send()
	if len(got) != 1 || !got[0].Down || got[0].Channel.ID != a.ID || got[0].Channel.Name != "运维群" || got[0].Streak != DownAfter+1 || !strings.Contains(got[0].Err, "310000") {
		t.Fatalf("down notice: %+v", got)
	}
	if strings.Contains(got[0].Err, "SECRETTOKEN") {
		t.Fatal("token in the notice")
	}

	// recovery: one "up", then nothing more on later successes
	set("/a", "")
	send()
	send()
	if len(got) != 2 || got[1].Down || got[1].Channel.ID != a.ID || failing() != 0 {
		t.Fatalf("up notice: %+v, failing %d", got, failing())
	}

	// a down channel whose address changes starts over (no "up" for the new address)
	set("/a", `{"errcode":1}`)
	for i := 0; i < DownAfter; i++ {
		send()
	}
	if len(got) != 3 || !got[2].Down {
		t.Fatalf("second outage: %+v", got)
	}
	if _, err := s.Save(ctx, a.ID, Input{Kind: "dingtalk", Name: "运维群", URL: srv.URL + "/a2", Groups: []string{"failed"}}); err != nil {
		t.Fatal(err)
	}
	send()
	if len(got) != 3 {
		t.Fatalf("new address raised a notice: %+v", got)
	}

	// SkipChannel: the "down" notice goes to the other channels only
	before := hit("/a2")
	if st, err := s.Dispatch(ctx, Message{Event: "channel_down", Title: "down", SkipChannel: a.ID}, ""); err != nil || st != "delivered" {
		t.Fatalf("dispatch: %s %v", st, err)
	}
	if hit("/a2") != before || hit("/b") == 0 {
		t.Fatalf("skip channel: a2 %d→%d, b %d", before, hit("/a2"), hit("/b"))
	}

	// disabled channels don't count as failing
	set("/b", `{"errcode":1}`)
	send()
	if failing() != 1 {
		t.Fatalf("failing %d", failing())
	}
	if err := s.SetEnabled(ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if failing() != 0 {
		t.Fatalf("disabled still failing: %d", failing())
	}
}
