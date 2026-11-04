package app

import (
	"context"
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

func TestInstalledCapabilityDiscoveryRequiresIntegrity(t *testing.T) {
	a, r := installedCap(t)
	out, err := a.testInstalledCapability(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(capabilityTestResult)
	if result.ID != r.ID || result.ReadOnlyMCPEnabled {
		t.Fatal("incorrect discovery scope")
	}
	if _, err := a.testInstalledCapability(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("uninstalled capability accepted")
	}
	path := filepath.Join(a.Cfg.DataDir, "capabilities", r.ID, r.VersionID, "src", "SKILL.md")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.testInstalledCapability(context.Background(), r.ID); err == nil {
		t.Fatal("tampered capability accepted")
	}
}
func TestManagedExecutableIntegrity(t *testing.T) {
	a, _, _ := capFixture(t)
	dir := filepath.Join(a.Cfg.DataDir, "fixture-env")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture-tool")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	hash, err := managedExecutableDigest(context.Background(), a.Cfg.DataDir, path)
	if err != nil {
		t.Fatal(err)
	}
	treeHash, err := managedTreeDigest(context.Background(), a.Cfg.DataDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	lock := capability.Lock{Resolved: map[string]capability.Resolved{"fixture": {Source: "managed", Status: "ready", Path: path, SHA256: hash, Directory: dir, TreeSHA256: treeHash}}}
	if err := a.verifyManagedCapabilityDependencies(context.Background(), lock); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.verifyManagedCapabilityDependencies(context.Background(), lock); err == nil {
		t.Fatal("tampered executable accepted")
	}
	lock.Requires.Tools = []capability.ToolRequirement{{Name: "missing"}}
	if err := a.verifyManagedCapabilityDependencies(context.Background(), lock); err == nil {
		t.Fatal("missing required dependency accepted")
	}
}

func TestCapabilityHTTPDiscoveryUsesConnectedOAuthWithoutManifestFlag(t *testing.T) {
	a := newTestApp(t)
	a.Secrets = secrets.NewMemory()
	component := capability.Component{Name: "remote", Type: "mcp", MCP: &capability.MCPServer{Type: "http", URL: "https://mcp.example.com/mcp"}}
	record, err := a.caps().Install(context.Background(), capability.Lock{Format: capability.LockFormat, ID: "connected-discovery", Title: "Connected discovery", Source: capability.Source{Kind: "registry", Trust: "unknown", Adapter: "mcp-registry"}, Components: []capability.Component{component}}, map[string][]byte{"server.json": []byte(`{"name":"test","remotes":[{"type":"streamable-http","url":"https://mcp.example.com/mcp"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	key := "cap:" + record.ID + ":oauth:" + component.Name
	config, _ := json.Marshal(capabilityOAuthConfig{Metadata: oauth.Metadata{Issuer: "https://auth.example.com"}, ClientID: "client"})
	token, _ := json.Marshal(oauth.Token{Issuer: "https://auth.example.com", Resource: component.MCP.URL, ClientID: "client", AccessToken: "private-fixture-token", TokenType: "Bearer"})
	if err = a.Secrets.Set(key+":config", string(config)); err != nil {
		t.Fatal(err)
	}
	if err = a.Secrets.Set(key, string(token)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := oauth.NewClient(&http.Client{Transport: oauthFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer private-fixture-token" {
			t.Fatal("connected OAuth credential missing")
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		body := ""
		switch request.Method {
		case "initialize":
			body = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}}`
		case "notifications/initialized":
		case "tools/list":
			body = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"lookup","inputSchema":{"type":"object"}}]}}`
		default:
			t.Fatal("unexpected discovery method", request.Method)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}, a.Secrets)
	client.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	a.capState.oauthClient = client
	out, err := a.testInstalledCapability(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(capabilityTestResult)
	if calls != 3 || len(result.MCP) != 1 || result.MCP[0].Status != "ready" || len(result.MCP[0].Tools) != 1 {
		t.Fatalf("discovery failed: calls=%d result=%+v", calls, result)
	}
}
