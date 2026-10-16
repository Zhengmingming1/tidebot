package notify

import (
	"context"
	"encoding/json"
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

func TestDispatchRulesDigestQuiet(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.URL.Path+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	s := New(sqlDB, secrets.NewMemory())
	s.Now = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
	s.Loc = time.UTC
	ctx := context.Background()
	if _, err := s.AddChannel(ctx, "feishu", "fs", map[string]string{"url": srv.URL + "/fs", "token": "x"}, ""); err == nil {
		t.Fatal("token in config must be refused")
	}
	fs, _ := s.AddChannel(ctx, "feishu", "fs", map[string]string{"url": srv.URL + "/fs"}, "")
	tg, _ := s.AddChannel(ctx, "telegram", "tg", map[string]string{"chat_id": "42", "api_base": srv.URL}, "123:SECRET")
	_, _ = s.AddRule(ctx, "*", "", fs.ID)
	_, _ = s.AddRule(ctx, "run_failed", "", tg.ID)

	st, err := s.Dispatch(ctx, Message{Event: "run_failed", Title: "Run failed: backup", Body: "exit 1"}, "")
	if err != nil || st != "delivered" || len(got) != 2 {
		t.Fatalf("st=%s err=%v got=%v", st, err, got)
	}
	if !strings.Contains(strings.Join(got, "|"), "/bot123:SECRET/sendMessage") {
		t.Fatal("telegram path")
	}
	var fsBody map[string]any
	_ = json.Unmarshal([]byte(strings.SplitN(got[0], " ", 2)[1]), &fsBody)
	if fsBody["msg_type"] != "text" && !strings.Contains(got[1], "msg_type") {
		t.Fatal("feishu format")
	}
	got = nil
	if st, _ := s.Dispatch(ctx, Message{Event: "run_finished", Title: "ok"}, ""); st != "queued" {
		t.Fatal("low priority should queue", st)
	}
	if st, _ := s.Dispatch(ctx, Message{Event: "budget_breached", Title: "b"}, "11:00-13:00"); st != "queued" {
		t.Fatal("quiet hours should queue", st)
	}
	if st, _ := s.Dispatch(ctx, Message{Event: "budget_breached", Title: "b", Urgent: true}, "11:00-13:00"); st != "delivered" {
		t.Fatal("urgent bypasses quiet", st)
	}
	got = nil
	n, err := s.FlushDigest(ctx)
	if err != nil || n != 2 || len(got) != 1 || !strings.Contains(got[0], "digest (2)") {
		t.Fatalf("digest n=%d err=%v got=%v", n, err, got)
	}
	if !inQuiet("22:00-08:00", time.Date(1, 1, 1, 23, 0, 0, 0, time.UTC)) || inQuiet("22:00-08:00", time.Date(1, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("overnight window")
	}
	mail := string(BuildMail("a@x", "b@y", Message{Title: "t\r\nBcc: evil@z", Body: "hi"}))
	if strings.Contains(mail, "\r\nBcc:") {
		t.Fatal("header injection")
	}
}
