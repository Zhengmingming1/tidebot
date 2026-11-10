package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func bindTask(t *testing.T, a *App, name string, b tasksvc.SkillBinding) {
	t.Helper()
	id := mkTask(t, a, name)
	v, err := a.Tasks.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sp := *v.Spec
	sp.SkillBindings = []tasksvc.SkillBinding{b}
	if _, err := a.Tasks.UpdateSpec(context.Background(), id, sp, "user", "bind"); err != nil {
		t.Fatal(err)
	}
}

func currentNo(t *testing.T, a *App, skillID string) int {
	t.Helper()
	v, err := a.Ver.CurrentVersion(context.Background(), version.EntitySkill, skillID)
	if err != nil {
		t.Fatal(err)
	}
	return v.VersionNo
}

// The session skill loop: edit a draft, turns load it (and may change it in a
// command session), save it as a version (current unchanged), adopt it only on
// request, and "save as task" pins what was adopted.
func TestChatSkillDraftLoop(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	sandboxDeps(t, true)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	promptFile := filepath.Join(t.TempDir(), "prompt")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_PROMPT_FILE", promptFile)
	sk := importSkill(t, a, "weekly-notes", "Summarize the week, version one.")
	v1, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk)
	bindTask(t, a, "follows", tasksvc.SkillBinding{SkillID: sk, Mode: "follow", Role: "primary"})
	bindTask(t, a, "pinned", tasksvc.SkillBinding{SkillID: sk, Mode: "pinned", VersionID: v1.ID, Role: "primary"})

	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	other := importSkill(t, a, "other", "Other.")
	if _, err := a.StartChatDraft(ctx, ses.ID, other); !errors.Is(err, webui.ErrChatInvalid) {
		t.Fatalf("unbound skill: %v", err)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{sk}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveChatDraft(ctx, ses.ID, ""); !errors.Is(err, webui.ErrChatDraftNone) {
		t.Fatalf("save without draft: %v", err)
	}
	d, err := a.StartChatDraft(ctx, ses.ID, sk)
	if err != nil || !d.Active || d.BaseNo != 1 || d.Changed || d.Files != 2 || !d.Editable {
		t.Fatalf("start %v %+v", err, d)
	}
	dir := a.chatDraftDir(ses.ID)
	if !strings.HasPrefix(dir, a.Cfg.DataDir) {
		t.Fatalf("draft outside the data dir: %s", dir)
	}
	md := "---\nname: weekly-notes\ndescription: test skill\n---\nSummarize the week, version two.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, _ = a.ChatDraft(ctx, ses.ID); !d.Changed || !d.Unsaved {
		t.Fatalf("edited %+v", d)
	}

	// a turn loads the draft and may change it (command session: --add-dir)
	if _, err := a.SendChatTurn(ctx, ses.ID, "make it shorter"); err != nil {
		t.Fatal(err)
	}
	det := waitTurn(t, a, ses.ID, 1)
	args := readArgs(t, argsFile)
	if !contains(args, dir) || argAfter(args, "--add-dir") == "" {
		t.Fatalf("draft not writable in the turn: %q", args)
	}
	prompt, _ := os.ReadFile(promptFile)
	if !strings.Contains(string(prompt), "version two") || strings.Contains(string(prompt), "version one") {
		t.Fatalf("turn didn't load the draft: %s", prompt)
	}
	if !strings.Contains(strings.Join(args, "\n"), "Its draft is the folder") {
		t.Fatal("no draft note")
	}
	if rb, _ := a.Skills.RunBindings(ctx, det.TurnList[len(det.TurnList)-1].RunID); len(rb) != 0 {
		t.Fatalf("a draft recorded as a stored version: %+v", rb)
	}

	// save: a new version, current unchanged, nothing adopted
	d, err = a.SaveChatDraft(ctx, ses.ID, "shorter")
	if err != nil || d.Saved == nil || d.Saved.No != 2 || d.Saved.Status != "pending" || d.Unsaved || len(d.Adopted) != 0 {
		t.Fatalf("save %v %+v", err, d)
	}
	if currentNo(t, a, sk) != 1 {
		t.Fatal("saving moved current")
	}
	if d.Affects == nil || len(d.Affects.Follow) != 1 || d.Affects.Follow[0] != "follows" || d.Affects.Pinned != 1 {
		t.Fatalf("affects %+v", d.Affects)
	}
	pid := d.Saved.ProposalID
	if d2, err := a.SaveChatDraft(ctx, ses.ID, ""); err != nil || d2.Saved.ProposalID != pid {
		t.Fatalf("saving again made another version %v %+v", err, d2)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM skill_proposals WHERE skill_id=?`, sk).Scan(&n)
	if n != 1 {
		t.Fatalf("proposals %d", n)
	}

	// adopt only on request
	d, err = a.AdoptChatDraft(ctx, ses.ID)
	if err != nil || len(d.Adopted) != 1 || d.Adopted[0].No != 2 || d.Changed {
		t.Fatalf("adopt %v %+v", err, d)
	}
	if currentNo(t, a, sk) != 2 {
		t.Fatal("adopt didn't move current")
	}
	if _, err := a.SaveChatDraft(ctx, ses.ID, ""); !errors.Is(err, webui.ErrChatDraftUnchanged) {
		t.Fatalf("unchanged save: %v", err)
	}

	// save as task: the adopted version, pinned; the last message; the level
	p, err := a.ChatTaskPreset(ctx, ses.ID, false)
	if err != nil || p.Prompt != "make it shorter" || p.RunMode != "exec" || len(p.SkillBindings) != 1 ||
		p.SkillBindings[0].Mode != "pinned" || p.SkillBindings[0].VersionID != d.Adopted[0].VersionID || p.SkillBindings[0].Role != "primary" {
		t.Fatalf("preset %v %+v", err, p)
	}

	// read-only: the draft is loaded but not writable
	if _, err := a.SetChatMode(ctx, ses.ID, "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "again"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 2)
	args = readArgs(t, argsFile)
	if contains(args, dir) || !strings.Contains(strings.Join(args, "\n"), "cannot change the draft") {
		t.Fatalf("read turn %q", args)
	}

	// discard keeps what was adopted
	if d, err = a.DiscardChatDraft(ctx, ses.ID); err != nil || d.Active || len(d.Adopted) != 1 {
		t.Fatalf("discard %v %+v", err, d)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("draft folder left")
	}
	if err := a.DeleteChatSession(ctx, ses.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.chatDraftStatePath(ses.ID)); !os.IsNotExist(err) {
		t.Fatal("draft state left after delete")
	}
}

// A session from a run pins the run's skill version for "save as task" (and
// the run's request when saved from the run page); a draft with a link
// instead of a file is refused.
func TestChatTaskPresetFromRun(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", filepath.Join(t.TempDir(), "args"))
	sk := importSkill(t, a, "report", "Write the report.")
	v1, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, sk)
	ws := t.TempDir()
	rid, err := a.startRun(webui.RunRequest{Prompt: "write the report", Workspace: ws, Mode: "edit", SkillBindings: []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow", Role: "primary"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, rid)
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	importSkill(t, a, "report", "Write the report, newer.") // a newer current version
	p, err := a.ChatTaskPreset(ctx, ses.ID, true)
	if err != nil || p.Prompt != "write the report" || p.RunMode != "edit" || len(p.SkillBindings) != 1 || p.SkillBindings[0].VersionID != v1.ID {
		t.Fatalf("preset %v %+v", err, p)
	}
	if _, err := a.StartChatDraft(ctx, ses.ID, sk); err != nil {
		t.Fatal(err)
	}
	dir := a.chatDraftDir(ses.ID)
	if err := os.Symlink(filepath.Join(ws), filepath.Join(dir, "link")); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := a.SaveChatDraft(ctx, ses.ID, ""); !errors.Is(err, webui.ErrChatInvalid) {
		t.Fatalf("link accepted: %v", err)
	}
	_ = runner.Options{}
}
