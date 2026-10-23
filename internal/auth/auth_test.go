package auth

import (
	"context"
	"encoding/base32"
	"errors"
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

func newTOTPService(t *testing.T) (*Service, string) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := New(sqlDB)
	ctx := context.Background()
	if err := s.SetPassword(ctx, "supersecret1"); err != nil {
		t.Fatal(err)
	}
	sec, _, err := GenerateTOTPSecret("tidebot", "owner")
	if err != nil {
		t.Fatal(err)
	}
	return s, sec
}

// TestTOTPCodeUsedOnce: a code that enabled 2FA or signed in can't sign in again,
// and neither can an older one still inside the ±1 step window.
func TestTOTPCodeUsedOnce(t *testing.T) {
	s, sec := newTOTPService(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.EnableTOTPWithCode(ctx, sec, "000000"); err == nil && TOTPCodeAt(sec, now) != "000000" {
		t.Fatal("wrong code enabled 2fa")
	}
	if err := s.EnableTOTPWithCode(ctx, sec, TOTPCodeAt(sec, now)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "supersecret1", TOTPCodeAt(sec, now), "10.0.0.1"); err == nil {
		t.Fatal("setup code replayed for login")
	}
	next := TOTPCodeAt(sec, now.Add(30*time.Second))
	if _, err := s.Login(ctx, "supersecret1", next, "10.0.0.2"); err != nil {
		t.Fatalf("next code: %v", err)
	}
	if _, err := s.Login(ctx, "supersecret1", next, "10.0.0.3"); err == nil {
		t.Fatal("code accepted twice")
	}
	if _, err := s.Login(ctx, "supersecret1", TOTPCodeAt(sec, now.Add(-30*time.Second)), "10.0.0.4"); err == nil {
		t.Fatal("older code accepted after a newer one")
	}
	var detail string
	_ = s.DB.QueryRow(`SELECT detail FROM login_attempts WHERE ip='10.0.0.3'`).Scan(&detail)
	if detail != "totp_reused" {
		t.Fatalf("detail %q", detail)
	}
	// Turning 2FA off clears the marker and the recovery codes, so a fresh setup starts clean.
	if err := s.DisableTOTP(ctx); err != nil {
		t.Fatal(err)
	}
	var step int64
	var rec string
	_ = s.DB.QueryRow(`SELECT totp_last_step, recovery_codes_hash FROM users WHERE id='owner'`).Scan(&step, &rec)
	if step != 0 || rec != "" {
		t.Fatalf("after disable: step %d rec %q", step, rec)
	}
}

// TestTOTPConcurrentReuse: two sign-ins racing with one code, only one gets in.
func TestTOTPConcurrentReuse(t *testing.T) {
	s, sec := newTOTPService(t)
	ctx := context.Background()
	if err := s.EnableTOTP(ctx, sec); err != nil {
		t.Fatal(err)
	}
	code := TOTPCodeAt(sec, time.Now())
	const n = 6
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			_, err := s.Login(ctx, "supersecret1", code, "10.1.0."+string(rune('1'+i)))
			errs <- err
		}(i)
	}
	ok := 0
	for i := 0; i < n; i++ {
		if <-errs == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d sign-ins with one code", ok)
	}
}

func TestDisableTOTPCheckedRateLimit(t *testing.T) {
	s, sec := newTOTPService(t)
	ctx := context.Background()
	if err := s.EnableTOTP(ctx, sec); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < LoginMaxFails; i++ {
		if err := s.DisableTOTPChecked(ctx, "wrong", "10.2.0.1"); err != ErrBadCurrent {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	var rl *RateLimitError
	if err := s.DisableTOTPChecked(ctx, "supersecret1", "10.2.0.1"); !errors.As(err, &rl) {
		t.Fatalf("want rate limit, got %v", err)
	}
	// The lockout is per IP and shared with sign-in.
	if _, err := s.Login(ctx, "supersecret1", TOTPCodeAt(sec, time.Now()), "10.2.0.1"); !errors.As(err, &rl) {
		t.Fatalf("login not locked: %v", err)
	}
	if err := s.DisableTOTPChecked(ctx, "supersecret1", "10.2.0.2"); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.TOTPEnabled(ctx); on {
		t.Fatal("still on")
	}
}

func TestTOTPStepWindow(t *testing.T) {
	sec, _, _ := GenerateTOTPSecret("tidebot", "owner")
	now := time.Unix(1_800_000_015, 0)
	for d, want := range map[time.Duration]bool{-60 * time.Second: false, -30 * time.Second: true, 0: true, 30 * time.Second: true, 60 * time.Second: false} {
		step, ok := totpStep(sec, TOTPCodeAt(sec, now.Add(d)), now)
		if ok != want || (ok && step != now.Add(d).Unix()/30) {
			// A different step can collide on the 6 digits about once in a million.
			t.Errorf("offset %v: ok=%v step=%d", d, ok, step)
		}
	}
	if _, ok := totpStep(sec, "12345", now); ok {
		t.Error("5 digits accepted")
	}
}
