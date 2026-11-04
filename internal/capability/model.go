// Package capability stores reviewed, version-locked capability snapshots. It
// never executes upstream installation instructions or mutates system tools.
package capability

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const LockFormat = "tidebot-capability/1"
const ScannerVersion = "tidebot-scan/1"
const MaxFiles = 4096
const MaxFileBytes = 8 << 20
const MaxSnapshotBytes = 64 << 20

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Source struct {
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Ref       string    `json:"ref,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Subpaths  []string  `json:"subpaths,omitempty"`
	Adapter   string    `json:"adapter"`
	Trust     string    `json:"trust"`
	Publisher string    `json:"publisher,omitempty"`
	License   string    `json:"license,omitempty"`
	Registry  *Registry `json:"registry,omitempty"`
}
type Registry struct {
	Name  string `json:"name"`
	Entry string `json:"entry"`
}
type Component struct {
	LocallyModified bool       `json:"locally_modified,omitempty"`
	FilesSHA256     string     `json:"files_sha256,omitempty"`
	Type            string     `json:"type"`
	Name            string     `json:"name"`
	SkillID         string     `json:"skill_id,omitempty"`
	VersionID       string     `json:"version_id,omitempty"`
	Path            string     `json:"path,omitempty"`
	Disabled        bool       `json:"disabled,omitempty"`
	MCP             *MCPServer `json:"mcp,omitempty"`
}

// MCPServer contains launch metadata only. Credential values never belong here.
type MCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	OAuth   bool              `json:"oauth,omitempty"`
	Package string            `json:"package,omitempty"`
}
type RecipeSignature struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Identity string `json:"identity"`
}
type RecipeAsset struct {
	Signature *RecipeSignature `json:"signature,omitempty"`
	URL       string           `json:"url"`
	SHA256    string           `json:"sha256"`
	Format    string           `json:"format"`
}
type RecipePythonPackage struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	SHA256  []string `json:"sha256"`
}

// RecipePythonTarget binds marker and wheel resolution to the inspected interpreter.
type RecipePythonTarget struct {
	Version        string `json:"version"`
	Implementation string `json:"implementation"`
	Platform       string `json:"platform"`
	Machine        string `json:"machine"`
	System         string `json:"system"`
	OSName         string `json:"os_name"`
	SysPlatform    string `json:"sys_platform"`
	Libc           string `json:"libc,omitempty"`
	GILDisabled    bool   `json:"gil_disabled,omitempty"`
}
type Recipe struct {
	PythonTarget   *RecipePythonTarget   `json:"python_target,omitempty"`
	Signature      *RecipeSignature      `json:"signature,omitempty"`
	LockJSON       json.RawMessage       `json:"lock_json,omitempty"`
	PythonPackages []RecipePythonPackage `json:"python_packages,omitempty"`

	URL        string                 `json:"url,omitempty"`
	Format     string                 `json:"format,omitempty"`
	Executable string                 `json:"executable,omitempty"`
	Assets     map[string]RecipeAsset `json:"assets,omitempty"`

	Backend   string `json:"backend"`
	Package   string `json:"package,omitempty"`
	Integrity string `json:"integrity,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Asset     string `json:"asset,omitempty"`
	Version   string `json:"version,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Docs      string `json:"docs,omitempty"`
}
type ToolRequirement struct {
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Check    []string `json:"check,omitempty"`
	Optional bool     `json:"optional,omitempty"`
	Packages []string `json:"packages,omitempty"`
	Recipes  []Recipe `json:"recipes,omitempty"`
}
type SecretRequirement struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
}
type Requirements struct {
	OS       []string            `json:"os,omitempty"`
	Tools    []ToolRequirement   `json:"tools,omitempty"`
	Runtimes []ToolRequirement   `json:"runtimes,omitempty"`
	Secrets  []SecretRequirement `json:"secrets,omitempty"`
}
type Permissions struct {
	Tools       []string `json:"tools,omitempty"`
	Network     []string `json:"network,omitempty"`
	SideEffects bool     `json:"side_effects"`
}
type Finding struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Note     string `json:"note"`
	Severity string `json:"severity"`
}
type Scan struct {
	Scanner  string    `json:"scanner"`
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
}
type Resolved struct {
	Directory  string `json:"directory,omitempty"`
	TreeSHA256 string `json:"tree_sha256,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Source     string `json:"source"`
	Path       string `json:"path,omitempty"`
	Version    string `json:"version,omitempty"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
}
type Lock struct {
	DependencyPreferences map[string]DependencyPreference `json:"dependency_preferences,omitempty"`
	Format                string                          `json:"format"`
	ID                    string                          `json:"id"`
	Title                 string                          `json:"title"`
	Source                Source                          `json:"source"`
	FilesSHA256           string                          `json:"files_sha256"`
	Components            []Component                     `json:"components"`
	Requires              Requirements                    `json:"requires"`
	Permissions           Permissions                     `json:"permissions"`
	Scan                  Scan                            `json:"scan"`
	Resolved              map[string]Resolved             `json:"resolved,omitempty"`
	InstalledAt           string                          `json:"installed_at,omitempty"`
	InstalledBy           string                          `json:"installed_by,omitempty"`
}

func (l Lock) Validate() error {
	if err := ValidatePermissions(l); err != nil {
		return err
	}
	if l.Format != LockFormat {
		return errors.New("unsupported capability lock format")
	}
	if !safeID.MatchString(l.ID) || l.ID == "." || l.ID == ".." {
		return errors.New("invalid capability id")
	}
	if l.Title == "" || len(l.Title) > 256 {
		return errors.New("invalid capability title")
	}
	switch l.Source.Trust {
	case "official", "verified", "community", "unknown":
	default:
		return errors.New("invalid trust level")
	}
	if l.Source.Kind == "git" && !regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`).MatchString(l.Source.Commit) {
		return errors.New("git capability requires immutable commit")
	}
	if l.Source.URL != "" {
		u, err := url.Parse(l.Source.URL)
		if err != nil || u.User != nil {
			return errors.New("source URL must not contain credentials")
		}
	}
	if len(l.Components) == 0 || len(l.Components) > 128 {
		return errors.New("capability requires 1 to 128 components")
	}
	for _, c := range l.Components {
		if c.Name == "" {
			return errors.New("component name is required")
		}
		if c.Path != "" && (strings.Contains(c.Path, "\\") || strings.HasPrefix(c.Path, "/") || path.Clean(c.Path) != c.Path || c.Path == ".." || strings.HasPrefix(c.Path, "../")) {
			return errors.New("invalid component path")
		}
		switch c.Type {
		case "skill", "command":
			if c.SkillID == "" || c.VersionID == "" {
				return errors.New("skill component must reference a registered version")
			}
		case "agent":
			if c.Path == "" {
				return errors.New("agent component requires snapshot path")
			}
		case "mcp":
			if c.MCP == nil {
				return errors.New("MCP configuration required")
			}
			if err := c.MCP.Validate(); err != nil {
				return err
			}
		case "hooks", "lsp", "monitors", "tidebot-plugin":
			if !c.Disabled {
				return errors.New("unsupported execution components must remain disabled")
			}
		default:
			return errors.New("unsupported component type")
		}
	}
	if len(l.Requires.Tools)+len(l.Requires.Runtimes) > 64 {
		return errors.New("too many dependencies")
	}
	return nil
}

