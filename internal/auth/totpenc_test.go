package auth

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func encService(t *testing.T) (*Service, context.Context) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := New(sqlDB)
	ctx := context.Background()
	if err := s.EnsureOwner(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func storedSecret(t *testing.T, s *Service) string {
	var v string
	if err := s.DB.QueryRow(`SELECT totp_secret FROM users WHERE id='owner'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTOTPEncryptedAtRestAndMigration(t *testing.T) {
	s, ctx := encService(t)
	sec, _, _ := GenerateTOTPSecret("tidebot", "owner")
	// Legacy row: plaintext written before encryption existed.
	if err := s.EnableTOTP(ctx, sec); err != nil {
		t.Fatal(err)
	}
	if storedSecret(t, s) != sec {
		t.Fatal("expected legacy plaintext")
	}
	if err := s.SetMasterKey(bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	// Legacy plaintext still logs in before migration.
	t0 := time.Now()
	if _, err := s.Login(ctx, "correct horse battery", TOTPCodeAt(sec, t0), "10.0.0.1"); err != nil {
		t.Fatalf("legacy login: %v", err)
	}
	n, err := s.MigrateTOTP(ctx)
	if err != nil || n != 1 {
		t.Fatalf("migrate n=%d err=%v", n, err)
	}
	enc := storedSecret(t, s)
	if !strings.HasPrefix(enc, totpEncPrefix) || strings.Contains(enc, sec) {
		t.Fatal("secret not encrypted")
	}
	if n, _ := s.MigrateTOTP(ctx); n != 0 || storedSecret(t, s) != enc {
		t.Fatal("migration not idempotent")
	}
	// Next step's code: the one above is spent.
	if _, err := s.Login(ctx, "correct horse battery", TOTPCodeAt(sec, t0.Add(30*time.Second)), "10.0.0.2"); err != nil {
		t.Fatalf("encrypted login: %v", err)
	}
	// Fresh enable encrypts directly, nonce differs per record.
	if err := s.EnableTOTP(ctx, sec); err != nil {
		t.Fatal(err)
	}
	if e2 := storedSecret(t, s); e2 == enc || !strings.HasPrefix(e2, totpEncPrefix) {
		t.Fatal("expected fresh nonce")
	}
}

func TestTOTPTamperAndWrongKey(t *testing.T) {
	s, ctx := encService(t)
	s.SetMasterKey(bytes.Repeat([]byte{1}, 32))
	sec, _, _ := GenerateTOTPSecret("tidebot", "owner")
	if err := s.EnableTOTP(ctx, sec); err != nil {
		t.Fatal(err)
	}
	enc := storedSecret(t, s)
	b := []byte(enc)
	if b[len(b)-3] == 'A' {
		b[len(b)-3] = 'B'
	} else {
		b[len(b)-3] = 'A'
	}
	if _, err := s.openTOTP("owner", string(b)); err != ErrTOTPKey {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := s.openTOTP("someone-else", enc); err != ErrTOTPKey {
		t.Fatal("AAD must bind user id")
	}
	s.DB.Exec(`UPDATE users SET totp_secret=? WHERE id='owner'`, string(b))
	if _, err := s.Login(ctx, "correct horse battery", TOTPCodeAt(sec, time.Now()), "10.0.0.3"); err == nil {
		t.Fatal("login with tampered secret must fail")
	}
	s.DB.Exec(`UPDATE users SET totp_secret=? WHERE id='owner'`, enc)
	s.SetMasterKey(bytes.Repeat([]byte{2}, 32))
	if _, err := s.openTOTP("owner", enc); err != ErrTOTPKey {
		t.Fatal("wrong key must fail")
	}
}
