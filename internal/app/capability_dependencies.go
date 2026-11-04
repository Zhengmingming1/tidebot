package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

type capabilityDependencyPlan struct {
	Preference *capability.DependencyPreference `json:"preference,omitempty"`
	Resolved   *capability.Resolved             `json:"resolved,omitempty"`
	Manual     []capabilityManualDependency     `json:"manual,omitempty"`
	Name       string                           `json:"name"`
	Runtime    bool                             `json:"runtime"`
	Status     string                           `json:"status"`
	Recipe     *capability.Recipe               `json:"recipe,omitempty"`
	Note       string                           `json:"note,omitempty"`
}

var capabilityExactVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
var capabilitySHA256 = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func dependencyPlan(lock capability.Lock) []capabilityDependencyPlan {
	out := []capabilityDependencyPlan{}
	appendRequirements := func(reqs []capability.ToolRequirement, isRuntime bool) {
		for _, req := range reqs {
			item := capabilityDependencyPlan{Name: req.Name, Runtime: isRuntime, Status: "manual", Note: "No complete pinned recipe for this platform; install manually and recheck."}
			item.Manual = dependencyManualInstructions(req)
			if pref, ok := lock.DependencyPreferences[req.Name]; ok {
				copy := pref
				item.Preference = &copy
			}
			if resolved, ok := lock.Resolved[req.Name]; ok {
				copy := resolved
				item.Resolved = &copy
			}
			if lock.Resolved[req.Name].Status == "ready" {
				item.Status = "ready"
				item.Note = "Reuse compatible system or managed command"
				out = append(out, item)
				continue
			}
			for _, recipe := range req.Recipes {
				if !capabilityExactVersion.MatchString(recipe.Version) && recipe.Backend != "oci" && !(recipe.Backend == "uv" && registryPythonStableVersion.MatchString(recipe.Version)) {
					continue
				}
				if compatible, err := capability.SatisfiesVersion(strings.TrimPrefix(recipe.Version, "v"), req.Version); err != nil || !compatible {
					continue
				}
				supported := false
				switch recipe.Backend {
				case "http-archive", "github-release":
					asset, ok := recipe.Assets[runtime.GOOS+"-"+runtime.GOARCH]
					if !ok {
						asset = capability.RecipeAsset{URL: recipe.URL, SHA256: recipe.SHA256, Format: recipe.Format, Signature: recipe.Signature}
					}
					supported = capabilitySHA256.MatchString(asset.SHA256) && strings.HasPrefix(asset.URL, "https://") && recipe.Executable != "" && (asset.Format == "zip" || asset.Format == "tar.gz" || asset.Format == "binary")
				case "npm":
					supported = len(recipe.LockJSON) > 0 && recipe.Package != "" && recipe.Executable != ""
				case "uv":
					supported = len(recipe.PythonPackages) > 0 && recipe.Executable != ""
				}
				if supported {
					copy := recipe
					item.Recipe = &copy
					item.Status = "download"
					item.Note = "Explicit approval required. Installs only into tidebot data; archive SHA256 verifies integrity, not publisher identity."
					if isNodeRuntimeRecipe(item) {
						item.Note = "Explicit approval required: download the official Node.js and npm runtime bundle. SHA256 from nodejs.org verifies integrity only; no publisher signature verification."
					}
					break
				}
			}
			out = append(out, item)
		}
	}
	appendRequirements(lock.Requires.Runtimes, true)
	appendRequirements(lock.Requires.Tools, false)
	bundledNode := false
	for _, item := range out {
		if item.Status == "download" && isNodeRuntimeRecipe(item) {
			bundledNode = true
		}
	}
	if bundledNode {
		for index := range out {
			if out[index].Runtime && out[index].Name == "node" && out[index].Status == "manual" {
				out[index].Status = "download"
				out[index].Note = "Provided by the explicitly approved Node/npm bundle if no compatible system Node is available."
			}
		}
	}
	return out
}

