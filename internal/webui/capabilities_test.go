package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testCapabilityAPI struct {
	action string
	req    CapabilityRequest
	calls  int
}

func (a *testCapabilityAPI) CapabilityRequest(_ context.Context, action string, r CapabilityRequest) (any, error) {
	a.action = action
	a.req = r
	a.calls++
	return map[string]bool{"ok": true}, nil
}
func TestCapabilityRoutesAndImmutableConfirmation(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, action string
		status                     int
	}{
		{"GET", "/api/capabilities", "", "list", 200},
		{"GET", "/api/capabilities/storage", "", "storage", 200},
		{"GET", "/api/capabilities/defaults", "", "default_get", 200},
		{"GET", "/api/capabilities/run-bindings?run_id=r", "", "run_bindings", 200},
		{"POST", "/api/capabilities/marketplace", `{"source":"https://github.com/example/market"}`, "marketplace", 200},
		{"POST", "/api/capabilities/test-cap/default", `{"default_enabled":true}`, "default_set", 200},
		{"POST", "/api/capabilities/test-cap/deactivate", `{"reviewed":true}`, "deactivate", 200},
		{"POST", "/api/capabilities/storage/gc", `{"reviewed":true}`, "storage_gc", 200},
		{"POST", "/api/capabilities/test-cap/gc", `{"reviewed":true}`, "gc", 200},
		{"POST", "/api/capabilities/test-cap/credentials_clear", `{"reviewed":true}`, "credentials_clear", 200},
		{"GET", "/api/capabilities/storage/gc", "", "", 404},
		{"GET", "/api/capabilities/search?q=pdf", "", "search", 200},
		{"POST", "/api/capabilities/proposals", `{"source":"pdf","session_id":"s"}`, "propose", 201},
		{"POST", "/api/capabilities/proposals/p/confirm", `{"session_id":"s","reviewed":true,"secondary_confirm":true}`, "confirm", 200},
		{"GET", "/api/capabilities/proposals/p/file?session_id=s&path=SKILL.md", "", "file", 200},
		{"POST", "/api/capabilities/proposals/p/confirm", `{"session_id":"s","lock":{"source":"evil"}}`, "", 400},
		{"POST", "/api/capabilities/proposals/p/confirm", `{"session_id":"s"} {"reviewed":true}`, "", 400},
		{"GET", "/api/capabilities/proposals/p/confirm", "", "", 404},
		{"POST", "/api/capabilities/install", `{}`, "", 404},
	} {
		t.Run(tc.method+tc.path+tc.action, func(t *testing.T) {
			api := &testCapabilityAPI{}
			s := &Server{deps: Deps{Capabilities: api}}
			w := httptest.NewRecorder()
			s.handleCapabilities(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || api.action != tc.action {
				t.Fatalf("status %d action %q body %s", w.Code, api.action, w.Body.String())
			}
			if tc.action == "confirm" && api.req.ProposalID != "p" {
				t.Fatalf("wrong proposal %q", api.req.ProposalID)
			}
		})
	}
}
func TestCapabilityConfirmPathCannotBeOverridden(t *testing.T) {
	api := &testCapabilityAPI{}
	s := &Server{deps: Deps{Capabilities: api}}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodPost, "/api/capabilities/proposals/original/confirm", strings.NewReader(`{"session_id":"s","proposal_id":"other"}`)))
	if w.Code != 200 || api.req.ProposalID != "original" {
		t.Fatal(w.Code, api.req)
	}
}

func TestCapabilityOAuthRoutes(t *testing.T) {
	for _, tc := range []struct{ method, path, body, action string }{
		{"POST", "/api/capabilities/cap-test/oauth/prepare", `{"component":"remote-1"}`, "oauth_prepare"},
		{"POST", "/api/capabilities/cap-test/oauth/start", `{"preparation_id":"p","client_id":"test-client","register_client":false,"scopes":["read"]}`, "oauth_start"},
		{"POST", "/api/capabilities/cap-test/oauth/revoke", `{"component":"remote-1"}`, "oauth_revoke"},
		{"GET", "/api/capabilities/cap-test/oauth/callback/p?code=fake-code&state=fake-state", "", "oauth_callback"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			api := &testCapabilityAPI{}
			s := &Server{deps: Deps{Capabilities: api}}
			w := httptest.NewRecorder()
			s.handleCapabilities(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != 200 || api.action != tc.action || api.req.CapabilityID != "cap-test" {
				t.Fatal(w.Code, api)
			}
			if tc.action == "oauth_callback" && (api.req.Code != "fake-code" || api.req.State != "fake-state" || api.req.PreparationID != "p" || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal(api.req, w.Header())
			}
		})
	}
}

func TestCapabilityRecoveryAndOptimizationRoutes(t *testing.T) {
	for _, tc := range []struct{ path, body, action string }{
		{"recover", `{"run_id":"run_failed"}`, "recover"},
		{"skill-optimize", `{"session_id":"chat_a","skill_id":"skill_a","reason":"Improve checks"}`, "skill-optimize"},
	} {
		api := &testCapabilityAPI{}
		s := &Server{deps: Deps{Capabilities: api}}
		w := httptest.NewRecorder()
		s.handleCapabilities(w, httptest.NewRequest(http.MethodPost, "/api/capabilities/"+tc.path, strings.NewReader(tc.body)))
		if w.Code != 200 || api.action != tc.action {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.action == "recover" && api.req.RunID != "run_failed" || tc.action == "skill-optimize" && (api.req.SkillID != "skill_a" || api.req.Reason != "Improve checks") {
			t.Fatalf("bad input: %+v", api.req)
		}
	}
}

func TestCapabilityOAuthLandingDoesNotExchangeWithoutOwner(t *testing.T) {
	api := &testCapabilityAPI{}
	s := &Server{deps: Deps{Capabilities: api}}
	w := httptest.NewRecorder()
	s.handleCapabilityRoute(w, httptest.NewRequest("GET", "/api/capabilities/cap-test/oauth/callback/p?code=fake-code&state=fake-state", nil))
	if w.Code != 200 || api.calls != 0 || !strings.Contains(w.Body.String(), "/api/capabilities/cap-test/oauth/callback/p/complete?") || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Code, w.Body.String(), api.calls)
	}
	w = httptest.NewRecorder()
	s.handleCapabilityRoute(w, httptest.NewRequest("GET", "/api/capabilities/cap-test/oauth/callback/p/complete?code=fake-code&state=fake-state", nil))
	if w.Code != 401 || api.calls != 0 {
		t.Fatal("callback bypassed owner auth", w.Code, api.calls)
	}
}
