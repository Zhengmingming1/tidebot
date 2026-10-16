package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileBackend stores secrets AES-256-GCM encrypted in one file. The key lives in a separate
// 0600 file next to it (server mode, final-plan §8.3); desktop builds can swap in the OS keychain.
type FileBackend struct {
	Path    string
	KeyPath string
	mu      sync.Mutex
}

// NewFileBackend uses <dir>/secrets.enc and <dir>/secrets.key.
func NewFileBackend(dir string) *FileBackend {
	return &FileBackend{Path: filepath.Join(dir, "secrets.enc"), KeyPath: filepath.Join(dir, "secrets.key")}
}

func (f *FileBackend) key() ([]byte, error) {
	if b, err := os.ReadFile(f.KeyPath); err == nil && len(b) == 32 {
		return b, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(f.KeyPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(f.KeyPath, k, 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func (f *FileBackend) load() (map[string]string, error) {
	m := map[string]string{}
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	k, err := f.key()
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	if len(data) < g.NonceSize() {
		return nil, errors.New("secrets file corrupt")
	}
	plain, err := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("secrets file cannot be decrypted")
	}
	return m, json.Unmarshal(plain, &m)
}

func (f *FileBackend) save(m map[string]string) error {
	k, err := f.key()
	if err != nil {
		return err
	}
	plain, _ := json.Marshal(m)
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, plain, nil))
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

// Get returns a secret.
func (f *FileBackend) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("secret %q not found", name)
	}
	return v, nil
}

// Set stores a secret.
func (f *FileBackend) Set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[name] = value
	return f.save(m)
}

// Delete removes a secret.
func (f *FileBackend) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, name)
	return f.save(m)
}
