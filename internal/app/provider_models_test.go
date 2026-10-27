package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestModelListURLs(t *testing.T) {
	for base, want := range map[string][]string{
		"":                                       {"https://api.anthropic.com/v1/models"},
		"https://gw.example.com/":                {"https://gw.example.com/v1/models"},
		"https://gw.example.com/v1":              {"https://gw.example.com/v1/models"},
		"https://api.deepseek.com/anthropic":     {"https://api.deepseek.com/anthropic/v1/models", "https://api.deepseek.com/v1/models", "https://api.deepseek.com/models"},
		"https://open.bigmodel.cn/api/anthropic": {"https://open.bigmodel.cn/api/anthropic/v1/models", "https://open.bigmodel.cn/v1/models", "https://open.bigmodel.cn/models"},
		"https://dashscope.aliyuncs.com/apps/anthropic": {"https://dashscope.aliyuncs.com/apps/anthropic/v1/models", "https://dashscope.aliyuncs.com/v1/models", "https://dashscope.aliyuncs.com/models", "https://dashscope.aliyuncs.com/compatible-mode/v1/models"},
		"https://api.kimi.com/coding/":                  {"https://api.kimi.com/coding/v1/models", "https://api.kimi.com/v1/models", "https://api.kimi.com/models"},
	} {
		got, bearer := modelListURLs(base)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v", base, got)
		}
		if bearer[0] || (len(bearer) > 1 && !bearer[1]) {
			t.Errorf("%q bearer %v", base, bearer)
		}
	}
}

func TestParseModelList(t *testing.T) {
	ms, more, last, err := parseModelList([]byte(`{"data":[{"id":"claude-a","display_name":"Claude A","created_at":"2025-01-01T00:00:00Z"},{"id":"claude-b","display_name":"claude-b","created_at":"2026-01-01T00:00:00Z"}],"has_more":true,"last_id":"claude-b"}`))
	if err != nil || !more || last != "claude-b" || len(ms) != 2 || ms[0].Name != "Claude A" || ms[1].Name != "" {
		t.Fatalf("%+v %v %q %v", ms, more, last, err)
	}
	if c := cleanModels(ms); c[0].ID != "claude-b" { // dated: newest first
		t.Fatalf("order %+v", c)
	}
	ms, _, _, _ = parseModelList([]byte(`{"object":"list","data":[{"id":"z-chat","owned_by":"x","created":0},{"id":"a-chat"},{"id":"a-chat"},{"id":"bad id"},{"id":"<x>"}]}`))
	if c := cleanModels(ms); len(c) != 2 || c[0].ID != "a-chat" || c[1].Owner != "x" {
		t.Fatalf("clean %+v", c)
	}
	ms, _, _, err = parseModelList([]byte(`{"models":[{"slug":"glm-5"},{"name":"models/glm-4.6"}]}`))
	if err != nil || len(ms) != 2 || ms[1].ID != "glm-4.6" {
		t.Fatalf("models[] %+v %v", ms, err)
	}
	if _, _, _, err := parseModelList([]byte(`<html>`)); err == nil {
		t.Fatal("html accepted")
	}
	if _, _, _, err := parseModelList([]byte(`{"ok":1}`)); err == nil {
		t.Fatal("not a list accepted")
	}
}

// A gateway that speaks Anthropic under /anthropic and lists models only on its root: the
// sub-path 404 moves on, the answer is cached, refresh asks again, and the key never shows.
func TestListModelsFallbackCacheAndSecrets(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	pa := providerAdmin{a}
	const secret = "sk-list-SUPERSECRET-77aa"
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/anthropic/v1/models":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`<html>not found</html>`))
		case "/v1/models":
			if r.Header.Get("authorization") != "Bearer "+secret {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid key ` + r.Header.Get("authorization") + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gw-pro","owned_by":"gw"},{"id":"gw-flash","owned_by":"gw"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "gwl", BaseURL: srv.URL + "/anthropic", Model: "gw-pro", APIKey: strp(secret)}); err != nil {
		t.Fatal(err)
	}
	r, err := pa.ListModels(ctx, "gwl", false)
	if err != nil || r["ok"] != true || r["path"] != "/v1/models" || r["cached"] != false {
		t.Fatalf("list %v %v", r, err)
	}
	ms := r["models"].([]listedModel)
	if len(ms) != 2 || ms[0].ID != "gw-flash" {
		t.Fatalf("models %+v", ms)
	}
	n := hits.Load()
	if r, _ := pa.ListModels(ctx, "gwl", false); r["cached"] != true || hits.Load() != n {
		t.Fatalf("not cached %v", r)
	}
	if r, _ := pa.ListModels(ctx, "gwl", true); r["cached"] != false || hits.Load() == n {
		t.Fatalf("refresh served from cache %v", r)
	}
	// a new key drops the cached list; a refused key is the answer (no other candidates)
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{OriginalName: "gwl", Name: "gwl", BaseURL: srv.URL + "/anthropic", APIKey: strp("sk-wrong-OTHERSECRET-0000")}); err != nil {
		t.Fatal(err)
	}
	r, _ = pa.ListModels(ctx, "gwl", false)
	b, _ := json.Marshal(r)
	if r["ok"] != false || r["status"] != 401 || r["unsupported"] != nil || strings.Contains(string(b), "OTHERSECRET") || !strings.Contains(string(b), "invalid key") {
		t.Fatalf("refused key %s", b)
	}
	if r, _ := pa.ListModels(ctx, "nope", false); r != nil {
		t.Fatal("unknown provider answered")
	}
}

// Anthropic's own list pages with has_more / after_id and takes x-api-key; a provider whose
// every candidate 404s is reported as not publishing a list.
func TestListModelsPagesAndUnsupported(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	pa := providerAdmin{a}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sk-ant-pages-1234" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-new","display_name":"Claude New","created_at":"2026-05-01T00:00:00Z"}],"has_more":true,"last_id":"claude-new"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-old","display_name":"Claude Old","created_at":"2025-05-01T00:00:00Z"}],"has_more":false,"last_id":"claude-old"}`))
	}))
	defer srv.Close()
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "antp", BaseURL: srv.URL, AuthEnv: "ANTHROPIC_API_KEY", APIKey: strp("sk-ant-pages-1234")}); err != nil {
		t.Fatal(err)
	}
	r, _ := pa.ListModels(ctx, "antp", false)
	ms, _ := r["models"].([]listedModel)
	if r["ok"] != true || len(ms) != 2 || ms[0].ID != "claude-new" || ms[1].Name != "Claude Old" {
		t.Fatalf("pages %v", r)
	}
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "nolist", BaseURL: srv.URL + "/anthropic", APIKey: strp("sk-nolist-1234")}); err != nil {
		t.Fatal(err)
	}
	if r, _ := pa.ListModels(ctx, "nolist", false); r["ok"] != false || r["unsupported"] != true {
		t.Fatalf("unsupported %v", r)
	}
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "nokey", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if r, _ := pa.ListModels(ctx, "nokey", false); r["ok"] != false {
		t.Fatalf("no key %v", r)
	}
}
