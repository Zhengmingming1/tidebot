package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapabilitySourceRoutesAndImmutablePath(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, action string
		status                     int
	}{
		{"POST", "/api/capabilities/source-skills", `{"source":"https://github.com/example/skills"}`, "source-skills", 200},
		{"GET", "/api/capabilities/sources", "", "sources", 200},
		{"POST", "/api/capabilities/sources", `{"source_kind":"marketplace","name":"Example","source":"https://github.com/example/market"}`, "source_save", 200},
		{"POST", "/api/capabilities/sources/original/refresh", `{"source_id":"spoofed"}`, "source_refresh", 200},
		{"POST", "/api/capabilities/sources/original/delete", `{}`, "source_delete", 200},
		{"GET", "/api/capabilities/sources/original/delete", "", "", 404},
		{"GET", "/api/capabilities/sources/original/refresh", "", "", 404},
		{"POST", "/api/capabilities/sources", `{"source_kind":"marketplace","trust":"official"}`, "", 400},
		{"POST", "/api/capabilities/sources", `{"source_kind":"marketplace","token":"secret"}`, "", 400},
		{"POST", "/api/capabilities/sources", `{} {}`, "", 400},
		{"GET", "/api/capabilities/search?q=pdf&providers=skills.sh,github", "", "search", 200},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			api := &testCapabilityAPI{}
			s := &Server{deps: Deps{Capabilities: api}}
			w := httptest.NewRecorder()
			s.handleCapabilities(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || api.action != tc.action {
				t.Fatal(w.Code, api.action, w.Body.String())
			}
			if (tc.action == "source_refresh" || tc.action == "source_delete") && api.req.SourceID != "original" {
				t.Fatal("body overrode source path", api.req)
			}
			if tc.action == "search" && api.req.Providers != "skills.sh,github" {
				t.Fatal(api.req)
			}
		})
	}
}

func TestCapabilitySourceWritesRequireOwnerAndCSRF(t *testing.T) {
	c := login(t)
	api := &testCapabilityAPI{}
	c.s.deps.Capabilities = api
	for _, path := range []string{"/api/capabilities/sources", "/api/capabilities/sources/id/refresh", "/api/capabilities/sources/id/delete"} {
		anonymous := httptest.NewRecorder()
		c.s.routes().ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if anonymous.Code != 401 {
			t.Fatalf("anonymous: %s %d", path, anonymous.Code)
		}
		original := c.csrf
		c.csrf = "invalid"
		code, _ := c.json("POST", path, map[string]any{})
		c.csrf = original
		if code != 403 || api.calls != 0 {
			t.Fatalf("missing CSRF: %s %d calls=%d", path, code, api.calls)
		}
	}
	if code, _ := c.json("POST", "/api/capabilities/sources", map[string]any{"source_kind": "marketplace", "source": "https://github.com/example/market"}); code != 200 || api.calls != 1 {
		t.Fatal(code, api.calls)
	}
}
