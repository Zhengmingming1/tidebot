package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInPresetsHaveSources(t *testing.T) {
	for _, p := range BuiltIn() {
		if p.ID == "" || p.SourceURL == "" {
			t.Fatalf("preset missing id/source: %+v", p)
		}
		if p.ID != "anthropic" && p.BaseURL == "" {
			t.Fatalf("non-anthropic preset needs base url: %s", p.ID)
		}
	}
}

func TestResolveCustomOverride(t *testing.T) {
	p := Profile{Name: "my-kimi", Preset: "kimi", Model: "custom-model"}
	got, err := p.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://api.moonshot.ai/anthropic" {
		t.Fatalf("base=%s", got.BaseURL)
	}
	if got.Model != "custom-model" {
		t.Fatalf("model=%s", got.Model)
	}
}

func TestChildEnvStripsConflictsAndSetsAuth(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=old-key",
		"ANTHROPIC_BASE_URL=https://example.invalid",
		"HOME=/tmp",
	}
	p := Profile{
		Name: "deepseek", Preset: "deepseek",
		BaseURL: "https://api.deepseek.com/anthropic",
		AuthEnv: "ANTHROPIC_AUTH_TOKEN",
		Model:   "deepseek-flash",
	}
	env := ChildEnv(base, p, "sk-secret-value")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "ANTHROPIC_API_KEY=") {
		t.Fatalf("API_KEY should be stripped when using AUTH_TOKEN: %s", joined)
	}
	if !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=sk-secret-value") {
		t.Fatalf("missing auth token: %s", joined)
	}
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic") {
		t.Fatalf("missing base url: %s", joined)
	}
	if strings.Contains(joined, "example.invalid") {
		t.Fatalf("old base url leaked: %s", joined)
	}
}

func TestMaskSecret(t *testing.T) {
	s := MaskSecret("token=abc123 end", "abc123")
	if strings.Contains(s, "abc123") {
		t.Fatal(s)
	}
}

func TestLoadFileAndRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")
	body := `
providers:
  - name: local-relay
    base_url: http://127.0.0.1:9000
    auth_env: ANTHROPIC_AUTH_TOKEN
    model: foo
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(list)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Get("local-relay")
	if !ok || p.BaseURL != "http://127.0.0.1:9000" {
		t.Fatalf("%+v", p)
	}
	if _, ok := reg.Get("kimi"); !ok {
		t.Fatal("expected built-in kimi")
	}
}

func TestEnvFromOS(t *testing.T) {
	t.Setenv("TIDEBOT_API_KEY_KIMI", "kimi-key")
	p := FromPreset(MustPreset("kimi"))
	key, err := EnvFromOS(p)
	if err != nil || key != "kimi-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func MustPreset(id string) Preset {
	p, ok := LookupPreset(id)
	if !ok {
		panic(id)
	}
	return p
}

func TestKimiCodingPreset(t *testing.T) {
	p, ok := LookupPreset("kimi-coding")
	if !ok {
		t.Fatal("missing kimi-coding")
	}
	if p.BaseURL != "https://api.kimi.com/coding/" {
		t.Fatalf("base=%s", p.BaseURL)
	}
	if p.AuthEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("auth=%s", p.AuthEnv)
	}
	if p.SecretEnv != "env:KIMI_API_KEY" {
		t.Fatalf("secret=%s", p.SecretEnv)
	}
	prof := FromPreset(p)
	resolved, err := prof.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_API_KEY", "test-kimi-key-value")
	key, err := EnvFromOS(resolved)
	if err != nil || key != "test-kimi-key-value" {
		t.Fatalf("key err=%v", err)
	}
	env := ChildEnv([]string{"PATH=/bin", "KIMI_API_KEY=test-kimi-key-value", "ANTHROPIC_AUTH_TOKEN=stale"}, resolved, key)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=test-kimi-key-value") {
		t.Fatalf("missing API_KEY injection: %s", joined)
	}
	if strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=") {
		t.Fatalf("AUTH_TOKEN should be stripped: %s", joined)
	}
	if strings.Contains(joined, "KIMI_API_KEY=") {
		t.Fatalf("source secret env should be stripped from child: %s", joined)
	}
	if !strings.Contains(joined, "CLAUDE_CODE_EFFORT_LEVEL=high") {
		t.Fatalf("missing effort: %s", joined)
	}
}

func TestNormalizeSecretEnv(t *testing.T) {
	if NormalizeSecretEnv("env:KIMI_API_KEY") != "KIMI_API_KEY" {
		t.Fatal()
	}
	if NormalizeSecretEnv("KIMI_API_KEY") != "KIMI_API_KEY" {
		t.Fatal()
	}
}
