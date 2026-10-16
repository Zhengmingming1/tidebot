package secrets

import "testing"

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
