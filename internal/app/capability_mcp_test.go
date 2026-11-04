package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

func TestCapabilityStdioCredentialsStayServerScoped(t *testing.T) {
	for _, withBase := range []bool{false, true} {
		t.Run(map[bool]string{false: "session_before_base", true: "initialized_base"}[withBase], func(t *testing.T) {
			a := newTestApp(t)
			a.Secrets = secrets.NewMemory()
			opts := runner.Options{NoPluginMCP: true}
			if withBase {
				opts.EnvBase = []string{"PATH=/fixture/bin", "HOME=/fixture/home", "API_KEY=host-value"}
			}
			original := append([]string(nil), opts.EnvBase...)
			command := filepath.Join(t.TempDir(), "fixture-mcp")
			want := map[string]string{}
			for _, id := range []string{"first", "second"} {
				value := id + "-private-credential"
				if err := a.Secrets.Set("cap:"+id+":TOKEN", value); err != nil {
					t.Fatal(err)
				}
				record := capability.Record{ID: id, Lock: capability.Lock{
					Components: []capability.Component{{Name: "server", Type: "mcp", MCP: &capability.MCPServer{Type: "stdio", Command: command, Env: map[string]string{"API_KEY": "${secret:TOKEN}"}}}},
					Requires:   capability.Requirements{Secrets: []capability.SecretRequirement{{Name: "TOKEN"}}},
				}}
				cleanup, err := a.applyCapabilityMCP(context.Background(), &opts, record)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				name := "cap_" + capKey(id+"\x00server")
				want[name] = value
				config := opts.MCPServers[name].(map[string]any)
				if got := config["env"].(map[string]string)["API_KEY"]; got != value {
					t.Fatal("server credential mismatch")
				}
			}
			if !reflect.DeepEqual(original, opts.EnvBase) {
				t.Fatal("MCP credentials changed the parent environment")
			}
			cleanup := a.applyRunMCP(context.Background(), &opts, cost.BucketProduction)
			defer cleanup()
			path := argAfter(opts.ExtraArgs, "--mcp-config")
			if path == "" {
				t.Fatal("missing run-local MCP configuration")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("configuration mode = %o", info.Mode().Perm())
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Servers map[string]struct {
					Env map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err = json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for name, value := range want {
				if doc.Servers[name].Env["API_KEY"] != value {
					t.Fatal("serialized credential crossed server boundary")
				}
				if strings.Contains(strings.Join(opts.ExtraArgs, " "), value) || strings.Contains(strings.Join(opts.ContextArgs, " "), value) {
					t.Fatal("credential exposed in CLI arguments")
				}
			}
			cleanup()
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("run-local credential file survived cleanup", err)
			}
		})
	}
}

func TestCapabilityMCPOptionalSecrets(t *testing.T) {
	for _, required := range []bool{false, true} {
		for _, missingStore := range []bool{false, true} {
			a := newTestApp(t)
			if !missingStore {
				a.Secrets = secrets.NewMemory()
			} else {
				a.Secrets = nil
			}
			component := capability.Component{Name: "server", Type: "mcp", MCP: &capability.MCPServer{Type: "stdio", Command: filepath.Join(t.TempDir(), "fixture-mcp"), Env: map[string]string{"API_KEY": "${secret:TOKEN}"}}}
			record := capability.Record{ID: "optional-test", Lock: capability.Lock{Components: []capability.Component{component}, Requires: capability.Requirements{Secrets: []capability.SecretRequirement{{Name: "TOKEN", Optional: !required}}}}}
			opts := runner.Options{}
			cleanup, err := a.applyCapabilityMCP(context.Background(), &opts, record)
			cleanup()
			if required {
				if err == nil {
					t.Fatal("missing required stdio credential accepted")
				}
			} else {
				if err != nil {
					t.Fatal("missing optional stdio credential rejected", err)
				}
				for _, server := range opts.MCPServers {
					if env, ok := server.(map[string]any)["env"].(map[string]string); ok && len(env) > 0 {
						t.Fatal("missing optional credential serialized")
					}
				}
			}
			component.MCP = &capability.MCPServer{Type: "http", URL: "https://example.com/mcp", Headers: map[string]string{"X-API-Key": "${secret:TOKEN}"}}
			record, err = a.caps().Install(context.Background(), capability.Lock{Format: capability.LockFormat, ID: "optional-http", Title: "Optional HTTP", Source: capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}, Components: []capability.Component{component}, Requires: record.Lock.Requires}, map[string][]byte{"README.md": []byte("inert")})
			if err != nil {
				t.Fatal(err)
			}
			headers, err := a.capabilityHeadersForScope(context.Background(), capabilityHeaderScope{Record: record, Component: component})
			if required {
				if err == nil {
					t.Fatal("missing required HTTP credential accepted")
				}
			} else {
				if err != nil {
					t.Fatal("missing optional HTTP credential rejected", err)
				}
				if len(headers.(map[string]string)) != 0 {
					t.Fatal("missing optional HTTP credential serialized")
				}
				if !missingStore {
					if err = a.Secrets.Set("cap:"+record.ID+":TOKEN", "present-token"); err != nil {
						t.Fatal(err)
					}
					headers, err = a.capabilityHeadersForScope(context.Background(), capabilityHeaderScope{Record: record, Component: component})
					if err != nil || headers.(map[string]string)["X-API-Key"] != "present-token" {
						t.Fatal("present optional HTTP credential omitted", err)
					}
				}
			}
		}
	}
}
