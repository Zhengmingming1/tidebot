package skillpkg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pluginFixture() map[string]string {
	return map[string]string{
		".claude-plugin/plugin.json":                `{"name":"trunk-merge","description":"Merge trunk"}`,
		"commands/trunk-merge.md":                   "---\nname: trunk-merge\nargs:\n  - name: taskNo\n    required: true\n---\nMain entry calls branch, changes and stage.\n",
		"skills/query-task-branch/SKILL.md":         "---\nname: query-task-branch\ndescription: Query branch\n---\nOnly use scripts/skills/query-task-branch/query.py.\n",
		"skills/query-task-change-file/SKILL.md":    "---\nname: query-task-change-file\n---\nQuery change files.\n",
		"skills/query-task-stage/SKILL.md":          "---\nname: query-task-stage\n---\nQuery stage.\n",
		"scripts/skills/query-task-branch/query.py": "# script preserved, never executed",
		"scripts/trunk_merge.py":                    "# shared script preserved",
	}
}

func TestPrepareRootAdaptsStandardPlugin(t *testing.T) {
	for _, prefix := range []string{"", "trunk-merge/"} {
		t.Run(prefix, func(t *testing.T) {
			d := t.TempDir()
			original := pluginFixture()
			files := map[string]string{".DS_Store": "junk", "__MACOSX/._package": "junk"}
			for p, data := range original {
				files[prefix+p] = data
			}
			writeRootFixture(t, d, files)
			root, bundle, err := PrepareRoot(d)
			if err != nil {
				t.Fatal(err)
			}
			if root != filepath.Join(d, prefix) || bundle.Name != "trunk-merge" || bundle.SkillCount != 3 || bundle.CommandCount != 1 || bundle.Entry != "trunk-merge" {
				t.Fatalf("root=%q bundle=%+v", root, bundle)
			}
			for p, want := range original {
				got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
				if err != nil || string(got) != want {
					t.Fatalf("original %s changed or missing: %v", p, err)
				}
			}
			md, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			meta, err := ParseFrontmatter(md)
			if err != nil || meta.Name != "trunk-merge" || meta.Description != "Merge trunk" || !meta.PluginBundle {
				t.Fatalf("generated frontmatter %+v %v", meta, err)
			}
			for _, want := range []string{"Package root: ${CLAUDE_SKILL_DIR}", "${CLAUDE_PLUGIN_ROOT}", "tidebot-plugin-bundle: true", "Keep the user's working directory", "Default entry command: `trunk-merge`", "Original document: `commands/trunk-merge.md`", "required: true", "Only use scripts/skills/query-task-branch/query.py.", "Query change files.", "Query stage."} {
				if !strings.Contains(string(md), want) {
					t.Fatalf("missing instruction %q", want)
				}
			}
		})
	}
}

func TestPrepareRootPreservesSingleSkillAndAmbiguity(t *testing.T) {
	d := t.TempDir()
	writeRootFixture(t, d, map[string]string{"skills/single/SKILL.md": "# Original", ".claude-plugin/plugin.json": `{"name":"single"}`})
	root, bundle, err := PrepareRoot(d)
	if err != nil || bundle != nil || root != filepath.Join(d, "skills", "single") {
		t.Fatalf("single skill changed: %s %+v %v", root, bundle, err)
	}
	for _, manifest := range []string{"", "extra/inner/.claude-plugin/plugin.json"} {
		d := t.TempDir()
		files := map[string]string{"skills/one/SKILL.md": "# One", "skills/two/SKILL.md": "# Two"}
		if manifest != "" {
			files[manifest] = `{"name":"hidden"}`
		}
		writeRootFixture(t, d, files)
		root, bundle, err := PrepareRoot(d)
		var multiple *MultipleSkillsError
		if root != "" || bundle != nil || !errors.As(err, &multiple) {
			t.Fatalf("arbitrary multiple skills accepted: %q %+v %v", root, bundle, err)
		}
	}
}

