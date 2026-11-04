package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNodeRecipePinsOfficialMetadataOnly(t *testing.T) {
	for _, platform := range []struct{ os, arch string }{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}} {
		t.Run(platform.os+platform.arch, func(t *testing.T) {
			a := newTestApp(t)
			calls := 0
			name, _, npm, format, _ := managed.NodeLayout("24.1.2", platform.os, platform.arch)
			a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				content := ""
				switch r.URL.String() {
				case "https://nodejs.org/dist/index.json":
					content = `[{"version":"v25.0.0","lts":false},{"version":"v24.1.2","lts":"Krypton"},{"version":"v22.9.0","lts":"Jod"}]`
				case "https://nodejs.org/dist/v24.1.2/SHASUMS256.txt":
					content = strings.Repeat("a", 64) + "  " + name + "\n"
				default:
					t.Fatalf("unexpected archive request %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Request: r}, nil
			})}
			recipe, err := a.resolveNodeRecipe(context.Background(), platform.os, platform.arch)
			if err != nil {
				t.Fatal(err)
			}
			asset := recipe.Assets[platform.os+"-"+platform.arch]
			if calls != 2 || recipe.Version != "24.1.2" || recipe.Executable != npm || asset.Format != format || asset.SHA256 != strings.Repeat("a", 64) {
				t.Fatalf("recipe %+v calls %d", recipe, calls)
			}
		})
	}
}
func TestNodeRecipeRejectsMissingChecksum(t *testing.T) {
	a := newTestApp(t)
	a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) {
		content := `[{"version":"v24.1.2","lts":"Krypton"}]`
		if strings.HasSuffix(r.URL.Path, ".txt") {
			content = strings.Repeat("a", 64) + "  unrelated.tar.gz"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Request: r}, nil
	})}
	if _, err := a.resolveNodeRecipe(context.Background(), "linux", "amd64"); err == nil {
		t.Fatal("accepted missing checksum")
	}
}

