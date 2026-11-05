package skillpkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PluginBundle describes a Claude plugin adapted to one self-contained skill.
// Its original manifest and auxiliary files stay inert inside the skill folder.
type PluginBundle struct {
	Name         string   `json:"name"`
	SkillCount   int      `json:"skill_count"`
	CommandCount int      `json:"command_count"`
	Entry        string   `json:"entry"`
	Notes        []string `json:"notes"`
}

var pluginPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

const maxPluginInstructions = 256 << 10

// PrepareRoot preserves legacy single-skill selection. A standard plugin that
// otherwise has multiple skill candidates becomes one skill with shared files.
func PrepareRoot(dest string) (string, *PluginBundle, error) {
	root, err := FindRoot(dest)
	if err == nil {
		md, readErr := os.ReadFile(filepath.Join(root, "SKILL.md"))
		if readErr == nil {
			if meta, parseErr := ParseFrontmatter(md); parseErr == nil && meta.PluginBundle {
				bundle, adaptErr := adaptPluginPackage(root, true)
				if adaptErr != nil {
					return "", nil, adaptErr
				}
				return root, bundle, nil
			}
		}
		return root, nil, nil
	}
	var multiple *MultipleSkillsError
	if !errors.As(err, &multiple) {
		return "", nil, err
	}
	pluginRoot, found, findErr := pluginPackageRoot(dest)
	if findErr != nil {
		return "", nil, findErr
	}
	if !found {
		return "", nil, err
	}
	bundle, err := adaptPluginPackage(pluginRoot, false)
	if err != nil {
		return "", nil, err
	}
	return pluginRoot, bundle, nil
}

func pluginPackageRoot(dest string) (string, bool, error) {
	bases := []string{dest}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return "", false, err
	}
	var meaningful []os.DirEntry
	for _, entry := range entries {
		if entry.Name() != ".DS_Store" && entry.Name() != "__MACOSX" {
			meaningful = append(meaningful, entry)
		}
	}
	if len(meaningful) == 1 && meaningful[0].IsDir() {
		bases = append(bases, filepath.Join(dest, meaningful[0].Name()))
	}
	for _, base := range bases {
		info, err := os.Lstat(filepath.Join(base, ".claude-plugin", "plugin.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if !info.Mode().IsRegular() {
			return "", false, errors.New("plugin manifest must be a regular file")
		}
		return base, true, nil
	}
	return "", false, nil
}

func standardComponentPath(raw json.RawMessage, dir string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	var one string
	if json.Unmarshal(raw, &one) == nil {
		values = []string{one}
	} else if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("plugin %s paths must be strings", dir)
	}
	for _, value := range values {
		if value != dir && value != "./"+dir {
			return fmt.Errorf("plugin %s path %q is unsupported; use the standard %s directory", dir, value, dir)
		}
	}
	return nil
}

type packageDocument struct {
	name, path, metadata, body string
}

func readPackageDocument(root, rel, name string) (packageDocument, error) {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return packageDocument{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPluginInstructions+1))
	if err != nil {
		return packageDocument{}, err
	}
	if len(data) > maxPluginInstructions {
		return packageDocument{}, errors.New("plugin instructions exceed the 256 KiB context budget")
	}
	if _, err := ParseFrontmatter(data); err != nil {
		return packageDocument{}, fmt.Errorf("%s: %w", rel, err)
	}
	doc := packageDocument{name: name, path: rel, body: strings.TrimSpace(string(data))}
	s := strings.TrimPrefix(string(data), "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasPrefix(s, "---\n") {
		rest := s[4:]
		for offset := 0; offset < len(rest); {
			line, _, newline := strings.Cut(rest[offset:], "\n")
			if strings.TrimRight(line, " \t") == "---" {
				doc.metadata = strings.TrimSpace(rest[:offset])
				doc.body = strings.TrimSpace(rest[offset+len(line):])
				break
			}
			if !newline {
				break
			}
			offset += len(line) + 1
		}
	}
	return doc, nil
}

func writePackageDocument(out *strings.Builder, kind string, doc packageDocument) {
	fmt.Fprintf(out, "\n## %s: %s\n\nOriginal document: `%s`\n\n", kind, doc.name, doc.path)
	if kind == "Helper skill" {
		base := "${CLAUDE_PLUGIN_ROOT}/" + filepath.ToSlash(filepath.Dir(doc.path))
		fmt.Fprintf(out, "Original helper base directory: `%s`. Resolve helper-local file paths against this directory; paths explicitly documented as plugin-root paths still use Package root.\n\n", base)
		doc.metadata = strings.ReplaceAll(doc.metadata, "${CLAUDE_SKILL_DIR}", base)
		doc.body = strings.ReplaceAll(doc.body, "${CLAUDE_SKILL_DIR}", base)
	}
	if doc.metadata != "" {
		fence := "```"
		for strings.Contains(doc.metadata, fence) {
			fence += "`"
		}
		fmt.Fprintf(out, "Original metadata (including parameters):\n\n%syaml\n%s\n%s\n\n", fence, doc.metadata, fence)
	}
	out.WriteString(doc.body)
	out.WriteByte('\n')
}

