package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
)

func pythonLinuxFixtureTarget() *capability.RecipePythonTarget {
	return &capability.RecipePythonTarget{Version: "3.12.8", Implementation: "cpython", Platform: "linux-x86_64", Machine: "x86_64", System: "Linux", OSName: "posix", SysPlatform: "linux", Libc: "glibc 2.36"}
}
func TestPythonStableVersionRangesAndMarkers(t *testing.T) {
	for _, tc := range []struct {
		v, s string
		want bool
	}{{"3.11", ">=3.8,<4", true}, {"3.12.8", "~=3.12", true}, {"4.0.0", "~=3.12", false}, {"1.5", "~=1.4.5", false}, {"1.4.9", "~=1.4.5", true}, {"1.2.5", "!=1.2.*", false}, {"1.3.0", "==1.*", true}, {"1.2", "==1.2.0", true}, {"1.2.3.4", ">=1.2.3", true}} {
		got, e := matchPythonVersion(tc.v, tc.s)
		if e != nil || got != tc.want {
			t.Fatalf("%s %s => %v %v", tc.v, tc.s, got, e)
		}
	}
	env := pythonMarkerEnvironment(pythonLinuxFixtureTarget(), "cli")
	for _, tc := range []struct {
		s    string
		want bool
	}{{`python_version >= "3.10" and sys_platform == "linux"`, true}, {`python_version < "3.10" or sys_platform == "win32"`, false}, {`(extra == "cli" and os_name == "posix") or python_version < "3"`, true}, {`implementation_name == "cpython" and platform_machine in "x86_64,arm64"`, true}, {`extra == "test"`, false}, {`"3.10" < python_version`, true}} {
		got, e := evaluatePythonMarker(tc.s, env)
		if e != nil || got != tc.want {
			t.Fatalf("%s => %v %v", tc.s, got, e)
		}
	}
	for _, s := range []string{`unknown == "x"`, `python_version === "3.12"`, `sys_platform == "linux" garbage`, `(__import__ == "os")`, `sys_platform < "linux"`} {
		if _, e := evaluatePythonMarker(s, env); e == nil {
			t.Fatalf("unsupported marker accepted: %s", s)
		}
	}
	req, e := parsePythonRequirement(`demo[cli, speed] (>=1.2,!=1.3.*,<2); python_version >= "3.10"`)
	if e != nil || req.name != "demo" || len(req.extras) != 2 {
		t.Fatalf("requirement parse: %+v %v", req, e)
	}
}
func TestPythonTargetProbeIsFixedBoundedAndVerified(t *testing.T) {
	target := pythonLinuxFixtureTarget()
	data, _ := json.Marshal(target)
	path := filepath.Join(t.TempDir(), "python3")
	runner := func(ctx context.Context, exe string, args []string) ([]byte, error) {
		if exe != path || !reflect.DeepEqual(args, []string{"-I", "-S", "-c", pythonTargetProbe}) {
			t.Fatal("probe parameters not fixed")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("probe unbounded")
		}
		return data, nil
	}
	got, e := inspectPythonTarget(context.Background(), path, runner)
	if e != nil || !reflect.DeepEqual(got, target) {
		t.Fatal(got, e)
	}
	if e = verifyPythonRecipeTargetWithRunner(context.Background(), capability.Recipe{PythonTarget: target}, path, runner); e != nil {
		t.Fatal(e)
	}
	changed := *target
	changed.Version = "3.13.0"
	if e = verifyPythonRecipeTargetWithRunner(context.Background(), capability.Recipe{PythonTarget: &changed}, path, runner); e == nil {
		t.Fatal("changed interpreter accepted")
	}
	if _, e = inspectPythonTarget(context.Background(), path, func(context.Context, string, []string) ([]byte, error) { return make([]byte, 16385), nil }); e == nil {
		t.Fatal("unbounded probe output accepted")
	}
}
func TestPythonWheelTargetSelection(t *testing.T) {
	target := pythonLinuxFixtureTarget()
	for filename, want := range map[string]bool{"demo-1.0.0-py3-none-any.whl": true, "demo-1.0.0-py2.py3-none-any.whl": true, "demo-1.0.0-cp312-cp312-manylinux_2_17_x86_64.whl": true, "demo-1.0.0-cp39-abi3-manylinux2014_x86_64.whl": true, "demo-1.0.0-cp313-cp313-manylinux_2_17_x86_64.whl": false, "demo-1.0.0-cp312-cp312-manylinux_2_40_x86_64.whl": false, "demo-1.0.0-cp312-cp312-manylinux_2_17_aarch64.whl": false, "demo-1.0.0-cp312-cp312-musllinux_1_2_x86_64.whl": false, "demo-1.0.0-cp312t-cp312t-manylinux_2_17_x86_64.whl": false} {
		if got := pythonWheelRank(filename, target) > 0; got != want {
			t.Fatalf("%s=%v want %v", filename, got, want)
		}
	}
	target.GILDisabled = true
	if pythonWheelRank("demo-1.0.0-cp312-cp312-manylinux_2_17_x86_64.whl", target) != 0 {
		t.Fatal("free-threaded ABI guessed")
	}
}
func addPythonFixtureRelease(t *testing.T, responses map[string]string, name, version, metadata string) pypiRecipeFile {
	wheel := fixtureRecipeWheel(t, "Metadata-Version: 2.1\nName: "+name+"\nVersion: "+version+"\n"+metadata+"\n", "[console_scripts]\n"+name+"="+name+":main\n")
	sum := sha256.Sum256(wheel)
	file := pypiRecipeFile{Filename: fmt.Sprintf("%s-%s-py3-none-any.whl", name, version), URL: fmt.Sprintf("https://files.pythonhosted.org/packages/%s-%s.whl", name, version), Type: "bdist_wheel"}
	file.Digests.SHA256 = hex.EncodeToString(sum[:])
	var m pypiRecipeMetadata
	m.Info.Name = name
	m.Info.Version = version
	m.URLs = []pypiRecipeFile{file}
	raw, _ := json.Marshal(m)
	responses["https://pypi.org/pypi/"+name+"/"+version+"/json"] = string(raw)
	responses[file.URL] = string(wheel)
	return file
}
func addPythonFixtureIndex(responses map[string]string, name string, releases map[string][]pypiRecipeFile) {
	raw, _ := json.Marshal(map[string]any{"info": map[string]string{"name": name}, "releases": releases})
	responses["https://pypi.org/pypi/"+name+"/json"] = string(raw)
}
func TestPythonResolutionRangesMarkersExtrasAndBacktracking(t *testing.T) {
	responses := map[string]string{}
	addPythonFixtureRelease(t, responses, "example", "1.2.3", "Requires-Dist: dep[cli]>=1,<3\nRequires-Dist: shared<2\nRequires-Dist: windows-only; sys_platform == 'win32'\n")
	dep1 := addPythonFixtureRelease(t, responses, "dep", "1.9", "Provides-Extra: cli\nRequires-Dist: shared>=1,<2\nRequires-Dist: extra-tool==1.0; extra == 'cli'\n")
	dep2 := addPythonFixtureRelease(t, responses, "dep", "2.1", "Provides-Extra: cli\nRequires-Dist: shared>=2\nRequires-Dist: extra-tool==1.0; extra == 'cli'\n")
	shared1 := addPythonFixtureRelease(t, responses, "shared", "1.5", "\n")
	shared2 := addPythonFixtureRelease(t, responses, "shared", "2.5", "\n")
	addPythonFixtureRelease(t, responses, "extra-tool", "1.0", "\n")
	addPythonFixtureIndex(responses, "dep", map[string][]pypiRecipeFile{"1.9": {dep1}, "2.1": {dep2}})
	addPythonFixtureIndex(responses, "shared", map[string][]pypiRecipeFile{"1.5": {shared1}, "2.5": {shared2}})
	r := packageReaderFixture(t, responses)
	r.pythonTarget = pythonLinuxFixtureTarget()
	recipe, e := r.python(context.Background(), capability.Recipe{Backend: "uv", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	pins := map[string]string{}
	for _, p := range recipe.PythonPackages {
		pins[p.Name] = p.Version
	}
	if !reflect.DeepEqual(pins, map[string]string{"example": "1.2.3", "dep": "1.9", "shared": "1.5", "extra-tool": "1.0"}) {
		t.Fatalf("wrong closure: %+v", pins)
	}
	if recipe.PythonTarget == nil || recipe.PythonTarget.Version != "3.12.8" {
		t.Fatal("target not pinned")
	}
	for url := range responses {
		if strings.Contains(url, "windows-only") {
			t.Fatal("inactive marker fetched")
		}
	}
}

func TestPythonResolutionVerifiesNativeWheelAndInterpreterPin(t *testing.T) {
	responses := map[string]string{}
	file := addPythonFixtureRelease(t, responses, "example", "1.2.3", "Requires-Python: >=3.12,<3.13\n")
	file.Filename = "example-1.2.3-cp312-cp312-manylinux_2_17_x86_64.whl"
	var m pypiRecipeMetadata
	m.Info.Name = "example"
	m.Info.Version = "1.2.3"
	m.URLs = []pypiRecipeFile{file}
	raw, _ := json.Marshal(m)
	responses["https://pypi.org/pypi/example/1.2.3/json"] = string(raw)
	r := packageReaderFixture(t, responses)
	r.pythonTarget = pythonLinuxFixtureTarget()
	got, e := r.python(context.Background(), capability.Recipe{Backend: "uv", Package: "example", Version: "1.2.3"})
	if e != nil {
		t.Fatal(e)
	}
	if got.PythonPackages[0].SHA256[0] != file.Digests.SHA256 || got.PythonTarget.Implementation != "cpython" {
		t.Fatalf("native wheel not pinned: %+v", got)
	}
	r = packageReaderFixture(t, responses)
	r.pythonTarget = pythonLinuxFixtureTarget()
	r.pythonTarget.Version = "3.13.0"
	if _, e = r.python(context.Background(), capability.Recipe{Backend: "uv", Package: "example", Version: "1.2.3"}); e == nil {
		t.Fatal("foreign ABI accepted")
	}
}

func TestPythonStablePinsAcceptedByManagedInstaller(t *testing.T) {
	executor := &recipeTestExecutor{}
	checker := &recipeTestChecker{}
	root := t.TempDir()
	installer := &managed.Installer{DataDir: root, Executor: executor, MalwareChecker: checker}
	_, e := installer.InstallUV(context.Background(), managed.PythonRecipe{CapabilityID: "fixture", VersionID: "version", UVPath: filepath.Join(root, "uv"), PythonPath: filepath.Join(root, "python3"), Packages: []managed.PythonPackage{{Name: "idna", Version: "3.11", SHA256: []string{strings.Repeat("a", 64)}}, {Name: "certifi", Version: "2026.1.4", SHA256: []string{strings.Repeat("b", 64)}}}})
	if e != nil {
		t.Fatal(e)
	}
	if len(executor.calls) != 3 || len(checker.calls) != 2 {
		t.Fatalf("missing managed gates %+v %+v", executor.calls, checker.calls)
	}
	install := strings.Join(executor.calls[1].Args, " ")
	if !strings.Contains(install, "--require-hashes --no-deps --only-binary :all:") {
		t.Fatalf("unsafe Python install flags: %s", install)
	}
}
func TestPythonRuntimePinPreservesSingleRequirement(t *testing.T) {
	lock := capability.Lock{}
	pinPythonRecipeRuntime(&lock, "3.12.8")
	pinPythonRecipeRuntime(&lock, "3.12.8")
	if len(lock.Requires.Runtimes) != 1 || lock.Requires.Runtimes[0].Version != "3.12.8" {
		t.Fatalf("runtime pin lost: %+v", lock.Requires)
	}
}

func TestRegistryPythonInspectionPinsUVRuntimeWithoutExecution(t *testing.T) {
	responses := map[string]string{}
	addPythonFixtureRelease(t, responses, "example", "1.2.3", "Requires-Python: >=3.10\n")
	responses[capabilityRegistryURL("com.example/python", "latest")] = `{"server":{"name":"com.example/python","version":"1.2.3","packages":[{"registryType":"pypi","identifier":"example","version":"1.2.3"}]}}`
	a := &App{}
	a.capState.registryClient = packageReaderFixture(t, responses).client
	probes := 0
	a.capState.pythonTargetProbe = func(context.Context) (*capability.RecipePythonTarget, error) {
		probes++
		return pythonLinuxFixtureTarget(), nil
	}
	release, calls := releaseFixtureClient(t, "uv", runtime.GOOS, runtime.GOARCH, nil)
	a.capState.releaseClient = release
	lock, e := a.inspectCapabilityRegistry(context.Background(), "mcp-registry:com.example/python")
	if e != nil {
		t.Fatal(e)
	}
	if probes != 1 || *calls != 2 {
		t.Fatalf("unexpected metadata/probe calls: probes=%d release=%d", probes, *calls)
	}
	if len(lock.Requires.Tools[0].Recipes[0].PythonPackages) != 1 || lock.Requires.Tools[0].Recipes[0].PythonTarget == nil {
		t.Fatal("Python closure/target missing")
	}
	runtimes := map[string]capability.ToolRequirement{}
	for _, req := range lock.Requires.Runtimes {
		if _, exists := runtimes[req.Name]; exists {
			t.Fatal("duplicate runtime")
		}
		runtimes[req.Name] = req
	}
	if runtimes["python3"].Version != "3.12.8" || len(runtimes["uv"].Recipes) != 1 || runtimes["uv"].Recipes[0].Version != "1.2.3" {
		t.Fatalf("runtime not pinned: %+v", runtimes)
	}
	for _, item := range dependencyPlan(lock) {
		if item.Name == "uv" && item.Status != "download" {
			t.Fatalf("uv recipe not installable: %+v", item)
		}
	}
}
