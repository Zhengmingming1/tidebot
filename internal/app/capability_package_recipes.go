package app

// Registry package resolution is deliberately metadata-only. Unsupported graphs
// remain manual instead of asking a package manager to run an unreviewed resolver.
import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

var registryNPMName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var registryPythonName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var registryBinName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var registryPythonNormalize = regexp.MustCompile(`[-_.]+`)

type packageMetadataReader struct {
	client                *http.Client
	remaining             int64
	requests              int
	pythonTarget          *capability.RecipePythonTarget
	pythonTargetAttempted bool
}

func (r *packageMetadataReader) read(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Host != "registry.npmjs.org" && u.Host != "pypi.org" && u.Host != "files.pythonhosted.org") {
		return nil, errors.New("package metadata origin rejected")
	}
	r.requests++
	if r.requests > 64 || r.remaining <= 0 {
		return nil, errors.New("package metadata budget exceeded")
	}
	if limit > r.remaining {
		limit = r.remaining
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/json")
	resp, e := r.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("package metadata unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	r.remaining -= int64(len(b))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("package metadata size exceeded")
	}
	return b, nil
}
func validPackageIntegrity(s string) bool {
	alg, b, ok := strings.Cut(s, "-")
	raw, e := base64.StdEncoding.DecodeString(b)
	return ok && e == nil && (alg == "sha512" && len(raw) == 64 || alg == "sha256" && len(raw) == 32)
}
func (a *App) addRegistryPackageRecipes(ctx context.Context, lock *capability.Lock) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	reader := packageMetadataReader{client: a.capabilityRegistryHTTPClient(), remaining: 32 << 20}
	runtimes := map[string]bool{}
	for _, r := range lock.Requires.Runtimes {
		runtimes[r.Name] = true
	}
	for i := range lock.Requires.Tools {
		req := &lock.Requires.Tools[i]
		for j := range req.Recipes {
			original := req.Recipes[j]
			var resolved capability.Recipe
			var err error
			switch original.Backend {
			case "npm":
				if len(original.LockJSON) > 0 {
					continue
				}
				resolved, err = reader.npm(ctx, original)
			case "uv":
				if len(original.PythonPackages) > 0 {
					continue
				}
				if err = a.preparePythonRecipeReader(ctx, &reader); err == nil {
					resolved, err = reader.python(ctx, original)
				}
			default:
				continue
			}
			if err != nil {
				lock.Scan.Findings = append(lock.Scan.Findings, capability.Finding{ID: "package-recipe-unavailable", Severity: "info", Note: req.Name + ": pinned package metadata could not be safely resolved; install manually. " + err.Error()})
				continue
			}
			req.Recipes[j] = resolved
			if resolved.PythonTarget != nil {
				pinPythonRecipeRuntime(lock, resolved.PythonTarget.Version)
				runtimes["python3"] = true
			}
			names := []string{"npm"}
			if original.Backend == "uv" {
				names = []string{"uv", "python3"}
			}
			for _, name := range names {
				if !runtimes[name] {
					lock.Requires.Runtimes = append(lock.Requires.Runtimes, capability.ToolRequirement{Name: name})
					runtimes[name] = true
				}
			}
		}
	}
	a.addNodeRuntimeRecipe(ctx, lock)
}

