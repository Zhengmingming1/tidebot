package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func skillMD(extra string) []byte {
	return []byte("---\nname: w\ndescription: d\n---\nstep one\nstep two\nstep three\nstep four\n" + extra)
}

// skills.promote refuses when current moved on, unless the caller names that
// newer current (the confirmation the skills page shows).
func TestSkillsPromoteRPCRefusesStaleCurrent(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), skillMD(""), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, err := a.Skills.ImportDir(ctx, "w", d, "user")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Optimize.Propose(ctx, sk.ID, map[string][]byte{"SKILL.md": skillMD("step five\n")}, "user", "logic", "clearer")
	if err != nil || p.Status != "pending" || p.BaseVersionID != cur.ID {
		t.Fatalf("%v %+v", err, p)
	}
	other, _, err := a.Skills.CommitFiles(ctx, sk.ID, "w", map[string][]byte{"SKILL.md": skillMD("step six\n")}, "edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"proposal_id": p.ID})
	if _, err = a.CallExt(ctx, "skills.promote", raw); !errors.Is(err, webui.ErrProposalStale) {
		t.Fatalf("want ErrProposalStale, got %v", err)
	}
	if now, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk.ID); now.ID != other.ID {
		t.Fatal("refused promote moved current")
	}
	if st, _ := a.Optimize.Proposals(ctx, sk.ID); st[0].Status != "pending" {
		t.Fatal("refused promote changed status")
	}
	raw, _ = json.Marshal(map[string]any{"proposal_id": p.ID, "expect_current": other.ID})
	out, err := a.CallExt(ctx, "skills.promote", raw)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := out.(map[string]bool)
	if !ok["ok"] {
		t.Fatalf("result %T %v", out, out)
	}
	if now, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk.ID); now.ID != p.VersionID {
		t.Fatal("not promoted")
	}
}
