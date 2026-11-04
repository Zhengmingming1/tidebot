package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
)

type MarketplaceEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// ParseMarketplace returns discovery metadata only. Local source paths remain
// relative to the marketplace snapshot. Remote refs are discovery metadata only;
// the selected source must be resolved to a commit before proposal confirmation.
func ParseMarketplace(data []byte) ([]MarketplaceEntry, error) {
	var doc struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if err := decode(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Plugins) > 128 {
		return nil, errors.New("too many marketplace entries")
	}
	out := make([]MarketplaceEntry, 0, len(doc.Plugins))
	seen := map[string]bool{}
	for _, p := range doc.Plugins {
		if p.Name == "" || seen[p.Name] {
			return nil, errors.New("invalid or duplicate marketplace name")
		}
		seen[p.Name] = true
		entry := MarketplaceEntry{Name: p.Name}
		if json.Unmarshal(p.Source, &entry.Source) == nil {
			v, err := safePath("", entry.Source)
			if err != nil {
				return nil, err
			}
			entry.Source = v
		} else {
			var source struct{ Source, Repo, URL, Ref, SHA string }
			if err := json.Unmarshal(p.Source, &source); err != nil {
				return nil, err
			}
			if source.SHA != "" && (len(source.SHA) != 40 || strings.Trim(source.SHA, "0123456789abcdefABCDEF") != "") {
				return nil, errors.New("invalid remote marketplace commit")
			}
			switch source.Source {
			case "github":
				if strings.Count(source.Repo, "/") != 1 || strings.ContainsAny(source.Repo, " .\\:@") {
					return nil, errors.New("invalid GitHub source")
				}
				entry.Source = "https://github.com/" + source.Repo
			case "url":
				if err := capability.ValidateRemoteURL(source.URL); err != nil {
					return nil, err
				}
				entry.Source = source.URL
			default:
				return nil, errors.New("unsupported marketplace source")
			}
			if _, err := skillrepo.ValidateURL(entry.Source); err != nil {
				return nil, err
			}
			if err := skillrepo.ValidateRef(source.Ref); err != nil {
				return nil, err
			}
			entry.Ref = source.Ref
			entry.Commit = source.SHA
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func safePath(root, p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", errors.New("invalid relative plugin path")
	}
	// Do not normalize away traversal even when it would remain under root.
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("plugin path traversal")
		}
	}
	clean := path.Clean(path.Join(root, p))
	if root != "" && clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", errors.New("plugin path escapes root")
	}
	return clean, nil
}
func paths(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, errors.New("component paths must be strings")
	}
	return many, nil
}

