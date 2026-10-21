package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPluginPrefill(t *testing.T) {
	c := login(t)
	code, out := c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "email", "field": "from", "value": "Ming <ming@qq.com>"})
	v, _ := out["values"].(map[string]any)
	if code != 200 || out["provider"] != "qq" || out["note"] != "auth_code" || v["smtp_host"] != "smtp.qq.com" || v["smtp_port"].(float64) != 465 || v["imap_username"] != "ming@qq.com" {
		t.Fatalf("email %d %v", code, out)
	}
	key := "693a91f6-7a1b-4bc4-97a0-0ec2a1fa5aaa"
	_, out = c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "wecom", "field": "webhook_url", "value": key})
	v, _ = out["values"].(map[string]any)
	if out["built"] != true || !strings.HasSuffix(v["webhook_url"].(string), key) || strings.Contains(out["masked"].(string), key) {
		t.Fatalf("wecom %v", out)
	}
	_, out = c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "dingtalk", "field": "webhook_url", "value": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + key})
	if out["mismatch"] != true || len(out["values"].(map[string]any)) != 0 {
		t.Fatalf("mismatch %v", out)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>T</title><link rel="alternate" type="application/atom+xml" href="/atom"></head><body>x</body></html>`))
	}))
	defer srv.Close()
	_, out = c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "rss-feed", "field": "url", "value": srv.URL})
	if out["found"] != false || out["error_code"] != "private" {
		t.Fatalf("ssrf %v", out)
	}
	_, out = c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "rss-feed", "field": "url", "value": srv.URL, "allow_private": true})
	d, _ := out["discovery"].(map[string]any)
	if out["found"] != true || d == nil || len(d["feeds"].([]any)) != 1 {
		t.Fatalf("discover %v", out)
	}
	if code, _ := c.json(http.MethodPost, "/api/plugins/prefill", map[string]any{"plugin": "nope", "value": "x"}); code != 404 {
		t.Fatalf("unknown %d", code)
	}
}
