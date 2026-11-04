// Package oauth implements the remote MCP OAuth wire protocol. Calling an action
// method is not authorization: the application must first obtain user approval.
// Tokens are stored only through SecretStore, never in capability lock files.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

const maxBody = 1 << 20

// SecretStore is implemented by the application's existing encrypted secret store.
// Values must never be included in audit logs or serialized application config.
type SecretStore interface {
	Set(name, value string) error
	Get(name string) (string, error)
	Delete(name string) error
}

type Client struct {
	http    *http.Client
	secrets SecretStore
	// LookupIP is injectable for deterministic tests; production defaults to DNS.
	LookupIP func(context.Context, string) ([]net.IPAddr, error)
}

// NewClient preserves an injected proxy-aware HTTP transport and limits all
// responses and redirects. Custom RoundTrippers are trusted network boundaries.
// For the standard transport, direct dials resolve and pin a public IP, preventing
// DNS rebinding. A configured proxy remains responsible for its own DNS routing.
func NewClient(base *http.Client, secrets SecretStore) *Client {
	if base == nil {
		base = &http.Client{}
	}
	h := *base
	if h.Timeout <= 0 || h.Timeout > 30*time.Second {
		h.Timeout = 30 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("OAuth redirects are not followed") }
	c := &Client{http: &h, secrets: secrets, LookupIP: net.DefaultResolver.LookupIPAddr}
	transport := h.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if t, ok := transport.(*http.Transport); ok {
		clone := t.Clone()
		var proxies sync.Map
		if originalProxy := clone.Proxy; originalProxy != nil {
			clone.Proxy = func(req *http.Request) (*url.URL, error) {
				proxy, err := originalProxy(req)
				if err == nil && proxy != nil {
					port := proxy.Port()
					if port == "" {
						port = "80"
						if proxy.Scheme == "https" {
							port = "443"
						}
						if proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" {
							port = "1080"
						}
					}
					proxies.Store(net.JoinHostPort(proxy.Hostname(), port), true)
				}
				return proxy, err
			}
		}
		// A caller-provided TLS dialer would bypass the guarded TCP dial below.
		clone.DialTLSContext = nil
		clone.DialTLS = nil
		clone.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if _, trustedProxy := proxies.Load(addr); trustedProxy {
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// The transport may dial an explicitly configured local proxy. Permit that
			// address only as an injected trusted proxy, never as the resource target.
			ips, err := c.publicIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		}
		h.Transport = clone
	} else {
		h.Transport = transport
	}
	return c
}

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Also exclude shared, documentation, benchmark, reserved and IPv4-mapped
	// translation ranges that can reach internal services through intermediaries.
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		_, n, _ := net.ParseCIDR(cidr)
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
func (c *Client) publicIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return nil, errors.New("OAuth endpoint resolved to a non-public address")
		}
		return []net.IP{ip}, nil
	}
	ips, err := c.LookupIP(ctx, host)
	if err != nil {
		return nil, errors.New("OAuth endpoint DNS lookup failed")
	}
	if len(ips) == 0 {
		return nil, errors.New("OAuth endpoint has no addresses")
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return nil, errors.New("OAuth endpoint resolved to a non-public address")
		}
		out = append(out, ip.IP)
	}
	return out, nil
}
func (c *Client) validateURL(ctx context.Context, raw string) error {
	if err := capability.ValidateRemoteURL(raw); err != nil {
		return err
	}
	u, _ := url.Parse(raw)
	_, err := c.publicIPs(ctx, u.Hostname())
	return err
}
func (c *Client) request(ctx context.Context, method, endpoint, contentType string, body io.Reader, out any) error {
	if err := c.validateURL(ctx, endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("invalid OAuth request")
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("OAuth HTTP request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return errors.New("OAuth response read failed")
	}
	if len(data) > maxBody {
		return errors.New("OAuth response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OAuth endpoint returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("invalid OAuth JSON response")
	}
	return nil
}

type ProtectedResource struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}
type Metadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint            string   `json:"revocation_endpoint,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// DiscoverResource accepts an optional resource_metadata URL from a validated
// WWW-Authenticate challenge. Both that URL and advertised issuers are SSRF-checked.
func (c *Client) DiscoverResource(ctx context.Context, resource, metadataURL string) (ProtectedResource, error) {
	var result ProtectedResource
	if err := c.validateURL(ctx, resource); err != nil {
		return result, err
	}
	if metadataURL == "" {
		u, _ := url.Parse(resource)
		u.Path = "/.well-known/oauth-protected-resource" + strings.TrimSuffix(u.Path, "/")
		u.RawPath = ""
		u.RawQuery = ""
		metadataURL = u.String()
	}
	if err := c.request(ctx, http.MethodGet, metadataURL, "", nil, &result); err != nil {
		return result, err
	}
	if result.Resource != resource {
		return result, errors.New("protected resource metadata does not match requested resource")
	}
	if len(result.AuthorizationServers) == 0 || len(result.AuthorizationServers) > 8 {
		return result, errors.New("invalid authorization server list")
	}
	for _, issuer := range result.AuthorizationServers {
		if err := c.validateURL(ctx, issuer); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (c *Client) DiscoverIssuer(ctx context.Context, issuer string) (Metadata, error) {
	var result Metadata
	if err := c.validateURL(ctx, issuer); err != nil {
		return result, err
	}
	u, _ := url.Parse(issuer)
	if u.RawQuery != "" {
		return result, errors.New("issuer query not permitted")
	}
	p := strings.TrimSuffix(u.Path, "/")
	u.Path = "/.well-known/oauth-authorization-server" + p
	u.RawPath = ""
	err := c.request(ctx, http.MethodGet, u.String(), "", nil, &result)
	if err != nil {
		u.Path = p + "/.well-known/openid-configuration"
		if err = c.request(ctx, http.MethodGet, u.String(), "", nil, &result); err != nil {
			return result, err
		}
	}
	if result.Issuer != issuer {
		return result, errors.New("authorization server issuer mismatch")
	}
	for _, endpoint := range []string{result.AuthorizationEndpoint, result.TokenEndpoint} {
		if err := c.validateURL(ctx, endpoint); err != nil {
			return result, err
		}
	}
	for _, endpoint := range []string{result.RegistrationEndpoint, result.RevocationEndpoint} {
		if endpoint != "" {
			if err := c.validateURL(ctx, endpoint); err != nil {
				return result, err
			}
		}
	}
	if !contains(result.CodeChallengeMethodsSupported, "S256") {
		return result, errors.New("authorization server must support PKCE S256")
	}
	return result, nil
}
func contains(items []string, s string) bool {
	for _, item := range items {
		if item == s {
			return true
		}
	}
	return false
}

func randomString() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type Flow struct {
	State       string `json:"-"`
	Verifier    string `json:"-"`
	Resource    string
	Issuer      string
	ClientID    string
	RedirectURI string
	ExpiresAt   time.Time
}

func (Flow) String() string   { return "OAuth flow [redacted]" }
func (Flow) GoString() string { return "OAuth flow [redacted]" }
func NewFlow(resource, issuer, clientID, redirect string) (Flow, error) {
	if err := capability.ValidateRemoteURL(resource); err != nil {
		return Flow{}, err
	}
	if err := capability.ValidateRemoteURL(issuer); err != nil {
		return Flow{}, err
	}
	if clientID == "" {
		return Flow{}, errors.New("client ID required")
	}
	if err := validateRedirect(redirect); err != nil {
		return Flow{}, err
	}
	state, err := randomString()
	if err != nil {
		return Flow{}, err
	}
	verifier, err := randomString()
	if err != nil {
		return Flow{}, err
	}
	return Flow{State: state, Verifier: verifier, Resource: resource, Issuer: issuer, ClientID: clientID, RedirectURI: redirect, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}
func validateRedirect(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return errors.New("invalid OAuth redirect URI")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return nil
	}
	if u.Scheme != "https" {
		return errors.New("OAuth redirect requires HTTPS or a literal loopback address")
	}
	return nil
}
func AuthorizationURL(metadata Metadata, flow Flow, scopes []string) (string, error) {
	if metadata.Issuer != flow.Issuer || !contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return "", errors.New("flow issuer or PKCE mismatch")
	}
	if err := capability.ValidateRemoteURL(metadata.AuthorizationEndpoint); err != nil {
		return "", err
	}
	if flow.State == "" || len(flow.Verifier) < 43 || time.Now().After(flow.ExpiresAt) {
		return "", errors.New("invalid or expired authorization flow")
	}
	u, _ := url.Parse(metadata.AuthorizationEndpoint)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", flow.ClientID)
	q.Set("redirect_uri", flow.RedirectURI)
	q.Set("state", flow.State)
	q.Set("code_challenge", Challenge(flow.Verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("resource", flow.Resource)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// PendingFlows consumes state exactly once, including failed callbacks. The app
// must bind the key to the authenticated user/session initiating authorization.
type PendingFlows struct {
	mu    sync.Mutex
	flows map[string]Flow
}

func (p *PendingFlows) Put(key string, flow Flow) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flows == nil {
		p.flows = map[string]Flow{}
	}
	for k, v := range p.flows {
		if time.Now().After(v.ExpiresAt) {
			delete(p.flows, k)
		}
	}
	p.flows[key] = flow
}
func (p *PendingFlows) Consume(key, state string) (Flow, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	flow, ok := p.flows[key]
	if !ok {
		return Flow{}, errors.New("unknown OAuth flow")
	}
	delete(p.flows, key)
	if subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 || time.Now().After(flow.ExpiresAt) {
		return Flow{}, errors.New("invalid or expired OAuth state")
	}
	return flow, nil
}

type Registration struct {
	ClientID                string `json:"client_id"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

// Register performs dynamic public-client registration only after caller approval.
func (c *Client) Register(ctx context.Context, metadata Metadata, name, redirect string) (Registration, error) {
	var result Registration
	if metadata.RegistrationEndpoint == "" {
		return result, errors.New("dynamic registration unavailable")
	}
	if err := validateRedirect(redirect); err != nil {
		return result, err
	}
	request := map[string]any{"client_name": name, "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}
	b, _ := json.Marshal(request)
	if err := c.request(ctx, http.MethodPost, metadata.RegistrationEndpoint, "application/json", strings.NewReader(string(b)), &result); err != nil {
		return result, err
	}
	if result.ClientID == "" || result.TokenEndpointAuthMethod != "none" {
		return result, errors.New("authorization server did not register a public client")
	}
	return result, nil
}

type Token struct {
	Issuer       string    `json:"issuer"`
	Resource     string    `json:"resource"`
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func (Token) String() string   { return "OAuth token [redacted]" }
func (Token) GoString() string { return "OAuth token [redacted]" }
func (c *Client) saveToken(key string, token Token) error {
	if c.secrets == nil {
		return errors.New("encrypted token store unavailable")
	}
	if !strings.HasPrefix(key, "cap:") || len(key) > 256 {
		return errors.New("invalid capability token key")
	}
	b, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return c.secrets.Set(key, string(b))
}
func (c *Client) LoadToken(key string) (Token, error) {
	var token Token
	if c.secrets == nil {
		return token, errors.New("encrypted token store unavailable")
	}
	raw, err := c.secrets.Get(key)
	if err != nil {
		return token, err
	}
	if err = json.Unmarshal([]byte(raw), &token); err != nil {
		return token, errors.New("invalid stored OAuth token")
	}
	return token, nil
}
func (c *Client) token(ctx context.Context, endpoint, key string, values url.Values, oldRefresh, issuer string) error {
	var token Token
	if c.secrets == nil || !strings.HasPrefix(key, "cap:") || len(key) > 256 {
		return errors.New("encrypted capability token store required")
	}
	if err := c.request(ctx, http.MethodPost, endpoint, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), &token); err != nil {
		return err
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || strings.ContainsAny(token.AccessToken, "\r\n") || token.ExpiresIn < 0 || token.ExpiresIn > 31536000 {
		return errors.New("invalid OAuth token response")
	}
	token.Issuer = issuer
	token.Resource = values.Get("resource")
	token.ClientID = values.Get("client_id")
	if token.RefreshToken == "" {
		token.RefreshToken = oldRefresh
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return c.saveToken(key, token)
}

// Exchange requires a flow returned by PendingFlows.Consume. No tokens are returned
// to the caller, so handlers cannot accidentally place them in the callback page.
func (c *Client) Exchange(ctx context.Context, metadata Metadata, flow Flow, code, key string) error {
	if metadata.Issuer != flow.Issuer || time.Now().After(flow.ExpiresAt) || code == "" {
		return errors.New("invalid OAuth exchange")
	}
	return c.token(ctx, metadata.TokenEndpoint, key, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {flow.ClientID}, "redirect_uri": {flow.RedirectURI}, "code_verifier": {flow.Verifier}, "resource": {flow.Resource}}, "", metadata.Issuer)
}
func (c *Client) Refresh(ctx context.Context, metadata Metadata, clientID, resource, key string) error {
	if err := capability.ValidateRemoteURL(resource); err != nil {
		return err
	}
	token, err := c.LoadToken(key)
	if err != nil {
		return err
	}
	if token.Issuer != metadata.Issuer || token.Resource != resource || token.ClientID != clientID {
		return errors.New("OAuth token audience or issuer mismatch")
	}
	if token.RefreshToken == "" {
		return errors.New("OAuth refresh token unavailable")
	}
	return c.token(ctx, metadata.TokenEndpoint, key, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "client_id": {clientID}, "resource": {resource}}, token.RefreshToken, metadata.Issuer)
}
func (c *Client) Revoke(ctx context.Context, metadata Metadata, clientID, key string) error {
	token, err := c.LoadToken(key)
	if err != nil {
		return err
	}
	if token.Issuer != metadata.Issuer || token.ClientID != clientID {
		return errors.New("OAuth token issuer mismatch")
	}
	if metadata.RevocationEndpoint == "" {
		return errors.New("OAuth revocation endpoint unavailable")
	}
	value, hint := token.RefreshToken, "refresh_token"
	if value == "" {
		value, hint = token.AccessToken, "access_token"
	}
	values := url.Values{"token": {value}, "token_type_hint": {hint}, "client_id": {clientID}}
	if err := c.request(ctx, http.MethodPost, metadata.RevocationEndpoint, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil); err != nil {
		return err
	}
	return c.secrets.Delete(key)
}

// DoResourceRequest uses the same public-address pinning and no-redirect boundary
// for MCP resource requests. Callers must bound response bytes and redact errors.
func (c *Client) DoResourceRequest(req *http.Request) (*http.Response, error) {
	if err := c.validateURL(req.Context(), req.URL.String()); err != nil {
		return nil, err
	}
	return c.http.Do(req)
}
