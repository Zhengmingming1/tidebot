package app

// Curated release recipes are metadata only. Inspect pins an official release
// asset and its checksum; the separate owner-confirmed installer downloads it.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

type capabilityReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type capabilityRelease struct {
	Tag        string                   `json:"tag_name"`
	Draft      bool                     `json:"draft"`
	Prerelease bool                     `json:"prerelease"`
	Assets     []capabilityReleaseAsset `json:"assets"`
}

func officialToolRepository(name string) string {
	switch name {
	case "dws":
		return "DingTalk-Real-AI/dingtalk-workspace-cli"
	case "github-mcp-server":
		return "github/github-mcp-server"
	case "uv":
		return "astral-sh/uv"
	}
	return ""
}
func releasePlatformAsset(tool, version, goos, arch string) (name, checksum, executable, format string) {
	if goos != "darwin" && goos != "linux" && goos != "windows" || arch != "amd64" && arch != "arm64" {
		return
	}
	format = "tar.gz"
	executable = tool
	if goos == "windows" {
		format = "zip"
		executable += ".exe"
	}
	switch tool {
	case "dws":
		name = "dws-" + goos + "-" + arch + "." + format
		checksum = "checksums.txt"
	case "github-mcp-server":
		osName := map[string]string{"darwin": "Darwin", "linux": "Linux", "windows": "Windows"}[goos]
		a := arch
		if a == "amd64" {
			a = "x86_64"
		}
		name = "github-mcp-server_" + osName + "_" + a + "." + format
		checksum = "github-mcp-server_" + version + "_checksums.txt"
	case "uv":
		a := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]
		target := map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-gnu", "windows": "pc-windows-msvc"}[goos]
		stem := "uv-" + a + "-" + target
		name = stem + "." + format
		checksum = "sha256.sum"
		if goos != "windows" {
			executable = stem + "/uv"
		}
	default:
		return "", "", "", ""
	}
	return
}
func (a *App) capabilityReleaseHTTPClient() *http.Client {
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	if a.capState.releaseClient == nil {
		a.capState.releaseClient = netproxy.Client(10 * time.Second)
	}
	c := *a.capState.releaseClient
	c.Jar = nil
	if c.Timeout <= 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" {
			return errors.New("release redirect rejected")
		}
		if req.URL.Host != "release-assets.githubusercontent.com" && req.URL.Host != "objects.githubusercontent.com" {
			return errors.New("release redirect outside GitHub assets")
		}
		return nil
	}
	return &c
}
func readReleaseMetadata(ctx context.Context, c *http.Client, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tidebot-capability-inspect")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release metadata HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release metadata too large")
	}
	return data, nil
}
func findReleaseAsset(release capabilityRelease, repo, name string) (capabilityReleaseAsset, error) {
	var found capabilityReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		if found.Name != "" {
			return found, errors.New("duplicate release asset")
		}
		found = asset
	}
	expected := "https://github.com/" + repo + "/releases/download/" + url.PathEscape(release.Tag) + "/" + url.PathEscape(name)
	if found.Name == "" || found.URL != expected {
		return found, errors.New("official release asset unavailable")
	}
	return found, nil
}
func releaseChecksum(data []byte, name string) (string, error) {
	hash := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if !capabilitySHA256.MatchString(fields[0]) || hash != "" {
			return "", errors.New("invalid or ambiguous release checksum")
		}
		hash = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if hash == "" {
		return "", errors.New("release checksum missing")
	}
	return hash, nil
}
func (a *App) resolveOfficialReleaseRecipe(ctx context.Context, requirement capability.ToolRequirement, goos, arch string) (capability.Recipe, error) {
	return a.resolveOfficialReleaseRecipeAt(ctx, requirement, goos, arch, "latest")
}

