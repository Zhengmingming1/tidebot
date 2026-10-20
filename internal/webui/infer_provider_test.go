package webui

import (
	"net/http"
	"testing"
)

func TestProviderInfer(t *testing.T) {
	c := login(t)
	code, out := c.json(http.MethodPost, "/api/providers/infer", map[string]any{"base_url": "api.deepseek.com", "env": map[string]string{"ANTHROPIC_MODEL": "deepseek-reasoner", "ANTHROPIC_AUTH_TOKEN": "sk-secret"}})
	ms, _ := out["models"].(map[string]any)
	if code != 200 || out["preset"] != "deepseek" || out["base_url"] != "https://api.deepseek.com/anthropic" || out["fixed"] != true || ms["model"] != "deepseek-reasoner" {
		t.Fatalf("provider infer %d %v", code, out)
	}
	for _, v := range ms {
		if v == "sk-secret" {
			t.Fatal("secret echoed")
		}
	}
	_, out = c.json(http.MethodPost, "/api/providers/infer", map[string]any{"key_prefix": "sk-ant-api03-very-long"})
	if out["preset"] != "anthropic" || out["auth_env"] != "ANTHROPIC_API_KEY" {
		t.Fatalf("key prefix %v", out)
	}
	if rr := c.do(http.MethodPost, "/api/providers/infer", "application/json", []byte("{")); rr.Code != 400 {
		t.Fatalf("bad json %d", rr.Code)
	}
}

func TestFreeName(t *testing.T) {
	taken := map[string]bool{"deepseek": true, "deepseek-2": true}
	if freeName("deepseek", taken) != "deepseek-3" || freeName("kimi", taken) != "kimi" {
		t.Fatal(freeName("deepseek", taken))
	}
}