type npmRecipeMetadata struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Bin          json.RawMessage   `json:"bin"`
	Dependencies map[string]string `json:"dependencies"`
	Optional     map[string]string `json:"optionalDependencies"`
	OS           []string          `json:"os"`
	CPU          []string          `json:"cpu"`
	Libc         []string          `json:"libc"`
	Engines      map[string]string `json:"engines"`
	Peers        map[string]string `json:"peerDependencies"`
	PeerMeta     map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
	Bundled      json.RawMessage `json:"bundleDependencies"`
	BundledAlias json.RawMessage `json:"bundledDependencies"`
	Dist         struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func (r *packageMetadataReader) npm(ctx context.Context, recipe capability.Recipe) (capability.Recipe, error) {
	if !registryNPMName.MatchString(recipe.Package) {
		return recipe, errors.New("invalid npm package name")
	}
	if _, err := parsePackageVersion(recipe.Version); err != nil {
		return recipe, errors.New("npm root requires an exact stable version")
	}
	type node struct {
		name, version, location string
		parent                  *node
		metadata                npmRecipeMetadata
		optional                bool
	}
	packages := map[string]any{"": map[string]any{"dependencies": map[string]string{recipe.Package: recipe.Version}}}
	installed := map[string]*node{}
	documents := map[string]map[string]npmRecipeMetadata{}
	metadata := map[string]npmRecipeMetadata{}
	peerConstraints := map[string][]string{}
	binOwners := map[string]string{}
	executable := ""
	selectMetadata := func(name, constraint string) (npmRecipeMetadata, error) {
		if !registryNPMName.MatchString(name) {
			return npmRecipeMetadata{}, errors.New("invalid npm dependency name")
		}
		if _, e := matchNPMRange("1.0.0", constraint); e != nil {
			return npmRecipeMetadata{}, e
		}
		key := name + "@" + constraint
		if m, ok := metadata[key]; ok {
			return m, nil
		}
		exact := constraint
		if !strings.Contains(constraint, "||") {
			terms := strings.Fields(constraint)
			if len(terms) > 0 {
				if _, e := parsePackageVersion(terms[0]); e == nil {
					matches, e := matchNPMRange(terms[0], constraint)
					if e != nil || !matches {
						return npmRecipeMetadata{}, errors.New("conflicting exact npm peer constraint")
					}
					exact = terms[0]
				}
			}
		}
		if _, e := parsePackageVersion(exact); e == nil {
			if cached, exists := metadata[name+"@"+exact]; exists {
				metadata[key] = cached
				return cached, nil
			}
			b, e := r.read(ctx, "https://registry.npmjs.org/"+url.PathEscape(name)+"/"+url.PathEscape(exact), 2<<20)
			if e != nil {
				return npmRecipeMetadata{}, e
			}
			var m npmRecipeMetadata
			if json.Unmarshal(b, &m) != nil || m.Name != name || m.Version != exact {
				return m, errors.New("npm metadata identity mismatch")
			}
			metadata[key] = m
			return m, nil
		}
		versions, ok := documents[name]
		if !ok {
			b, e := r.read(ctx, "https://registry.npmjs.org/"+url.PathEscape(name), 8<<20)
			if e != nil {
				return npmRecipeMetadata{}, e
			}
			var doc struct {
				Name     string                       `json:"name"`
				Versions map[string]npmRecipeMetadata `json:"versions"`
			}
			if json.Unmarshal(b, &doc) != nil || doc.Name != name || len(doc.Versions) == 0 || len(doc.Versions) > 20000 {
				return npmRecipeMetadata{}, errors.New("invalid npm version index")
			}
			versions = doc.Versions
			documents[name] = versions
		}
		selected := ""
		var best packageVersion
		for v, m := range versions {
			pv, e := parsePackageVersion(v)
			if e != nil {
				continue
			}
			ok, e := matchNPMRange(v, constraint)
			if e != nil {
				return npmRecipeMetadata{}, e
			}
			if ok && (selected == "" || comparePackageVersion(pv, best) > 0) {
				if m.Name != name || m.Version != v {
					return npmRecipeMetadata{}, errors.New("npm index identity mismatch")
				}
				selected = v
				best = pv
			}
		}
		if selected == "" {
			return npmRecipeMetadata{}, errors.New("no supported stable npm version satisfies range")
		}
		m := versions[selected]
		metadata[key] = m
		return m, nil
	}
	scope := func(owner *node) string {
		if owner == nil {
			return "node_modules"
		}
		return owner.location + "/node_modules"
	}
	var visit func(string, string, *node, bool, bool) (*node, error)
	visit = func(name, constraint string, owner *node, peer, optional bool) (*node, error) {
		if owner != nil && owner.optional {
			optional = true
		}
		if constraints := peerConstraints[scope(owner)+"/"+name]; len(constraints) > 0 {
			var e error
			constraint, e = intersectNPMRanges(append([]string{constraint}, constraints...)...)
			if e != nil {
				return nil, e
			}
		}
		if !registryNPMName.MatchString(name) {
			return nil, errors.New("invalid npm dependency name")
		}
		if _, e := matchNPMRange("1.0.0", constraint); e != nil {
			return nil, e
		}
		// Node resolves the nearest ancestor. Reuse only that matching pin; a
		// conflicting ordinary dependency gets a private nested node_modules entry.
		for ancestor := owner; ; ancestor = ancestor.parent {
			if existing := installed[scope(ancestor)+"/"+name]; existing != nil {
				ok, e := matchNPMRange(existing.version, constraint)
				if e != nil {
					return nil, e
				}
				if ok && (!existing.optional || optional) {
					return existing, nil
				}
				break
			}
			if ancestor == nil {
				break
			}
		}
		location := scope(owner) + "/" + name
		if installed[location] != nil {
			return nil, errors.New("conflicting npm peer requirements need a reviewed lockfile")
		}
		if len(installed) >= 128 || strings.Count(location, "node_modules") > 24 {
			return nil, errors.New("npm dependency graph too large")
		}
		m, e := selectMetadata(name, constraint)
		if e != nil {
			return nil, e
		}
		if len(m.Bundled) > 0 && string(m.Bundled) != "[]" && string(m.Bundled) != "false" || len(m.BundledAlias) > 0 && string(m.BundledAlias) != "[]" && string(m.BundledAlias) != "false" {
			return nil, errors.New("npm bundled dependencies require a reviewed lockfile")
		}
		u, e := url.Parse(m.Dist.Tarball)
		if e != nil || u.Scheme != "https" || u.Host != "registry.npmjs.org" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || !validPackageIntegrity(m.Dist.Integrity) {
			return nil, errors.New("npm artifact needs official origin and strong integrity")
		}
		root := owner == nil && name == recipe.Package
		if root && recipe.SHA256 != "" {
			alg, encoded, _ := strings.Cut(m.Dist.Integrity, "-")
			digest, _ := base64.StdEncoding.DecodeString(encoded)
			if alg != "sha256" || !strings.EqualFold(hex.EncodeToString(digest), recipe.SHA256) {
				return nil, errors.New("npm SHA256 cannot be confirmed from package integrity")
			}
		}
		if root && recipe.Integrity != "" && recipe.Integrity != m.Dist.Integrity {
			return nil, errors.New("npm integrity differs from registry manifest")
		}
		bins := map[string]string{}
		if len(m.Bin) > 0 && string(m.Bin) != "null" {
			var single string
			if json.Unmarshal(m.Bin, &single) == nil {
				bins[path.Base(name)] = single
			} else if json.Unmarshal(m.Bin, &bins) != nil {
				return nil, errors.New("invalid npm executable metadata")
			}
		}
		for bin, target := range bins {
			key := scope(owner) + "/.bin/" + bin
			if existing := binOwners[key]; existing != "" && existing != name {
				return nil, errors.New("npm executable name collision")
			}
			binOwners[key] = name
			if !registryBinName.MatchString(bin) || target == "" || path.IsAbs(target) || strings.ContainsAny(target, "\\\x00") || path.Clean(target) == ".." || strings.HasPrefix(path.Clean(target), "../") {
				return nil, errors.New("unsafe npm executable")
			}
		}
		if root {
			if len(bins) != 1 {
				return nil, errors.New("npm package must declare one unambiguous executable")
			}
			for bin := range bins {
				executable = "node_modules/.bin/" + bin
				if runtime.GOOS == "windows" {
					executable += ".cmd"
				}
			}
			recipe.Integrity = m.Dist.Integrity
		}
		n := &node{name: name, version: m.Version, location: location, parent: owner, metadata: m, optional: optional}
		installed[location] = n
		entry := map[string]any{"name": name, "version": m.Version, "resolved": m.Dist.Tarball, "integrity": m.Dist.Integrity, "dependencies": m.Dependencies, "bin": bins}
		if len(m.Peers) > 0 {
			entry["peerDependencies"] = m.Peers
			entry["peerDependenciesMeta"] = m.PeerMeta
		}
		if peer {
			entry["peer"] = true
		}
		if optional {
			entry["optional"] = true
		}
		if len(m.Optional) > 0 {
			entry["optionalDependencies"] = m.Optional
		}
		if len(m.OS) > 0 {
			entry["os"] = m.OS
		}
		if len(m.CPU) > 0 {
			entry["cpu"] = m.CPU
		}
		if len(m.Libc) > 0 {
			entry["libc"] = m.Libc
		}
		if len(m.Engines) > 0 {
			entry["engines"] = m.Engines
		}
		packages[location] = entry
		dependencies := map[string]string{}
		for dep, constraint := range m.Dependencies {
			dependencies[dep] = constraint
		}
		for dep, constraint := range m.Optional {
			dependencies[dep] = constraint
		}
		keys := make([]string, 0, len(dependencies))
		for dep := range dependencies {
			keys = append(keys, dep)
		}
		sort.Strings(keys)
		// Collect sibling peer constraints before choosing a broad direct range
		// (for example zod ^3 || ^4 with a sibling requiring zod ^3).
		for _, dep := range keys {
			var child npmRecipeMetadata
			found := false
			for ancestor := n; ; ancestor = ancestor.parent {
				if existing := installed[scope(ancestor)+"/"+dep]; existing != nil {
					matches, e := matchNPMRange(existing.version, dependencies[dep])
					if e != nil {
						return nil, e
					}
					if matches {
						child = existing.metadata
						found = true
					}
					break
				}
				if ancestor == nil {
					break
				}
			}
			if !found {
				var e error
				child, e = selectMetadata(dep, dependencies[dep])
				if e != nil {
					return nil, e
				}
			}
			for peerName, peerRange := range child.Peers {
				if child.PeerMeta[peerName].Optional {
					continue
				}
				key := scope(n) + "/" + peerName
				peerConstraints[key] = append(peerConstraints[key], peerRange)
			}
		}
		for _, dep := range keys {
			_, isOptional := m.Optional[dep]
			if _, e := visit(dep, dependencies[dep], n, false, isOptional); e != nil {
				return nil, e
			}
		}

		keys = keys[:0]
		for dep := range m.Peers {
			keys = append(keys, dep)
		}
		sort.Strings(keys)
		for _, dep := range keys {
			if m.PeerMeta[dep].Optional {
				continue
			} // npm does not auto-install absent optional peers.
			if _, e := visit(dep, m.Peers[dep], owner, true, optional); e != nil {
				return nil, e
			}
		}
		return n, nil
	}
	if _, e := visit(recipe.Package, recipe.Version, nil, false, false); e != nil {
		return recipe, e
	}
	// Validate all peers after graph expansion, including optional peers that
	// another dependency introduced after the declaring package was visited.
	for _, n := range installed {
		for dep, constraint := range n.metadata.Peers {
			var found *node
			for ancestor := n.parent; ; ancestor = ancestor.parent {
				found = installed[scope(ancestor)+"/"+dep]
				if found != nil || ancestor == nil {
					break
				}
			}
			if found == nil {
				if n.metadata.PeerMeta[dep].Optional {
					continue
				}
				return recipe, errors.New("npm peer dependency missing")
			}
			ok, e := matchNPMRange(found.version, constraint)
			if e != nil || !ok {
				return recipe, errors.New("conflicting npm peer dependency closure")
			}
		}
	}
	b, e := json.Marshal(map[string]any{"lockfileVersion": 3, "requires": true, "packages": packages})
	if e != nil {
		return recipe, e
	}
	recipe.LockJSON = b
	recipe.Executable = executable
	return recipe, nil
}

