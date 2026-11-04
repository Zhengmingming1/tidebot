package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/textproto"
	"net/url"
	"runtime"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

type pythonResolvedMetadata struct {
	name, version, hash, bin string
	deps                     []pythonRequirement
	extras                   map[string]bool
}
type pythonSelectedPackage struct {
	metadata pythonResolvedMetadata
	expanded map[string]bool
}

func (r *packageMetadataReader) python(ctx context.Context, recipe capability.Recipe) (capability.Recipe, error) {
	indices := map[string]map[string][]pypiRecipeFile{}
	cache := map[string]pythonResolvedMetadata{}
	decisions := 0
	pythonCompatible := func(spec string) (bool, error) {
		if strings.TrimSpace(spec) == "" {
			return true, nil
		}
		if r.pythonTarget == nil {
			return false, errors.New("Python target required for version markers")
		}
		return matchPythonVersion(r.pythonTarget.Version, spec)
	}
	candidates := func(req pythonRequirement) ([]string, error) {
		if strings.HasPrefix(req.spec, "==") && !strings.Contains(req.spec, ",") {
			version := strings.TrimSpace(strings.TrimPrefix(req.spec, "=="))
			if registryPythonStableVersion.MatchString(version) {
				return []string{version}, nil
			}
		}
		releases, ok := indices[req.name]
		if !ok {
			b, e := r.read(ctx, "https://pypi.org/pypi/"+url.PathEscape(req.name)+"/json", 8<<20)
			if e != nil {
				return nil, e
			}
			var index struct {
				Info struct {
					Name string `json:"name"`
				} `json:"info"`
				Releases map[string][]pypiRecipeFile `json:"releases"`
			}
			if json.Unmarshal(b, &index) != nil || normalizePythonPackage(index.Info.Name) != req.name || len(index.Releases) == 0 || len(index.Releases) > 20000 {
				return nil, errors.New("invalid Python release index")
			}
			releases = index.Releases
			indices[req.name] = releases
		}
		versions := []string{}
		for version, files := range releases {
			if !registryPythonStableVersion.MatchString(version) {
				continue
			}
			ok, e := matchPythonVersion(version, req.spec)
			if e != nil {
				return nil, e
			}
			if !ok {
				continue
			}
			compatible := false
			for _, file := range files {
				if file.Yanked || file.Type != "bdist_wheel" || pythonWheelRank(file.Filename, r.pythonTarget) == 0 {
					continue
				}
				match, e := pythonCompatible(file.RequiresPython)
				if e != nil {
					return nil, e
				}
				compatible = compatible || match
			}
			if compatible {
				versions = append(versions, version)
			}
		}
		sort.Slice(versions, func(i, j int) bool {
			a, _, _ := pythonVersionNumber(versions[i])
			b, _, _ := pythonVersionNumber(versions[j])
			cmp := comparePythonVersion(a, b)
			if cmp == 0 {
				return versions[i] > versions[j]
			}
			return cmp > 0
		})
		if len(versions) > 16 {
			versions = versions[:16]
		}
		if len(versions) == 0 {
			return nil, errors.New("no compatible stable Python wheel satisfies constraints")
		}
		return versions, nil
	}
	load := func(name, version string) (pythonResolvedMetadata, error) {
		key := name + "@" + version
		if cached, ok := cache[key]; ok {
			return cached, nil
		}
		var result pythonResolvedMetadata
		var m pypiRecipeMetadata
		if files, exists := indices[name][version]; exists {
			// The release index already supplied this version's artifact hashes.
			// Verify identity/requirements from the hashed wheel itself instead
			// of spending another request on redundant version JSON.
			m.Info.Name = name
			m.Info.Version = version
			m.URLs = files
		} else {
			b, e := r.read(ctx, "https://pypi.org/pypi/"+url.PathEscape(name)+"/"+url.PathEscape(version)+"/json", 2<<20)
			if e != nil {
				return result, e
			}
			if json.Unmarshal(b, &m) != nil || normalizePythonPackage(m.Info.Name) != name || m.Info.Version != version {
				return result, errors.New("Python metadata identity mismatch")
			}
		}
		ok, e := pythonCompatible(m.Info.RequiresPython)
		if e != nil {
			return result, e
		}
		if !ok {
			return result, errors.New("Python release requires a different interpreter")
		}
		selected := -1
		best := 0
		for i, file := range m.URLs {
			if file.Yanked || file.Type != "bdist_wheel" {
				continue
			}
			rank := pythonWheelRank(file.Filename, r.pythonTarget)
			if rank == 0 {
				continue
			}
			ok, e := pythonCompatible(file.RequiresPython)
			if e != nil {
				return result, e
			}
			if !ok {
				continue
			}
			if name == normalizePythonPackage(recipe.Package) && recipe.SHA256 != "" && !strings.EqualFold(recipe.SHA256, file.Digests.SHA256) {
				continue
			}
			if selected < 0 || rank > best || rank == best && file.Filename < m.URLs[selected].Filename {
				selected = i
				best = rank
			} else if rank == best && file.Filename == m.URLs[selected].Filename {
				return result, errors.New("duplicate Python wheel metadata")
			}
		}
		if selected < 0 {
			return result, errors.New("no wheel matches the reviewed Python target and hash")
		}
		file := m.URLs[selected]
		u, e := url.Parse(file.URL)
		if e != nil || u.Host != "files.pythonhosted.org" || !strings.HasPrefix(u.Path, "/packages/") || !capabilitySHA256.MatchString(file.Digests.SHA256) {
			return result, errors.New("invalid Python wheel origin or hash")
		}
		wheel, e := r.read(ctx, file.URL, 8<<20)
		if e != nil {
			return result, e
		}
		hash := sha256.Sum256(wheel)
		if hex.EncodeToString(hash[:]) != strings.ToLower(file.Digests.SHA256) {
			return result, errors.New("Python wheel checksum mismatch")
		}
		metadata, entries, e := readRecipeWheel(wheel)
		if e != nil {
			return result, e
		}
		headers, e := textproto.NewReader(bufio.NewReader(strings.NewReader(strings.TrimRight(metadata, "\r\n") + "\n\n"))).ReadMIMEHeader()
		if e != nil {
			return result, errors.New("invalid wheel metadata headers")
		}
		if len(headers.Values("Name")) != 1 || len(headers.Values("Version")) != 1 || normalizePythonPackage(headers.Get("Name")) != name || headers.Get("Version") != version {
			return result, errors.New("wheel identity mismatch")
		}
		if len(headers.Values("Requires-Python")) > 1 {
			return result, errors.New("ambiguous wheel Python constraint")
		}
		ok, e = pythonCompatible(headers.Get("Requires-Python"))
		if e != nil {
			return result, e
		}
		if !ok {
			return result, errors.New("wheel requires a different Python version")
		}
		result = pythonResolvedMetadata{name: name, version: version, hash: strings.ToLower(file.Digests.SHA256), extras: map[string]bool{}}
		for _, extra := range headers.Values("Provides-Extra") {
			extra = normalizePythonPackage(extra)
			if !registryPythonName.MatchString(extra) {
				return result, errors.New("invalid wheel extra")
			}
			result.extras[extra] = true
		}
		for _, dep := range headers.Values("Requires-Dist") {
			parsed, e := parsePythonRequirement(dep)
			if e != nil {
				return result, e
			}
			result.deps = append(result.deps, parsed)
		}
		if name == normalizePythonPackage(recipe.Package) {
			bin, e := recipeConsoleScript(entries)
			if e != nil {
				return result, e
			}
			result.bin = bin
		}
		cache[key] = result
		return result, nil
	}
	cloneState := func(state map[string]pythonSelectedPackage) map[string]pythonSelectedPackage {
		out := map[string]pythonSelectedPackage{}
		for name, selected := range state {
			expanded := map[string]bool{}
			for extra, v := range selected.expanded {
				expanded[extra] = v
			}
			selected.expanded = expanded
			out[name] = selected
		}
		return out
	}
	var solve func([]pythonRequirement, map[string]pythonSelectedPackage) (map[string]pythonSelectedPackage, error)
	solve = func(pending []pythonRequirement, state map[string]pythonSelectedPackage) (map[string]pythonSelectedPackage, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if len(pending) == 0 {
			return state, nil
		}
		if len(pending) > 512 || len(state) > 64 {
			return nil, errors.New("Python dependency graph too large")
		}
		req := pending[0]
		rest := pending[1:]
		expand := func(selected pythonSelectedPackage, next map[string]pythonSelectedPackage) (map[string]pythonSelectedPackage, error) {
			extras := append([]string(nil), req.extras...)
			if len(extras) == 0 {
				extras = []string{""}
			}
			dependencies := []pythonRequirement{}
			for _, extra := range extras {
				if selected.expanded[extra] {
					continue
				}
				if extra != "" && !selected.metadata.extras[extra] {
					return nil, errors.New("requested Python extra is not provided")
				}
				selected.expanded[extra] = true
				env := pythonMarkerEnvironment(r.pythonTarget, extra)
				for _, dep := range selected.metadata.deps {
					include, e := evaluatePythonMarker(dep.marker, env)
					if e != nil {
						return nil, e
					}
					if include {
						dep.marker = ""
						dependencies = append(dependencies, dep)
					}
				}
			}
			next[req.name] = selected
			return solve(append(dependencies, rest...), next)
		}
		if selected, exists := state[req.name]; exists {
			ok, e := matchPythonVersion(selected.metadata.version, req.spec)
			if e != nil {
				return nil, e
			}
			if !ok {
				return nil, errors.New("conflicting Python version requirements")
			}
			return expand(selected, cloneState(state))
		}
		versions, e := candidates(req)
		if e != nil {
			return nil, e
		}
		var last error
		for _, version := range versions {
			decisions++
			if decisions > 128 {
				return nil, errors.New("Python resolver search budget exceeded")
			}
			metadata, e := load(req.name, version)
			if e != nil {
				last = e
				continue
			}
			selected := pythonSelectedPackage{metadata: metadata, expanded: map[string]bool{}}
			out, e := expand(selected, cloneState(state))
			if e == nil {
				return out, nil
			}
			last = e
		}
		if last == nil {
			last = errors.New("Python dependency closure unavailable")
		}
		return nil, last
	}
	if !registryPythonName.MatchString(recipe.Package) || !registryPythonStableVersion.MatchString(recipe.Version) {
		return recipe, errors.New("Python root requires an exact stable version")
	}
	root := normalizePythonPackage(recipe.Package)
	state, e := solve([]pythonRequirement{{name: root, spec: "==" + recipe.Version}}, map[string]pythonSelectedPackage{})
	if e != nil {
		return recipe, e
	}
	locked := []capability.RecipePythonPackage{}
	for name, selected := range state {
		locked = append(locked, capability.RecipePythonPackage{Name: name, Version: selected.metadata.version, SHA256: []string{selected.metadata.hash}})
	}
	sort.Slice(locked, func(i, j int) bool { return locked[i].Name < locked[j].Name })
	recipe.PythonPackages = locked
	recipe.PythonTarget = r.pythonTarget
	recipe.Executable = "venv/bin/" + state[root].metadata.bin
	if runtime.GOOS == "windows" {
		recipe.Executable = "venv/Scripts/" + state[root].metadata.bin + ".exe"
	}
	return recipe, nil
}
