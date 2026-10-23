package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// GenerateTOTPSecret returns a base32-encoded secret and otpauth URI.
func GenerateTOTPSecret(issuer, account string) (secret, uri string, err error) {
	raw := make([]byte, 20)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	uri = fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&digits=6&period=30",
		issuer, account, secret, issuer)
	return secret, uri, nil
}

// VerifyTOTP checks a 6-digit code with ±1 step window. It does not remember used
// codes; sign-in and 2FA setup go through Service.acceptTOTP, which does.
func VerifyTOTP(secret, code string) bool {
	_, ok := totpStep(secret, code, time.Now())
	return ok
}

// totpStep returns the 30-second time step (now-1, now or now+1) whose code equals
// code. The comparison is constant-time.
func totpStep(secret, code string, now time.Time) (int64, bool) {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / 30
	for _, delta := range []int64{-1, 0, 1} {
		if subtle.ConstantTimeCompare([]byte(hotp(key, cur+delta)), []byte(code)) == 1 {
			return cur + delta, true
		}
	}
	return 0, false
}

func hotp(key []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	truncated := (int(sum[offset])&0x7f)<<24 | (int(sum[offset+1])&0xff)<<16 | (int(sum[offset+2])&0xff)<<8 | (int(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", truncated%1000000)
}

// TOTPCodeAt returns the 6-digit code for secret at time t ("" if the secret is invalid).
// Used by tests and by nothing on the request path.
func TOTPCodeAt(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return ""
	}
	return hotp(key, t.Unix()/30)
}
