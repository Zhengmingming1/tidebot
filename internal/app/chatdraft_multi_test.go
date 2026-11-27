package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A session tunes two skills at once: each has its own draft folder, a turn
// loads (and may change) both, save / adopt / discard act on the named one,
// and without a name they refuse rather than guess.
func TestChatSkillDraftsSeveral(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	sandboxDeps(t, true)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	promptFile := filepath.Join(t.TempDir(), "prompt")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_PROMPT_FILE", promptFile)
	one := importSkill(t, a, "weekly-notes", "Summarize the week, version one.")
	two := importSkill(t, a, "daily-report", "Write the daily report, version one.")
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{one, two}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartChatDraft(ctx, ses.ID, one); err != nil {
		t.Fatal(err)
	}
	d, err := a.StartChatDraft(ctx, ses.ID, two)
	if err != nil || len(d.Drafts) != 2 || d.SkillID != two || d.Drafts[0].SkillID != one || d.Drafts[1].SkillID != two {
		t.Fatalf("second draft %v %+v", err, d)
	}
	dirOne, dirTwo := a.chatDraftDir(ses.ID, one), a.chatDraftDir(ses.ID, two)
	if dirOne == "" || dirOne == dirTwo || !strings.HasPrefix(dirOne, a.chatDraftsDir(ses.ID)) {
		t.Fatalf("draft folders %q %q", dirOne, dirTwo)
	}
	write := func(dir, name, body string) {
		t.Helper()
		md := "---\nname: " + name + "\ndescription: test skill\n---\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(dirOne, "weekly-notes", "Summarize the week, version two.")
	write(dirTwo, "daily-report", "Write the daily report, version two.")
	// starting a skill that has a draft keeps the edits
	if d, err = a.StartChatDraft(ctx, ses.ID, one); err != nil || len(d.Drafts) != 2 || d.SkillID != one || !d.Unsaved {
		t.Fatalf("start again %v %+v", err, d)
	}

	if _, err := a.SendChatTurn(ctx, ses.ID, "tighten both"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	args := readArgs(t, argsFile)
	if !contains(args, dirOne) || !contains(args, dirTwo) {
		t.Fatalf("both drafts not writable: %q", args)
	}
	joined := strings.Join(args, "\n")
	if strings.Count(joined, "Its draft is the folder") != 2 {
		t.Fatalf("draft notes: %s", joined)
	}
	prompt, _ := os.ReadFile(promptFile)
	for _, want := range []string{"week, version two", "daily report, version two"} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("turn didn't load %q: %s", want, prompt)
		}
	}

	if _, err := a.SaveChatDraft(ctx, ses.ID, "", ""); !errors.Is(err, webui.ErrChatDraftWhich) {
		t.Fatalf("save without a skill: %v", err)
	}
	if _, err := a.AdoptChatDraft(ctx, ses.ID, "", ""); !errors.Is(err, webui.ErrChatDraftWhich) {
		t.Fatalf("adopt without a skill: %v", err)
	}
	if _, err := a.SaveChatDraft(ctx, ses.ID, "skill_nope", ""); !errors.Is(err, webui.ErrChatDraftNone) {
		t.Fatalf("save of a skill without draft: %v", err)
	}
	d, err = a.SaveChatDraft(ctx, ses.ID, two, "")
	if err != nil || d.SkillID != two || d.Saved == nil || d.Saved.No != 2 || d.Drafts[0].Saved != nil {
		t.Fatalf("save two %v %+v", err, d)
	}
	if d, err = a.AdoptChatDraft(ctx, ses.ID, two, ""); err != nil || len(d.Adopted) != 1 || d.Adopted[0].SkillID != two {
		t.Fatalf("adopt two %v %+v", err, d)
	}
	if currentNo(t, a, two) != 2 || currentNo(t, a, one) != 1 {
		t.Fatal("adopt moved the wrong skill")
	}
	if _, err := a.AdoptChatDraft(ctx, ses.ID, one, ""); !errors.Is(err, webui.ErrChatDraftNotSaved) {
		t.Fatalf("adopt unsaved one: %v", err)
	}

	// discard one: the other draft and what was adopted stay
	if d, err = a.DiscardChatDraft(ctx, ses.ID, one); err != nil || len(d.Drafts) != 1 || d.SkillID != two || len(d.Adopted) != 1 {
		t.Fatalf("discard one %v %+v", err, d)
	}
	if _, err := os.Stat(dirOne); !os.IsNotExist(err) {
		t.Fatal("discarded draft folder left")
	}
	if _, err := os.Stat(filepath.Join(dirTwo, "SKILL.md")); err != nil {
		t.Fatal("the other draft went too")
	}
	// one left: no name needed
	if d, err = a.DiscardChatDraft(ctx, ses.ID, ""); err != nil || d.Active || len(d.Drafts) != 0 {
		t.Fatalf("discard last %v %+v", err, d)
	}
}

