package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"
)

const (
	SessionCookie = "tidebot_session"
	CSRFHeader    = "X-CSRF-Token"
	SessionTTL    = 7 * 24 * time.Hour
	LoginWindow   = 15 * time.Minute
	LoginMaxFails = 5
)

// Service handles single-user auth (D6).
type Service struct {
	DB *sql.DB
}

func New(db *sql.DB) *Service { return &Service{DB: db} }

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// EnsureOwner creates the owner user row if missing.
func (s *Service) EnsureOwner(ctx context.Context) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE id='owner'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := nowUTC()
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO users(id, password_hash, totp_secret, totp_enabled, recovery_codes_hash, created_at, updated_at)
VALUES('owner','', '', 0, '', ?, ?)`, now, now)
	return err
}

// HasPassword reports whether the owner has set a password.
func (s *Service) HasPassword(ctx context.Context) (bool, error) {
	var h string
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id='owner'`).Scan(&h)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return h != "", err
}

// SetPassword hashes and stores the owner password.
func (s *Service) SetPassword(ctx context.Context, password string) error {
	if err := s.EnsureOwner(ctx); err != nil {
		return err
	}
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=? WHERE id='owner'`, h, nowUTC())
	return err
}

// EnableTOTP stores a secret and marks TOTP enabled after verify.
func (s *Service) EnableTOTP(ctx context.Context, secret string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_secret=?, totp_enabled=1, updated_at=? WHERE id='owner'`, secret, nowUTC())
	return err
}

// DisableTOTP clears TOTP.
func (s *Service) DisableTOTP(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_secret='', totp_enabled=0, updated_at=? WHERE id='owner'`, nowUTC())
	return err
}

// TOTPEnabled reports if 2FA is on.
func (s *Service) TOTPEnabled(ctx context.Context) (bool, error) {
	var en int
	err := s.DB.QueryRowContext(ctx, `SELECT totp_enabled FROM users WHERE id='owner'`).Scan(&en)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return en == 1, err
}

type Session struct {
	ID        string
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
}

// Login verifies password (+ optional TOTP) and creates a session.
func (s *Service) Login(ctx context.Context, password, totpCode, ip string) (*Session, error) {
	if err := s.checkRateLimit(ctx, ip); err != nil {
		_ = s.recordAttempt(ctx, ip, false, "rate_limited")
		return nil, err
	}
	var hash, secret string
	var totpEn int
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash, totp_secret, totp_enabled FROM users WHERE id='owner'`).Scan(&hash, &secret, &totpEn)
	if err != nil {
		_ = s.recordAttempt(ctx, ip, false, "no_user")
		return nil, errors.New("invalid credentials")
	}
	if hash == "" {
		_ = s.recordAttempt(ctx, ip, false, "password_unset")
		return nil, errors.New("password not set; use agentctl auth set-password")
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil || !ok {
		_ = s.recordAttempt(ctx, ip, false, "bad_password")
		return nil, errors.New("invalid credentials")
	}
	if totpEn == 1 {
		if !VerifyTOTP(secret, totpCode) {
			_ = s.recordAttempt(ctx, ip, false, "bad_totp")
			return nil, errors.New("invalid credentials")
		}
	}
	sess, err := s.createSession(ctx, ip)
	if err != nil {
		return nil, err
	}
	_ = s.recordAttempt(ctx, ip, true, "ok")
	return sess, nil
}

func (s *Service) createSession(ctx context.Context, ip string) (*Session, error) {
	sid, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exp := now.Add(SessionTTL)
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO sessions(id, user_id, csrf_token, created_at, expires_at, last_seen_at, ip)
VALUES(?,?,?,?,?,?,?)`, sid, "owner", csrf, now.Format(time.RFC3339), exp.Format(time.RFC3339), now.Format(time.RFC3339), ip)
	if err != nil {
		return nil, err
	}
	return &Session{ID: sid, UserID: "owner", CSRFToken: csrf, ExpiresAt: exp}, nil
}

// GetSession loads a non-expired session.
func (s *Service) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	if sessionID == "" {
		return nil, errors.New("no session")
	}
	var sess Session
	var expStr string
	err := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, csrf_token, expires_at FROM sessions WHERE id=?`, sessionID).Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &expStr)
	if err != nil {
		return nil, errors.New("invalid session")
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		_ = s.Logout(ctx, sessionID)
		return nil, errors.New("session expired")
	}
	sess.ExpiresAt = exp
	_, _ = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, nowUTC(), sessionID)
	return &sess, nil
}

// Logout deletes a session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID)
	return err
}

func (s *Service) checkRateLimit(ctx context.Context, ip string) error {
	if ip == "" {
		ip = "unknown"
	}
	since := time.Now().UTC().Add(-LoginWindow).Format(time.RFC3339)
	var fails int
	err := s.DB.QueryRowContext(ctx, `
SELECT COUNT(1) FROM login_attempts WHERE ip=? AND ok=0 AND at>=?`, ip, since).Scan(&fails)
	if err != nil {
		return err
	}
	if fails >= LoginMaxFails {
		return errors.New("too many login attempts; try later")
	}
	return nil
}

func (s *Service) recordAttempt(ctx context.Context, ip string, ok bool, detail string) error {
	flag := 0
	if ok {
		flag = 1
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO login_attempts(at, ip, ok, detail) VALUES(?,?,?,?)`, nowUTC(), ip, flag, detail)
	return err
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ClientIP extracts a best-effort client IP (no X-Forwarded-For trust — single-user loopback).
func ClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// IsLoopback reports whether addr is loopback.
func IsLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// Setting helpers reuse settings table.
func (s *Service) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE user_id='owner' AND key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Service) SetSetting(ctx context.Context, key, valueJSON string) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO settings(user_id, key, value_json, updated_at) VALUES('owner',?,?,?)
ON CONFLICT(user_id, key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`,
		key, valueJSON, nowUTC())
	return err
}
