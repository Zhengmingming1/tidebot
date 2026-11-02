package skillstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func TestContextMetadataFreezesOwnershipAndLoadsOnlyInstructions(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := New(d, version.New(d), nil, "")
	ctx := context.Background()
	v, _, err := s.CommitFiles(ctx, "skill-a", "A", map[string][]byte{"SKILL.md": []byte("# Required\nKeep this rule."), "references/large.txt": make([]byte, 1<<20)}, "test", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	bs := []tasksvc.SkillBinding{{SkillID: "skill-a", Mode: "pinned", VersionID: v.ID}}
	ms, err := s.ContextMaterials(ctx, bs, "execution")
	if err != nil || len(ms) != 1 || ms[0].Body != "" || ms[0].Load == nil || ms[0].Hash == "" {
		t.Fatal(ms, err)
	}
	p, err := (contextplan.Deterministic{}).Plan(ctx, contextplan.Request{Scope: "execution", Materials: ms})
	if err != nil || len(p.Materials) != 1 || p.Materials[0].Body != "# Required\nKeep this rule." {
		t.Fatal(p, err)
	}
	bs[0].SkillID = "foreign"
	if _, err = s.ContextMaterials(ctx, bs, "execution"); err == nil {
		t.Fatal("foreign version accepted")
	}
}