// A session whose draft.json is from before several drafts (one draft in the
// "draft" folder) keeps working: the draft shows, turns load it, it saves.
func TestChatSkillDraftLegacyState(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", filepath.Join(t.TempDir(), "args"))
	sk := importSkill(t, a, "weekly-notes", "Summarize the week, version one.")
	v1, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk)
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{sk}}); err != nil {
		t.Fatal(err)
	}
	files, err := a.Skills.Files(ctx, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	files["SKILL.md"] = []byte("---\nname: weekly-notes\ndescription: test skill\n---\nSummarize the week, legacy edit.\n")
	if err := writeDraftFiles(a.chatLegacyDraftDir(ses.ID), files); err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(map[string]any{"skill_id": sk, "base_version_id": v1.ID})
	if err := os.WriteFile(a.chatDraftStatePath(ses.ID), old, 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := a.ChatDraft(ctx, ses.ID)
	if err != nil || !d.Active || d.SkillID != sk || len(d.Drafts) != 1 || !d.Unsaved || a.chatDraftDir(ses.ID, sk) != a.chatLegacyDraftDir(ses.ID) {
		t.Fatalf("legacy draft %v %+v", err, d)
	}
	if st := a.chatDraftStage(chatRow{ChatSession: webui.ChatSession{ID: ses.ID}}); len(st) != 1 || !strings.Contains(string(st[0].Files["SKILL.md"]), "legacy edit") {
		t.Fatalf("legacy draft not staged %+v", st)
	}
	if d, err = a.SaveChatDraft(ctx, ses.ID, "", ""); err != nil || d.Saved == nil || d.Saved.No != 2 {
		t.Fatalf("legacy save %v %+v", err, d)
	}
	// stored in the new form, still pointing at the old folder
	b, _ := os.ReadFile(a.chatDraftStatePath(ses.ID))
	var st chatDraftState
	if json.Unmarshal(b, &st) != nil || st.SkillID != "" || len(st.Drafts) != 1 || st.Drafts[0].Dir != "draft" || st.Drafts[0].ProposalID == "" {
		t.Fatalf("stored %s", b)
	}
	// a second skill's draft goes to its own folder
	if newDraftDir(st, sk) != "drafts/"+safeChatID(sk) {
		t.Fatal(newDraftDir(st, sk))
	}
	st.Drafts = append(st.Drafts, chatDraftEntry{SkillID: "x", Dir: "drafts/a_b"})
	if got := newDraftDir(st, "a.b"); got != "drafts/a_b-2" {
		t.Fatalf("clashing folder %q", got)
	}
	// a folder outside the session in a hand-edited file is not followed
	if dir := a.chatDraftEntryDir(ses.ID, chatDraftEntry{SkillID: sk, Dir: "../elsewhere"}); !strings.HasPrefix(dir, a.chatDraftsDir(ses.ID)) {
		t.Fatalf("followed %q", dir)
	}
}
