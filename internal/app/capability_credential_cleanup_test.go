package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCapabilityCredentialCleanupRequiresReviewAndRevokesFirst(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "revocation_failure"}[failed], func(t *testing.T) {
			a, rec, _ := oauthAppFixture(t)
			ctx := oauthOwnerContext(t, a)
			rec.Lock.FilesSHA256 = ""
			rec.Lock.Requires.Secrets = []capability.SecretRequirement{{Name: "API_KEY", Secret: true}}
			var err error
			rec, err = a.caps().Install(context.Background(), rec.Lock, map[string][]byte{"server.json": []byte(`{"name":"test","remotes":[{"type":"http","url":"https://mcp.example.com/mcp"}]}`)})
			if err != nil {
				t.Fatal(err)
			}
			key := "cap:" + rec.ID + ":oauth:remote-1"
			cfg, _ := json.Marshal(capabilityOAuthConfig{Metadata: oauth.Metadata{Issuer: "https://auth.example.com", RevocationEndpoint: "https://auth.example.com/revoke"}, ClientID: "test-client"})
			token, _ := json.Marshal(oauth.Token{AccessToken: "fake-test-access", RefreshToken: "fake-test-refresh", Issuer: "https://auth.example.com", Resource: "https://mcp.example.com/mcp", ClientID: "test-client", ExpiresAt: time.Now().Add(time.Hour)})
			for k, v := range map[string]string{key: string(token), key + ":config": string(cfg), "cap:" + rec.ID + ":API_KEY": "fake-test-key", "cap:other:API_KEY": "fake-test-other"} {
				if err = a.Secrets.Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			if failed {
				lookup := a.capState.oauthClient.LookupIP
				a.capState.oauthClient = oauth.NewClient(&http.Client{Transport: oauthFixtureTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("revocation unavailable") })}, a.Secrets)
				a.capState.oauthClient.LookupIP = lookup
			}
			req := webui.CapabilityRequest{CapabilityID: rec.ID}
			if _, err = a.CapabilityRequest(ctx, "credentials_clear", req); err == nil {
				t.Fatal("cleanup without review")
			}
			req.Reviewed = true
			_, err = a.CapabilityRequest(ctx, "credentials_clear", req)
			if failed {
				if err == nil {
					t.Fatal("revocation failure ignored")
				}
				if _, err = a.Secrets.Get(key); err != nil {
					t.Fatal("failed revocation lost local token")
				}
				if _, err = a.Secrets.Get("cap:" + rec.ID + ":API_KEY"); err != nil {
					t.Fatal("partial secret cleanup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{key, key + ":config", "cap:" + rec.ID + ":API_KEY"} {
				if _, err = a.Secrets.Get(k); err == nil {
					t.Fatal("credential remains", k)
				}
			}
			if _, err = a.Secrets.Get("cap:other:API_KEY"); err != nil {
				t.Fatal("other capability affected")
			}
		})
	}
}