func TestPrepareRootRejectsInvalidPlugin(t *testing.T) {
	for name, manifest := range map[string]string{
		"invalid JSON":          "{",
		"empty name":            `{}`,
		"unsafe name":           `{"name":"../trunk"}`,
		"custom skills":         `{"name":"trunk-merge","skills":"./custom"}`,
		"traversal commands":    `{"name":"trunk-merge","commands":"../commands"}`,
		"invalid commands type": `{"name":"trunk-merge","commands":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			files := pluginFixture()
			files[".claude-plugin/plugin.json"] = manifest
			writeRootFixture(t, d, files)
			if _, _, err := PrepareRoot(d); err == nil {
				t.Fatal("invalid plugin accepted")
			}
			if _, err := os.Stat(filepath.Join(d, "SKILL.md")); !os.IsNotExist(err) {
				t.Fatal("failed preparation left generated instructions")
			}
		})
	}
}

func TestPrepareRootDefaultCommandSelection(t *testing.T) {
	for name, want := range map[string]string{"trunk-merge": "trunk-merge", "other": ""} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			files := pluginFixture()
			files[".claude-plugin/plugin.json"] = `{"name":"` + name + `"}`
			files["commands/inspect.md"] = "# Inspect"
			writeRootFixture(t, d, files)
			_, bundle, err := PrepareRoot(d)
			if err != nil || bundle.Entry != want || bundle.CommandCount != 2 {
				t.Fatalf("command selection %+v %v", bundle, err)
			}
		})
	}
}

func TestPrepareRootRejectsConflictsAndOversizedInstructions(t *testing.T) {
	for _, name := range []string{"case collision", "large document", "large combined documents", "unclosed frontmatter"} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			files := pluginFixture()
			switch name {
			case "case collision":
				files["commands/Inspect.md"] = "one"
				files["commands/inspect.md"] = "two"
			case "large document":
				files["commands/trunk-merge.md"] = strings.Repeat("x", maxPluginInstructions+1)
			case "large combined documents":
				files["skills/query-task-branch/SKILL.md"] = strings.Repeat("x", maxPluginInstructions/2)
				files["skills/query-task-stage/SKILL.md"] = strings.Repeat("y", maxPluginInstructions/2)
			case "unclosed frontmatter":
				files["commands/trunk-merge.md"] = "---\nname: trunk\n"
			}
			writeRootFixture(t, d, files)
			if name == "case collision" {
				a, _ := os.ReadFile(filepath.Join(d, "commands", "Inspect.md"))
				b, _ := os.ReadFile(filepath.Join(d, "commands", "inspect.md"))
				if string(a) == string(b) {
					t.Skip("case-insensitive filesystem cannot create collision fixture")
				}
			}
			if _, _, err := PrepareRoot(d); err == nil {
				t.Fatal("invalid package accepted")
			}
			if _, err := os.Stat(filepath.Join(d, "SKILL.md")); !os.IsNotExist(err) {
				t.Fatal("failed adaptation left generated root")
			}
		})
	}
}

func TestPrepareRootRejectsLinksWithoutReadingThem(t *testing.T) {
	d := t.TempDir()
	writeRootFixture(t, d, pluginFixture())
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d, "scripts", "link")); err != nil {
		t.Skip(err)
	}
	if _, _, err := PrepareRoot(d); err == nil {
		t.Fatal("plugin tree symlink accepted")
	}
}

func TestPrepareRootPreservesExistingRoot(t *testing.T) {
	d := t.TempDir()
	files := pluginFixture()
	files["SKILL.md"] = "# Existing root instructions"
	writeRootFixture(t, d, files)
	root, bundle, err := PrepareRoot(d)
	if err != nil || root != d || bundle != nil {
		t.Fatalf("existing root changed: %q %+v %v", root, bundle, err)
	}
	got, _ := os.ReadFile(filepath.Join(d, "SKILL.md"))
	if string(got) != files["SKILL.md"] {
		t.Fatal("existing root overwritten")
	}
}

func TestPrepareRootRejectsAdaptedPackageSize(t *testing.T) {
	d := t.TempDir()
	files := pluginFixture()
	originalSize := 0
	for _, data := range files {
		originalSize += len(data)
	}
	remaining := MaxTotalBytes - originalSize - 10
	for n := 0; remaining > 0; n++ {
		size := remaining
		if size > MaxFileBytes {
			size = MaxFileBytes
		}
		files["resources/"+strings.Repeat("x", n+1)+".txt"] = strings.Repeat("x", size)
		remaining -= size
	}
	writeRootFixture(t, d, files)
	if _, _, err := PrepareRoot(d); err == nil || !strings.Contains(err.Error(), "adapted plugin package") {
		t.Fatalf("generated document escaped package size limit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("oversized adapted package left generated root")
	}
}

func TestPrepareRootPreservesHelperAndPluginPathScopes(t *testing.T) {
	d := t.TempDir()
	files := pluginFixture()
	files["skills/query-task-branch/SKILL.md"] = "---\nname: query-task-branch\n---\nRun ${CLAUDE_SKILL_DIR}/scripts/local.py and ${CLAUDE_PLUGIN_ROOT}/scripts/shared.py.\n"
	files["skills/query-task-stage/SKILL.md"] = "---\nname: query-task-stage\n---\nRun ${CLAUDE_SKILL_DIR}/scripts/local.py and ${CLAUDE_PLUGIN_ROOT}/scripts/shared.py.\n"
	files["skills/query-task-branch/scripts/local.py"] = "branch-local"
	files["skills/query-task-stage/scripts/local.py"] = "stage-local"
	files["scripts/shared.py"] = "package-shared"
	writeRootFixture(t, d, files)
	root, _, err := PrepareRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	for _, want := range []string{
		"Run ${CLAUDE_PLUGIN_ROOT}/skills/query-task-branch/scripts/local.py and ${CLAUDE_PLUGIN_ROOT}/scripts/shared.py.",
		"Run ${CLAUDE_PLUGIN_ROOT}/skills/query-task-stage/scripts/local.py and ${CLAUDE_PLUGIN_ROOT}/scripts/shared.py.",
		"Original helper base directory: `${CLAUDE_PLUGIN_ROOT}/skills/query-task-branch`",
		"Original helper base directory: `${CLAUDE_PLUGIN_ROOT}/skills/query-task-stage`",
		"Resolve helper-local paths against the original helper base directory",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("lost helper/package path scope %q", want)
		}
	}
	for _, p := range []string{"skills/query-task-branch/SKILL.md", "skills/query-task-stage/SKILL.md", "skills/query-task-branch/scripts/local.py", "skills/query-task-stage/scripts/local.py", "scripts/shared.py"} {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || string(got) != files[p] {
			t.Fatalf("original scoped file %s changed: %v", p, err)
		}
	}
}

func TestPrepareRootWithoutCommandUsesRequestedHelper(t *testing.T) {
	d := t.TempDir()
	files := pluginFixture()
	delete(files, "commands/trunk-merge.md")
	files[".claude-plugin/plugin.json"] = `{"name":"trunk-merge","skills":["./skills"],"hooks":{"PreToolUse":[]},"mcpServers":{"remote":{"url":"https://example.invalid"}}}`
	writeRootFixture(t, d, files)
	root, bundle, err := PrepareRoot(d)
	if err != nil || bundle.Entry != "" || bundle.CommandCount != 0 || len(bundle.Notes) != 1 || bundle.Notes[0] != "components_inert" {
		t.Fatalf("helper-only plugin %+v %v", bundle, err)
	}
	md, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if !strings.Contains(string(md), "If the intended entry is unclear, ask the user before executing.") {
		t.Fatal("helper-only plugin selected an arbitrary entry")
	}
	if !strings.Contains(string(md), "Original manifest hooks, MCP servers, agents, LSP servers and monitors are not activated") {
		t.Fatal("executable extensions silently activated")
	}
}

func TestPrepareRootRetainsBundleMetadataOnRoundTrip(t *testing.T) {
	d := t.TempDir()
	writeRootFixture(t, d, pluginFixture())
	root, originalBundle, err := PrepareRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	originalMD, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, roundTripBundle, err := PrepareRoot(d)
	if err != nil || gotRoot != root || roundTripBundle == nil {
		t.Fatalf("prepared bundle lost metadata: root=%s bundle=%+v err=%v", gotRoot, roundTripBundle, err)
	}
	if roundTripBundle.Name != originalBundle.Name || roundTripBundle.SkillCount != originalBundle.SkillCount || roundTripBundle.CommandCount != originalBundle.CommandCount || roundTripBundle.Entry != originalBundle.Entry || strings.Join(roundTripBundle.Notes, ",") != strings.Join(originalBundle.Notes, ",") {
		t.Fatalf("round trip metadata changed: %+v -> %+v", originalBundle, roundTripBundle)
	}
	gotMD, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil || string(gotMD) != string(originalMD) {
		t.Fatal("round trip regenerated original bundle instructions")
	}
}
