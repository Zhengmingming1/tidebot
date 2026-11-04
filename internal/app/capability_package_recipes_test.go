package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

func packageReaderFixture(t *testing.T, responses map[string]string) *packageMetadataReader {
	t.Helper()
	return &packageMetadataReader{remaining: 32 << 20, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: capabilityRegistryTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.Body != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("metadata resolution transmitted credentials")
		}
		b, ok := responses[r.URL.String()]
		if !ok {
			return registryFixtureResponse(`{}`), nil
		}
		return registryFixtureResponse(b), nil
	})}}
}
func npmRecipeFixture(name, version string, deps map[string]string) string {
	b, _ := json.Marshal(map[string]any{"name": name, "version": version, "bin": map[string]string{name: "bin.js"}, "dependencies": deps, "dist": map[string]string{"tarball": "https://registry.npmjs.org/" + name + "/-/fixture.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}})
	return string(b)
}
func TestRegistryPackageNPMRecipePinsExactClosure(t *testing.T) {
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": npmRecipeFixture("example", "1.2.3", map[string]string{"dependency": "2.0.0"}), "https://registry.npmjs.org/dependency/2.0.0": npmRecipeFixture("dependency", "2.0.0", nil)})
	got, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	var lock struct {
		Version  int                        `json:"lockfileVersion"`
		Packages map[string]json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(got.LockJSON, &lock) != nil || lock.Version != 3 || len(lock.Packages) != 3 || got.Executable == "" {
		t.Fatalf("incomplete recipe: %+v", got)
	}
	plan := dependencyPlan(capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "package-1", Version: "1.2.3", Recipes: []capability.Recipe{got}}}}})
	if len(plan) != 1 || plan[0].Status != "download" {
		t.Fatalf("not installable: %+v", plan)
	}
}
func TestRegistryPackageNPMRejectsUnsafeOrUnresolvedMetadata(t *testing.T) {
	base := npmRecipeFixture("example", "1.2.3", nil)
	tests := map[string]string{
		"range":      npmRecipeFixture("example", "1.2.3", map[string]string{"dependency": "^2.0.0"}),
		"identity":   strings.Replace(base, `"version":"1.2.3"`, `"version":"2.0.0"`, 1),
		"artifact":   strings.Replace(base, "https://registry.npmjs.org/example/-/fixture.tgz", "https://evil.example/fixture.tgz", 1),
		"bin escape": strings.Replace(base, "bin.js", "../outside", 1),
		"weak hash":  strings.Replace(base, "sha512-", "sha1-", 1),
		"peer":       strings.Replace(base, `"dependencies":null`, `"dependencies":null,"peerDependencies":{"peer":"1.0.0"}`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": body})
			if _, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"}); e == nil {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": base})
	if _, e := r.npm(context.Background(), capability.Recipe{Package: "example", Version: "1.2.3", Integrity: "sha256-wrong"}); e == nil {
		t.Fatal("manifest hash mismatch accepted")
	}
}
func fixtureRecipeWheel(t *testing.T, metadata, entries string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, content := range map[string]string{"example-1.2.3.dist-info/METADATA": metadata, "example-1.2.3.dist-info/entry_points.txt": entries} {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(content))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func fixturePythonResponse(wheel []byte) string {
	hash := sha256.Sum256(wheel)
	return fmt.Sprintf(`{"info":{"name":"example","version":"1.2.3"},"urls":[{"filename":"example-1.2.3-py3-none-any.whl","url":"https://files.pythonhosted.org/packages/example.whl","packagetype":"bdist_wheel","digests":{"sha256":%q}}]}`, hex.EncodeToString(hash[:]))
}
func TestRegistryPackagePythonPinsVerifiedWheel(t *testing.T) {
	wheel := fixtureRecipeWheel(t, "Metadata-Version: 2.1\nName: example\nVersion: 1.2.3\n\nDescription\n", "[console_scripts]\nexample = example:main\n")
	r := packageReaderFixture(t, map[string]string{"https://pypi.org/pypi/example/1.2.3/json": fixturePythonResponse(wheel), "https://files.pythonhosted.org/packages/example.whl": string(wheel)})
	got, e := r.python(context.Background(), capability.Recipe{Backend: "uv", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	if len(got.PythonPackages) != 1 || len(got.PythonPackages[0].SHA256) != 1 || got.Executable == "" {
		t.Fatalf("incomplete recipe: %+v", got)
	}
	plan := dependencyPlan(capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "package-1", Version: "1.2.3", Recipes: []capability.Recipe{got}}}}})
	if len(plan) != 1 || plan[0].Status != "download" {
		t.Fatalf("not installable: %+v", plan)
	}
}
func TestRegistryPackagePythonRejectsUnsafeRecipes(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, entries string
		corrupt                 bool
	}{
		{"range", "Name: example\nVersion: 1.2.3\nRequires-Dist: dependency>=2.0.0\n", "[console_scripts]\nexample=example:main", false},
		{"ambiguous", "Name: example\nVersion: 1.2.3\n", "[console_scripts]\none=example:main\ntwo=example:main", false},
		{"wrong identity", "Name: other\nVersion: 1.2.3\n", "[console_scripts]\nexample=example:main", false},
		{"corrupt", "Name: example\nVersion: 1.2.3\n", "[console_scripts]\nexample=example:main", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wheel := fixtureRecipeWheel(t, tc.metadata, tc.entries)
			metadata := fixturePythonResponse(wheel)
			if tc.corrupt {
				wheel = append(wheel, 'x')
			}
			r := packageReaderFixture(t, map[string]string{"https://pypi.org/pypi/example/1.2.3/json": metadata, "https://files.pythonhosted.org/packages/example.whl": string(wheel)})
			if _, e := r.python(context.Background(), capability.Recipe{Backend: "uv", Package: "example", Version: "1.2.3"}); e == nil {
				t.Fatal("unsafe wheel accepted")
			}
		})
	}
}
func TestRegistryPackageReadBoundaries(t *testing.T) {
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/test": "123456"})
	for _, u := range []string{"http://registry.npmjs.org/test", "https://user:password@registry.npmjs.org/test", "https://registry.npmjs.org:443/test", "https://127.0.0.1/test", "https://registry.npmjs.org/test?secret=x"} {
		if _, e := r.read(context.Background(), u, 8); e == nil {
			t.Fatalf("accepted unsafe URL %s", u)
		}
	}
	if _, e := r.read(context.Background(), "https://registry.npmjs.org/test", 3); e == nil {
		t.Fatal("size bound ignored")
	}
	r.requests = 64
	if _, e := r.read(context.Background(), "https://registry.npmjs.org/test", 8); e == nil {
		t.Fatal("request bound ignored")
	}
}
func TestRegistryInspectionAddsReviewablePackageRecipe(t *testing.T) {
	a := &App{}
	asset, _, _, _, err := managed.NodeLayout("24.1.2", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	a.capState.releaseClient = packageReaderFixture(t, map[string]string{
		"https://nodejs.org/dist/index.json":             `[{"version":"v24.1.2","lts":"Krypton"}]`,
		"https://nodejs.org/dist/v24.1.2/SHASUMS256.txt": strings.Repeat("a", 64) + "  " + asset + "\n",
	}).client
	a.capState.registryClient = packageReaderFixture(t, map[string]string{
		capabilityRegistryURL("com.example/fixture", "latest"): `{"server":{"name":"com.example/fixture","version":"1.2.3","packages":[{"registryType":"npm","identifier":"example","version":"1.2.3"}]}}`,
		"https://registry.npmjs.org/example/1.2.3":             npmRecipeFixture("example", "1.2.3", nil),
	}).client
	got, e := a.inspectCapabilityRegistry(context.Background(), "mcp-registry:com.example/fixture")
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Requires.Tools) != 1 || len(got.Requires.Tools[0].Recipes[0].LockJSON) == 0 || len(got.Requires.Runtimes) != 2 || got.Requires.Runtimes[0].Name != "npm" || got.Requires.Runtimes[1].Name != "node" {
		t.Fatalf("missing reviewed recipe/runtime: %+v", got.Requires)
	}
}

