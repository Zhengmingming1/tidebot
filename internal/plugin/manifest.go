// Package plugin implements the plugin system (final-plan §8.2–8.5): plugin.yaml manifests,
// immutable content-hashed versions with a current pointer, permission approval, secrets,
// a JSON-RPC 2.0 stdio host with supervision, MCP connectors and workflow packs.
package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is plugin.yaml.
type Manifest struct {
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Version     string      `yaml:"version" json:"version"`
	Runtime     Runtime     `yaml:"runtime" json:"runtime"`
	Contributes Contributes `yaml:"contributes" json:"contributes"`
	Permissions Permissions `yaml:"permissions" json:"permissions"`
	// ConfigSchema is a JSON Schema subset (object of string/number/integer/boolean/array
	// properties, enum, required, writeOnly) the WebUI turns into a settings form.
	ConfigSchema map[string]any `yaml:"config_schema" json:"config_schema,omitempty"`
}

// Runtime describes the plugin process.
type Runtime struct {
	Command string `yaml:"command" json:"command"`
	// Builtin names an official plugin runtime compiled into tidebot itself. Only plugins
	// installed from the embedded official catalog (hash-verified) may use it.
	Builtin         string   `yaml:"builtin" json:"builtin,omitempty"`
	Args            []string `yaml:"args" json:"args,omitempty"`
	Protocol        string   `yaml:"protocol" json:"protocol"`
	Lifecycle       string   `yaml:"lifecycle" json:"lifecycle"` // lazy | keepalive
	IdleExitSeconds int      `yaml:"idle_exit_seconds" json:"idle_exit_seconds"`
	Limits          Limits   `yaml:"limits" json:"limits"`
}

// Limits are resource caps.
type Limits struct {
	MemoryMB   int `yaml:"memory_mb" json:"memory_mb"`
	CPUPercent int `yaml:"cpu_percent" json:"cpu_percent"`
}

// Contributes lists what the plugin adds.
type Contributes struct {
	Triggers      []TriggerDecl   `yaml:"triggers" json:"triggers,omitempty"`
	Connectors    []ConnectorDecl `yaml:"connectors" json:"connectors,omitempty"`
	Notifiers     []NotifierDecl  `yaml:"notifiers" json:"notifiers,omitempty"`
	WorkflowPacks []PackDecl      `yaml:"workflow_packs" json:"workflow_packs,omitempty"`
}

// TriggerDecl declares a trigger kind.
type TriggerDecl struct {
	ID           string         `yaml:"id" json:"id"`
	Kind         string         `yaml:"kind" json:"kind"` // email | webhook | file_change | im
	Title        string         `yaml:"title" json:"title"`
	ConfigSchema map[string]any `yaml:"config_schema" json:"config_schema,omitempty"`
}

// ConnectorDecl is an MCP server for Claude.
type ConnectorDecl struct {
	ID          string `yaml:"id" json:"id"`
	SideEffects bool   `yaml:"side_effects" json:"side_effects"`
	MCP         struct {
		Command string            `yaml:"command" json:"command"`
		Builtin string            `yaml:"builtin" json:"builtin,omitempty"`
		Args    []string          `yaml:"args" json:"args,omitempty"`
		Env     map[string]string `yaml:"env" json:"env,omitempty"`
	} `yaml:"mcp" json:"mcp"`
}

// NotifierDecl is a notification channel provided by the plugin.
type NotifierDecl struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
}

// PackDecl references a bundled workflow pack.
type PackDecl struct {
	Path string `yaml:"path" json:"path"`
}

// Permissions the owner approves item by item.
type Permissions struct {
	Network []string `yaml:"network" json:"network,omitempty"`
	FS      struct {
		Read  []string `yaml:"read" json:"read,omitempty"`
		Write []string `yaml:"write" json:"write,omitempty"`
	} `yaml:"fs" json:"fs"`
	HostAPI []string `yaml:"host_api" json:"host_api,omitempty"`
	Secrets []string `yaml:"secrets" json:"secrets,omitempty"`
}

var (
	reID         = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reSemver     = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	reSecretRef  = regexp.MustCompile(`\$\{secret:([a-z0-9_]+)\}`)
	hostAPIs     = map[string]bool{"runs.start": true, "notify.send": true, "tasks.read": true, "kv": true, "log.write": true}
	triggerKinds = map[string]bool{"email": true, "webhook": true, "file_change": true, "im": true, "rss": true, "web_monitor": true, "poll": true}
	// TriggerKindList is the accepted trigger kinds (for errors and the UI).
	TriggerKindList = "email|webhook|file_change|im|rss|web_monitor|poll"
)

// ParseManifest reads and validates plugin.yaml in dir.
func ParseManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return nil, errors.New("plugin.yaml not found")
	}
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.yaml: %w", err)
	}
	return &m, m.Validate(dir)
}

