package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
)

type capabilityHeaderScope struct {
	Record    capability.Record
	Component capability.Component
	Expires   time.Time
}

func capabilitySecretName(reference string) (string, error) {
	if !strings.HasPrefix(reference, "${secret:") || !strings.HasSuffix(reference, "}") {
		return "", errors.New("expected a secret reference")
	}
	name := strings.TrimSuffix(strings.TrimPrefix(reference, "${secret:"), "}")
	if name == "" || strings.ContainsAny(name, "{}\\\r\n\x00") {
		return "", errors.New("invalid secret reference")
	}
	return name, nil
}
func declaredCapabilitySecret(lock capability.Lock, name string) bool {
	for _, s := range lock.Requires.Secrets {
		if s.Name == name {
			return true
		}
	}
	return false
}
func optionalCapabilitySecret(lock capability.Lock, name string) bool {
	for _, s := range lock.Requires.Secrets {
		if s.Name == name {
			return s.Optional
		}
	}
	return false
}
func quoteCapabilityHelper(s string) (string, error) {
	if strings.ContainsAny(s, "\x00\r\n") {
		return "", errors.New("invalid helper argument")
	}
	if runtime.GOOS == "windows" {
		if strings.ContainsAny(s, "\"%!") {
			return "", errors.New("unsupported Windows helper path")
		}
		return "\"" + s + "\"", nil
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'", nil
}

// applyCapabilityMCP adds only the selected lock's servers, never global plugin
// connectors. Read-only runs do not receive third-party tools with unknown effects.
func (a *App) applyCapabilityMCP(ctx context.Context, opts *runner.Options, record capability.Record) (func(), error) {
	if err := a.verifyManagedCapabilityDependencies(ctx, record.Lock); err != nil {
		return func() {}, err
	}
	tokens := []string{}
	cleanup := func() {
		a.capState.mu.Lock()
		defer a.capState.mu.Unlock()
		for _, token := range tokens {
			delete(a.capState.headerTokens, token)
		}
	}
	fail := func(err error) (func(), error) { cleanup(); return func() {}, err }
	for _, component := range record.Lock.Components {
		if component.Disabled || component.Type != "mcp" {
			continue
		}
		restricted := opts.KeepSession || opts.RunMode == "read" || opts.TriggerKind == "session"
		var allowed, denied []string
		if restricted {
			var err error
			allowed, denied, err = a.reviewedCapabilityTools(ctx, record, component)
			if err != nil {
				return fail(err)
			}
			if len(allowed) == 0 {
				continue
			}
		}
		if component.MCP == nil {
			return fail(errors.New("MCP configuration missing"))
		}
		m := *component.MCP
		if err := m.Validate(); err != nil {
			return fail(err)
		}
		config := map[string]any{}
		switch m.Type {
		case "stdio":
			command := m.Command
			if resolved, ok := record.Lock.Resolved[component.Name]; ok && resolved.Status == "ready" {
				command = resolved.Path
			}
			if command == "" {
				return fail(fmt.Errorf("capability %s package must be installed first", record.ID))
			}
			if !filepath.IsAbs(command) {
				p, e := exec.LookPath(command)
				if e != nil {
					return fail(errors.New("MCP executable unavailable"))
				}
				command = p
			}
			switch strings.ToLower(strings.TrimSuffix(filepath.Base(command), ".exe")) {
			case "npx", "uvx", "bash", "sh", "zsh", "cmd", "powershell", "pwsh":
				return fail(errors.New("automatic package or shell MCP launch is forbidden"))
			}
			config["command"] = command
			config["args"] = m.Args
			env := map[string]string{}
			for key, value := range m.Env {
				if !allowedCapabilityEnv(key) {
					return fail(errors.New("reserved MCP environment name"))
				}
				if strings.HasPrefix(value, "${secret:") {
					name, e := capabilitySecretName(value)
					if e != nil || !declaredCapabilitySecret(record.Lock, name) {
						return fail(errors.New("MCP secret is undeclared"))
					}
					if a.Secrets == nil {
						if optionalCapabilitySecret(record.Lock, name) {
							continue
						}
						return fail(errors.New("secret store unavailable"))
					}
					value, e = a.Secrets.Get("cap:" + record.ID + ":" + name)
					if e != nil || value == "" {
						if optionalCapabilitySecret(record.Lock, name) {
							continue
						}
						return fail(fmt.Errorf("MCP requires secret %s", name))
					}
				}
				// Keep credentials scoped to this server. mcpArgs serializes this
				// configuration into a run-local 0600 file, removed after the run.
				// The Claude process and other MCP servers must not inherit them.
				env[key] = value
			}
			if len(env) > 0 {
				config["env"] = env
			}
		case "http", "sse", "streamable-http":
			config["type"] = m.Type
			if m.Type == "streamable-http" {
				config["type"] = "http"
			}
			config["url"] = m.URL
			if m.OAuth || a.hasCapabilityOAuth(record.ID, component.Name) || len(m.Headers) > 0 {
				if plugin.BuiltinExe == "" {
					return fail(errors.New("headers helper executable unavailable"))
				}
				for _, reference := range m.Headers {
					name, e := capabilitySecretName(reference)
					if e != nil || !declaredCapabilitySecret(record.Lock, name) {
						return fail(errors.New("HTTP secret is undeclared"))
					}
				}
				random := make([]byte, 32)
				if _, e := rand.Read(random); e != nil {
					return fail(e)
				}
				token := hex.EncodeToString(random)
				a.capState.mu.Lock()
				if a.capState.headerTokens == nil {
					a.capState.headerTokens = map[string]capabilityHeaderScope{}
				}
				a.capState.headerTokens[token] = capabilityHeaderScope{record, component, time.Now().Add(2 * time.Hour)}
				a.capState.mu.Unlock()
				tokens = append(tokens, token)
				parts := []string{}
				for _, part := range []string{plugin.BuiltinExe, sessiontools.HeadersArg, a.Cfg.IPCPath, token} {
					quoted, e := quoteCapabilityHelper(part)
					if e != nil {
						return fail(e)
					}
					parts = append(parts, quoted)
				}
				config["headersHelper"] = strings.Join(parts, " ")
			}
		}
		if opts.MCPServers == nil {
			opts.MCPServers = map[string]any{}
		}
		name := "cap_" + capKey(record.ID+"\x00"+component.Name)
		opts.MCPServers[name] = config
		if restricted {
			appendCapabilityTools(opts, allowed)
			appendCapabilityDeniedTools(opts, denied)
		} else {
			appendCapabilityTools(opts, []string{"mcp__" + name + "__*"})
		}
	}
	return cleanup, nil
}
func (a *App) rpcCapabilityHeaders(ctx context.Context, raw json.RawMessage) (any, error) {
	var req struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &req) != nil {
		return nil, errors.New("invalid headers request")
	}
	a.capState.mu.Lock()
	scope, ok := a.capState.headerTokens[req.Token]
	a.capState.mu.Unlock()
	if !ok || time.Now().After(scope.Expires) {
		return nil, errors.New("headers scope expired")
	}
	return a.capabilityHeadersForScope(ctx, scope)
}
func (a *App) capabilityHeadersForScope(ctx context.Context, scope capabilityHeaderScope) (any, error) {
	if err := a.caps().Verify(ctx, scope.Record.ID, scope.Record.VersionID); err != nil {
		return nil, errors.New("capability integrity check failed")
	}
	headers := map[string]string{}
	for key, reference := range scope.Component.MCP.Headers {
		name, e := capabilitySecretName(reference)
		if e != nil {
			return nil, e
		}
		if a.Secrets == nil {
			if optionalCapabilitySecret(scope.Record.Lock, name) {
				continue
			}
			return nil, errors.New("secret store unavailable")
		}
		value, e := a.Secrets.Get("cap:" + scope.Record.ID + ":" + name)
		if e != nil || value == "" {
			if optionalCapabilitySecret(scope.Record.Lock, name) {
				continue
			}
			return nil, errors.New("HTTP credential missing or invalid")
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("HTTP credential missing or invalid")
		}
		headers[key] = value
	}
	if scope.Component.MCP.OAuth || a.hasCapabilityOAuth(scope.Record.ID, scope.Component.Name) {
		client := a.capabilityOAuthClient()
		key := "cap:" + scope.Record.ID + ":oauth:" + scope.Component.Name
		unlock := a.lockCapabilityOAuth(key)
		defer unlock()
		token, e := client.LoadToken(key)
		if e != nil {
			return nil, errors.New("OAuth login required")
		}
		var config capabilityOAuthConfig
		raw, e := a.Secrets.Get(key + ":config")
		if e != nil || json.Unmarshal([]byte(raw), &config) != nil {
			return nil, errors.New("OAuth login required")
		}
		if token.Resource != scope.Component.MCP.URL || token.Issuer != config.Metadata.Issuer || token.ClientID != config.ClientID {
			return nil, errors.New("OAuth token binding changed; reconnect required")
		}
		if !token.ExpiresAt.IsZero() && time.Now().After(token.ExpiresAt.Add(-time.Minute)) {
			if e = client.Refresh(ctx, config.Metadata, config.ClientID, scope.Component.MCP.URL, key); e != nil {
				return nil, errors.New("OAuth refresh failed; reconnect required")
			}
			token, e = client.LoadToken(key)
			if e != nil {
				return nil, errors.New("OAuth token unavailable")
			}
		}
		if token.AccessToken == "" || strings.ContainsAny(token.AccessToken, "\r\n") {
			return nil, errors.New("OAuth token invalid")
		}
		headers["Authorization"] = "Bearer " + token.AccessToken
	}
	return headers, nil
}

type capabilityOAuthConfig struct {
	Metadata    oauth.Metadata `json:"metadata"`
	ClientID    string         `json:"client_id"`
	RedirectURI string         `json:"redirect_uri"`
}
