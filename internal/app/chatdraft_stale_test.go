package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// 采用 replaces only the version the draft started from, or the current version
// the page showed (and the confirmation named). A skill changed elsewhere since
// (adopted in another session, edited on the skill page) is not silently
// replaced: before, adopting a draft from v1 over v3 dropped v3's changes.
func TestChatDraftAdoptRefusesNewerCurrent(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	sk := importSkill(t, a, "weekly-notes", "Summarize the week, version one.")
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{sk}}); err != nil {
		t.Fatal(err)
	}
	d, err := a.StartChatDraft(ctx, ses.ID, sk)
	if err != nil || d.BaseNo != 1 || d.CurrentNo != 1 || d.CurrentVersionID != d.BaseVersionID {
		t.Fatalf("start %v %+v", err, d)
	}
	md := "---\nname: weekly-notes\ndescription: test skill\n---\nSummarize the week, from the session.\n"
	if err := os.WriteFile(filepath.Join(a.chatDraftDir(ses.ID, sk), "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err = a.SaveChatDraft(ctx, ses.ID, "", "")
	if err != nil || d.Saved == nil || d.Saved.No != 2 {
		t.Fatalf("save %v %+v", err, d)
	}

	// the skill changes elsewhere meanwhile: v3
	importSkill(t, a, "weekly-notes", "Summarize the week, edited on the skill page.")
	v3, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk)
	if v3.VersionNo != 3 {
		t.Fatalf("v%d", v3.VersionNo)
	}
	if _, err := a.AdoptChatDraft(ctx, ses.ID, "", ""); !errors.Is(err, webui.ErrChatDraftStale) {
		t.Fatalf("adopt over a newer current: %v", err)
	}
	if currentNo(t, a, sk) != 3 {
		t.Fatal("a refused adopt moved current")
	}
	d, _ = a.ChatDraft(ctx, ses.ID)
	if d.CurrentVersionID != v3.ID || d.CurrentNo != 3 || d.Saved == nil || d.Saved.Status != "pending" || len(d.Adopted) != 0 {
		t.Fatalf("after refusal %+v", d)
	}

	// shown v3, but v4 lands before the click: still refused
	importSkill(t, a, "weekly-notes", "Summarize the week, edited once more.")
	if _, err := a.AdoptChatDraft(ctx, ses.ID, "", v3.ID); !errors.Is(err, webui.ErrChatDraftStale) {
		t.Fatalf("adopt over the version after the one shown: %v", err)
	}
	// the owner saw v4 and agreed to replace it
	v4, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk)
	d, err = a.AdoptChatDraft(ctx, ses.ID, "", v4.ID)
	if err != nil || len(d.Adopted) != 1 || d.Adopted[0].No != 2 {
		t.Fatalf("adopt %v %+v", err, d)
	}
	if currentNo(t, a, sk) != 2 {
		t.Fatal("not adopted")
	}
}
