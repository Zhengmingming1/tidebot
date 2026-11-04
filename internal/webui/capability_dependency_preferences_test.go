package webui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type testDependencyPreferenceAPI struct {
	testCapabilityAPI
	method  string
	request CapabilityDependencyPreferenceRequest
}

func (a *testDependencyPreferenceAPI) CapabilityDependencyPreferenceRequest(_ context.Context, method string, request CapabilityDependencyPreferenceRequest) (any, error) {
	a.method = method
	a.request = request
	return map[string]any{"preferences": []CapabilityDependencyPreferenceRequest{request}}, nil
}
func TestCapabilityDependencyPreferenceRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 200}, {"PUT", `{"name":"dws","path":"/opt/bin/dws","prefer_managed":true}`, 200}, {"DELETE", `{"name":"dws"}`, 200}, {"POST", `{}`, 405}, {"PUT", `{"name":"dws","execute":true}`, 400}, {"PUT", `{} {}`, 400},
	} {
		t.Run(tc.method+tc.body, func(t *testing.T) {
			api := &testDependencyPreferenceAPI{}
			s := &Server{deps: Deps{Capabilities: api}}
			w := httptest.NewRecorder()
			s.handleCapabilityDependencyPreferences(w, httptest.NewRequest(tc.method, "/api/capabilities/dependency-preferences", strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.status == 200 && api.method != tc.method {
				t.Fatal(api)
			}
			if tc.status == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("preference path cached")
			}
		})
	}
}
