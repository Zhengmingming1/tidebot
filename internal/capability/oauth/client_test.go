package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type memorySecrets map[string]string

func (m memorySecrets) Set(k, v string) error { m[k] = v; return nil }
func (m memorySecrets) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (m memorySecrets) Delete(k string) error { delete(m, k); return nil }
func fakeClient(t *testing.T, store SecretStore, fn func(*http.Request) string) *Client {
	t.Helper()
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := fn(r)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}, store)
	c.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	return c
}
func metadata() Metadata {
	return Metadata{Issuer: "https://auth.example.com", AuthorizationEndpoint: "https://auth.example.com/authorize", TokenEndpoint: "https://auth.example.com/token", RegistrationEndpoint: "https://auth.example.com/register", RevocationEndpoint: "https://auth.example.com/revoke", CodeChallengeMethodsSupported: []string{"S256"}}
}
func TestPKCEStateAndAuthorization(t *testing.T) {
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal(got)
	}
	flow, err := NewFlow("https://mcp.example.com/mcp", metadata().Issuer, "client-test", "http://127.0.0.1:8765/callback")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := AuthorizationURL(metadata(), flow, []string{"read", "write"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("resource") != flow.Resource || q.Get("code_challenge_method") != "S256" || q.Get("state") != flow.State {
		t.Fatal("missing OAuth binding")
	}
	pending := &PendingFlows{}
	pending.Put("session-test", flow)
	if _, err := pending.Consume("session-test", "wrong"); err == nil {
		t.Fatal("invalid state accepted")
	}
	if _, err := pending.Consume("session-test", flow.State); err == nil {
		t.Fatal("consumed state reused")
	}
	pending.Put("session-test", flow)
	if _, err := pending.Consume("session-test", flow.State); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Consume("session-test", flow.State); err == nil {
		t.Fatal("state replay")
	}
	flow.ExpiresAt = time.Now().Add(-time.Second)
	pending.Put("session-test", flow)
	if _, err := pending.Consume("session-test", flow.State); err == nil {
		t.Fatal("expired flow accepted")
	}
	if strings.Contains(fmt.Sprint(flow), flow.Verifier) {
		t.Fatal("flow secret formatted")
	}
}
func TestDiscovery(t *testing.T) {
	c := fakeClient(t, nil, func(r *http.Request) string {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			return `{"resource":"https://mcp.example.com/mcp","authorization_servers":["https://auth.example.com"]}`
		case "/.well-known/oauth-authorization-server":
			b, _ := json.Marshal(metadata())
			return string(b)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return ""
		}
	})
	resource, err := c.DiscoverResource(context.Background(), "https://mcp.example.com/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DiscoverIssuer(context.Background(), resource.AuthorizationServers[0]); err != nil {
		t.Fatal(err)
	}
	c = fakeClient(t, nil, func(*http.Request) string {
		return `{"resource":"https://other.example.com","authorization_servers":["https://auth.example.com"]}`
	})
	if _, err := c.DiscoverResource(context.Background(), "https://mcp.example.com/mcp", ""); err == nil {
		t.Fatal("resource mixup accepted")
	}
}
func TestExchangeRefreshRevokeAndRegistration(t *testing.T) {
	store := memorySecrets{}
	calls := 0
	c := fakeClient(t, store, func(r *http.Request) string {
		if r.Method != "POST" {
			t.Fatal("expected POST")
		}
		calls++
		if r.URL.Path == "/register" {
			return `{"client_id":"client-test","token_endpoint_auth_method":"none"}`
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code_verifier") == "" || r.Form.Get("resource") == "" {
				t.Fatal("missing PKCE/resource")
			}
			return `{"access_token":"fake-test-access","refresh_token":"fake-test-refresh","token_type":"Bearer","expires_in":3600}`
		case "refresh_token":
			if r.Form.Get("refresh_token") != "fake-test-refresh" {
				t.Fatal("missing refresh")
			}
			return `{"access_token":"fake-test-access-2","token_type":"Bearer","expires_in":3600}`
		default:
			if r.URL.Path != "/revoke" || r.Form.Get("token") != "fake-test-refresh" {
				t.Fatal("unexpected request")
			}
			return ""
		}
	})
	ctx := context.Background()
	m := metadata()
	if _, err := c.Register(ctx, m, "Test", "http://127.0.0.1:1234/callback"); err != nil {
		t.Fatal(err)
	}
	flow, err := NewFlow("https://mcp.example.com/mcp", m.Issuer, "client-test", "http://127.0.0.1:1234/callback")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Exchange(ctx, m, flow, "fake-code", "cap:test:oauth"); err != nil {
		t.Fatal(err)
	}
	if err = c.Refresh(ctx, m, "client-test", flow.Resource, "cap:test:oauth"); err != nil {
		t.Fatal(err)
	}
	token, err := c.LoadToken("cap:test:oauth")
	if err != nil || token.RefreshToken != "fake-test-refresh" || token.AccessToken != "fake-test-access-2" {
		t.Fatal("refresh rotation lost token")
	}
	if strings.Contains(fmt.Sprint(token), token.AccessToken) {
		t.Fatal("token formatted")
	}
	if err = c.Revoke(ctx, m, "client-test", "cap:test:oauth"); err != nil {
		t.Fatal(err)
	}
	if len(store) != 0 || calls != 4 {
		t.Fatal("token not removed")
	}
}
func TestRejectsSSRFAndUnsafeResponses(t *testing.T) {
	c := fakeClient(t, nil, func(*http.Request) string { t.Fatal("network called for private address"); return "" })
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "2001:db8::1"} {
		c.LookupIP = func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil }
		if err := c.validateURL(context.Background(), "https://auth.example.com"); err == nil {
			t.Fatalf("accepted %s", ip)
		}
	}
	c = fakeClient(t, nil, func(*http.Request) string { return strings.Repeat("x", maxBody+1) })
	if err := c.request(context.Background(), "GET", "https://auth.example.com", "", nil, &Metadata{}); err == nil {
		t.Fatal("oversize response accepted")
	}
	if err := c.http.CheckRedirect(&http.Request{}, nil); err == nil {
		t.Fatal("redirect allowed")
	}
}