// installCapabilityDependencies is called only from the owner's confirmation
// handler with the explicit install_dependencies flag, never through model IPC.
func (a *App) installCapabilityDependencies(ctx context.Context, p *capability.Proposal) (cleanup func(), err error) {
	created := []string{}
	cleanup = func() {
		for _, dir := range created {
			_ = os.RemoveAll(dir)
		}
	}
	installer := &managed.Installer{DataDir: a.Cfg.DataDir, Client: netproxy.Client(30 * time.Second)}
	if p.Lock.Resolved == nil {
		p.Lock.Resolved = map[string]capability.Resolved{}
	}
	for index, item := range dependencyPlan(p.Lock) {
		if item.Status != "download" || item.Recipe == nil {
			continue
		}
		r := *item.Recipe
		var result managed.Result
		reused := false
		switch r.Backend {
		case "http-archive", "github-release":
			assets := map[string]managed.Asset{}
			for k, v := range r.Assets {
				assets[k] = managed.Asset{URL: v.URL, SHA256: v.SHA256, Format: v.Format, Signature: managedRecipeSignature(v.Signature)}
			}
			if len(assets) == 0 {
				assets[runtime.GOOS+"-"+runtime.GOARCH] = managed.Asset{URL: r.URL, SHA256: r.SHA256, Format: r.Format, Signature: managedRecipeSignature(r.Signature)}
			}
			if isNodeRuntimeRecipe(item) {
				result, reused, err = a.reuseNodeRuntime(ctx, item, p.Lock)
				if err == nil && !reused {
					result, err = installer.InstallNode(ctx, r.Version, assets[runtime.GOOS+"-"+runtime.GOARCH])
					if err == nil {
						err = prepareNodeInterpreter(ctx, &result, p.Lock)
					}
					if err != nil && result.Directory != "" {
						created = append(created, result.Directory)
					}
				}
			} else {
				result, err = installer.InstallArchive(ctx, managed.ArchiveRecipe{Name: item.Name, Version: r.Version, Backend: r.Backend, Runtime: item.Runtime, Assets: assets, Executables: map[string]string{item.Name: r.Executable}})
			}
		case "npm":
			resolved := p.Lock.Resolved["npm"]
			if resolved.Status != "ready" {
				resolved = capability.Detect(ctx, []capability.ToolRequirement{{Name: "npm"}}, capabilityDependencyDetectOptions(p.Lock))["npm"]
			}
			if resolved.Status != "ready" {
				return cleanup, errors.New("npm must be installed before this dependency")
			}
			if resolved.Source == "managed" {
				tree, e := managedTreeDigest(ctx, a.Cfg.DataDir, resolved.Directory)
				if e != nil || !capabilitySHA256.MatchString(resolved.TreeSHA256) || tree != resolved.TreeSHA256 {
					return cleanup, errors.New("managed npm runtime tree integrity check failed")
				}
				hash, e := managedExecutableDigest(ctx, a.Cfg.DataDir, resolved.Path)
				if e != nil || !capabilitySHA256.MatchString(resolved.SHA256) || hash != resolved.SHA256 {
					return cleanup, errors.New("managed npm executable integrity check failed")
				}
			}
			npmRecipe := managed.PackageRecipe{CapabilityID: p.Lock.ID, VersionID: p.ID + "-" + strconv.Itoa(index), NPMPath: resolved.Path, Package: r.Package, Version: r.Version, LockJSON: r.LockJSON}
			if resolved.Source == "managed" && p.Lock.Resolved["node"].Status == "ready" {
				for _, runtimeReq := range p.Lock.Requires.Runtimes {
					if runtimeReq.Name != "npm" {
						continue
					}
					for _, nodeRecipe := range runtimeReq.Recipes {
						if !isNodeRuntimeRecipe(capabilityDependencyPlan{Name: "npm", Runtime: true, Recipe: &nodeRecipe}) {
							continue
						}
						_, bundleNode, _, _, e := managed.NodeLayout(nodeRecipe.Version, runtime.GOOS, runtime.GOARCH)
						if e != nil {
							return cleanup, e
						}
						npmRecipe.NodePath = p.Lock.Resolved["node"].Path
						npmRecipe.NPMCLI = nodeNPMFile(filepath.Join(resolved.Directory, filepath.FromSlash(bundleNode)), "bin", "npm-cli.js")
					}
				}
			}
			result, err = installer.InstallNPM(ctx, npmRecipe)
		case "uv":
			resolved := capability.Detect(ctx, []capability.ToolRequirement{{Name: "uv"}, {Name: "python3"}}, capabilityDependencyDetectOptions(p.Lock))
			if resolved["uv"].Status != "ready" || resolved["python3"].Status != "ready" {
				return cleanup, errors.New("uv and Python must be installed before this dependency")
			}
			packages := []managed.PythonPackage{}
			for _, v := range r.PythonPackages {
				packages = append(packages, managed.PythonPackage{Name: v.Name, Version: v.Version, SHA256: v.SHA256})
			}
			if e := verifyPythonRecipeTarget(ctx, r, resolved["python3"].Path); e != nil {
				return cleanup, e
			}
			result, err = installer.InstallUV(ctx, managed.PythonRecipe{CapabilityID: p.Lock.ID, VersionID: p.ID + "-" + strconv.Itoa(index), UVPath: resolved["uv"].Path, PythonPath: resolved["python3"].Path, Packages: packages})
		}
		if err != nil {
			return cleanup, err
		}
		if !reused {
			created = append(created, result.Directory)
		}
		executable := result.Executables[item.Name]
		if executable == "" {
			if filepath.IsAbs(r.Executable) || strings.Contains(r.Executable, "..") {
				return cleanup, errors.New("invalid package executable")
			}
			executable = filepath.Join(result.Directory, filepath.FromSlash(r.Executable))
			info, e := os.Stat(executable)
			if e != nil || !info.Mode().IsRegular() {
				return cleanup, errors.New("package executable missing")
			}
		}
		digest, e := managedExecutableDigest(ctx, a.Cfg.DataDir, executable)
		if e != nil {
			return cleanup, e
		}
		treeHash, e := managedTreeDigest(ctx, a.Cfg.DataDir, result.Directory)
		if e != nil {
			return cleanup, e
		}
		p.Lock.Resolved[item.Name] = capability.Resolved{Directory: result.Directory, TreeSHA256: treeHash, SHA256: digest, Source: "managed", Status: "ready", Path: executable, Version: r.Version, Note: result.Verification}
		if isNodeRuntimeRecipe(item) {
			nodeDigest, e := managedExecutableDigest(ctx, a.Cfg.DataDir, result.Executables["node"])
			if e != nil {
				return cleanup, e
			}
			p.Lock.Resolved["node"] = capability.Resolved{Directory: result.Directory, TreeSHA256: treeHash, SHA256: nodeDigest, Source: "managed", Status: "ready", Path: result.Executables["node"], Version: r.Version, Note: result.Verification}
			if interpreter := result.Executables["interpreter"]; interpreter != "" && interpreter != result.Executables["node"] {
				resolvedNode := capability.Detect(ctx, []capability.ToolRequirement{{Name: "node"}}, capability.DetectOptions{Paths: map[string]string{"node": interpreter}})["node"]
				if resolvedNode.Status != "ready" {
					return cleanup, errors.New("selected system Node is no longer available")
				}
				p.Lock.Resolved["node"] = resolvedNode
			}
			npm := p.Lock.Resolved[item.Name]
			npm.Version, e = installedNodeNPMVersion(result.Executables["node"])
			if e != nil {
				return cleanup, e
			}
			npm.Note += "; bundled with Node " + r.Version
			p.Lock.Resolved[item.Name] = npm
		}
	}
	for _, req := range append(append([]capability.ToolRequirement{}, p.Lock.Requires.Tools...), p.Lock.Requires.Runtimes...) {
		if !req.Optional && p.Lock.Resolved[req.Name].Status != "ready" {
			return cleanup, errors.New("required dependency still unavailable: " + req.Name)
		}
	}
	return cleanup, nil
}