// Exercise the generated lock through the real managed installer with only fake
// process/OSV boundaries: no package code, network install or script is run.
type recipeTestExecutor struct{ calls []managed.Command }

func (e *recipeTestExecutor) Run(_ context.Context, c managed.Command) error {
	e.calls = append(e.calls, c)
	return nil
}

type recipeTestChecker struct{ calls []string }

func (c *recipeTestChecker) Check(_ context.Context, ecosystem, name, version string) error {
	c.calls = append(c.calls, ecosystem+":"+name+"@"+version)
	return nil
}
func TestRegistryPackageGeneratedNPMRecipePassesManagedGates(t *testing.T) {
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": npmRecipeFixture("example", "1.2.3", map[string]string{"dependency": "2.0.0"}), "https://registry.npmjs.org/dependency/2.0.0": npmRecipeFixture("dependency", "2.0.0", nil)})
	recipe, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	executor := &recipeTestExecutor{}
	checker := &recipeTestChecker{}
	root := t.TempDir()
	installer := &managed.Installer{DataDir: root, Executor: executor, MalwareChecker: checker}
	_, e = installer.InstallNPM(context.Background(), managed.PackageRecipe{CapabilityID: "fixture", VersionID: "version", NPMPath: filepath.Join(root, "npm"), Package: recipe.Package, Version: recipe.Version, LockJSON: recipe.LockJSON})
	if e != nil {
		t.Fatal(e)
	}
	if len(checker.calls) != 2 || len(executor.calls) != 1 || !strings.Contains(strings.Join(executor.calls[0].Args, " "), "--ignore-scripts") || executor.calls[0].Args[0] != "ci" {
		t.Fatalf("missing install gates: %+v %+v", checker.calls, executor.calls)
	}
}
func TestRegistryPackageResolutionFailureStaysManual(t *testing.T) {
	a := &App{}
	a.capState.registryClient = packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": npmRecipeFixture("example", "1.2.3", map[string]string{"dependency": "^2.0.0"})}).client
	lock := capability.Lock{Scan: capability.Scan{Verdict: "safe"}, Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "package-1", Version: "1.2.3", Recipes: []capability.Recipe{{Backend: "npm", Package: "example", Version: "1.2.3"}}}}}}
	a.addRegistryPackageRecipes(context.Background(), &lock)
	if dependencyPlan(lock)[0].Status != "manual" || lock.Scan.Verdict != "safe" || len(lock.Scan.Findings) != 1 || len(lock.Requires.Tools[0].Recipes[0].LockJSON) != 0 {
		t.Fatalf("partial resolution became installable: %+v", lock)
	}
}

