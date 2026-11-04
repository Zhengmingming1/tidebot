package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
)

// Node release metadata is fetched only during inspection. The confirmation
// stores an immutable archive URL and SHA256; installation never resolves latest.
func (a *App) resolveNodeRecipe(ctx context.Context, goos, arch string) (capability.Recipe, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := *a.capabilityReleaseHTTPClient()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return errors.New("Node metadata redirects are not permitted")
	}
	data, err := readReleaseMetadata(ctx, &client, "https://nodejs.org/dist/index.json", 4<<20)
	if err != nil {
		return capability.Recipe{}, err
	}
	var releases []struct {
		Version string          `json:"version"`
		LTS     json.RawMessage `json:"lts"`
	}
	if json.Unmarshal(data, &releases) != nil {
		return capability.Recipe{}, errors.New("invalid Node release metadata")
	}
	version := ""
	for _, r := range releases {
		// A maintained even-numbered LTS line is selected, never current/prerelease.
		if len(r.LTS) == 0 || r.LTS[0] != '"' || !capabilityExactVersion.MatchString(r.Version) || strings.ContainsAny(r.Version, "+-") {
			continue
		}
		v := strings.TrimPrefix(r.Version, "v")
		supported, e := capability.SatisfiesVersion(v, ">=22.0.0")
		if e != nil || !supported {
			continue
		}
		if version == "" {
			version = v
			continue
		}
		newer, e := capability.SatisfiesVersion(v, ">"+version)
		if e == nil && newer {
			version = v
		}
	}
	if version == "" {
		return capability.Recipe{}, errors.New("supported Node LTS release unavailable")
	}
	asset, _, npm, format, err := managed.NodeLayout(version, goos, arch)
	if err != nil {
		return capability.Recipe{}, err
	}
	base := "https://nodejs.org/dist/v" + version + "/"
	sums, err := readReleaseMetadata(ctx, &client, base+"SHASUMS256.txt", 1<<20)
	if err != nil {
		return capability.Recipe{}, err
	}
	hash, err := releaseChecksum(sums, asset)
	if err != nil {
		return capability.Recipe{}, err
	}
	return capability.Recipe{Backend: "http-archive", Version: version, Executable: npm, Assets: map[string]capability.RecipeAsset{goos + "-" + arch: {URL: base + asset, SHA256: hash, Format: format}}}, nil
}

func (a *App) addNodeRuntimeRecipe(ctx context.Context, lock *capability.Lock) {
	needed := false
	for _, req := range lock.Requires.Tools {
		for _, r := range req.Recipes {
			if r.Backend == "npm" && len(r.LockJSON) > 0 {
				needed = true
			}
		}
	}
	if !needed {
		return
	}
	npmIndex := -1
	for i, req := range lock.Requires.Runtimes {
		if req.Name == "npm" {
			npmIndex = i
		}
	}
	if npmIndex < 0 {
		lock.Requires.Runtimes = append(lock.Requires.Runtimes, capability.ToolRequirement{Name: "npm"})
		npmIndex = len(lock.Requires.Runtimes) - 1
	}
	// npm's bundled Node version is not an npm version constraint. Never reinterpret
	// a source's explicit npm constraint as a Node constraint.
	if lock.Requires.Runtimes[npmIndex].Version != "" {
		return
	}
	if len(lock.Requires.Runtimes[npmIndex].Recipes) > 0 {
		return
	}
	recipe, err := a.resolveNodeRecipe(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		lock.Scan.Findings = append(lock.Scan.Findings, capability.Finding{ID: "node-runtime-unavailable", Severity: "info", Note: "Official Node/npm runtime metadata unavailable; install npm manually. " + err.Error()})
		return
	}
	for _, req := range lock.Requires.Runtimes {
		if req.Name == "node" {
			compatible, e := capability.SatisfiesVersion(recipe.Version, req.Version)
			if e != nil || !compatible {
				lock.Scan.Findings = append(lock.Scan.Findings, capability.Finding{ID: "node-runtime-incompatible", Severity: "info", Note: "Official Node LTS does not satisfy the declared Node requirement; install a compatible runtime manually."})
				return
			}
		}
	}
	lock.Requires.Runtimes[npmIndex].Recipes = []capability.Recipe{recipe}
	hasNode := false
	for _, req := range lock.Requires.Runtimes {
		if req.Name == "node" {
			hasNode = true
		}
	}
	if !hasNode {
		lock.Requires.Runtimes = append(lock.Requires.Runtimes, capability.ToolRequirement{Name: "node"})
	}
}

