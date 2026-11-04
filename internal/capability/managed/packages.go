package managed

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var npmName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var pythonPinnedVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)
var pythonName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type PackageRecipe struct {
	CapabilityID, VersionID string
	NodePath                string // optional resolved Node for managed npm
	NPMCLI                  string // npm-cli.js in the same verified bundle
	NPMPath                 string // resolved absolute npm executable, never npx
	Package, Version        string
	// Full reviewed package-lock v2/v3 locks every transitive dependency and SRI.
	LockJSON []byte
}
type PythonPackage struct {
	Name, Version string
	SHA256        []string
}
type PythonRecipe struct {
	CapabilityID, VersionID string
	UVPath, PythonPath      string          // previously resolved compatible executable paths
	Packages                []PythonPackage // complete transitive closure; installs use --no-deps
}
type npmLockEntry struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Resolved     string            `json:"resolved"`
	Integrity    string            `json:"integrity"`
	Link         bool              `json:"link"`
	Dependencies map[string]string `json:"dependencies"`
}
type npmLock struct {
	LockfileVersion int                     `json:"lockfileVersion"`
	Packages        map[string]npmLockEntry `json:"packages"`
}

func validateLock(r PackageRecipe) (npmLock, error) {
	var lock npmLock
	if len(r.LockJSON) > 8<<20 || json.Unmarshal(r.LockJSON, &lock) != nil || lock.LockfileVersion < 2 || lock.LockfileVersion > 3 {
		return lock, errors.New("requires npm lockfile v2 or v3")
	}
	root, ok := lock.Packages[""]
	if !ok || len(root.Dependencies) != 1 || root.Dependencies[r.Package] != r.Version {
		return lock, errors.New("npm lock root differs from reviewed package")
	}
	direct, ok := lock.Packages["node_modules/"+r.Package]
	if !ok || direct.Version != r.Version {
		return lock, errors.New("npm lock does not pin requested version")
	}
	if len(lock.Packages) > MaxArchiveFiles {
		return lock, errors.New("too many npm packages")
	}
	for path, p := range lock.Packages {
		if path == "" {
			continue
		}
		if !safeRelative(path) || !strings.HasPrefix(path, "node_modules/") || p.Link || !pinned(p.Version) {
			return lock, errors.New("npm lock has unpinned, linked or invalid package")
		}
		u, err := url.Parse(p.Resolved)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return lock, errors.New("npm lock requires HTTPS registry artifacts")
		}
		parts := strings.Split(p.Integrity, "-")
		if len(parts) != 2 {
			return lock, errors.New("npm package missing integrity")
		}
		raw, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil || !((parts[0] == "sha512" && len(raw) == 64) || (parts[0] == "sha256" && len(raw) == 32)) {
			return lock, errors.New("npm package integrity must be sha256 or sha512")
		}
		if !npmName.MatchString(lockPackageName(path, p)) {
			return lock, errors.New("invalid npm package name")
		}
	}
	return lock, nil
}
func lockPackageName(path string, p npmLockEntry) string {
	if p.Name != "" {
		return p.Name
	}
	pos := strings.LastIndex(path, "node_modules/")
	return path[pos+len("node_modules/"):]
}
func (i *Installer) checker() MalwareChecker {
	if i.MalwareChecker != nil {
		return i.MalwareChecker
	}
	return &OSVClient{Client: i.Client}
}
func (i *Installer) executor() Executor {
	if i.Executor != nil {
		return i.Executor
	}
	return ExecRunner{}
}

