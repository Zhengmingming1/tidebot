package secrets

import (
	"errors"
	"fmt"
	"testing"
)

func TestEnvBackend(t *testing.T) {
	var b EnvBackend
	t.Setenv("TIDEBOT_SECRET_PROVIDER_KIMI", "sekrit")
	v, err := b.Get("provider:kimi")
	if err != nil || v != "sekrit" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestMemoryAndMask(t *testing.T) {
	m := NewMemory()
	_ = m.Set("a", "abcdefghij")
	v, _ := m.Get("a")
	if Mask(v) == v {
		t.Fatal("mask failed")
	}
	chain := Chain{m, EnvBackend{}}
	got, err := chain.Get("a")
	if err != nil || got != "abcdefghij" {
		t.Fatal(got, err)
	}
}

type unavailableBackend struct{ Backend }

func (unavailableBackend) Get(string) (string, error) {
	return "", fmt.Errorf("fixture storage failure")
}
func TestMissingSecretIsDistinctFromStoreFailure(t *testing.T) {
	for _, b := range []Backend{NewMemory(), EnvBackend{}, NewFileBackend(t.TempDir()), Chain{NewMemory(), EnvBackend{}}} {
		if _, err := b.Get("nonexistent_fixture_secret"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing credential error = %v", err)
		}
	}
	chain := Chain{unavailableBackend{}, NewMemory()}
	if _, err := chain.Get("nonexistent_fixture_secret"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal("store failure hidden by missing fallback", err)
	}
}
