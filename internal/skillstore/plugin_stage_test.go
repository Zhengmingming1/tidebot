package skillstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestPluginBundleStageBindsItsOwnRoot(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	md := "---\nname: bundle\ntidebot-plugin-bundle: true\n---\nPackage root: ${CLAUDE_SKILL_DIR}\nUse ${CLAUDE_PLUGIN_ROOT}/scripts/run.py without changing the workspace.\n"
	original := map[string]string{
		"SKILL.md":                       md,
		"skills/helper/SKILL.md":         "Use ${CLAUDE_PLUGIN_ROOT}/scripts/run.py and ${CLAUDE_SKILL_DIR}/scripts/local.py",
		"skills/helper/scripts/local.py": "print('helper fixture')\n",
		"scripts/run.py":                 "print('resource fixture')\n",
		".claude-plugin/plugin.json":     `{"name":"bundle"}`,
	}
	sk := importSkill(t, s, "bundle", original)
	root := t.TempDir()
	staged, err := s.Stage(ctx, root, []tasksvc.SkillBinding{{SkillID: sk.ID, Mode: "follow"}})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	st := staged[0]
	if st.Command() != "/tidebot:bundle" || st.Dir != filepath.Join(root, "skills", "bundle") {
		t.Fatalf("command or package root: %+v", st)
	}
	if !strings.Contains(string(st.SkillMD), "Package root: "+st.Dir) || !strings.Contains(string(st.SkillMD), st.Dir+"/scripts/run.py") || strings.Contains(string(st.SkillMD), "${CLAUDE_") {
		t.Fatalf("package root was not bound: %s", st.SkillMD)
	}
	for path, original := range original {
		got, err := os.ReadFile(filepath.Join(st.Dir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, ".md") {
			if strings.Contains(string(got), "${CLAUDE_") {
				t.Fatalf("unbound package path in %s", path)
			}
			if path == "skills/helper/SKILL.md" && !strings.Contains(string(got), filepath.Join(st.Dir, "skills", "helper")+"/scripts/local.py") {
				t.Fatalf("helper lost its original skill base: %s", got)
			}
		} else if string(got) != original {
			t.Fatalf("resource changed: %s", path)
		}
	}
	stored, err := s.Files(ctx, sk.CurrentVersion.ID)
	if err != nil || !bytes.Equal(stored["SKILL.md"], []byte(md)) || string(stored["skills/helper/SKILL.md"]) != original["skills/helper/SKILL.md"] {
		t.Fatalf("staging changed the stored version: %v", err)
	}
	meta, err := skillpkg.ParseFrontmatter(st.SkillMD)
	if err != nil || !meta.PluginBundle {
		t.Fatalf("bundle metadata lost: %+v %v", meta, err)
	}
}

func TestOrdinarySkillKeepsPluginPlaceholders(t *testing.T) {
	s := newSvc(t)
	sk := importSkill(t, s, "ordinary", map[string]string{"SKILL.md": "---\nname: ordinary\n---\nUse ${CLAUDE_PLUGIN_ROOT} and ${CLAUDE_SKILL_DIR}."})
	staged, err := s.Stage(context.Background(), t.TempDir(), []tasksvc.SkillBinding{{SkillID: sk.ID, Mode: "follow"}})
	if err != nil || len(staged) != 1 || !strings.Contains(string(staged[0].SkillMD), "${CLAUDE_PLUGIN_ROOT}") || !strings.Contains(string(staged[0].SkillMD), "${CLAUDE_SKILL_DIR}") {
		t.Fatalf("ordinary skill substitution changed: %+v %v", staged, err)
	}
}