// ParseClaudePlugin reads a bounded, already materialized snapshot. Paths are
// resolved relative to root, never against the host filesystem. Hooks, LSP and
// monitors remain visible but disabled. Returned skills need registration before
// the lock is installed.
func ParseClaudePlugin(files map[string][]byte, root string) (Result, error) {
	var out Result
	if _, _, err := capability.HashFiles(files); err != nil {
		return out, err
	}
	if root == "." {
		root = ""
	}
	if root != "" {
		r, err := safePath("", root)
		if err != nil {
			return out, err
		}
		root = r
	}
	manifest := path.Join(root, ".claude-plugin/plugin.json")
	var doc struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Skills      json.RawMessage `json:"skills"`
		Commands    json.RawMessage `json:"commands"`
		Agents      json.RawMessage `json:"agents"`
		MCPServers  json.RawMessage `json:"mcpServers"`
		Hooks       json.RawMessage `json:"hooks"`
		LSPServers  json.RawMessage `json:"lspServers"`
		Monitors    json.RawMessage `json:"monitors"`
		UserConfig  map[string]struct {
			Type      string `json:"type"`
			Sensitive bool   `json:"sensitive"`
			Required  bool   `json:"required"`
			Title     string `json:"title"`
		} `json:"userConfig"`
	}
	if err := decode(files[manifest], &doc); err != nil {
		return out, fmt.Errorf("plugin manifest: %w", err)
	}
	if doc.Name == "" {
		return out, errors.New("plugin name required")
	}
	out.Title = doc.Name
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, kind := range []struct {
		typ, dir string
		raw      json.RawMessage
	}{{"skill", "skills", doc.Skills}, {"command", "commands", doc.Commands}, {"agent", "agents", doc.Agents}} {
		supplied, err := paths(kind.raw)
		if err != nil {
			return out, err
		}
		dirs := append([]string{kind.dir}, supplied...)
		seen := map[string]bool{}
		for _, dir := range dirs {
			resolved, err := safePath(root, dir)
			if err != nil {
				return out, err
			}
			for _, file := range keys {
				if file != resolved && !strings.HasPrefix(file, resolved+"/") {
					continue
				}
				if kind.typ == "skill" && path.Base(file) != "SKILL.md" {
					continue
				}
				if kind.typ != "skill" && !strings.HasSuffix(strings.ToLower(file), ".md") {
					continue
				}
				if seen[file] {
					continue
				}
				seen[file] = true
				name := strings.TrimSuffix(path.Base(file), path.Ext(file))
				if kind.typ == "skill" {
					name = path.Base(path.Dir(file))
				}
				out.Components = append(out.Components, capability.Component{Type: kind.typ, Name: name, Path: file})
			}
		}
	}
	for _, disabled := range []struct {
		name string
		raw  json.RawMessage
		file string
	}{{"hooks", doc.Hooks, "hooks/hooks.json"}, {"lsp", doc.LSPServers, ".lsp.json"}, {"monitors", doc.Monitors, "monitors"}} {
		present := len(disabled.raw) > 0 && string(disabled.raw) != "null"
		for _, key := range keys {
			if key == path.Join(root, disabled.file) || strings.HasPrefix(key, path.Join(root, disabled.file)+"/") {
				present = true
			}
		}
		if present {
			out.Components = append(out.Components, capability.Component{Type: disabled.name, Name: disabled.name, Disabled: true})
			out.Findings = append(out.Findings, capability.Finding{ID: "disabled-" + disabled.name, File: manifest, Severity: "caution", Note: "Executable plugin extension is imported disabled."})
		}
	}
	for name, cfg := range doc.UserConfig {
		if !envName.MatchString(name) {
			return out, errors.New("invalid userConfig key")
		}
		out.Requires.Secrets = append(out.Requires.Secrets, capability.SecretRequirement{Name: name, Secret: cfg.Sensitive, Optional: !cfg.Required})
	}
	mcpFiles := []string{}
	if _, ok := files[path.Join(root, ".mcp.json")]; ok {
		mcpFiles = append(mcpFiles, path.Join(root, ".mcp.json"))
	}
	if len(doc.MCPServers) > 0 {
		p, err := paths(doc.MCPServers)
		if err == nil {
			for _, f := range p {
				resolved, err := safePath(root, f)
				if err != nil {
					return out, err
				}
				mcpFiles = append(mcpFiles, resolved)
			}
		} else {
			if err := parseMCP(&out, doc.MCPServers); err != nil {
				return out, err
			}
		}
	}
	seenFiles := map[string]bool{}
	for _, file := range mcpFiles {
		if seenFiles[file] {
			continue
		}
		seenFiles[file] = true
		b, ok := files[file]
		if !ok {
			return out, errors.New("referenced MCP manifest missing")
		}
		if err := parseMCP(&out, b); err != nil {
			return out, err
		}
	}
	if len(out.Components) == 0 || len(out.Components) > 128 {
		return out, errors.New("plugin requires 1 to 128 supported components")
	}
	normalize(&out)
	return out, nil
}

