package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

func curatedBinarySource(source capability.Source, allowLatest bool) bool {
	if source.Ref == "latest" && !allowLatest || source.Ref != "latest" && (!capabilityExactVersion.MatchString(source.Ref) || strings.ContainsAny(source.Ref, "+-")) {
		return false
	}
	for _, entry := range capability.Catalog() {
		if entry.Source.Kind == "curated-binary" {
			expected := entry.Source
			expected.Ref = source.Ref
			if reflect.DeepEqual(source, expected) {
				return true
			}
		}
	}
	return false
}
func curatedBinaryManifest(recipe capability.Recipe) capability.Lock {
	return capability.Lock{Format: capability.LockFormat, ID: capKey("curated-binary:github-mcp-server"), Title: "GitHub MCP", Components: []capability.Component{{Type: "mcp", Name: "github-mcp-server", MCP: &capability.MCPServer{Type: "stdio", Command: "github-mcp-server", Args: []string{"stdio", "--read-only"}, Env: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${secret:GITHUB_PERSONAL_ACCESS_TOKEN}"}}}}, Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "github-mcp-server", Version: recipe.Version, Check: []string{"--version"}, Recipes: []capability.Recipe{recipe}}}, Secrets: []capability.SecretRequirement{{Name: "GITHUB_PERSONAL_ACCESS_TOKEN", Secret: true}}}, Permissions: capability.Permissions{Tools: []string{"mcp__github-mcp-server__*"}, Network: []string{"api.github.com"}}}
}
func curatedBinaryFiles(recipe capability.Recipe) (map[string][]byte, error) {
	b, e := json.Marshal(curatedBinaryManifest(recipe))
	if e != nil {
		return nil, e
	}
	return map[string][]byte{"tidebot-capability.json": b}, nil
}
func (a *App) readCuratedBinaryCapabilitySnapshot(ctx context.Context, source capability.Source) (map[string][]byte, error) {
	if !curatedBinarySource(source, false) {
		return nil, errors.New("curated binary requires exact pinned built-in source")
	}
	recipe, e := a.resolveOfficialReleaseRecipeAt(ctx, capability.ToolRequirement{Name: "github-mcp-server", Version: strings.TrimPrefix(source.Ref, "v")}, runtime.GOOS, runtime.GOARCH, source.Ref)
	if e != nil {
		return nil, e
	}
	return curatedBinaryFiles(recipe)
}
func (a *App) inspectCuratedBinaryCapability(ctx context.Context, source capability.Source) (capability.Lock, error) {
	if !curatedBinarySource(source, true) {
		return capability.Lock{}, errors.New("unknown curated binary source")
	}
	recipe, e := a.resolveOfficialReleaseRecipeAt(ctx, capability.ToolRequirement{Name: "github-mcp-server"}, runtime.GOOS, runtime.GOARCH, source.Ref)
	if e != nil {
		return capability.Lock{}, e
	}
	source.Ref = strings.TrimPrefix(recipe.Docs, "https://github.com/github/github-mcp-server/releases/tag/")
	files, e := curatedBinaryFiles(recipe)
	if e != nil {
		return capability.Lock{}, e
	}
	_, hash, e := capability.HashFiles(files)
	if e != nil {
		return capability.Lock{}, e
	}
	lock := curatedBinaryManifest(recipe)
	lock.Source = source
	lock.FilesSHA256 = hash
	lock.Scan, e = scanCuratedBinaryCapability(source, files)
	if e != nil {
		return capability.Lock{}, e
	}

	if e := lock.Validate(); e != nil {
		return capability.Lock{}, e
	}
	a.cacheCapabilityInspection(lock)
	return lock, nil
}

// Keep the review and confirmation scan deterministic. Integrity disclosure is
// source policy, not a transient network-resolution finding.
func scanCuratedBinaryCapability(source capability.Source, files map[string][]byte) (capability.Scan, error) {
	if !curatedBinarySource(source, false) {
		return capability.Scan{}, errors.New("curated binary requires exact pinned built-in source")
	}
	scan := capability.ScanFiles(files)
	scan.Findings = append(scan.Findings, capability.Finding{ID: "release-integrity-only", Severity: "caution", Note: "Official release SHA256 verifies download integrity, not an independent signature. Read-only mode is a server option, not a sandbox."})
	if scan.Verdict == "safe" {
		scan.Verdict = "caution"
	}
	return scan, nil
}
