package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

const (
	capabilityRegistryPrefix   = "mcp-registry:"
	capabilityRegistryOrigin   = "https://registry.modelcontextprotocol.io"
	capabilityRegistryServers  = capabilityRegistryOrigin + "/v0.1/servers"
	capabilityRegistryMaxBytes = 2 << 20
	capabilityRegistryLimit    = 50
)

var capabilityRegistryName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*/[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var capabilityRegistryVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,254}$`)

type capabilityRegistryRecord struct {
	Server json.RawMessage `json:"server"`
	Meta   struct {
		Official struct {
			Status   string `json:"status"`
			IsLatest *bool  `json:"isLatest"`
		} `json:"io.modelcontextprotocol.registry/official"`
	} `json:"_meta"`
}

type capabilityRegistryManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func validCapabilityRegistryName(name string) bool {
	if len(name) > 200 || !capabilityRegistryName.MatchString(name) {
		return false
	}
	namespace, _, _ := strings.Cut(name, "/")
	for _, label := range strings.Split(namespace, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return strings.Contains(namespace, ".")
}

func validCapabilityRegistryVersion(version string) bool {
	if !capabilityRegistryVersion.MatchString(version) || strings.EqualFold(version, "latest") {
		return false
	}
	// Server versions may be non-semantic, but ranges must never be pinned.
	for _, part := range strings.Split(version, ".") {
		if part == "x" || part == "X" {
			return false
		}
	}
	return true
}

func capabilityRegistryURL(name, version string) string {
	return capabilityRegistryServers + "/" + url.PathEscape(name) + "/versions/" + url.PathEscape(version)
}

func capabilityRegistrySource(name, version string) capability.Source {
	namespace, _, _ := strings.Cut(name, "/")
	trust := "unknown"
	// Registry ownership proves identity, not a code audit. Only exact GitHub
	// namespaces already curated by Tidebot receive verified (never official).
	for _, entry := range capability.Catalog() {
		u, err := url.Parse(entry.Source.URL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || entry.Source.Trust != "official" {
			continue
		}
		owner, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
		if owner != "" && strings.EqualFold(namespace, "io.github."+owner) {
			trust = "verified"
			break
		}
	}
	return capability.Source{
		Kind: "registry", URL: capabilityRegistryURL(name, version), Ref: version,
		Adapter: "mcp-registry", Trust: trust, Publisher: namespace,
		Registry: &capability.Registry{Name: "mcp-registry", Entry: name},
	}
}

func (a *App) capabilityRegistryHTTPClient() *http.Client {
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	if a.capState.registryClient == nil {
		a.capState.registryClient = netproxy.Client(15 * time.Second)
	}
	client := *a.capState.registryClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	return &client
}

func (a *App) fetchCapabilityRegistry(ctx context.Context, endpoint string, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "registry.modelcontextprotocol.io" || u.User != nil || u.Fragment != "" || (u.Path != "/v0.1/servers" && !strings.HasPrefix(u.Path, "/v0.1/servers/")) {
		return capError(400, "invalid_source", "invalid MCP registry endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.capabilityRegistryHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("MCP registry request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return capError(404, "not_found", "MCP registry server or version not found")
		}
		return capError(502, "registry_unavailable", fmt.Sprintf("MCP registry returned HTTP %d", resp.StatusCode))
	}
	if resp.ContentLength > capabilityRegistryMaxBytes {
		return capError(502, "registry_response_limit", "MCP registry response exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, capabilityRegistryMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read MCP registry response: %w", err)
	}
	if len(raw) > capabilityRegistryMaxBytes {
		return capError(502, "registry_response_limit", "MCP registry response exceeds size limit")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return capError(502, "invalid_registry_response", "invalid MCP registry response")
	}
	return nil
}

func (record capabilityRegistryRecord) manifest() (capabilityRegistryManifest, []byte, error) {
	var manifest capabilityRegistryManifest
	if status := record.Meta.Official.Status; status != "" && status != "active" {
		return manifest, nil, capError(409, "registry_inactive", "MCP registry server is not active")
	}
	// Canonicalize only the server document: registry timestamps and isLatest
	// change independently of the reviewed content. Preserve unknown fields and
	// exact JSON numbers, and normalize object ordering and whitespace.
	decoder := json.NewDecoder(bytes.NewReader(record.Server))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || document == nil {
		return manifest, nil, errors.New("invalid registry server document")
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return manifest, nil, err
	}
	// Read identity from the same canonical document the adapter consumes.
	if err := json.Unmarshal(raw, &manifest); err != nil || !validCapabilityRegistryName(manifest.Name) || !validCapabilityRegistryVersion(manifest.Version) {
		return manifest, nil, capError(502, "invalid_registry_response", "registry server requires a valid name and concrete version")
	}
	return manifest, raw, nil
}

func (a *App) searchCapabilityRegistry(ctx context.Context, query string) ([]capability.CatalogEntry, error) {
	query = strings.TrimSpace(query)
	if len(query) > 512 {
		return nil, capError(400, "invalid_query", "registry query is too long")
	}
	params := url.Values{"search": {query}, "version": {"latest"}, "limit": {"50"}}
	var response struct {
		Servers []capabilityRegistryRecord `json:"servers"`
	}
	if err := a.fetchCapabilityRegistry(ctx, capabilityRegistryServers+"?"+params.Encode(), &response); err != nil {
		return nil, err
	}
	if response.Servers == nil || len(response.Servers) > capabilityRegistryLimit {
		return nil, capError(502, "invalid_registry_response", "invalid MCP registry search results")
	}
	out := []capability.CatalogEntry{}
	seen := map[string]bool{}
	for _, record := range response.Servers {
		manifest, raw, err := record.manifest()
		if err != nil || seen[manifest.Name] {
			continue
		}
		if latest := record.Meta.Official.IsLatest; latest != nil && !*latest {
			continue
		}
		parsed, err := adapters.ParseRegistry(raw)
		if err != nil || strings.TrimSpace(parsed.Title) == "" || len(parsed.Title) > 256 {
			continue
		}
		seen[manifest.Name] = true
		out = append(out, capability.CatalogEntry{
			ID: capabilityRegistryPrefix + manifest.Name, Title: parsed.Title,
			Description: manifest.Description, Source: capabilityRegistrySource(manifest.Name, manifest.Version), Requires: parsed.Requires,
		})
	}
	return out, nil
}

func (a *App) inspectCapabilityRegistry(ctx context.Context, source string) (capability.Lock, error) {
	name, ok := strings.CutPrefix(strings.TrimSpace(source), capabilityRegistryPrefix)
	if !ok || !validCapabilityRegistryName(name) {
		return capability.Lock{}, capError(400, "invalid_source", "use mcp-registry: followed by a registry server name")
	}
	var record capabilityRegistryRecord
	if err := a.fetchCapabilityRegistry(ctx, capabilityRegistryURL(name, "latest"), &record); err != nil {
		return capability.Lock{}, err
	}
	manifest, raw, err := record.manifest()
	if err != nil {
		return capability.Lock{}, err
	}
	if manifest.Name != name {
		return capability.Lock{}, capError(502, "invalid_registry_response", "MCP registry returned a different server")
	}
	parsed, err := adapters.ParseRegistry(raw)
	if err != nil {
		return capability.Lock{}, err
	}
	if strings.TrimSpace(parsed.Title) == "" {
		return capability.Lock{}, capError(502, "invalid_registry_response", "registry server requires a title")
	}
	files := map[string][]byte{"server.json": raw}
	_, hash, err := capability.HashFiles(files)
	if err != nil {
		return capability.Lock{}, err
	}
	src := capabilityRegistrySource(name, manifest.Version)
	scan, err := scanCapabilitySource(src, files)
	if err != nil {
		return capability.Lock{}, err
	}
	lock := capability.Lock{Format: capability.LockFormat, ID: capKey(capabilityRegistryPrefix + name), Title: parsed.Title, Source: src, FilesSHA256: hash, Components: parsed.Components, Requires: parsed.Requires, Permissions: parsed.Permissions, Scan: scan}
	a.addRegistryPackageRecipes(ctx, &lock)
	a.addOfficialReleaseRecipes(ctx, &lock)
	if err := lock.Validate(); err != nil {
		return capability.Lock{}, err
	}
	a.cacheCapabilityInspection(lock)
	return lock, nil
}

func (a *App) readCapabilityRegistrySnapshot(ctx context.Context, source capability.Source) (map[string][]byte, error) {
	if source.Kind != "registry" || source.Adapter != "mcp-registry" || source.Registry == nil || source.Registry.Name != "mcp-registry" || !validCapabilityRegistryName(source.Registry.Entry) || !validCapabilityRegistryVersion(source.Ref) || source.URL != capabilityRegistryURL(source.Registry.Entry, source.Ref) {
		return nil, capError(400, "invalid_source", "registry snapshot requires a pinned official registry source")
	}
	var record capabilityRegistryRecord
	if err := a.fetchCapabilityRegistry(ctx, source.URL, &record); err != nil {
		return nil, err
	}
	manifest, raw, err := record.manifest()
	if err != nil {
		return nil, err
	}
	if manifest.Name != source.Registry.Entry || manifest.Version != source.Ref {
		return nil, capError(409, "source_changed", "MCP registry returned a different server or version")
	}
	// proposalFiles compares this snapshot with the reviewed FilesSHA256 before
	// installation, so even mutation of a nominally pinned version fails closed.
	return map[string][]byte{"server.json": raw}, nil
}