// Export omits machine-local resolution and actor information; no credential values are represented.
func (l Lock) Export() ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	l.Resolved = nil
	l.DependencyPreferences = nil
	l.InstalledBy = ""
	return json.MarshalIndent(l, "", "  ")
}

var headerName = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)
var envVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var secretReference = regexp.MustCompile(`^\$\{secret:[A-Za-z_][A-Za-z0-9_.:-]{0,127}\}$`)

func (m MCPServer) Validate() error {
	if len(m.Args) > 128 || len(m.Env) > 64 || len(m.Headers) > 64 {
		return errors.New("MCP configuration too large")
	}
	for _, arg := range m.Args {
		if len(arg) > 8192 || strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("invalid MCP argument")
		}
	}
	for k, v := range m.Headers {
		if !headerName.MatchString(k) || !secretReference.MatchString(v) {
			return errors.New("MCP headers must contain secret references only")
		}
	}
	for k, v := range m.Env {
		if !envVariable.MatchString(k) || !secretReference.MatchString(v) {
			return errors.New("MCP environment values must be secret or setting references")
		}
	}
	switch m.Type {
	case "stdio":
		if m.URL != "" || len(m.Headers) > 0 || m.OAuth {
			return errors.New("stdio MCP cannot carry remote configuration")
		}
		if m.Command == "" && m.Package == "" {
			return errors.New("MCP command or pinned package required")
		}
		if strings.ContainsAny(m.Command, "\r\n\x00") {
			return errors.New("invalid MCP command")
		}
	case "http", "sse", "streamable-http":
		if m.Command != "" || len(m.Args) > 0 || len(m.Env) > 0 || m.Package != "" {
			return errors.New("remote MCP cannot carry local execution configuration")
		}
		if err := ValidateRemoteURL(m.URL); err != nil {
			return err
		}
	default:
		return errors.New("unsupported MCP transport")
	}
	return nil
}

// ValidateRemoteURL is a syntax/literal-address check. Network callers must
// additionally resolve and validate addresses at connection time to prevent rebinding.
func ValidateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("remote endpoint must be an HTTPS URL without userinfo or fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return errors.New("local remote endpoint forbidden")
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return errors.New("private remote endpoint forbidden")
	}
	return nil
}