type pypiRecipeFile struct {
	Filename       string `json:"filename"`
	URL            string `json:"url"`
	Type           string `json:"packagetype"`
	Yanked         bool   `json:"yanked"`
	RequiresPython string `json:"requires_python"`
	Digests        struct {
		SHA256 string `json:"sha256"`
	} `json:"digests"`
}
type pypiRecipeMetadata struct {
	Info struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		RequiresPython string `json:"requires_python"`
	} `json:"info"`
	URLs []pypiRecipeFile `json:"urls"`
}

func normalizePythonPackage(s string) string {
	return strings.ToLower(registryPythonNormalize.ReplaceAllString(s, "-"))
}

func readRecipeWheel(data []byte) (string, string, error) {
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return "", "", e
	}
	if len(z.File) > 4096 {
		return "", "", errors.New("wheel file limit exceeded")
	}
	meta, entries := "", ""
	root := ""
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".dist-info/METADATA") && !strings.HasSuffix(f.Name, ".dist-info/entry_points.txt") {
			continue
		}
		dir := path.Dir(f.Name)
		if strings.Contains(dir, "/") || root != "" && root != dir {
			return "", "", errors.New("ambiguous wheel metadata")
		}
		root = dir
		if f.UncompressedSize64 > 256<<10 {
			return "", "", errors.New("wheel metadata too large")
		}
		r, e := f.Open()
		if e != nil {
			return "", "", e
		}
		b, e := io.ReadAll(io.LimitReader(r, (256<<10)+1))
		r.Close()
		if e != nil || len(b) > 256<<10 {
			return "", "", errors.New("invalid wheel metadata")
		}
		if strings.HasSuffix(f.Name, "/METADATA") {
			if meta != "" {
				return "", "", errors.New("duplicate wheel metadata")
			}
			meta = string(b)
		} else {
			if entries != "" {
				return "", "", errors.New("duplicate wheel entry points")
			}
			entries = string(b)
		}
	}
	if meta == "" {
		return "", "", errors.New("wheel metadata missing")
	}
	return meta, entries, nil
}
func recipeConsoleScript(entries string) (string, error) {
	section := ""
	name := ""
	for _, line := range strings.Split(entries, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[console_scripts]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !registryBinName.MatchString(key) || strings.TrimSpace(value) == "" || name != "" {
			return "", errors.New("requires one unambiguous Python console script")
		}
		name = key
	}
	if name == "" {
		return "", errors.New("Python console script missing")
	}
	return name, nil
}