func (a *App) resolveOfficialReleaseRecipeAt(ctx context.Context, requirement capability.ToolRequirement, goos, arch, tag string) (capability.Recipe, error) {
	repo := officialToolRepository(requirement.Name)
	if repo == "" {
		return capability.Recipe{}, errors.New("tool has no curated release recipe")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client := a.capabilityReleaseHTTPClient()
	endpoint := "https://api.github.com/repos/" + repo + "/releases/latest"
	if tag != "latest" {
		if !capabilityExactVersion.MatchString(tag) || strings.ContainsAny(tag, "+-") {
			return capability.Recipe{}, errors.New("invalid pinned release tag")
		}
		endpoint = "https://api.github.com/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	}
	raw, err := readReleaseMetadata(ctx, client, endpoint, 2<<20)
	if err != nil {
		return capability.Recipe{}, err
	}
	var release capabilityRelease
	if json.Unmarshal(raw, &release) != nil || release.Draft || release.Prerelease || !capabilityExactVersion.MatchString(release.Tag) || strings.ContainsAny(release.Tag, "+-") {
		return capability.Recipe{}, errors.New("invalid stable release")
	}
	if tag != "latest" && release.Tag != tag {
		return capability.Recipe{}, errors.New("release tag differs from reviewed source")
	}
	version := strings.TrimPrefix(release.Tag, "v")
	compatible, err := capability.SatisfiesVersion(version, requirement.Version)
	if err != nil || !compatible {
		return capability.Recipe{}, errors.New("official release does not satisfy required version")
	}
	name, checksums, executable, format := releasePlatformAsset(requirement.Name, version, goos, arch)
	if name == "" {
		return capability.Recipe{}, errors.New("unsupported release platform")
	}
	asset, err := findReleaseAsset(release, repo, name)
	if err != nil {
		return capability.Recipe{}, err
	}
	check, err := findReleaseAsset(release, repo, checksums)
	if err != nil {
		return capability.Recipe{}, err
	}
	data, err := readReleaseMetadata(ctx, client, check.URL, 512<<10)
	if err != nil {
		return capability.Recipe{}, err
	}
	hash, err := releaseChecksum(data, name)
	if err != nil {
		return capability.Recipe{}, err
	}
	if asset.Digest != "" && asset.Digest != "sha256:"+hash {
		return capability.Recipe{}, errors.New("release checksum differs from asset digest")
	}
	return capability.Recipe{Backend: "github-release", Repo: repo, Version: version, Executable: executable, Docs: "https://github.com/" + repo + "/releases/tag/" + release.Tag, Assets: map[string]capability.RecipeAsset{goos + "-" + arch: {URL: asset.URL, SHA256: hash, Format: format}}}, nil
}
func (a *App) addOfficialReleaseRecipes(ctx context.Context, lock *capability.Lock) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Deduplicate repeated requirements without repeating network reads. Never
	// replace an explicit upstream recipe or infer execution from a README.
	recipes := map[string]capability.Recipe{}
	failures := map[string]bool{}
	add := func(reqs []capability.ToolRequirement) {
		for i := range reqs {
			r := &reqs[i]
			if len(r.Recipes) > 0 || officialToolRepository(r.Name) == "" {
				continue
			}
			key := r.Name + "\x00" + r.Version
			recipe, ok := recipes[key]
			if !ok && !failures[key] {
				var err error
				recipe, err = a.resolveOfficialReleaseRecipe(ctx, *r, runtime.GOOS, runtime.GOARCH)
				if err != nil {
					failures[key] = true
					lock.Scan.Findings = append(lock.Scan.Findings, capability.Finding{ID: "release-recipe-unavailable", Severity: "info", Note: r.Name + ": official release checksum metadata unavailable or incompatible; use an existing tool or install manually."})
					continue
				}
				recipes[key] = recipe
			}
			if failures[key] {
				continue
			}
			r.Recipes = []capability.Recipe{recipe}
		}
	}
	add(lock.Requires.Tools)
	add(lock.Requires.Runtimes)
}