// Validate checks the manifest against the plugin directory.
func (m *Manifest) Validate(dir string) error {
	if !reID.MatchString(m.ID) {
		return errors.New("id must be kebab-case")
	}
	if m.Name == "" || !reSemver.MatchString(m.Version) {
		return errors.New("name and semver version are required")
	}
	if m.Runtime.Protocol == "" {
		m.Runtime.Protocol = "jsonrpc-stdio"
	}
	if m.Runtime.Protocol != "jsonrpc-stdio" {
		return errors.New("runtime.protocol must be jsonrpc-stdio")
	}
	if m.Runtime.Lifecycle == "" {
		m.Runtime.Lifecycle = "lazy"
	}
	if m.Runtime.Lifecycle != "lazy" && m.Runtime.Lifecycle != "keepalive" {
		return errors.New("runtime.lifecycle must be lazy or keepalive")
	}
	if m.Runtime.IdleExitSeconds <= 0 {
		m.Runtime.IdleExitSeconds = 600
	}
	if m.Runtime.Limits.MemoryMB <= 0 {
		m.Runtime.Limits.MemoryMB = 256
	}
	if m.Runtime.Limits.CPUPercent <= 0 {
		m.Runtime.Limits.CPUPercent = 50
	}
	if m.Runtime.Command != "" && m.Runtime.Builtin != "" {
		return errors.New("runtime: set command or builtin, not both")
	}
	if m.Runtime.Command != "" {
		if err := insideDir(dir, m.Runtime.Command); err != nil {
			return fmt.Errorf("runtime.command: %w", err)
		}
	}
	for _, t := range m.Contributes.Triggers {
		if !triggerKinds[t.Kind] || t.ID == "" {
			return fmt.Errorf("trigger %q: kind must be %s", t.ID, TriggerKindList)
		}
	}
	declared := map[string]bool{}
	for _, s := range m.Permissions.Secrets {
		declared[s] = true
	}
	for _, c := range m.Contributes.Connectors {
		if c.ID == "" || (c.MCP.Command == "") == (c.MCP.Builtin == "") {
			return errors.New("connector needs id and exactly one of mcp.command / mcp.builtin")
		}
		if c.MCP.Command != "" {
			if err := insideDir(dir, c.MCP.Command); err != nil {
				return fmt.Errorf("connector %s: %w", c.ID, err)
			}
		}
		for _, v := range c.MCP.Env {
			for _, mm := range reSecretRef.FindAllStringSubmatch(v, -1) {
				if !declared[mm[1]] {
					return fmt.Errorf("connector %s uses undeclared secret %s", c.ID, mm[1])
				}
			}
		}
	}
	if m.ConfigSchema != nil {
		if _, err := ParseSchema(m.ConfigSchema); err != nil {
			return fmt.Errorf("config_schema: %w", err)
		}
	}
	for _, h := range m.Permissions.HostAPI {
		if !hostAPIs[h] {
			return fmt.Errorf("unknown host_api %q", h)
		}
	}
	for _, p := range m.Contributes.WorkflowPacks {
		if err := insideDir(dir, p.Path); err != nil {
			return fmt.Errorf("workflow pack: %w", err)
		}
	}
	return nil
}

func insideDir(dir, rel string) error {
	if filepath.IsAbs(rel) {
		return errors.New("must be relative to the plugin")
	}
	p := filepath.Clean(filepath.Join(dir, rel))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(filepath.Separator)) {
		return errors.New("escapes the plugin directory")
	}
	return nil
}

// PermissionList flattens permissions for display/approval, sorted.
func (p Permissions) PermissionList() []string {
	var out []string
	for _, n := range p.Network {
		out = append(out, "network:"+n)
	}
	for _, r := range p.FS.Read {
		out = append(out, "fs.read:"+r)
	}
	for _, w := range p.FS.Write {
		out = append(out, "fs.write:"+w)
	}
	for _, h := range p.HostAPI {
		out = append(out, "host_api:"+h)
	}
	for _, s := range p.Secrets {
		out = append(out, "secret:"+s)
	}
	sort.Strings(out)
	return out
}

// NewPermissions returns entries in next that prev does not cover.
func NewPermissions(prev, next []string) []string {
	have := map[string]bool{}
	for _, p := range prev {
		have[p] = true
	}
	var out []string
	for _, p := range next {
		if !have[p] {
			out = append(out, p)
		}
	}
	return out
}

// UsesBuiltin reports whether the manifest asks for a runtime compiled into tidebot.
func (m *Manifest) UsesBuiltin() bool {
	if m.Runtime.Builtin != "" {
		return true
	}
	for _, c := range m.Contributes.Connectors {
		if c.MCP.Builtin != "" {
			return true
		}
	}
	return false
}