func isNodeRuntimeRecipe(item capabilityDependencyPlan) bool {
	if !item.Runtime || item.Name != "npm" || item.Recipe == nil || item.Recipe.Backend != "http-archive" {
		return false
	}
	asset, _, npm, format, err := managed.NodeLayout(item.Recipe.Version, runtime.GOOS, runtime.GOARCH)
	if err != nil || item.Recipe.Executable != npm {
		return false
	}
	a := item.Recipe.Assets[runtime.GOOS+"-"+runtime.GOARCH]
	return a.Format == format && a.URL == "https://nodejs.org/dist/v"+item.Recipe.Version+"/"+asset
}

// Reuse only a runtime anchored by an installed, verified immutable lock. An
// orphaned or modified directory is never adopted merely because its name fits.
func (a *App) reuseNodeRuntime(ctx context.Context, item capabilityDependencyPlan, reviewed ...capability.Lock) (managed.Result, bool, error) {
	records, err := a.caps().List(ctx)
	if err != nil {
		return managed.Result{}, false, err
	}
	for _, record := range records {
		node, npm := record.Lock.Resolved["node"], record.Lock.Resolved["npm"]
		if (node.Source != "managed" && node.Source != "system") || npm.Source != "managed" || npm.Directory == "" || node.Source == "managed" && (node.Version != item.Recipe.Version || node.Directory != npm.Directory) {
			continue
		}
		if len(reviewed) > 0 {
			selected := reviewed[0].Resolved["node"]
			if node.Source == "system" && (reviewed[0].DependencyPreferences["node"].PreferManaged || selected.Source != "system" || selected.Status != "ready" || selected.Path != node.Path || selected.Version != node.Version) {
				continue
			}
			if node.Source == "managed" && selected.Source == "system" && selected.Status == "ready" {
				continue
			}
		}
		// Matching version alone is insufficient if proposal metadata changed.
		same := false
		for _, plan := range dependencyPlanForNodeReuse(record.Lock) {
			if plan.Recipe != nil && reflect.DeepEqual(plan.Recipe, item.Recipe) {
				same = true
			}
		}
		if !same {
			continue
		}
		if err := a.caps().Verify(ctx, record.ID, record.VersionID); err != nil {
			return managed.Result{}, false, err
		}
		tree, err := managedTreeDigest(ctx, a.Cfg.DataDir, npm.Directory)
		if err != nil || node.Source == "managed" && tree != node.TreeSHA256 || tree != npm.TreeSHA256 {
			return managed.Result{}, false, errors.New("existing Node runtime tree integrity check failed")
		}
		for _, resolved := range []capability.Resolved{node, npm} {
			if resolved.Source == "system" {
				checked := capability.Detect(ctx, []capability.ToolRequirement{{Name: "node", Version: resolved.Version}}, capability.DetectOptions{Paths: map[string]string{"node": resolved.Path}})["node"]
				if checked.Status != "ready" {
					return managed.Result{}, false, errors.New("shared npm runtime's system Node changed")
				}
				continue
			}
			hash, e := managedExecutableDigest(ctx, a.Cfg.DataDir, resolved.Path)
			if e != nil || hash != resolved.SHA256 {
				return managed.Result{}, false, errors.New("existing Node runtime executable integrity check failed")
			}
		}
		_, bundleNode, _, _, e := managed.NodeLayout(item.Recipe.Version, runtime.GOOS, runtime.GOARCH)
		if e != nil {
			return managed.Result{}, false, e
		}
		return managed.Result{Source: "managed", Directory: npm.Directory, Version: item.Recipe.Version, Executables: map[string]string{"node": filepath.Join(npm.Directory, filepath.FromSlash(bundleNode)), "npm": npm.Path, "interpreter": node.Path}, Verification: npm.Note}, true, nil
	}
	return managed.Result{}, false, nil
}
func dependencyPlanForNodeReuse(lock capability.Lock) []capabilityDependencyPlan {
	// Do not use ready statuses to suppress the recipe while comparing identity.
	lock.Resolved = nil
	return dependencyPlan(lock)
}

