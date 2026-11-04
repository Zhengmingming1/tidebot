package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

// -I ignores Python environment overrides and cwd; -S disables user/system site
// startup. The script is fixed and interpolates no package or manifest data.
const pythonTargetProbe = `import json,os,platform,sys,sysconfig
print(json.dumps({"version":"%d.%d.%d" % sys.version_info[:3],"implementation":sys.implementation.name,"platform":sysconfig.get_platform(),"machine":platform.machine(),"system":platform.system(),"os_name":os.name,"sys_platform":sys.platform,"libc":os.confstr("CS_GNU_LIBC_VERSION") if hasattr(os,"confstr") and "CS_GNU_LIBC_VERSION" in os.confstr_names else "","gil_disabled":bool(sysconfig.get_config_var("Py_GIL_DISABLED"))}))`

type pythonProbeRunner func(context.Context, string, []string) ([]byte, error)
type boundedPythonOutput struct{ bytes.Buffer }

func (b *boundedPythonOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<10 {
		return 0, errors.New("Python target output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func runPythonTargetProbe(ctx context.Context, executable string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = []string{"PATH=" + filepath.Dir(executable), "LANG=C", "LC_ALL=C"}
	if root := os.Getenv("SystemRoot"); root != "" {
		cmd.Env = append(cmd.Env, "SystemRoot="+root)
	}
	var out boundedPythonOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.Bytes(), err
}
func inspectPythonTarget(ctx context.Context, executable string, runner pythonProbeRunner) (*capability.RecipePythonTarget, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("Python target requires resolved interpreter")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if runner == nil {
		runner = runPythonTargetProbe
	}
	b, e := runner(ctx, executable, []string{"-I", "-S", "-c", pythonTargetProbe})
	if e != nil {
		return nil, errors.New("isolated Python target probe failed")
	}
	if len(b) > 16<<10 {
		return nil, errors.New("Python target output too large")
	}
	var target capability.RecipePythonTarget
	if json.Unmarshal(b, &target) != nil || !stablePackageVersion.MatchString(target.Version) || (target.Implementation != "cpython" && target.Implementation != "pypy") || target.Platform == "" || target.Machine == "" || target.System == "" || (target.SysPlatform != "linux" && target.SysPlatform != "darwin" && target.SysPlatform != "win32") || (target.OSName != "posix" && target.OSName != "nt") {
		return nil, errors.New("unsupported Python target metadata")
	}
	for _, v := range []string{target.Platform, target.Machine, target.System, target.SysPlatform, target.Libc} {
		if len(v) > 128 || strings.ContainsAny(v, "\x00\r\n") {
			return nil, errors.New("invalid Python target metadata")
		}
	}
	return &target, nil
}
func (a *App) preparePythonRecipeReader(ctx context.Context, r *packageMetadataReader) error {
	if r.pythonTarget != nil {
		return nil
	}
	if r.pythonTargetAttempted {
		return errors.New("Python interpreter target unavailable")
	}
	r.pythonTargetAttempted = true
	a.capState.mu.Lock()
	probe := a.capState.pythonTargetProbe
	a.capState.mu.Unlock()
	if probe != nil {
		target, err := probe(ctx)
		if err != nil {
			return err
		}
		if target == nil {
			return errors.New("Python target probe returned no target")
		}
		r.pythonTarget = target
		return nil
	}
	probeLock := capability.Lock{Requires: capability.Requirements{Runtimes: []capability.ToolRequirement{{Name: "python3"}}}}
	if err := a.resolveCapabilityDependencies(ctx, &probeLock, false); err != nil {
		return err
	}
	detected := probeLock.Resolved["python3"]
	if detected.Status != "ready" {
		return errors.New("install Python before resolving platform-specific package closure")
	}
	target, e := inspectPythonTarget(ctx, detected.Path, nil)
	if e != nil {
		return e
	}
	if target.Version != detected.Version {
		return errors.New("Python version changed during inspection")
	}
	r.pythonTarget = target
	return nil
}
func pinPythonRecipeRuntime(lock *capability.Lock, version string) {
	for i := range lock.Requires.Runtimes {
		if lock.Requires.Runtimes[i].Name == "python3" {
			lock.Requires.Runtimes[i].Version = version
			return
		}
	}
	// Runtime declaration is added by addRegistryPackageRecipes immediately after
	// resolution; adding it here ensures its exact target pin is retained.
	lock.Requires.Runtimes = append(lock.Requires.Runtimes, capability.ToolRequirement{Name: "python3", Version: version})
}
func verifyPythonRecipeTarget(ctx context.Context, recipe capability.Recipe, pythonPath string) error {
	return verifyPythonRecipeTargetWithRunner(ctx, recipe, pythonPath, nil)
}
func verifyPythonRecipeTargetWithRunner(ctx context.Context, recipe capability.Recipe, pythonPath string, runner pythonProbeRunner) error {
	if recipe.PythonTarget == nil {
		return nil
	} // Hand-authored legacy hash locks retain their existing behavior.
	actual, e := inspectPythonTarget(ctx, pythonPath, runner)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, recipe.PythonTarget) {
		return errors.New("Python target changed; inspect and confirm a fresh package recipe")
	}
	return nil
}
func pythonMarkerEnvironment(target *capability.RecipePythonTarget, extra string) map[string]string {
	env := map[string]string{"extra": extra}
	if target == nil {
		return env
	}
	parts := strings.Split(target.Version, ".")
	env["python_full_version"] = target.Version
	env["python_version"] = strings.Join(parts[:2], ".")
	env["implementation_name"] = target.Implementation
	env["sys_platform"] = target.SysPlatform
	env["platform_system"] = target.System
	env["platform_machine"] = target.Machine
	env["os_name"] = target.OSName
	if target.Implementation == "cpython" {
		env["platform_python_implementation"] = "CPython"
		env["implementation_version"] = target.Version
	} else if target.Implementation == "pypy" {
		env["platform_python_implementation"] = "PyPy"
	}
	return env
}
