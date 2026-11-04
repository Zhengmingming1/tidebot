package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCuratedRemoteInstallOAuthDiscoveryExactGrants(t *testing.T) {
	for _, id := range []string{"notion", "linear", "linear-readonly"} {
		t.Run(id, func(t *testing.T) {
			a, git, sid := capFixture(t)
			a.Secrets = secrets.NewMemory()
			ctx := oauthOwnerContext(t, a)
			source, _, err := capSource(id)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, _ := url.Parse(source.URL)
			calls, writes, discoveries := 0, 0, 0
			client := oauth.NewClient(&http.Client{Transport: oauthFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := ""
				switch {
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"):
					if r.URL.Host != endpoint.Host {
						t.Fatalf("wrong resource discovery %s", r.URL)
					}
					b, _ := json.Marshal(map[string]any{"resource": source.URL, "authorization_servers": []string{"https://auth.example.com"}, "scopes_supported": []string{"read"}})
					body = string(b)
				case r.Method == "GET" && r.URL.String() == "https://auth.example.com/.well-known/oauth-authorization-server":
					body = `{"issuer":"https://auth.example.com","authorization_endpoint":"https://auth.example.com/authorize","token_endpoint":"https://auth.example.com/token","registration_endpoint":"https://auth.example.com/register","code_challenge_methods_supported":["S256"]}`
				case r.URL.String() == "https://auth.example.com/register":
					writes++
					body = `{"client_id":"fake-test-client","token_endpoint_auth_method":"none"}`
				case r.URL.String() == "https://auth.example.com/token":
					writes++
					body = `{"access_token":"fake-test-access","token_type":"Bearer","expires_in":3600}`
				case r.URL.String() == source.URL:
					if r.Header.Get("Authorization") != "Bearer fake-test-access" {
						t.Fatal("missing scoped access token")
					}
					var req struct {
						Method string `json:"method"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					discoveries++
					switch req.Method {
					case "initialize":
						body = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}}`
					case "notifications/initialized":
					case "tools/list":
						body = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"lookup","inputSchema":{"type":"object"}},{"name":"delete","inputSchema":{"type":"object"}}]}}`
					default:
						t.Fatalf("discovery called unsafe method %s", req.Method)
					}
				default:
					t.Fatalf("unexpected network request %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}, a.Secrets)
			client.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}
			a.capState.oauthClient = client
			v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: id, SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			p := v.(capabilityProposalView)
			if p.Lock.Source.Commit != p.Lock.FilesSHA256 {
				t.Fatalf("missing config pin/review %+v", p)
			}
			req := webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid}
			if records, err := a.caps().List(ctx); err != nil || len(records) != 0 {
				t.Fatal("proposal installed without explicit confirmation", records, err)
			}
			req.Reviewed = true
			v, err = a.CapabilityRequest(ctx, "confirm", req)
			if err != nil {
				t.Fatal(err)
			}
			rec := v.(capability.Record)
			if calls != 0 || len(git.reads) != 0 {
				t.Fatal("install fetched or executed remote content")
			}
			if !rec.Lock.Components[0].MCP.OAuth {
				t.Fatal("OAuth not required")
			}
			opts := runner.Options{KeepSession: true, RunMode: "read"}
			cleanup, err := a.applySessionCapabilityMCP(ctx, &opts, sid)
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			if len(opts.MCPServers) != 0 {
				t.Fatal("install auto-granted runtime access")
			}
			oauthReq := webui.CapabilityRequest{CapabilityID: rec.ID, Component: "remote-1"}
			v, err = a.CapabilityRequest(ctx, "oauth_prepare", oauthReq)
			if err != nil {
				t.Fatal(err)
			}
			oauthReq.PreparationID = v.(map[string]any)["preparation_id"].(string)
			if writes != 0 {
				t.Fatal("prepare mutated provider")
			}
			oauthReq.RegisterClient = true
			oauthReq.Scopes = []string{"read"}
			v, err = a.CapabilityRequest(ctx, "oauth_start", oauthReq)
			if err != nil {
				t.Fatal(err)
			}
			authURL, _ := url.Parse(v.(map[string]string)["authorization_url"])
			oauthReq.State = authURL.Query().Get("state")
			oauthReq.Code = "fake-test-code"
			if _, err = a.CapabilityRequest(ctx, "oauth_callback", oauthReq); err != nil {
				t.Fatal(err)
			}
			if writes != 2 {
				t.Fatalf("expected explicit registration and exchange, got %d", writes)
			}
			v, err = a.CapabilityRequest(ctx, "test", webui.CapabilityRequest{Source: rec.ID})
			if err != nil {
				t.Fatal(err)
			}
			result := v.(capabilityTestResult)
			if discoveries != 3 || len(result.MCP) != 1 || result.MCP[0].Status != "ready" {
				t.Fatalf("discovery failed %+v", result)
			}
			views, err := a.capabilityGrantViews(ctx, sid)
			if err != nil || len(views) != 1 {
				t.Fatal(views, err)
			}
			if len(views[0].GrantedTools) != 0 {
				t.Fatal("OAuth/discovery granted tools")
			}
			grant := webui.CapabilityGrantRequest{SessionID: sid, CapabilityID: rec.ID, VersionID: rec.VersionID, Component: "remote-1", DiscoveryHash: views[0].DiscoveryHash, Tools: []string{"*"}}
			if _, err = a.CapabilityGrantRequest(ctx, "PUT", grant); err == nil {
				t.Fatal("wildcard accepted")
			}
			grant.Tools = []string{"lookup"}
			if _, err = a.CapabilityGrantRequest(ctx, "PUT", grant); err != nil {
				t.Fatal(err)
			}
			opts = runner.Options{KeepSession: true, RunMode: "read"}
			cleanup, err = a.applySessionCapabilityMCP(ctx, &opts, sid)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if len(opts.MCPServers) != 1 || strings.Contains(argAfter(opts.ContextArgs, "--allowedTools"), "*") || !strings.Contains(argAfter(opts.ContextArgs, "--disallowedTools"), "__delete") {
				t.Fatal("runtime escaped exact grants", opts.ContextArgs)
			}
		})
	}
}

func TestCuratedRemoteRejectsSpoofedSourceAndSnapshot(t *testing.T) {
	a := &App{}
	source, _, err := capSource("notion")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.readCuratedRemoteCapabilitySnapshot(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	files["server.json"] = []byte(`{"name":"notion","remotes":[{"type":"streamable-http","url":"https://evil.example/mcp"}]}`)
	if _, err := scanCuratedRemoteCapability(source, files); err == nil {
		t.Fatal("accepted changed official config")
	}
	source.URL = "https://evil.example/mcp"
	if _, err := a.inspectCuratedRemoteCapability(context.Background(), source); err == nil {
		t.Fatal("accepted spoofed official URL")
	}
}