func installedNodeNPMVersion(nodePath string) (string, error) {
	file := filepath.Join(filepath.Dir(nodePath), "..", "lib", "node_modules", "npm", "package.json")
	if runtime.GOOS == "windows" {
		file = filepath.Join(filepath.Dir(nodePath), "node_modules", "npm", "package.json")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return "", err
	}
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if len(b) > 1<<20 || json.Unmarshal(b, &m) != nil || m.Name != "npm" || !capabilityExactVersion.MatchString(m.Version) {
		return "", errors.New("invalid bundled npm version metadata")
	}
	return m.Version, nil
}

func nodeNPMFile(nodePath string, suffix ...string) string {
	base := filepath.Join(filepath.Dir(nodePath), "..", "lib", "node_modules", "npm")
	if runtime.GOOS == "windows" {
		base = filepath.Join(filepath.Dir(nodePath), "node_modules", "npm")
	}
	return filepath.Join(append([]string{base}, suffix...)...)
}
func prepareNodeInterpreter(ctx context.Context, result *managed.Result, lock capability.Lock) error {
	req := capability.ToolRequirement{Name: "node"}
	for _, runtimeReq := range lock.Requires.Runtimes {
		if runtimeReq.Name == "node" {
			req = runtimeReq
		}
	}
	system := lock.Resolved["node"]
	if lock.DependencyPreferences["node"].PreferManaged || system.Source != "system" || system.Status != "ready" {
		system = capability.Resolved{}
	} else {
		req.Version = system.Version
		system = capability.Detect(ctx, []capability.ToolRequirement{req}, capability.DetectOptions{Paths: map[string]string{"node": system.Path}})["node"]
		if system.Status != "ready" {
			return errors.New("reviewed system Node changed; create a new proposal")
		}
	}
	return selectNodeInterpreter(result, system)
}
func selectNodeInterpreter(result *managed.Result, system capability.Resolved) error {
	result.Executables["interpreter"] = result.Executables["node"]
	f, err := os.Open(nodeNPMFile(result.Executables["node"], "package.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	var pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if len(b) > 1<<20 || json.Unmarshal(b, &pkg) != nil {
		return errors.New("invalid npm engine metadata")
	}
	compatible, e := matchNPMRange(system.Version, pkg.Engines.Node)
	if system.Status != "ready" || pkg.Engines.Node == "" || e != nil || !compatible {
		result.Verification += "; bundled Node used because a compatible system Node could not be verified"
		return nil
	}
	if err := writeSystemNodeLaunchers(result, system.Path); err != nil {
		return err
	}
	result.Executables["interpreter"] = system.Path
	result.Verification += "; compatible system Node reused"
	return nil
}
func writeSystemNodeLaunchers(result *managed.Result, node string) error {
	if !filepath.IsAbs(node) || strings.ContainsAny(node, "\x00\r\n") {
		return errors.New("invalid system Node path")
	}
	dir := filepath.Join(result.Directory, "launchers")
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	for _, name := range []string{"npm", "npx"} {
		cli := nodeNPMFile(result.Executables["node"], "bin", name+"-cli.js")
		filename := name
		body := "#!/bin/sh\nexec " + quote(node) + " " + quote(cli) + " \"$@\"\n"
		if runtime.GOOS == "windows" {
			if strings.ContainsAny(node+cli, "\"&|<>^%!\r\n") {
				return errors.New("unsafe Windows Node launcher path")
			}
			filename += ".cmd"
			body = "@echo off\r\n\"" + node + "\" \"" + cli + "\" %*\r\n"
		}
		target := filepath.Join(dir, filename)
		if err := os.WriteFile(target, []byte(body), 0755); err != nil {
			return err
		}
		if name == "npm" {
			result.Executables["npm"] = target
		}
	}
	return nil
}
