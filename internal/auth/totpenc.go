package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// TOTP secrets are stored as "enc:v1:" + base64(nonce || XChaCha20-Poly1305 ciphertext).
// The key is HKDF-derived from the secret store master key; AAD binds the record
// to the user id and purpose so a ciphertext can't be moved to another row/field.
const totpEncPrefix = "enc:v1:"

var ErrTOTPKey = errors.New("2fa secret can't be decrypted")

// SetMasterKey enables at-rest encryption for TOTP secrets.
func (s *Service) SetMasterKey(master []byte) error {
	if len(master) < 32 {
		return errors.New("master key too short")
	}
	k := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, master, nil, []byte("tidebot/auth/totp/v1")), k); err != nil {
		return err
	}
	s.totpKey = k
	return nil
}

func totpAAD(userID string) []byte { return []byte("tidebot:totp:" + userID) }

func (s *Service) sealTOTP(userID, secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	if s.totpKey == nil {
		return "", errors.New("2fa encryption key not configured")
	}
	aead, err := chacha20poly1305.NewX(s.totpKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nonce, nonce, []byte(secret), totpAAD(userID))
	return totpEncPrefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

func (s *Service) openTOTP(userID, stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, totpEncPrefix) {
		return stored, nil // legacy plaintext; MigrateTOTP rewrites it at startup
	}
	if s.totpKey == nil {
		return "", ErrTOTPKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(stored[len(totpEncPrefix):])
	if err != nil {
		return "", ErrTOTPKey
	}
	aead, err := chacha20poly1305.NewX(s.totpKey)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", ErrTOTPKey
	}
	pt, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], totpAAD(userID))
	if err != nil {
		return "", ErrTOTPKey
	}
	return string(pt), nil
}

// MigrateTOTP encrypts any plaintext TOTP secrets in place. Idempotent.
func (s *Service) MigrateTOTP(ctx context.Context) (int, error) {
	if s.totpKey == nil {
		return 0, errors.New("2fa encryption key not configured")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, totp_secret FROM users WHERE totp_secret <> '' AND totp_secret NOT LIKE 'enc:v1:%'`)
	if err != nil {
		return 0, err
	}
	type rec struct{ id, sec string }
	var todo []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.sec); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	n := 0
	for _, r := range todo {
		enc, err := s.sealTOTP(r.id, r.sec)
		if err != nil {
			return n, err
		}
		// Only replace the exact plaintext we read (safe against a concurrent change).
		res, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_secret=? WHERE id=? AND totp_secret=?`, enc, r.id, r.sec)
		if err != nil {
			return n, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			n++
		}
	}
	return n, nil
}
