// Package adapters translates inert source metadata into reviewable components.
// It never executes installers, resolves floating package versions or imports hooks.
package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

type Result struct {
	Title       string                  `json:"title"`
	Components  []capability.Component  `json:"components"`
	Requires    capability.Requirements `json:"requires"`
	Permissions capability.Permissions  `json:"permissions"`
	Findings    []capability.Finding    `json:"findings"`
}
type variable struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Default    string `json:"default"`
	IsSecret   bool   `json:"isSecret"`
	IsRequired bool   `json:"isRequired"`
}
type argument struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Value      string `json:"value"`
	IsSecret   bool   `json:"isSecret"`
	IsRequired bool   `json:"isRequired"`
}
type registryPackage struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
	Version      string `json:"version"`
	FileSHA256   string `json:"fileSha256"`
	Integrity    string `json:"integrity"`
	Transport    struct {
		Type string `json:"type"`
	} `json:"transport"`
	EnvironmentVariables []variable `json:"environmentVariables"`
	PackageArguments     []argument `json:"packageArguments"`
	RuntimeArguments     []argument `json:"runtimeArguments"`
}

var exactVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9][A-Za-z0-9.-]*)?$`)
var npmName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var pyName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func decode(data []byte, v any) error {
	if len(data) == 0 || len(data) > capability.MaxFileBytes {
		return errors.New("manifest size limit exceeded")
	}
	return json.Unmarshal(data, v)
}

// ParseRegistry accepts the official server.json schema. Packages are inert
// recipes: no npx/uvx auto-install command is emitted. Missing integrity is a
// finding and must be resolved by the installer before download/execution.
func ParseRegistry(data []byte) (Result, error) {
	var doc struct {
		Name     string            `json:"name"`
		Title    string            `json:"title"`
		Packages []registryPackage `json:"packages"`
		Remotes  []struct {
			Type    string     `json:"type"`
			URL     string     `json:"url"`
			Headers []variable `json:"headers"`
		} `json:"remotes"`
	}
	var out Result
	if err := decode(data, &doc); err != nil {
		return out, err
	}
	if doc.Name == "" || len(doc.Packages)+len(doc.Remotes) == 0 || len(doc.Packages)+len(doc.Remotes) > 128 {
		return out, errors.New("invalid registry server")
	}
	out.Title = doc.Title
	if out.Title == "" {
		out.Title = doc.Name
	}
	for i, p := range doc.Packages {
		if p.Transport.Type != "" && p.Transport.Type != "stdio" {
			return out, errors.New("package transport must be stdio")
		}
		if len(p.RuntimeArguments) > 0 {
			return out, errors.New("arbitrary package runtime arguments are not supported")
		}
		recipe := capability.Recipe{Package: p.Identifier, Version: p.Version, SHA256: p.FileSHA256, Integrity: p.Integrity}
		switch p.RegistryType {
		case "npm":
			if !npmName.MatchString(p.Identifier) || !exactVersion.MatchString(p.Version) {
				return out, errors.New("npm package requires a name and exact version")
			}
			recipe.Backend = "npm"
		case "pypi":
			if !pyName.MatchString(p.Identifier) || !exactVersion.MatchString(p.Version) {
				return out, errors.New("PyPI package requires a name and exact version")
			}
			recipe.Backend = "uv"
		case "oci":
			split := strings.Split(p.Identifier, "@")
			if len(split) != 2 || !digest.MatchString(split[1]) || strings.ContainsAny(split[0], " \t\r\n") || strings.HasPrefix(split[0], "-") {
				return out, errors.New("OCI package requires an immutable digest")
			}
			recipe.Backend = "oci"
			recipe.Digest = split[1]
		case "mcpb":
			out.Components = append(out.Components, capability.Component{Type: "mcp", Name: fmt.Sprintf("package-%d", i+1), Disabled: true, MCP: &capability.MCPServer{Type: "stdio", Package: p.Identifier}})
			out.Findings = append(out.Findings, capability.Finding{ID: "unsupported-mcpb", Severity: "caution", Note: "MCP bundles are not executed; use a supported package or remote transport."})
			continue
		default:
			return out, errors.New("unsupported package registry")
		}
		server := &capability.MCPServer{Type: "stdio", Package: p.Identifier, Env: map[string]string{}}
		for _, a := range p.PackageArguments {
			if a.IsSecret || a.Value == "" || strings.ContainsAny(a.Value, "\x00\r\n") || strings.Contains(a.Value, "{") {
				return out, errors.New("package arguments must be fixed non-secret values")
			}
			if a.Type == "named" {
				if !strings.HasPrefix(a.Name, "-") || strings.ContainsAny(a.Name, " \t\r\n") {
					return out, errors.New("invalid package argument")
				}
				server.Args = append(server.Args, a.Name)
			} else if a.Type != "positional" {
				return out, errors.New("unsupported package argument")
			}
			server.Args = append(server.Args, a.Value)
		}
		for _, v := range p.EnvironmentVariables {
			if !envName.MatchString(v.Name) {
				return out, errors.New("invalid environment variable")
			}
			// All values, including ordinary settings, are entered through the
			// reviewed configuration UI rather than copied from upstream files.
			out.Requires.Secrets = append(out.Requires.Secrets, capability.SecretRequirement{Name: v.Name, Secret: v.IsSecret, Optional: !v.IsRequired})
			server.Env[v.Name] = "${secret:" + v.Name + "}"

		}
		name := fmt.Sprintf("package-%d", i+1)
		out.Components = append(out.Components, capability.Component{Type: "mcp", Name: name, MCP: server})
		out.Requires.Tools = append(out.Requires.Tools, capability.ToolRequirement{Name: name, Version: p.Version, Recipes: []capability.Recipe{recipe}})
		if recipe.Backend != "oci" && recipe.SHA256 == "" && recipe.Integrity == "" {
			out.Findings = append(out.Findings, capability.Finding{ID: "package-integrity-required", Severity: "caution", Note: "Package integrity must be locked before installation."})
		}
	}
	for i, r := range doc.Remotes {
		typ := r.Type
		if typ == "streamable-http" {
			typ = "http"
		}
		server := &capability.MCPServer{Type: typ, URL: r.URL, Headers: map[string]string{}}
		for _, h := range r.Headers {
			if !envName.MatchString(strings.ReplaceAll(h.Name, "-", "_")) {
				return out, errors.New("invalid remote header")
			}
			// Even non-secret upstream literal headers are not copied into a lock file.
			name := strings.ReplaceAll(h.Name, "-", "_")
			server.Headers[h.Name] = "${secret:" + name + "}"
			out.Requires.Secrets = append(out.Requires.Secrets, capability.SecretRequirement{Name: name, Secret: true, Optional: !h.IsRequired})
		}
		if err := server.Validate(); err != nil {
			return out, err
		}
		out.Components = append(out.Components, capability.Component{Type: "mcp", Name: fmt.Sprintf("remote-%d", i+1), MCP: server})
	}
	normalize(&out)
	return out, nil
}
func normalize(out *Result) {
	for _, component := range out.Components {
		if component.Type != "mcp" || component.Disabled || component.MCP == nil {
			continue
		}
		out.Permissions.SideEffects = true
		out.Permissions.Tools = append(out.Permissions.Tools, "mcp__"+component.Name+"__*")
		if component.MCP.URL != "" {
			u, err := url.Parse(component.MCP.URL)
			if err == nil {
				out.Permissions.Network = append(out.Permissions.Network, u.Hostname())
			}
		}
	}
	sort.Strings(out.Permissions.Tools)
	sort.Strings(out.Permissions.Network)
	out.Permissions.Tools = unique(out.Permissions.Tools)
	out.Permissions.Network = unique(out.Permissions.Network)

	sort.Slice(out.Components, func(i, j int) bool { return out.Components[i].Name < out.Components[j].Name })
	sort.Slice(out.Requires.Secrets, func(i, j int) bool { return out.Requires.Secrets[i].Name < out.Requires.Secrets[j].Name })
	merged := out.Requires.Secrets[:0]
	for _, s := range out.Requires.Secrets {
		if len(merged) > 0 && merged[len(merged)-1].Name == s.Name {
			m := &merged[len(merged)-1]
			m.Secret = m.Secret || s.Secret
			m.Optional = m.Optional && s.Optional
		} else {
			merged = append(merged, s)
		}
	}
	out.Requires.Secrets = merged
}

func unique(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
