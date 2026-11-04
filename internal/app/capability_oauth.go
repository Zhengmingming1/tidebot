package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type capabilityOAuthPreparation struct {
	Owner, CapabilityID, VersionID, Component, Resource, RedirectURI string
	Metadata                                                         oauth.Metadata
	Scopes                                                           []string
	ClientID                                                         string
	Expires                                                          time.Time
	Flow                                                             *oauth.Flow
}

func (a *App) capabilityOAuthClient() *oauth.Client {
	if a.capState.oauthClient != nil {
		return a.capState.oauthClient
	}
	return oauth.NewClient(netproxy.Client(30*time.Second), a.Secrets)
}
func (a *App) capabilityOAuthRedirect(id, _ string) (string, error) {
	host, port, err := net.SplitHostPort(a.Cfg.WebUIAddr)
	if err != nil || host == "" {
		return "", errors.New("configure a concrete WebUI callback address")
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsUnspecified() {
		return "", errors.New("configure a concrete WebUI callback address")
	}
	scheme := "https"
	if a.Cfg.WebUITLSCert == "" {
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("OAuth callbacks require HTTPS or literal loopback WebUI address")
		}
		scheme = "http"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/api/capabilities/" + url.PathEscape(id) + "/oauth/callback/return", nil
}
func (a *App) capabilityOAuthComponent(ctx context.Context, id, name string) (capability.Record, capability.Component, error) {
	rec, err := a.caps().Get(ctx, id)
	if err != nil {
		return rec, capability.Component{}, err
	}
	if err = a.caps().Verify(ctx, id, rec.VersionID); err != nil {
		return rec, capability.Component{}, err
	}
	for _, c := range rec.Lock.Components {
		if c.Name == name && !c.Disabled && c.Type == "mcp" && c.MCP != nil && c.MCP.Type != "stdio" {
			return rec, c, nil
		}
	}
	return rec, capability.Component{}, errors.New("installed remote MCP component required")
}
func (a *App) capabilityOAuthRequest(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil || sess.ID == "" {
		return nil, capError(403, "owner_required", "authenticated owner session required")
	}
	if a.Secrets == nil {
		return nil, errors.New("encrypted secret store unavailable")
	}
	client := a.capabilityOAuthClient()
	switch action {
	case "oauth_prepare":
		rec, c, err := a.capabilityOAuthComponent(ctx, r.CapabilityID, r.Component)
		if err != nil {
			return nil, err
		}
		resource, err := client.DiscoverResource(ctx, c.MCP.URL, "")
		if err != nil {
			return nil, err
		}
		meta, err := client.DiscoverIssuer(ctx, resource.AuthorizationServers[0])
		if err != nil {
			return nil, err
		}
		b := make([]byte, 24)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		id := hex.EncodeToString(b)
		redirect, err := a.capabilityOAuthRedirect(rec.ID, id)
		if err != nil {
			return nil, err
		}
		p := capabilityOAuthPreparation{Owner: sess.ID, CapabilityID: rec.ID, VersionID: rec.VersionID, Component: c.Name, Resource: resource.Resource, RedirectURI: redirect, Metadata: meta, Scopes: resource.ScopesSupported, Expires: time.Now().Add(10 * time.Minute)}
		raw, e := a.Secrets.Get("cap:" + rec.ID + ":oauth:" + c.Name + ":config")
		if e == nil {
			var previous capabilityOAuthConfig
			if json.Unmarshal([]byte(raw), &previous) == nil && previous.Metadata.Issuer == meta.Issuer && previous.RedirectURI == redirect {
				p.ClientID = previous.ClientID
			}
		}
		a.capState.mu.Lock()
		if a.capState.oauthPreparations == nil {
			a.capState.oauthPreparations = map[string]capabilityOAuthPreparation{}
		}
		for k, v := range a.capState.oauthPreparations {
			if time.Now().After(v.Expires) {
				delete(a.capState.oauthPreparations, k)
			}
		}
		if len(a.capState.oauthPreparations) >= 128 {
			a.capState.mu.Unlock()
			return nil, errors.New("too many pending OAuth authorizations")
		}
		a.capState.oauthPreparations[id] = p
		a.capState.mu.Unlock()
		return map[string]any{"preparation_id": id, "resource": p.Resource, "issuer": meta.Issuer, "auth_endpoint": meta.AuthorizationEndpoint, "registration_endpoint": meta.RegistrationEndpoint, "scopes": p.Scopes, "client_id": p.ClientID, "redirect_uri": redirect}, nil
	case "oauth_start", "oauth_callback":
		a.capState.mu.Lock()
		// A stable redirect allows registered clients to be reused. Resolve the
		// one-time preparation from its cryptographic state, still scoped to
		// the authenticated owner and exact capability before consuming it.
		if action == "oauth_callback" && r.PreparationID == "return" {
			r.PreparationID = ""
			for id, pending := range a.capState.oauthPreparations {
				if pending.Owner == sess.ID && pending.CapabilityID == r.CapabilityID && pending.Flow != nil && r.State != "" && subtle.ConstantTimeCompare([]byte(pending.Flow.State), []byte(r.State)) == 1 {
					r.PreparationID = id
					break
				}
			}
		}
		p, ok := a.capState.oauthPreparations[r.PreparationID]
		if !ok || p.Owner != sess.ID || p.CapabilityID != r.CapabilityID || time.Now().After(p.Expires) {
			a.capState.mu.Unlock()
			return nil, capError(400, "invalid_preparation", "invalid or expired OAuth preparation")
		}
		// Consume before network activity, including failed callbacks and starts.
		delete(a.capState.oauthPreparations, r.PreparationID)
		a.capState.mu.Unlock()
		rec, _, err := a.capabilityOAuthComponent(ctx, p.CapabilityID, p.Component)
		if err != nil {
			return nil, err
		}
		if rec.VersionID != p.VersionID {
			return nil, errors.New("capability changed; prepare authorization again")
		}
		if action == "oauth_start" {
			if p.Flow != nil {
				return nil, errors.New("authorization already started")
			}
			for _, scope := range r.Scopes {
				if strings.ContainsAny(scope, " \t\r\n") || scope == "" {
					return nil, errors.New("invalid OAuth scope")
				}
				found := false
				for _, allowed := range p.Scopes {
					if allowed == scope {
						found = true
					}
				}
				if !found {
					return nil, errors.New("scope was not advertised by the resource")
				}
			}
			clientID := strings.TrimSpace(r.ClientID)
			if clientID == "" {
				clientID = p.ClientID
			}
			if r.RegisterClient {
				if clientID != "" {
					return nil, errors.New("choose an existing client or explicit registration")
				}
				registration, e := client.Register(ctx, p.Metadata, "tidebot", p.RedirectURI)
				if e != nil {
					return nil, e
				}
				clientID = registration.ClientID
			}
			flow, e := oauth.NewFlow(p.Resource, p.Metadata.Issuer, clientID, p.RedirectURI)
			if e != nil {
				return nil, e
			}
			target, e := oauth.AuthorizationURL(p.Metadata, flow, r.Scopes)
			if e != nil {
				return nil, e
			}
			p.Flow = &flow
			p.ClientID = clientID
			a.capState.mu.Lock()
			a.capState.oauthPreparations[r.PreparationID] = p
			a.capState.mu.Unlock()
			return map[string]string{"authorization_url": target}, nil
		}
		if p.Flow == nil {
			return nil, errors.New("authorization has not started")
		}
		pending := &oauth.PendingFlows{}
		pending.Put(r.PreparationID, *p.Flow)
		flow, err := pending.Consume(r.PreparationID, r.State)
		if err != nil {
			return nil, err
		}
		if r.OAuthError != "" {
			return nil, errors.New("authorization was denied by the provider")
		}
		key := "cap:" + p.CapabilityID + ":oauth:" + p.Component
		unlock := a.lockCapabilityOAuth(key)
		defer unlock()
		if err = a.rememberCapabilityCredential(ctx, p.CapabilityID, "oauth", p.Component); err != nil {
			return nil, err
		}
		if err = client.Exchange(ctx, p.Metadata, flow, r.Code, key); err != nil {
			return nil, err
		}
		config, _ := json.Marshal(capabilityOAuthConfig{Metadata: p.Metadata, ClientID: p.ClientID, RedirectURI: p.RedirectURI})
		if err = a.Secrets.Set(key+":config", string(config)); err != nil {
			_ = a.Secrets.Delete(key)
			return nil, err
		}
		return map[string]any{"connected": true, "component": p.Component}, nil
	case "oauth_revoke":
		_, c, err := a.capabilityOAuthComponent(ctx, r.CapabilityID, r.Component)
		if err != nil {
			return nil, err
		}
		if err = a.revokeStoredCapabilityOAuth(ctx, r.CapabilityID, c.Name); err != nil {
			return nil, err
		}
		return map[string]bool{"revoked": true}, nil
	}
	return nil, errors.New("unsupported OAuth action")
}

func (a *App) lockCapabilityOAuth(key string) func() {
	value, _ := a.capState.oauthLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (a *App) hasCapabilityOAuth(id, component string) bool {
	if a.Secrets == nil {
		return false
	}
	raw, err := a.Secrets.Get("cap:" + id + ":oauth:" + component + ":config")
	return err == nil && raw != ""
}

// revokeStoredCapabilityOAuth also handles components removed from newer versions.
// Callers must authenticate the owner and obtain approval before invoking it.
func (a *App) revokeStoredCapabilityOAuth(ctx context.Context, capabilityID, component string) error {
	key := "cap:" + capabilityID + ":oauth:" + component
	unlock := a.lockCapabilityOAuth(key)
	defer unlock()
	raw, err := a.Secrets.Get(key + ":config")
	if errors.Is(err, secrets.ErrNotFound) {
		if _, tokenErr := a.Secrets.Get(key); errors.Is(tokenErr, secrets.ErrNotFound) {
			return nil
		} else if tokenErr != nil {
			return tokenErr
		}
		return errors.New("OAuth configuration missing; token retained for recovery")
	}
	if err != nil {
		return err
	}
	// A previous successful revocation may have removed the token before config
	// cleanup failed. Never repeat that provider request.
	if _, tokenErr := a.Secrets.Get(key); errors.Is(tokenErr, secrets.ErrNotFound) {
		return a.Secrets.Delete(key + ":config")
	} else if tokenErr != nil {
		return tokenErr
	}

	var config capabilityOAuthConfig
	if json.Unmarshal([]byte(raw), &config) != nil {
		return errors.New("invalid OAuth configuration")
	}
	if err = a.capabilityOAuthClient().Revoke(ctx, config.Metadata, config.ClientID, key); err != nil {
		return err
	}
	if err = a.Secrets.Delete(key + ":config"); err != nil {
		return err
	}
	a.capState.mu.Lock()
	for k, p := range a.capState.oauthPreparations {
		if p.CapabilityID == capabilityID && p.Component == component {
			delete(a.capState.oauthPreparations, k)
		}
	}
	a.capState.mu.Unlock()
	return nil
}
