package managed

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"golang.org/x/crypto/blake2b"
	"io"
	"os"
	"strings"
)

// MinisignVerifier authenticates modern (ED, prehashed) Minisign signatures.
// Keys maps an independently trusted publisher identity to its base64 public key
// or complete .pub text. Keys MUST come from reviewed configuration/curation, not
// from the downloaded artifact, signature, or untrusted recipe itself.
// Format: https://jedisct1.github.io/minisign/#signature-format
// Legacy non-prehashed Ed signatures are deliberately rejected.
type MinisignVerifier struct {
	Client HTTPDoer
	Keys   map[string]string
}

func (v *MinisignVerifier) Verify(ctx context.Context, filename string, s Signature) error {
	if s.Kind != "minisign" {
		return errors.New("unsupported signature scheme")
	}
	keyText, ok := v.Keys[s.Identity]
	if !ok || s.Identity == "" {
		return errors.New("no trusted minisign publisher key")
	}
	keyLines := strings.Split(strings.TrimSpace(keyText), "\n")
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyLines[len(keyLines)-1]))
	if err != nil || len(key) != 42 || string(key[:2]) != "Ed" {
		return errors.New("invalid trusted minisign public key")
	}
	var body bytes.Buffer
	i := Installer{Client: v.Client}
	if err = i.download(ctx, s.URL, &body, 16<<10); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(body.String(), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "untrusted comment: ") || !strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("invalid minisign signature format")
	}
	signed, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(signed) != 74 || string(signed[:2]) != "ED" || !bytes.Equal(signed[2:10], key[2:10]) {
		return errors.New("minisign algorithm or key ID mismatch")
	}
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return errors.New("invalid minisign comment signature")
	}
	public := ed25519.PublicKey(key[10:])
	comment := strings.TrimPrefix(lines[2], "trusted comment: ")
	message := append(append([]byte(nil), signed[10:]...), []byte(comment)...)
	if !ed25519.Verify(public, message, global) {
		return errors.New("minisign trusted comment authentication failed")
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	hash, _ := blake2b.New512(nil)
	n, err := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, r: f}, MaxDownloadBytes+1))
	if err != nil {
		return err
	}
	if n > MaxDownloadBytes {
		return errors.New("signed archive exceeds limit")
	}
	if !ed25519.Verify(public, hash.Sum(nil), signed[10:]) {
		return errors.New("minisign artifact authentication failed")
	}
	return ctx.Err()
}