func TestExplicitLocalProxyIsAllowedButPrivateTargetIsNot(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.Host != "auth.example.com:443" {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	c := NewClient(&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}, nil)
	c.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	// The fake proxy deliberately refuses the connection. Reaching it proves the
	// explicit loopback proxy was permitted, without contacting any public server.
	if err := c.request(context.Background(), "GET", "https://auth.example.com", "", nil, nil); err == nil {
		t.Fatal("expected fake proxy refusal")
	}
	if calls.Load() != 1 {
		t.Fatal("explicit loopback proxy was rejected")
	}
	if err := c.request(context.Background(), "GET", "https://127.0.0.1/private", "", nil, nil); err == nil {
		t.Fatal("private target accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("private target reached proxy")
	}
}
func TestRefreshRejectsIssuerResourceMixup(t *testing.T) {
	store := memorySecrets{}
	token := Token{AccessToken: "fake-test-access", RefreshToken: "fake-test-refresh", TokenType: "Bearer", Issuer: metadata().Issuer, Resource: "https://mcp.example.com/mcp", ClientID: "client-test"}
	b, _ := json.Marshal(token)
	store["cap:test:oauth"] = string(b)
	c := fakeClient(t, store, func(*http.Request) string { t.Fatal("token transmitted after mixup"); return "" })
	if err := c.Refresh(context.Background(), metadata(), "client-test", "https://other.example.com", "cap:test:oauth"); err == nil {
		t.Fatal("resource mixup accepted")
	}
	m := metadata()
	m.Issuer = "https://other.example.com"
	if err := c.Refresh(context.Background(), m, "client-test", token.Resource, "cap:test:oauth"); err == nil {
		t.Fatal("issuer mixup accepted")
	}
}
