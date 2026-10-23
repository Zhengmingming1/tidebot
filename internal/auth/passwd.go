package auth

import (
	"context"
	"errors"
	"time"
)

// Errors returned by ChangePassword; the WebUI maps each to its own message.
var (
	ErrBadCurrent   = errors.New("invalid credentials")
	ErrPasswordLen  = errors.New("password must be 8 to 1024 characters")
	ErrSamePassword = errors.New("new password is the same as the current one")
)

const (
	PasswordMinLen = 8
	PasswordMaxLen = 1024
)

// ChangePassword replaces the owner password from a signed-in session. The current
// password is required once one is set (a desktop window can sign in without one, so
// the first password needs none). Wrong guesses count as failed logins from ip, so a
// stolen session can't be used to brute-force the password. On success every other
// session is signed out; keepSession stays. Returns how many were signed out.
func (s *Service) ChangePassword(ctx context.Context, current, next, keepSession, ip string) (int, error) {
	if len(next) < PasswordMinLen || len(next) > PasswordMaxLen || len(current) > PasswordMaxLen {
		return 0, ErrPasswordLen
	}
	if ip == "" {
		ip = "unknown"
	}
	if err := s.checkRateLimit(ctx, ip); err != nil {
		_ = s.recordAttempt(ctx, ip, false, "rate_limited")
		return 0, err
	}
	if err := s.EnsureOwner(ctx); err != nil {
		return 0, err
	}
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id='owner'`).Scan(&hash); err != nil {
		return 0, err
	}
	if hash != "" {
		if ok, err := VerifyPassword(hash, current); err != nil || !ok {
			_ = s.recordAttempt(ctx, ip, false, "pw_change_bad")
			return 0, ErrBadCurrent
		}
		if next == current {
			return 0, ErrSamePassword
		}
	}
	h, err := HashPassword(next)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=? WHERE id='owner'`, h, nowUTC()); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id='owner' AND id<>?`, keepSession)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	_ = s.recordAttempt(ctx, ip, true, "pw_changed")
	n, _ := res.RowsAffected()
	return int(n), nil
}

// OtherSessions counts the owner's live sessions besides keep.
func (s *Service) OtherSessions(ctx context.Context, keep string) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM sessions WHERE user_id='owner' AND id<>? AND expires_at>?`,
		keep, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n
}

// SignOutOthers ends every owner session except keep and returns how many ended.
func (s *Service) SignOutOthers(ctx context.Context, keep string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id='owner' AND id<>?`, keep)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