func npmIndexFixture(name string, versions map[string]map[string]string) string {
	entries := map[string]json.RawMessage{}
	for version, deps := range versions {
		entries[version] = json.RawMessage(npmRecipeFixture(name, version, deps))
	}
	b, _ := json.Marshal(map[string]any{"name": name, "versions": entries})
	return string(b)
}
func TestRegistryPackageNPMRangesNestedConflictsAndCycles(t *testing.T) {
	r := packageReaderFixture(t, map[string]string{
		"https://registry.npmjs.org/example/1.2.3": npmRecipeFixture("example", "1.2.3", map[string]string{"a": "^1.0.0", "b": "~2.0.0"}),
		"https://registry.npmjs.org/a":             npmIndexFixture("a", map[string]map[string]string{"1.0.0": {"shared": "1.x"}, "1.5.0": {"shared": "1.x"}, "2.0.0": nil}),
		"https://registry.npmjs.org/b":             npmIndexFixture("b", map[string]map[string]string{"2.0.1": {"shared": "^2.0.0", "example": "^1.0.0"}, "2.1.0": nil}),
		"https://registry.npmjs.org/shared":        npmIndexFixture("shared", map[string]map[string]string{"1.4.0": nil, "2.3.0": nil, "3.0.0-beta": nil}),
	})
	got, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if e = json.Unmarshal(got.LockJSON, &lock); e != nil {
		t.Fatal(e)
	}
	for location, want := range map[string]string{"node_modules/example/node_modules/a": "1.5.0", "node_modules/example/node_modules/b": "2.0.1", "node_modules/example/node_modules/a/node_modules/shared": "1.4.0", "node_modules/example/node_modules/b/node_modules/shared": "2.3.0"} {
		if lock.Packages[location].Version != want {
			t.Fatalf("%s got %s want %s", location, lock.Packages[location].Version, want)
		}
	}
	if len(lock.Packages) != 6 || r.requests != 4 {
		t.Fatalf("cycle/cache bounds: nodes=%d requests=%d", len(lock.Packages), r.requests)
	}
}
func TestRegistryPackageNPMResolvesRequiredPeers(t *testing.T) {
	body := strings.Replace(npmRecipeFixture("example", "1.2.3", nil), `"dependencies":null`, `"dependencies":null,"peerDependencies":{"peer":"^2.0.0"}`, 1)
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": body, "https://registry.npmjs.org/peer": npmIndexFixture("peer", map[string]map[string]string{"2.1.0": nil})})
	got, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
			Peer    bool   `json:"peer"`
		} `json:"packages"`
	}
	json.Unmarshal(got.LockJSON, &lock)
	if lock.Packages["node_modules/peer"].Version != "2.1.0" || !lock.Packages["node_modules/peer"].Peer {
		t.Fatalf("peer not pinned in parent scope: %s", got.LockJSON)
	}
}