func parseMCP(out *Result, data []byte) error {
	var servers map[string]json.RawMessage
	if err := decode(data, &servers); err != nil {
		return err
	}
	if nested, ok := servers["mcpServers"]; ok {
		servers = nil
		if err := decode(nested, &servers); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !pyName.MatchString(name) {
			return errors.New("invalid MCP server name")
		}
		for _, component := range out.Components {
			if component.Type == "mcp" && component.Name == name {
				return errors.New("duplicate MCP server name")
			}
		}
		var server capability.MCPServer
		if err := decode(servers[name], &server); err != nil {
			return err
		}
		if server.Type == "" {
			server.Type = "stdio"
			if server.URL != "" {
				server.Type = "http"
			}
		}
		if server.Type == "streamable-http" {
			server.Type = "http"
		}
		if server.Type == "stdio" {
			if !pyName.MatchString(server.Command) {
				return errors.New("plugin commands must be executable names, not paths or scripts")
			}
			switch strings.ToLower(server.Command) {
			case "sh", "bash", "zsh", "cmd", "powershell", "pwsh", "python", "python3", "node", "ruby", "perl", "env":
				return errors.New("arbitrary plugin interpreter execution is not supported")
			case "npx", "uvx":
				args := server.Args
				if len(args) > 0 && (args[0] == "-y" || args[0] == "--yes") {
					args = args[1:]
				}
				if len(args) == 0 {
					return errors.New("package launcher requires pinned package")
				}
				pkg, version, backend := "", "", "npm"
				if server.Command == "npx" {
					i := strings.LastIndex(args[0], "@")
					if i <= 0 {
						return errors.New("npm launcher must be pinned")
					}
					pkg, version = args[0][:i], args[0][i+1:]
					if !npmName.MatchString(pkg) {
						return errors.New("invalid npm package")
					}
				} else {
					backend = "uv"
					parts := strings.Split(args[0], "==")
					if len(parts) != 2 || !pyName.MatchString(parts[0]) {
						return errors.New("uvx launcher must be pinned")
					}
					pkg, version = parts[0], parts[1]
				}
				if !exactVersion.MatchString(version) {
					return errors.New("package version must be exact")
				}
				server.Command = ""
				server.Package = pkg
				server.Args = args[1:]
				out.Requires.Tools = append(out.Requires.Tools, capability.ToolRequirement{Name: name, Version: version, Recipes: []capability.Recipe{{Backend: backend, Package: pkg, Version: version}}})
				out.Findings = append(out.Findings, capability.Finding{ID: "package-integrity-required", Severity: "caution", Note: "Package integrity must be locked before installation."})
			}
			for _, arg := range server.Args {
				if strings.Contains(arg, "${CLAUDE_PLUGIN_ROOT}") || strings.ContainsAny(arg, "\x00\r\n") {
					return errors.New("plugin-relative executable arguments require an explicit native recipe")
				}
			}
		}
		for key, value := range server.Env {
			if strings.HasPrefix(value, "${user_config.") && strings.HasSuffix(value, "}") {
				value = "${secret:" + strings.TrimSuffix(strings.TrimPrefix(value, "${user_config."), "}") + "}"
				server.Env[key] = value
			}
			if !strings.HasPrefix(value, "${secret:") {
				return errors.New("plugin environment must use declared secret or userConfig references")
			}
		}
		for key, value := range server.Headers {
			if strings.HasPrefix(value, "${user_config.") && strings.HasSuffix(value, "}") {
				server.Headers[key] = "${secret:" + strings.TrimSuffix(strings.TrimPrefix(value, "${user_config."), "}") + "}"
			}
		}
		if err := server.Validate(); err != nil {
			return err
		}
		for _, values := range []map[string]string{server.Env, server.Headers} {
			for _, value := range values {
				if strings.HasPrefix(value, "${secret:") && strings.HasSuffix(value, "}") {
					out.Requires.Secrets = append(out.Requires.Secrets, capability.SecretRequirement{Name: strings.TrimSuffix(strings.TrimPrefix(value, "${secret:"), "}"), Secret: true})
				}
			}
		}
		out.Components = append(out.Components, capability.Component{Type: "mcp", Name: name, MCP: &server})
	}
	return nil
}
