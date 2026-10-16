// Package secrets resolves API keys from environment or OS keychain backends.
// Secrets are never written to SQLite as plaintext and must not be logged.
package secrets

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Backend retrieves secrets by logical name (e.g. "provider:kimi").
type Backend interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// EnvBackend reads TIDEBOT_SECRET_<NAME> or a mapped env var.
type EnvBackend struct{}

func (EnvBackend) key(name string) string {
	sanitized := strings.ToUpper(name)
	sanitized = strings.ReplaceAll(sanitized, ":", "_")
	sanitized = strings.ReplaceAll(sanitized, "-", "_")
	sanitized = strings.ReplaceAll(sanitized, ".", "_")
	return "TIDEBOT_SECRET_" + sanitized
}

func (e EnvBackend) Get(name string) (string, error) {
	k := e.key(name)
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("secret %q not found in env %s", name, k)
}

func (e EnvBackend) Set(name, value string) error {
	return os.Setenv(e.key(name), value)
}

func (e EnvBackend) Delete(name string) error {
	return os.Unsetenv(e.key(name))
}

// MemoryBackend is an in-memory store for tests (not for production persistence).
type MemoryBackend struct {
	mu sync.RWMutex
	m  map[string]string
}

func NewMemory() *MemoryBackend {
	return &MemoryBackend{m: map[string]string{}}
}

func (m *MemoryBackend) Get(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.m[name]
	if !ok || v == "" {
		return "", fmt.Errorf("secret %q not found", name)
	}
	return v, nil
}

func (m *MemoryBackend) Set(name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[name] = value
	return nil
}

func (m *MemoryBackend) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, name)
	return nil
}

// Chain tries backends in order.
type Chain []Backend

func (c Chain) Get(name string) (string, error) {
	var last error
	for _, b := range c {
		v, err := b.Get(name)
		if err == nil && v != "" {
			return v, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("secret %q not found", name)
	}
	return "", last
}

func (c Chain) Set(name, value string) error {
	if len(c) == 0 {
		return fmt.Errorf("no secret backend")
	}
	return c[0].Set(name, value)
}

func (c Chain) Delete(name string) error {
	var last error
	for _, b := range c {
		if err := b.Delete(name); err != nil {
			last = err
		}
	}
	return last
}

// Mask returns a redacted form for logs (never the full secret).
func Mask(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:2] + "***" + secret[len(secret)-2:]
}