// privateEnv deliberately excludes npm/uv config overrides, credentials,
// NODE_OPTIONS, PYTHONPATH, and user-home package manager configuration.
func (i *Installer) privateEnv(stage string) ([]string, error) {
	home := filepath.Join(stage, ".home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	npmCache := filepath.Join(i.DataDir, "cache", "npm")
	uvCache := filepath.Join(i.DataDir, "cache", "uv")
	pythonDir := filepath.Join(i.DataDir, "runtimes", "python")
	for _, p := range []string{npmCache, uvCache, pythonDir} {
		if err := secureDir(p); err != nil {
			return nil, err
		}
	}
	if err := ownCache(npmCache, "npm"); err != nil {
		return nil, err
	}
	if err := ownCache(uvCache, "uv"); err != nil {
		return nil, err
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "NPM_CONFIG_USERCONFIG=" + os.DevNull, "NPM_CONFIG_GLOBALCONFIG=" + os.DevNull, "NPM_CONFIG_CACHE=" + npmCache, "NPM_CONFIG_IGNORE_SCRIPTS=true", "NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false", "UV_CACHE_DIR=" + uvCache, "UV_PYTHON_INSTALL_DIR=" + pythonDir, "UV_PYTHON_DOWNLOADS=never", "UV_NO_CONFIG=true", "PYTHONNOUSERSITE=1"}
	for _, key := range []string{"PATH", "SystemRoot", "SYSTEMROOT", "COMSPEC", "PATHEXT", "TEMP", "TMP", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env, nil
}
func (i *Installer) InstallNPM(ctx context.Context, r PackageRecipe) (Result, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ctx, cancel := i.bounded(ctx)
	defer cancel()
	if !npmName.MatchString(r.Package) || !pinned(r.Version) || !filepath.IsAbs(r.NPMPath) {
		return Result{}, errors.New("npm requires resolved executable and exact package version")
	}
	lock, err := validateLock(r)
	if err != nil {
		return Result{}, err
	}
	for path, p := range lock.Packages {
		if path != "" {
			if err = i.checker().Check(ctx, "npm", lockPackageName(path, p), p.Version); err != nil {
				return Result{}, err
			}
		}
	}
	if _, err := i.CheckQuota(ctx); err != nil {
		return Result{}, err
	}
	stage, dest, err := i.stage("envs", r.CapabilityID, r.VersionID)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	env, err := i.privateEnv(stage)
	if err != nil {
		return Result{}, err
	}
	manifest, _ := json.Marshal(map[string]any{"private": true, "dependencies": map[string]string{r.Package: r.Version}})
	if err = os.WriteFile(filepath.Join(stage, "package.json"), manifest, 0600); err != nil {
		return Result{}, err
	}
	if err = os.WriteFile(filepath.Join(stage, "package-lock.json"), r.LockJSON, 0600); err != nil {
		return Result{}, err
	}
	command := Command{Path: r.NPMPath, Args: []string{"ci", "--prefix", stage, "--ignore-scripts", "--engine-strict", "--no-audit", "--no-fund", "--userconfig", os.DevNull, "--globalconfig", os.DevNull}, Dir: stage, Env: env}
	if r.NodePath != "" {
		if !filepath.IsAbs(r.NodePath) || !filepath.IsAbs(r.NPMCLI) {
			return Result{}, errors.New("managed npm requires absolute node and CLI paths")
		}
		command.Path = r.NodePath
		command.Args = append([]string{r.NPMCLI}, command.Args...)
		foundPATH := false
		for j, value := range env {
			if strings.HasPrefix(value, "PATH=") {
				env[j] = "PATH=" + filepath.Dir(r.NodePath) + string(os.PathListSeparator) + strings.TrimPrefix(value, "PATH=")
				foundPATH = true
			}
		}
		if !foundPATH {
			env = append(env, "PATH="+filepath.Dir(r.NodePath))
			command.Env = env
		}
	}
	err = i.executor().Run(ctx, command)
	if err != nil {
		return Result{}, err
	}
	if err = checkTreeSize(stage); err != nil {
		return Result{}, err
	}
	if err = rejectExternalLinks(stage); err != nil {
		return Result{}, err
	}
	if err = i.touchCache(ctx, "npm", ""); err != nil {
		return Result{}, err
	}
	if err = i.finalize(ctx, stage, dest, Reference{Kind: "envs", Name: r.CapabilityID, Version: r.VersionID}); err != nil {
		return Result{}, err
	}
	return Result{Source: "managed", Directory: dest, Version: r.Version, BinDirs: []string{filepath.Join(dest, "node_modules", ".bin")}, Verification: "lockfile-integrity-and-osv"}, nil
}
func (i *Installer) InstallUV(ctx context.Context, r PythonRecipe) (Result, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ctx, cancel := i.bounded(ctx)
	defer cancel()
	if !filepath.IsAbs(r.UVPath) || !filepath.IsAbs(r.PythonPath) || len(r.Packages) == 0 || len(r.Packages) > 1024 {
		return Result{}, errors.New("uv requires resolved uv/python paths and locked packages")
	}
	var requirements strings.Builder
	seen := map[string]bool{}
	for _, p := range r.Packages {
		key := strings.ToLower(strings.ReplaceAll(p.Name, "_", "-"))
		if !pythonName.MatchString(p.Name) || (!pythonPinnedVersion.MatchString(p.Version) && !pinned(p.Version)) || len(p.SHA256) == 0 || seen[key] {
			return Result{}, errors.New("Python packages require unique names, exact versions and hashes")
		}
		seen[key] = true
		requirements.WriteString(p.Name + "==" + p.Version)
		for _, hash := range p.SHA256 {
			b, e := hex.DecodeString(hash)
			if e != nil || len(b) != 32 {
				return Result{}, errors.New("invalid Python package SHA256")
			}
			requirements.WriteString(" --hash=sha256:" + hash)
		}
		requirements.WriteByte('\n')
		if err := i.checker().Check(ctx, "PyPI", p.Name, p.Version); err != nil {
			return Result{}, err
		}
	}
	if _, err := i.CheckQuota(ctx); err != nil {
		return Result{}, err
	}
	stage, dest, err := i.stage("envs", r.CapabilityID, r.VersionID)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	env, err := i.privateEnv(stage)
	if err != nil {
		return Result{}, err
	}
	venv := filepath.Join(stage, "venv")
	req := filepath.Join(stage, "requirements.txt")
	if err = os.WriteFile(req, []byte(requirements.String()), 0600); err != nil {
		return Result{}, err
	}
	executor := i.executor()
	if err = executor.Run(ctx, Command{Path: r.UVPath, Args: []string{"venv", "--no-config", "--relocatable", "--python", r.PythonPath, venv}, Dir: stage, Env: env}); err != nil {
		return Result{}, err
	}
	goos, _ := i.platform()
	bin, python := "bin", "python"
	if goos == "windows" {
		bin, python = "Scripts", "python.exe"
	}
	if err = executor.Run(ctx, Command{Path: r.UVPath, Args: []string{"pip", "install", "--no-config", "--python", filepath.Join(venv, bin, python), "--require-hashes", "--no-deps", "--only-binary", ":all:", "-r", req}, Dir: stage, Env: env}); err != nil {
		return Result{}, err
	}
	if err = executor.Run(ctx, Command{Path: r.UVPath, Args: []string{"pip", "check", "--no-config", "--python", filepath.Join(venv, bin, python)}, Dir: stage, Env: env}); err != nil {
		return Result{}, err
	}
	if err = checkTreeSize(stage); err != nil {
		return Result{}, err
	}
	if err = i.touchCache(ctx, "uv", ""); err != nil {
		return Result{}, err
	}
	if err = i.finalize(ctx, stage, dest, Reference{Kind: "envs", Name: r.CapabilityID, Version: r.VersionID}); err != nil {
		return Result{}, err
	}
	return Result{Source: "managed", Directory: dest, Version: r.VersionID, BinDirs: []string{filepath.Join(dest, "venv", bin)}, Verification: "sha256-and-osv"}, nil
}
func checkTreeSize(root string) error {
	var size int64
	count := 0
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 100000 {
			return errors.New("installed environment has too many files")
		}
		if d.Type().IsRegular() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			size += info.Size()
			if size > MaxInstalledBytes {
				return fmt.Errorf("installed environment exceeds %d bytes", MaxInstalledBytes)
			}
		}
		return nil
	})
}

// npm may create bin symlinks, but they must never reach another environment or
// user/system files. Evaluate the full chain while the tree remains private.
func rejectExternalLinks(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		link, err := os.Readlink(path)
		if err != nil || filepath.IsAbs(link) {
			return errors.New("npm environment requires relative symlinks")
		}
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return errors.New("invalid npm environment symlink")
		}
		rel, err := filepath.Rel(root, target)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("npm environment link escapes managed prefix")
		}
		return nil
	})
}