func TestCapabilityCredentialCleanupRevokesRemovedHistoricalComponentsOnce(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			a, rec, calls := oauthAppFixture(t)
			ctx := oauthOwnerContext(t, a)
			key := "cap:" + rec.ID + ":oauth:remote-1"
			cfg, _ := json.Marshal(capabilityOAuthConfig{Metadata: oauth.Metadata{Issuer: "https://auth.example.com", RevocationEndpoint: "https://auth.example.com/revoke"}, ClientID: "historical-client"})
			token, _ := json.Marshal(oauth.Token{AccessToken: "old-access", RefreshToken: "old-refresh", Issuer: "https://auth.example.com", Resource: "https://mcp.example.com/mcp", ClientID: "historical-client"})
			for k, v := range map[string]string{key: string(token), key + ":config": string(cfg), "cap:other:oauth:remote-1": "other-token"} {
				if err := a.Secrets.Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			// Repeated historical component identities must not duplicate revocation.
			lock := rec.Lock
			lock.FilesSHA256 = ""
			lock.Source = capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}
			lock.Requires.Secrets = []capability.SecretRequirement{{Name: "HISTORICAL_KEY"}}
			for _, body := range []string{"history-one", "history-two"} {
				if _, err := a.caps().Install(ctx, lock, map[string][]byte{"README.md": []byte(body)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.SetCapabilitySecret(ctx, rec.ID, "HISTORICAL_KEY", "old-secret"); err != nil {
				t.Fatal(err)
			}
			lock.Requires.Secrets = nil
			lock.Components = []capability.Component{{Name: "replacement", Type: "mcp", MCP: &capability.MCPServer{Type: "http", URL: "https://replacement.example.com/mcp"}}}
			if _, err := a.caps().Install(ctx, lock, map[string][]byte{"README.md": []byte("current")}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.caps().Install(ctx, lock, map[string][]byte{"README.md": []byte("newest")}); err != nil {
				t.Fatal(err)
			}
			gc, err := a.caps().GC(ctx, rec.ID, "test")
			if err != nil || len(gc.RemovedVersions) < 3 {
				t.Fatalf("GC fixture did not remove old snapshots: %+v %v", gc, err)
			}
			versions, err := a.caps().Versions(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range versions {
				for _, c := range v.Lock.Components {
					if c.Name == "remote-1" {
						t.Fatal("old OAuth component still discoverable from snapshots")
					}
				}
			}
			if _, _, err := a.capabilityOAuthComponent(ctx, rec.ID, "remote-1"); err == nil {
				t.Fatal("fixture did not remove original component")
			}
			lookup := a.capState.oauthClient.LookupIP
			attempts := 0
			if failed {
				a.capState.oauthClient = oauth.NewClient(&http.Client{Transport: oauthFixtureTransport(func(r *http.Request) (*http.Response, error) {
					attempts++
					return nil, errors.New("fixture revoke failure")
				})}, a.Secrets)
				a.capState.oauthClient.LookupIP = lookup
			}
			req := webui.CapabilityRequest{CapabilityID: rec.ID, Reviewed: true}
			if _, err := a.CapabilityRequest(context.Background(), "credentials_clear", req); err == nil {
				t.Fatal("unauthenticated cleanup accepted")
			}
			_, err = a.CapabilityRequest(ctx, "credentials_clear", req)
			if failed {
				if err == nil || attempts != 1 {
					t.Fatalf("revocation failure not propagated once: %v attempts %d", err, attempts)
				}
				for _, k := range []string{key, key + ":config"} {
					if _, err := a.Secrets.Get(k); err != nil {
						t.Fatal("failed historical revocation lost credentials", k)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("historical grant revoked %d times", calls.Load())
			}
			for _, k := range []string{key, key + ":config", "cap:" + rec.ID + ":HISTORICAL_KEY"} {
				if _, err := a.Secrets.Get(k); err == nil {
					t.Fatal("historical credential remains", k)
				}
			}
			if _, err := a.Secrets.Get("cap:other:oauth:remote-1"); err != nil {
				t.Fatal("unrelated credential removed")
			}
			if _, err := a.CapabilityRequest(ctx, "credentials_clear", req); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("repeat cleanup revoked an already removed grant")
			}
		})
	}
}

func TestCapabilityCredentialIndexBackfillsExistingVersions(t *testing.T) {
	a, rec, _ := oauthAppFixture(t)
	if _, err := a.DB.Exec(`DROP TRIGGER capability_credential_keys_remember; DROP TABLE capability_credential_keys; DELETE FROM schema_migrations WHERE version=39;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(a.DB); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB.QueryRow(`SELECT count(*) FROM capability_credential_keys WHERE capability_id=? AND kind='oauth' AND name='remote-1'`, rec.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("historical OAuth index not backfilled", count, err)
	}
}

type unavailableCapabilitySecrets struct{ secrets.Backend }

func (unavailableCapabilitySecrets) Get(string) (string, error) {
	return "", errors.New("secret backend unavailable")
}
func TestCapabilityCredentialCleanupFailsClosedOnStoreReadError(t *testing.T) {
	a, rec, calls := oauthAppFixture(t)
	ctx := oauthOwnerContext(t, a)
	a.Secrets = unavailableCapabilitySecrets{a.Secrets}
	if _, err := a.CapabilityRequest(ctx, "credentials_clear", webui.CapabilityRequest{CapabilityID: rec.ID, Reviewed: true}); err == nil {
		t.Fatal("unreadable backend reported cleared")
	}
	if calls.Load() != 0 {
		t.Fatal("unreadable backend made a provider request")
	}
}
