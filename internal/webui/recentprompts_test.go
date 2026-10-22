package webui

import (
	"net/http"
	"testing"
)

func TestRecentPromptsAPI(t *testing.T) {
	c := login(t)
	code, out := c.json(http.MethodGet, "/api/runs/recent-prompts", nil)
	if code != 200 {
		t.Fatalf("get %d %v", code, out)
	}
	if l, ok := out["prompts"].([]any); !ok || len(l) != 0 {
		t.Fatalf("want empty list, got %v", out)
	}
	if code, _ := c.json(http.MethodPost, "/api/runs/recent-prompts", nil); code != 405 {
		t.Fatalf("post %d", code)
	}
}
