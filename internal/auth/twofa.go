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
	if !VerifyTOTP(p.secret, strings.TrimSpace(code)) {
		return nil, errors.New("code does not match")
	}
	codes, hashes, err := newRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET totp_secret=?, totp_enabled=1, recovery_codes_hash=?, updated_at=? WHERE id='owner'`,
		p.secret, strings.Join(hashes, ","), nowUTC())
	if err != nil {
		return nil, err
	}
	pendingMu.Lock()
	delete(pending, s)
	pendingMu.Unlock()
	return codes, nil
}

// DisableTOTPChecked turns 2FA off after re-checking the password.
func (s *Service) DisableTOTPChecked(ctx context.Context, password string) error {
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id='owner'`).Scan(&hash); err != nil {
		return errors.New("invalid credentials")
	}
	if ok, err := VerifyPassword(hash, password); err != nil || !ok {
		return errors.New("invalid credentials")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_secret='', totp_enabled=0, recovery_codes_hash='', updated_at=? WHERE id='owner'`, nowUTC())
	return err
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
