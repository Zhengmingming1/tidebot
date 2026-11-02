package skillstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"gopkg.in/yaml.v3"
)

func TestStageNormalizesOnlyCopiedName(t *testing.T) {
	for _, name := range []string{"Code Review!", "中文技能"} {
		t.Run(name, func(t *testing.T) {
			s := newSvc(t)
			body := "# Preserve body\r\nUse $ARGUMENTS.\r\n!`echo unchanged`\r\n"
			md := "---\r\nname: " + name + "\r\ndescription: |\r\n  First line\r\n  Second line\r\nallowed-tools: [Read, 'Bash(git status *)']\r\ndisable-model-invocation: true\r\ncustom: {level: 3}\r\n---\r\n" + body
			sk := importSkill(t, s, name, map[string]string{"SKILL.md": md, "refs/readme.txt": "reference"})
			st, err := s.Stage(context.Background(), t.TempDir(), []tasksvc.SkillBinding{{SkillID: sk.ID}})
			if err != nil {
				t.Fatal(err)
			}
			staged, err := os.ReadFile(filepath.Join(st[0].Dir, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			if st[0].Name == "" || CommandName(st[0].Name) != st[0].Name || !bytes.HasSuffix(staged, []byte(body)) {
				t.Fatalf("name/body changed incorrectly: %q %q", st[0].Name, staged)
			}
			header := strings.SplitN(strings.ReplaceAll(string(staged), "\r\n", "\n"), "\n---\n", 2)[0][4:]
			var fields map[string]any
			if err = yaml.Unmarshal([]byte(header), &fields); err != nil {
				t.Fatal(err)
			}
			if fields["name"] != st[0].Name || fields["description"] != "First line\nSecond line\n" || fields["disable-model-invocation"] != true || len(fields["allowed-tools"].([]any)) != 2 || fields["custom"].(map[string]any)["level"] != 3 {
				t.Fatalf("other frontmatter changed: %#v", fields)
			}
			original, err := s.Files(context.Background(), sk.CurrentVersion.ID)
			if err != nil || !bytes.Equal(original["SKILL.md"], []byte(md)) {
				t.Fatal("stored original was modified")
			}
		})
	}
}

func TestStagedNameWithoutFrontmatterAndInvalidYAML(t *testing.T) {
	body := []byte("# A skill\nRead ./references/info.md.\n")
	got, err := stagedSkillMD(body, "my-skill")
	if err != nil || !bytes.HasSuffix(got, body) || !bytes.Contains(got, []byte(`name: "my-skill"`)) {
		t.Fatalf("no frontmatter: %q %v", got, err)
	}
	for _, bad := range []string{"---\nname: x\nmissing close", "---\n[one, two]\n---\nbody", "---\nname: [broken\n---\nbody", "---\nname: one\nname: two\n---\nbody", "---\nname: x\n---not-a-delimiter\nbody", "---\nname: &original Code Review\ndescription: *original\n---\nbody"} {
		if _, err = stagedSkillMD([]byte(bad), "safe"); err == nil {
			t.Errorf("invalid frontmatter accepted: %q", bad)
		}
	}
}
