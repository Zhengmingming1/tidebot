package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// RateLimitError carries how long the client should wait before retrying.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "too many login attempts; try later" }

// ErrBadTOTP: the 6-digit code doesn't match (wrong, expired or already used).
var ErrBadTOTP = errors.New("code does not match")

// pendingTOTP holds a secret between "begin" and "confirm". It lives only in
// memory: 2FA is not enabled (and nothing is persisted) until a code verifies.
type pendingTOTP struct {
	secret string
	exp    time.Time
}

var (
	pendingMu sync.Mutex
	pending   = map[*Service]pendingTOTP{}
)

const PendingTOTPTTL = 10 * time.Minute

// BeginTOTP generates a fresh secret and keeps it pending for confirmation.
func (s *Service) BeginTOTP(ctx context.Context, issuer, account string) (secret, uri string, err error) {
	secret, uri, err = GenerateTOTPSecret(issuer, account)
	if err != nil {
		return "", "", err
	}
	pendingMu.Lock()
	pending[s] = pendingTOTP{secret: secret, exp: time.Now().Add(PendingTOTPTTL)}
	pendingMu.Unlock()
	return secret, uri, nil
}

// ConfirmTOTP verifies a code against the pending secret, enables 2FA and
// returns freshly generated recovery codes. Only hashes are stored.
func (s *Service) ConfirmTOTP(ctx context.Context, code string) ([]string, error) {
	pendingMu.Lock()
	p, ok := pending[s]
	pendingMu.Unlock()
	if !ok || time.Now().After(p.exp) {
		return nil, errors.New("no pending setup; start again")
	}
	step, match := totpStep(p.secret, strings.TrimSpace(code), time.Now())
	if !match {
		return nil, ErrBadTOTP
	}
	codes, hashes, err := newRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	stored, err := s.storeTOTP(p.secret)
	if err != nil {
		return nil, err
	}
	// The confirming code's step counts as used, so it can't be replayed to sign in.
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET totp_secret=?, totp_enabled=1, recovery_codes_hash=?, totp_last_step=?, updated_at=? WHERE id='owner'`,
		stored, strings.Join(hashes, ","), step, nowUTC())
	if err != nil {
		return nil, err
	}
	pendingMu.Lock()
	delete(pending, s)
	pendingMu.Unlock()
	return codes, nil
}

// DisableTOTPChecked turns 2FA off after re-checking the password. Like
// ChangePassword, wrong passwords count as failed logins from ip and the check is
// refused (RateLimitError) once that IP is locked out, so a stolen session can't
// guess the password here. No password set (desktop window) means none is asked.
func (s *Service) DisableTOTPChecked(ctx context.Context, password, ip string) error {
	if ip == "" {
		ip = "unknown"
	}
	if err := s.checkRateLimit(ctx, ip); err != nil {
		_ = s.recordAttempt(ctx, ip, false, "rate_limited")
		return err
	}
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id='owner'`).Scan(&hash); err != nil {
		return ErrBadCurrent
	}
	if hash != "" {
		if ok, err := VerifyPassword(hash, password); err != nil || !ok {
			_ = s.recordAttempt(ctx, ip, false, "2fa_off_bad")
			return ErrBadCurrent
		}
	}
	if err := s.DisableTOTP(ctx); err != nil {
		return err
	}
	_ = s.recordAttempt(ctx, ip, true, "2fa_off")
	return nil
}

// RecoveryCodesLeft counts unused recovery codes.
func (s *Service) RecoveryCodesLeft(ctx context.Context) int {
	var h string
	_ = s.DB.QueryRowContext(ctx, `SELECT recovery_codes_hash FROM users WHERE id='owner'`).Scan(&h)
	if h == "" {
		return 0
	}
	return len(strings.Split(h, ","))
}

// useRecoveryCode consumes a matching recovery code.
func (s *Service) useRecoveryCode(ctx context.Context, code string) bool {
	code = normRecovery(code)
	if len(code) != 10 {
		return false
	}
	var h string
	if err := s.DB.QueryRowContext(ctx, `SELECT recovery_codes_hash FROM users WHERE id='owner'`).Scan(&h); err != nil || h == "" {
		return false
	}
	want := hashRecovery(code)
	parts := strings.Split(h, ",")
	for i, p := range parts {
		if subtle.ConstantTimeCompare([]byte(p), []byte(want)) == 1 {
			rest := append(append([]string{}, parts[:i]...), parts[i+1:]...)
			_, err := s.DB.ExecContext(ctx, `UPDATE users SET recovery_codes_hash=?, updated_at=? WHERE id='owner'`, strings.Join(rest, ","), nowUTC())
			return err == nil
		}
	}
	return false
}

const recAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newRecoveryCodes(n int) (codes, hashes []string, err error) {
	for i := 0; i < n; i++ {
		b := make([]byte, 10)
		if _, err = rand.Read(b); err != nil {
			return nil, nil, err
		}
		for j := range b {
			b[j] = recAlphabet[int(b[j])%len(recAlphabet)]
		}
		c := string(b)
		codes = append(codes, c[:5]+"-"+c[5:])
		hashes = append(hashes, hashRecovery(c))
	}
	return codes, hashes, nil
}

func normRecovery(c string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

func hashRecovery(c string) string {
	sum := sha256.Sum256([]byte("tidebot-recovery:" + c))
	return hex.EncodeToString(sum[:])
}
