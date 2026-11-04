package webui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type testGrantAPI struct {
	testCapabilityAPI
	method string
	grant  CapabilityGrantRequest
}

func (a *testGrantAPI) CapabilityGrantRequest(_ context.Context, method string, req CapabilityGrantRequest) (any, error) {
	a.method = method
	a.grant = req
	return map[string]bool{"ok": true}, nil
}
func TestCapabilityGrantRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, body string
		status       int
	}{{"GET", "", 200}, {"PUT", `{"session_id":"s","capability_id":"c","version_id":"v","component":"server","discovery_hash":"h","tools":["lookup"]}`, 200}, {"DELETE", `{"session_id":"s","capability_id":"c","version_id":"v","component":"server"}`, 200}, {"POST", `{}`, 405}, {"PUT", `{"trusted":true}`, 400}, {"PUT", `{} {}`, 400}} {
		t.Run(tc.method+tc.body, func(t *testing.T) {
			api := &testGrantAPI{}
			s := &Server{deps: Deps{Capabilities: api}}
			w := httptest.NewRecorder()
			s.handleCapabilityGrants(w, httptest.NewRequest(tc.method, "/api/capability-grants?session_id=s", strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.status == 200 && (api.method != tc.method || api.grant.SessionID != "s") {
				t.Fatal(api)
			}
		})
	}
}
