package auth

import (
	"context"
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestPasswordArgon2(t *testing.T) {
	h, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("unexpected hash %s", h)
	}
	ok, err := VerifyPassword(h, "correct-horse")
	if err != nil || !ok {
		t.Fatalf("verify argon2: %v %v", ok, err)
	}
	ok, _ = VerifyPassword(h, "wrong")
	if ok {
		t.Fatal("expected fail")
	}
}

func TestLoginSessionAndRateLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()
	if err := s.EnsureOwner(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, "supersecret1"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Login(ctx, "supersecret1", "", "127.0.0.1")
	if err != nil || sess.CSRFToken == "" {
		t.Fatalf("%v %+v", err, sess)
	}
	got, err := s.GetSession(ctx, sess.ID)
	if err != nil || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("%v %+v", err, got)
	}
	for i := 0; i < LoginMaxFails; i++ {
		_, _ = s.Login(ctx, "bad", "", "10.0.0.9")
	}
	_, err = s.Login(ctx, "supersecret1", "", "10.0.0.9")
	if err == nil {
		t.Fatal("expected rate limit")
	}
}

func TestCSRFMiddleware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()
	_ = s.EnsureOwner(ctx)
	_ = s.SetPassword(ctx, "supersecret1")
	sess, err := s.Login(ctx, "supersecret1", "", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	h := RequireAuth(s, RequireCSRF(mux))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/ping", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess.ID})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 csrf got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess.ID})
	req.Header.Set(CSRFHeader, sess.CSRFToken)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("want 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestTOTPRoundTrip(t *testing.T) {
	sec, uri, err := GenerateTOTPSecret("tidebot", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "otpauth://totp/") {
		t.Fatalf("uri %s", uri)
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(sec)
	if err != nil {
		t.Fatal(err)
	}
	code := hotp(key, time.Now().Unix()/30)
	if !VerifyTOTP(sec, code) {
		t.Fatal("totp verify failed")
	}
}

// TestSessionSeenThrottled: a burst of authed requests writes last_seen_at once, not per call.
func TestSessionSeenThrottled(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()
	if err := s.EnsureOwner(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, "supersecret1"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Login(ctx, "supersecret1", "", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	changes := func() (n int64) {
		if err := s.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := s.GetSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	before := changes()
	for i := 0; i < 5; i++ {
		if _, err := s.GetSession(ctx, sess.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := changes(); got != before {
		t.Fatalf("GetSession wrote %d rows within a minute", got-before)
	}
	// a stale last_seen_at is refreshed
	if _, err := s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at='2000-01-01T00:00:00Z' WHERE id=?`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	var seen string
	_ = s.DB.QueryRowContext(ctx, `SELECT last_seen_at FROM sessions WHERE id=?`, sess.ID).Scan(&seen)
	if strings.HasPrefix(seen, "2000") {
		t.Fatalf("stale last_seen_at not refreshed: %s", seen)
	}
}
