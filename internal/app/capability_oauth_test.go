package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type oauthFixtureTransport func(*http.Request) (*http.Response, error)

func (f oauthFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func oauthOwnerContext(t *testing.T, a *App) context.Context {
	t.Helper()
	svc := auth.New(a.DB)
	sess, err := svc.CreateLocalSession(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	var ctx context.Context
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sess.ID})
	auth.RequireAuth(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ctx = r.Context() })).ServeHTTP(httptest.NewRecorder(), req)
	if ctx == nil {
		t.Fatal("session not authenticated")
	}
	return ctx
}
func oauthAppFixture(t *testing.T) (*App, capability.Record, *atomic.Int32) {
	t.Helper()
	a := newTestApp(t)
	a.Secrets = secrets.NewMemory()
	c := capability.Component{Type: "mcp", Name: "remote-1", MCP: &capability.MCPServer{Type: "http", URL: "https://mcp.example.com/mcp", OAuth: true}}
	rec, err := a.caps().Install(context.Background(), capability.Lock{Format: capability.LockFormat, ID: "oauth-test", Title: "OAuth fixture", Source: capability.Source{Kind: "registry", Trust: "unknown", Adapter: "mcp-registry"}, Components: []capability.Component{c}}, map[string][]byte{"server.json": []byte(`{"name":"test","remotes":[{"type":"streamable-http","url":"https://mcp.example.com/mcp"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	client := oauth.NewClient(&http.Client{Transport: oauthFixtureTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			body = `{"resource":"https://mcp.example.com/mcp","authorization_servers":["https://auth.example.com"],"scopes_supported":["read"]}`
		case "/.well-known/oauth-authorization-server":
			body = `{"issuer":"https://auth.example.com","authorization_endpoint":"https://auth.example.com/authorize","token_endpoint":"https://auth.example.com/token","registration_endpoint":"https://auth.example.com/register","revocation_endpoint":"https://auth.example.com/revoke","code_challenge_methods_supported":["S256"]}`
		case "/register":
			calls.Add(1)
			body = `{"client_id":"test-client","token_endpoint_auth_method":"none"}`
		case "/token":
			calls.Add(1)
			body = `{"access_token":"fake-test-access","refresh_token":"fake-test-refresh","token_type":"Bearer","expires_in":3600}`
		case "/revoke":
			calls.Add(1)
		default:
			t.Errorf("unexpected URL %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}, a.Secrets)
	client.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	a.capState.oauthClient = client
	return a, rec, calls
}
func TestCapabilityOAuthOwnerApprovalAndCallback(t *testing.T) {
	a, rec, calls := oauthAppFixture(t)
	ctx := oauthOwnerContext(t, a)
	other := oauthOwnerContext(t, a)
	r := webui.CapabilityRequest{CapabilityID: rec.ID, Component: "remote-1"}
	if _, err := a.CapabilityRequest(context.Background(), "oauth_prepare", r); err == nil {
		t.Fatal("non-owner prepared OAuth")
	}
	result, err := a.CapabilityRequest(ctx, "oauth_prepare", r)
	if err != nil {
		t.Fatal(err)
	}
	p := result.(map[string]any)
	r.PreparationID = p["preparation_id"].(string)
	if calls.Load() != 0 {
		t.Fatal("prepare mutated provider")
	}
	if _, err = a.CapabilityRequest(other, "oauth_start", r); err == nil {
		t.Fatal("other session consumed preparation")
	}
	r.RegisterClient = true
	r.Scopes = []string{"read"}
	result, err = a.CapabilityRequest(ctx, "oauth_start", r)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("explicit registration missing")
	}
	u, _ := url.Parse(result.(map[string]string)["authorization_url"])
	r.State = u.Query().Get("state")
	r.Code = "fake-test-code"
	if _, err = a.CapabilityRequest(other, "oauth_callback", r); err == nil {
		t.Fatal("cross-session callback accepted")
	}
	if _, err = a.CapabilityRequest(ctx, "oauth_callback", r); err != nil {
		t.Fatal(err)
	}
	if _, err = a.CapabilityRequest(ctx, "oauth_callback", r); err == nil {
		t.Fatal("callback replay")
	}
	if _, err = a.CapabilityRequest(ctx, "oauth_revoke", r); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("expected register exchange revoke", calls.Load())
	}
	if _, err = a.Secrets.Get("cap:" + rec.ID + ":oauth:remote-1"); err == nil {
		t.Fatal("token not removed")
	}
}
func TestCapabilityOAuthRedirectDoesNotTrustRequestHost(t *testing.T) {
	a := newTestApp(t)
	for _, addr := range []string{"0.0.0.0:7420", "[::]:7420", "example.com:7420", ":7420"} {
		a.Cfg.WebUIAddr = addr
		if _, err := a.capabilityOAuthRedirect("id", "p"); err == nil {
			t.Fatal(addr)
		}
	}
	a.Cfg.WebUIAddr = "127.0.0.1:7420"
	u, err := a.capabilityOAuthRedirect("id", "p")
	if err != nil || u != "http://127.0.0.1:7420/api/capabilities/id/oauth/callback/return" {
		t.Fatal(u, err)
	}
}
func TestCapabilityOAuthHeadersRejectAudienceMixupAndSerializeRefresh(t *testing.T) {
	a, rec, calls := oauthAppFixture(t)
	key := "cap:" + rec.ID + ":oauth:remote-1"
	metadata := oauth.Metadata{Issuer: "https://auth.example.com", TokenEndpoint: "https://auth.example.com/token"}
	cfg, _ := json.Marshal(capabilityOAuthConfig{Metadata: metadata, ClientID: "test-client"})
	if err := a.Secrets.Set(key+":config", string(cfg)); err != nil {
		t.Fatal(err)
	}
	token := oauth.Token{AccessToken: "fake-test-access", RefreshToken: "fake-test-refresh", Resource: "https://other.example.com/mcp", Issuer: metadata.Issuer, ClientID: "test-client", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	put := func() {
		b, _ := json.Marshal(token)
		if err := a.Secrets.Set(key, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	put()
	a.capState.headerTokens = map[string]capabilityHeaderScope{"test-scope": {Record: rec, Component: rec.Lock.Components[0], Expires: time.Now().Add(time.Hour)}}
	raw := json.RawMessage(`{"token":"test-scope"}`)
	if _, err := a.rpcCapabilityHeaders(context.Background(), raw); err == nil {
		t.Fatal("unexpired token leaked to changed resource")
	}
	token.Resource = rec.Lock.Components[0].MCP.URL
	token.ExpiresAt = time.Now().Add(-time.Minute)
	put()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := a.rpcCapabilityHeaders(context.Background(), raw)
			if err != nil {
				t.Error(err)
				return
			}
			if v.(map[string]string)["Authorization"] != "Bearer fake-test-access" {
				t.Error("missing header")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent token refresh rotation", calls.Load())
	}
}

func TestCapabilityOAuthStartRequiresExplicitRegistrationAndReviewedScopes(t *testing.T) {
	a, rec, calls := oauthAppFixture(t)
	ctx := oauthOwnerContext(t, a)
	for _, tc := range []struct {
		register bool
		scopes   []string
	}{{false, []string{"read"}}, {true, []string{"unadvertised"}}} {
		r := webui.CapabilityRequest{CapabilityID: rec.ID, Component: "remote-1"}
		out, err := a.CapabilityRequest(ctx, "oauth_prepare", r)
		if err != nil {
			t.Fatal(err)
		}
		r.PreparationID = out.(map[string]any)["preparation_id"].(string)
		r.RegisterClient = tc.register
		r.Scopes = tc.scopes
		if _, err = a.CapabilityRequest(ctx, "oauth_start", r); err == nil {
			t.Fatal("unapproved registration/scope accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("provider mutated without approval", calls.Load())
	}
}

func TestCapabilityOAuthStableCallbackReusesClient(t *testing.T) {
	a, rec, calls := oauthAppFixture(t)
	ctx := oauthOwnerContext(t, a)
	req := webui.CapabilityRequest{CapabilityID: rec.ID, Component: "remote-1"}
	prepared, err := a.CapabilityRequest(ctx, "oauth_prepare", req)
	if err != nil {
		t.Fatal(err)
	}
	p := prepared.(map[string]any)
	req.PreparationID = p["preparation_id"].(string)
	req.RegisterClient = true
	started, err := a.CapabilityRequest(ctx, "oauth_start", req)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(started.(map[string]string)["authorization_url"])
	req.PreparationID = "return"
	req.State = target.Query().Get("state")
	req.Code = "fake-test-code"
	bad := req
	bad.State = "unmatched-state"
	if _, err = a.CapabilityRequest(ctx, "oauth_callback", bad); err == nil {
		t.Fatal("unmatched state accepted")
	}
	if _, err = a.CapabilityRequest(oauthOwnerContext(t, a), "oauth_callback", req); err == nil {
		t.Fatal("cross-owner stable callback accepted")
	}
	if _, err = a.CapabilityRequest(ctx, "oauth_callback", req); err != nil {
		t.Fatal(err)
	}
	if _, err = a.CapabilityRequest(ctx, "oauth_callback", req); err == nil {
		t.Fatal("callback replay accepted")
	}
	again, err := a.CapabilityRequest(ctx, "oauth_prepare", webui.CapabilityRequest{CapabilityID: rec.ID, Component: "remote-1"})
	if err != nil {
		t.Fatal(err)
	}
	p2 := again.(map[string]any)
	if p2["redirect_uri"] != p["redirect_uri"] || p2["client_id"] != "test-client" || p2["preparation_id"] == p["preparation_id"] {
		t.Fatal("client not reusable", p2)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected registration or exchange", calls.Load())
	}
}