func TestRegistryPackageNPMIntersectsSiblingPeerRanges(t *testing.T) {
	bridge := strings.Replace(npmRecipeFixture("zod-bridge", "1.0.0", nil), `"dependencies":null`, `"dependencies":null,"peerDependencies":{"zod":"^3.25.0"}`, 1)
	r := packageReaderFixture(t, map[string]string{
		"https://registry.npmjs.org/example/1.2.3":    npmRecipeFixture("example", "1.2.3", map[string]string{"zod": "^3.25.0 || ^4.0.0", "zod-bridge": "1.0.0"}),
		"https://registry.npmjs.org/zod-bridge/1.0.0": bridge,
		"https://registry.npmjs.org/zod":              npmIndexFixture("zod", map[string]map[string]string{"3.25.9": nil, "4.1.0": nil}),
	})
	got, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	json.Unmarshal(got.LockJSON, &lock)
	if lock.Packages["node_modules/example/node_modules/zod"].Version != "3.25.9" {
		t.Fatalf("peer intersection ignored: %s", got.LockJSON)
	}
}
func TestRegistryPackageNPMPinsOptionalPlatformClosure(t *testing.T) {
	root := strings.Replace(npmRecipeFixture("example", "1.2.3", nil), `"dependencies":null`, `"dependencies":null,"optionalDependencies":{"native":"~2.0.0"}`, 1)
	native := strings.Replace(npmRecipeFixture("native", "2.0.1", map[string]string{"child": "1.0.0"}), `"dependencies":`, `"os":["darwin"],"cpu":["arm64"],"dependencies":`, 1)
	r := packageReaderFixture(t, map[string]string{"https://registry.npmjs.org/example/1.2.3": root, "https://registry.npmjs.org/native": `{"name":"native","versions":{"2.0.1":` + native + `}}`, "https://registry.npmjs.org/child/1.0.0": npmRecipeFixture("child", "1.0.0", nil)})
	got, e := r.npm(context.Background(), capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	var lock struct {
		Packages map[string]struct {
			Optional bool     `json:"optional"`
			OS       []string `json:"os"`
		} `json:"packages"`
	}
	json.Unmarshal(got.LockJSON, &lock)
	if !lock.Packages["node_modules/example/node_modules/native"].Optional || !lock.Packages["node_modules/example/node_modules/native/node_modules/child"].Optional || len(lock.Packages["node_modules/example/node_modules/native"].OS) != 1 {
		t.Fatalf("optional subtree/platform not pinned: %s", got.LockJSON)
	}
}