func adaptPluginPackage(root string, alreadyPrepared bool) (*PluginBundle, error) {
	var fileNames []string
	var total int64
	// Inspect the whole retained tree before reading manifests or instructions.
	if err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("plugin packages require regular files")
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if clean, err := CleanRel(rel); err != nil || clean != rel {
			return errors.New("unsafe plugin package path")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		fileNames = append(fileNames, rel)
		if info.Size() > MaxFileBytes || total > MaxTotalBytes || (len(fileNames) > MaxEntries || !alreadyPrepared && len(fileNames) == MaxEntries) {
			return errors.New("plugin package exceeds skill file limits")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, name := range fileNames {
		if strings.EqualFold(name, "SKILL.md") && !alreadyPrepared {
			return nil, errors.New("plugin package already contains a root skill document")
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil, err
	}
	var meta struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Skills      json.RawMessage `json:"skills"`
		Commands    json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(manifest, &meta); err != nil {
		return nil, fmt.Errorf("plugin manifest: %w", err)
	}
	if !pluginPackageName.MatchString(meta.Name) {
		return nil, errors.New("plugin name must be 1-64 lowercase letters, digits, _ or -")
	}
	if err := standardComponentPath(meta.Skills, "skills"); err != nil {
		return nil, err
	}
	if err := standardComponentPath(meta.Commands, "commands"); err != nil {
		return nil, err
	}
	sort.Strings(fileNames)
	var skills, commands []packageDocument
	seen := map[string]bool{}
	for _, rel := range fileNames {
		parts := strings.Split(rel, "/")
		kind, name := "", ""
		if len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" {
			kind, name = "skill", parts[1]
		} else if strings.HasPrefix(rel, "commands/") && strings.HasSuffix(strings.ToLower(rel), ".md") {
			kind = "command"
			name = strings.TrimSuffix(strings.TrimPrefix(rel, "commands/"), filepath.Ext(rel))
		}
		if kind == "" {
			continue
		}
		key := kind + ":" + strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate plugin %s name %q", kind, name)
		}
		seen[key] = true
		doc, err := readPackageDocument(root, rel, name)
		if err != nil {
			return nil, err
		}
		if kind == "skill" {
			skills = append(skills, doc)
		} else {
			commands = append(commands, doc)
		}
	}
	bundle := &PluginBundle{Name: meta.Name, SkillCount: len(skills), CommandCount: len(commands), Notes: []string{"components_inert"}}
	for _, command := range commands {
		if command.name == meta.Name || len(commands) == 1 {
			bundle.Entry = command.name
		}
	}
	if alreadyPrepared {
		return bundle, nil
	}
	if meta.Description == "" {
		meta.Description = fmt.Sprintf("%s plugin package with %d skills and %d commands", meta.Name, len(skills), len(commands))
	}
	nameJSON, _ := json.Marshal(meta.Name)
	descJSON, _ := json.Marshal(meta.Description)
	var out strings.Builder
	fmt.Fprintf(&out, "---\nname: %s\ndescription: %s\ntidebot-plugin-bundle: true\n---\n\n# %s\n\n", nameJSON, descJSON, meta.Name)
	out.WriteString("This skill adapts one Claude plugin package. The original commands, helper skills and shared files are preserved together.\n\nPackage root: ${CLAUDE_SKILL_DIR}\n\nResolve command file paths and paths explicitly described as belonging to the plugin root against Package root. Resolve helper-local paths against the original helper base directory stated in each helper section. Invoke scripts and read resources with absolute paths. Keep the user's working directory for workspace files, config.json and command output. Do not change the working directory to the package merely to find a script. In original documents, ${CLAUDE_PLUGIN_ROOT} means this package root, not the enclosing Tidebot runtime plugin. Each helper's skill-directory placeholder has been expanded to its own original helper directory in that section. Internal slash commands and helper skill names refer to the documents included below; they are not separately registered global commands. Apply these path rules to every included document.\n\n")
	if bundle.Entry != "" {
		fmt.Fprintf(&out, "Default entry command: `%s`. Follow that command unless the user explicitly requests another command or helper skill. Read its original parameter metadata below.\n", bundle.Entry)
	} else {
		out.WriteString("Select a command or helper skill from the user's request. If the intended entry is unclear, ask the user before executing.\n")
	}
	out.WriteString("\nHelper skill instructions apply when the chosen command calls that helper or when the user requests it directly. Original manifest hooks, MCP servers, agents, LSP servers and monitors are not activated by this adapter.\n")
	for _, command := range commands {
		writePackageDocument(&out, "Command", command)
	}
	for _, skill := range skills {
		writePackageDocument(&out, "Helper skill", skill)
	}
	data := []byte(out.String())
	if len(data) > maxPluginInstructions {
		return nil, errors.New("plugin instructions exceed the 256 KiB context budget")
	}
	if total+int64(len(data)) > MaxTotalBytes {
		return nil, errors.New("adapted plugin package exceeds skill file limits")
	}
	file, err := os.OpenFile(filepath.Join(root, "SKILL.md"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(filepath.Join(root, "SKILL.md"))
		return nil, errors.Join(writeErr, closeErr)
	}
	return bundle, nil
}
