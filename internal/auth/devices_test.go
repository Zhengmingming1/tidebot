package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestParseUA(t *testing.T) {
	for _, tc := range []struct{ ua, client, os string }{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome 130", "macOS"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.2849.68", "Edge 130", "Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox 132", "Linux"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", "Safari 18", "macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1", "Chrome 130", "iOS"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", "Chrome 130", "Android"},
		{"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome 130", "ChromeOS"},
		{"curl/8.7.1", "curl 8", ""},
		{"<script>alert(1)</script>", "", ""},
		{"", "", ""},
	} {
		c, o := ParseUA(tc.ua)
		if c != tc.client || o != tc.os {
			t.Errorf("%q → %q %q, want %q %q", tc.ua, c, o, tc.client, tc.os)
		}
	}
}

func TestSessionListSignOutAndPrune(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()
	_ = s.EnsureOwner(ctx)
	_ = s.SetPassword(ctx, "supersecret1")
	a, err := s.Login(ctx, "supersecret1", "", "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetSessionDevice(ctx, a.ID, "Firefox 132", "Linux")
	b, _ := s.Login(ctx, "supersecret1", "", "127.0.0.1")
	_ = s.SetSessionDevice(ctx, b.ID, "Chrome 130", "macOS")
	old1, _ := s.CreateLocalSession(ctx, OriginDesktop)
	old2, _ := s.CreateLocalSession(ctx, OriginDesktop)
	// an expired row is dropped by the listing
	_, _ = sqlDB.ExecContext(ctx, `INSERT INTO sessions(id, user_id, csrf_token, created_at, expires_at, last_seen_at, ip) VALUES('dead','owner','x',?,?,?,'10.0.0.3')`,
		"2020-01-01T00:00:00Z", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), "2020-01-01T00:00:00Z")

	if n, _ := s.PruneLocalSessions(ctx, OriginDesktop, old2.ID); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if _, err := s.GetSession(ctx, old1.ID); err == nil {
		t.Fatal("stale desktop session kept")
	}
	list, err := s.ListSessions(ctx, b.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("%v %+v", err, list)
	}
	if !list[0].Current || list[0].Client != "Chrome 130" || list[0].OS != "macOS" {
		t.Fatalf("current first: %+v", list[0])
	}
	var desk *SessionInfo
	for i := range list {
		if list[i].Handle == a.ID || list[i].Handle == b.ID || len(list[i].Handle) != 16 {
			t.Fatalf("handle leaks or bad: %q", list[i].Handle)
		}
		if list[i].Kind == "desktop" {
			desk = &list[i]
		}
	}
	if desk == nil || desk.IP != "" || desk.OS != HostOS() {
		t.Fatalf("desktop row %+v", desk)
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM sessions WHERE id='dead'`).Scan(&n)
	if n != 0 {
		t.Fatal("expired row kept")
	}
	if err := s.SignOutSession(ctx, SessionHandle(b.ID), b.ID); !errors.Is(err, ErrCurrentSession) {
		t.Fatalf("current: %v", err)
	}
	if err := s.SignOutSession(ctx, SessionHandle(a.ID), b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, a.ID); err == nil {
		t.Fatal("signed-out session still valid")
	}
	if err := s.SignOutSession(ctx, SessionHandle(a.ID), b.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("again: %v", err)
	}
	if err := s.SignOutSession(ctx, a.ID, b.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("raw id must not work as handle: %v", err)
	}
}
