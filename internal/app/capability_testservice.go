package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

type capabilityMCPTest struct {
	Name   string             `json:"name"`
	Status string             `json:"status"`
	Tools  []adapters.MCPTool `json:"tools,omitempty"`
	Note   string             `json:"note,omitempty"`
}
type capabilityTestResult struct {
	CLIAuth            []capabilityCLIAuthTest        `json:"cli_auth,omitempty"`
	Dependencies       map[string]capability.Resolved `json:"dependencies"`
	ID                 string                         `json:"id"`
	VersionID          string                         `json:"version_id"`
	MCP                []capabilityMCPTest            `json:"mcp"`
	ReadOnlyMCPEnabled bool                           `json:"read_only_mcp_enabled"`
	Note               string                         `json:"note"`
}

// testInstalledCapability is discovery, not permission escalation: it sends only
// initialize and tools/list to an already owner-installed server. Server tool
// annotations are untrusted and cannot authorize tools in a read-only session.
func (a *App) testInstalledCapability(ctx context.Context, id string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	record, err := a.caps().Get(ctx, id)
	if err != nil {
		return nil, errors.New("installed capability required")
	}
	if err := a.caps().Verify(ctx, record.ID, record.VersionID); err != nil {
		return nil, errors.New("capability integrity check failed")
	}
	if err := a.verifyManagedCapabilityDependencies(ctx, record.Lock); err != nil {
		return nil, err
	}
	result := capabilityTestResult{ID: record.ID, VersionID: record.VersionID, MCP: []capabilityMCPTest{}, Note: "Discovery never calls tools or starts CLI login. Session tools remain disabled until the owner grants exact reviewed tools. External tools may modify external data; annotations do not prove safety."}
	requirements := append(append([]capability.ToolRequirement{}, record.Lock.Requires.Tools...), record.Lock.Requires.Runtimes...)
	result.Dependencies = capability.Detect(ctx, requirements, capabilityDependencyDetectOptions(record.Lock))
	if curatedDWSLogin(record.Lock) {
		result.CLIAuth = append(result.CLIAuth, a.testDWSCLIAuth(ctx, record.Lock))
	}
	for _, component := range record.Lock.Components {
		if component.Type != "mcp" || component.Disabled {
			continue
		}
		item := capabilityMCPTest{Name: component.Name, Status: "unsupported"}
		if component.MCP == nil {
			return nil, errors.New("MCP configuration missing")
		}
		if component.MCP.Type != "stdio" {
			if component.MCP.Type == "sse" {
				item.Note = "Legacy SSE discovery is unsupported; use Streamable HTTP."
			} else {
				item = a.testCapabilityHTTP(ctx, record, component)
			}
			if err := a.saveCapabilityDiscovery(ctx, record, component, item.Tools); err != nil {
				item.Status = "failed"
				item.Note = "Could not persist discovery"
			}
			result.MCP = append(result.MCP, item)
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dir, err := os.MkdirTemp(a.Cfg.DataDir, ".cap-test-")
		if err != nil {
			return nil, err
		}
		item = a.testCapabilityStdio(ctx, record, component, dir)
		_ = os.RemoveAll(dir)
		if err := a.saveCapabilityDiscovery(ctx, record, component, item.Tools); err != nil {
			item.Status = "failed"
			item.Note = "Could not persist discovery"
		}
		result.MCP = append(result.MCP, item)
	}
	return result, nil
}

func (a *App) testCapabilityStdio(ctx context.Context, record capability.Record, component capability.Component, dir string) capabilityMCPTest {
	item := capabilityMCPTest{Name: component.Name, Status: "failed"}
	// Isolate state and avoid inheriting unrelated host credentials or loader flags.
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "USERPROFILE=" + dir, "TMPDIR=" + dir, "TEMP=" + dir, "TMP=" + dir}
	if root := os.Getenv("SystemRoot"); root != "" {
		env = append(env, "SystemRoot="+root)
	}
	for _, resolved := range record.Lock.Resolved {
		if resolved.Status == "ready" && filepath.IsAbs(resolved.Path) {
			env[0] += string(os.PathListSeparator) + filepath.Dir(resolved.Path)
		}
	}
	opts := runner.Options{EnvBase: env}
	single := record
	single.Lock.Components = []capability.Component{component}
	cleanup, err := a.applyCapabilityMCP(ctx, &opts, single)
	if err != nil {
		item.Note = err.Error()
		return item
	}
	defer cleanup()
	for _, server := range opts.MCPServers {
		config, ok := server.(map[string]any)
		if !ok {
			item.Note = "Invalid MCP launch configuration"
			return item
		}
		command, _ := config["command"].(string)
		args, _ := config["args"].([]string)
		scopedEnv, _ := config["env"].(map[string]string)
		for key, value := range scopedEnv {
			opts.EnvBase = setCapabilityEnv(opts.EnvBase, key, value)
		}
		tools, err := adapters.ValidateStdio(ctx, adapters.StdioLaunch{Command: command, Args: args, Env: opts.EnvBase, Dir: dir, Timeout: 10 * time.Second})
		if err != nil {
			item.Note = err.Error()
			return item
		}
		item.Status = "ready"
		item.Tools = tools
		item.Note = "initialize and tools/list succeeded; no tools were called. Tool metadata is untrusted."
		return item
	}
	item.Note = "MCP server unavailable"
	return item
}

func (a *App) testCapabilityHTTP(ctx context.Context, record capability.Record, component capability.Component) capabilityMCPTest {
	item := capabilityMCPTest{Name: component.Name, Status: "failed"}
	if err := component.MCP.Validate(); err != nil {
		item.Note = err.Error()
		return item
	}
	headers := map[string]string{}
	if component.MCP.OAuth || a.hasCapabilityOAuth(record.ID, component.Name) || len(component.MCP.Headers) > 0 {
		for _, reference := range component.MCP.Headers {
			name, err := capabilitySecretName(reference)
			if err != nil || !declaredCapabilitySecret(record.Lock, name) {
				item.Note = "MCP secret is undeclared"
				return item
			}
		}
		value, err := a.capabilityHeadersForScope(ctx, capabilityHeaderScope{Record: record, Component: component})
		if err != nil {
			item.Note = err.Error()
			return item
		}
		headers = value.(map[string]string)
	}
	tools, err := adapters.ValidateHTTP(ctx, adapters.HTTPLaunch{URL: component.MCP.URL, Headers: headers, Client: a.capabilityOAuthClient()})
	if err != nil {
		item.Note = err.Error()
		return item
	}
	item.Status = "ready"
	item.Tools = tools
	item.Note = "initialize and tools/list succeeded; no tools were called. Tool metadata is untrusted."
	return item
}