// managedExecutableDigest follows package-manager bin links only inside the
// data root, bounds reads and records executable integrity in the immutable lock.
func managedExecutableDigest(ctx context.Context, root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("managed executable escapes data directory")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > managed.MaxInstalledBytes {
		return "", errors.New("invalid managed executable")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, e := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (a *App) verifyManagedCapabilityDependencies(ctx context.Context, lock capability.Lock) error {
	for _, req := range append(append([]capability.ToolRequirement{}, lock.Requires.Tools...), lock.Requires.Runtimes...) {
		resolved := lock.Resolved[req.Name]
		if resolved.Status != "ready" {
			if req.Optional {
				continue
			}
			return errors.New("required capability dependency is not ready: " + req.Name)
		}
		if resolved.Source == "system" {
			// npm launchers are pinned to the interpreter selected against engines.node.
			// A changed system Node must be reviewed again before using those launchers.
			if req.Name == "node" && lock.Resolved["npm"].Source == "managed" {
				req.Version = resolved.Version
			}
			checked := capability.Detect(ctx, []capability.ToolRequirement{req}, capability.DetectOptions{Paths: map[string]string{req.Name: resolved.Path}})
			if checked[req.Name].Status != "ready" {
				return errors.New("system capability dependency no longer compatible: " + req.Name)
			}
		}
	}
	for _, resolved := range lock.Resolved {
		if resolved.Source != "managed" || resolved.Status != "ready" {
			continue
		}
		if !capabilitySHA256.MatchString(resolved.SHA256) {
			return errors.New("managed executable digest missing; reinstall capability")
		}
		if !capabilitySHA256.MatchString(resolved.TreeSHA256) {
			return errors.New("managed tree digest missing; reinstall capability")
		}
		treeHash, err := managedTreeDigest(ctx, a.Cfg.DataDir, resolved.Directory)
		if err != nil || treeHash != resolved.TreeSHA256 {
			return errors.New("managed dependency tree integrity check failed")
		}
		digest, err := managedExecutableDigest(ctx, a.Cfg.DataDir, resolved.Path)
		if err != nil || digest != resolved.SHA256 {
			return errors.New("managed executable integrity check failed")
		}
	}
	return nil
}

func managedRecipeSignature(s *capability.RecipeSignature) *managed.Signature {
	if s == nil {
		return nil
	}
	return &managed.Signature{Kind: s.Kind, URL: s.URL, Identity: s.Identity}
}

// managedTreeDigest covers every installed byte, path, permission and link. The
// installer ownership marker has mutable last-use metadata and is not code.
// External interpreter file symlinks (uv) are hashed too, never directory links.
func managedTreeDigest(ctx context.Context, dataRoot, directory string) (string, error) {
	root, err := filepath.EvalSymlinks(dataRoot)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("managed tree outside data directory")
	}
	h := sha256.New()
	count := 0
	var total int64
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if relative == ".tidebot-managed.json" {
			return nil
		}
		count++
		if count > managed.MaxArchiveFiles {
			return errors.New("managed tree file limit exceeded")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(relative), info.Mode())
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "link:%s\x00", target)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return errors.New("managed tree contains non-file link or special file")
		}
		if st.Size() > managed.MaxInstalledBytes {
			return errors.New("managed tree size limit exceeded")
		}
		fileHash := sha256.New()
		buf := make([]byte, 64<<10)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, e := f.Read(buf)
			if n > 0 {
				total += int64(n)
				if total > managed.MaxInstalledBytes {
					return errors.New("managed tree size limit exceeded")
				}
				_, _ = fileHash.Write(buf[:n])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		fmt.Fprintf(h, "%x\x00", fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func capabilityDependencyDetectOptions(lock capability.Lock) capability.DetectOptions {
	options := capability.DetectOptions{Paths: map[string]string{}, OnlyConfigured: true}
	for name, resolved := range lock.Resolved {
		if filepath.IsAbs(resolved.Path) {
			// Never replace an approved executable with an earlier PATH match.
			options.Paths[name] = resolved.Path
			if resolved.Status == "ready" {
				options.ExtraDirs = append(options.ExtraDirs, filepath.Dir(resolved.Path))
			}
		}
	}
	for name, pref := range lock.DependencyPreferences {
		if options.Paths[name] == "" && pref.Path != "" && !pref.PreferManaged {
			options.Paths[name] = pref.Path
		}
	}
	return options
}