func TestNodePlanPrefersSystemAndDisclosesBundle(t *testing.T) {
	asset, _, npm, format, err := managed.NodeLayout("24.1.2", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	recipe := capability.Recipe{Backend: "http-archive", Version: "24.1.2", Executable: npm, Assets: map[string]capability.RecipeAsset{runtime.GOOS + "-" + runtime.GOARCH: {URL: "https://nodejs.org/dist/v24.1.2/" + asset, Format: format, SHA256: strings.Repeat("a", 64)}}}
	lock := capability.Lock{Requires: capability.Requirements{Runtimes: []capability.ToolRequirement{{Name: "npm", Recipes: []capability.Recipe{recipe}}}}}
	plan := dependencyPlan(lock)
	if len(plan) != 1 || plan[0].Status != "download" || !strings.Contains(plan[0].Note, "Node.js and npm") || !strings.Contains(plan[0].Note, "Explicit approval") {
		t.Fatalf("missing download disclosure: %+v", plan)
	}
	lock.Resolved = map[string]capability.Resolved{"npm": {Source: "system", Status: "ready", Path: "/usr/bin/npm", Version: "11.2.0"}}
	plan = dependencyPlan(lock)
	if plan[0].Status != "ready" || plan[0].Recipe != nil {
		t.Fatalf("system npm not preferred: %+v", plan)
	}
}
func TestBundledNPMVersionIsNotNodeVersion(t *testing.T) {
	root := t.TempDir()
	node := filepath.Join(root, "bin", "node")
	pkg := filepath.Join(root, "lib", "node_modules", "npm", "package.json")
	if runtime.GOOS == "windows" {
		node = filepath.Join(root, "node.exe")
		pkg = filepath.Join(root, "node_modules", "npm", "package.json")
	}
	if err := os.MkdirAll(filepath.Dir(pkg), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte(`{"name":"npm","version":"11.2.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(node), 0700); err != nil {
		t.Fatal(err)
	}
	version, err := installedNodeNPMVersion(node)
	if err != nil || version != "11.2.0" {
		t.Fatalf("version %q: %v", version, err)
	}
}

func TestNodeReuseVerifiesExistingTree(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	asset, nodeRel, npmRel, format, err := managed.NodeLayout("24.1.2", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	directory := filepath.Join(a.Cfg.DataDir, "runtimes", "node", "24.1.2")
	paths := map[string]string{"node": filepath.Join(directory, filepath.FromSlash(nodeRel)), "npm": filepath.Join(directory, filepath.FromSlash(npmRel))}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("inert fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	recipe := capability.Recipe{Backend: "http-archive", Version: "24.1.2", Executable: npmRel, Assets: map[string]capability.RecipeAsset{runtime.GOOS + "-" + runtime.GOARCH: {URL: "https://nodejs.org/dist/v24.1.2/" + asset, Format: format, SHA256: strings.Repeat("a", 64)}}}
	lock := capability.Lock{Format: capability.LockFormat, ID: "node-fixture", Title: "Node fixture", Source: capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}, Components: []capability.Component{{Type: "agent", Name: "fixture", Path: "README.md"}}, Requires: capability.Requirements{Runtimes: []capability.ToolRequirement{{Name: "npm", Recipes: []capability.Recipe{recipe}}}}, Resolved: map[string]capability.Resolved{}}
	tree, err := managedTreeDigest(ctx, a.Cfg.DataDir, directory)
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range paths {
		hash, err := managedExecutableDigest(ctx, a.Cfg.DataDir, p)
		if err != nil {
			t.Fatal(err)
		}
		version := "24.1.2"
		if name == "npm" {
			version = "11.2.0"
		}
		lock.Resolved[name] = capability.Resolved{Source: "managed", Status: "ready", Path: p, Directory: directory, Version: version, SHA256: hash, TreeSHA256: tree}
	}
	if _, err := a.caps().Install(ctx, lock, map[string][]byte{"README.md": []byte("inert fixture")}); err != nil {
		t.Fatal(err)
	}
	item := capabilityDependencyPlan{Name: "npm", Runtime: true, Recipe: &recipe}
	result, reused, err := a.reuseNodeRuntime(ctx, item)
	if err != nil || !reused || result.Directory != directory {
		t.Fatalf("reuse %v %+v: %v", reused, result, err)
	}
	if err := os.WriteFile(paths["node"], []byte("modified"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.reuseNodeRuntime(ctx, item); err == nil {
		t.Fatal("reused tampered Node tree")
	}
}

func TestNodeRuntimeReusesCompatibleSystemWithoutPATHShadow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher inspection")
	}
	for _, version := range []string{"22.10.0", "18.1.0"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			node := filepath.Join(root, "bundle", "bin", "node")
			pkg := nodeNPMFile(node, "package.json")
			if err := os.MkdirAll(filepath.Dir(pkg), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(node), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pkg, []byte(`{"engines":{"node":"^20.17.0 || >=22.9.0"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			result := managed.Result{Directory: root, Executables: map[string]string{"node": node, "npm": filepath.Join(filepath.Dir(node), "npm")}}
			system := capability.Resolved{Source: "system", Status: "ready", Path: "/system path/node", Version: version}
			if err := selectNodeInterpreter(&result, system); err != nil {
				t.Fatal(err)
			}
			if version == "22.10.0" {
				if result.Executables["interpreter"] != system.Path || filepath.Dir(result.Executables["npm"]) != filepath.Join(root, "launchers") {
					t.Fatalf("system Node shadowed: %+v", result)
				}
				text, err := os.ReadFile(result.Executables["npm"])
				if err != nil || !strings.Contains(string(text), "exec '/system path/node'") {
					t.Fatalf("unpinned wrapper: %s %v", text, err)
				}
			} else if result.Executables["interpreter"] != node || !strings.Contains(result.Verification, "compatible system Node could not be verified") {
				t.Fatalf("unsafe fallback: %+v", result)
			}
		})
	}
}
