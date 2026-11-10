package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A session's skill draft (P3, see webui/chat_drafts.go). The draft is a copy
// of one skill version's files in the session's own folder
// (<data>/chat-sessions/<id>/draft), never in a skill's stored version or any
// folder Claude Code reads on its own. While it exists every turn stages it in
// place of the version it started from; in an edit or command session the
// draft folder is an --add-dir, so the model can change it. Saving stores the
// files as a new version (a "session" proposal: current is not moved);
// adopting moves current to it and only ever happens on the owner's click.
// The state (which skill, the version it started from, what was saved and
// adopted) is a small JSON file next to the draft; deleting the session
// removes both.

type chatDraftState struct {
	SkillID        string `json:"skill_id,omitempty"`
	BaseVersionID  string `json:"base_version_id,omitempty"`
	ProposalID     string `json:"proposal_id,omitempty"`
	SavedVersionID string `json:"saved_version_id,omitempty"`
	SavedHash      string `json:"saved_hash,omitempty"`
	// Adopted: skill id → the version adopted in this session.
	Adopted map[string]string `json:"adopted,omitempty"`
}

// chatDraftMu serializes draft changes (start, save, adopt, discard).
var chatDraftMu sync.Mutex

func (a *App) chatDraftDir(sid string) string {
	return filepath.Join(a.chatSessionsDir(), safeChatID(sid), "draft")
}

func (a *App) chatDraftStatePath(sid string) string {
	return filepath.Join(a.chatSessionsDir(), safeChatID(sid), "draft.json")
}

func (a *App) loadChatDraft(sid string) chatDraftState {
	var st chatDraftState
	if b, err := os.ReadFile(a.chatDraftStatePath(sid)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (a *App) storeChatDraft(sid string, st chatDraftState) error {
	p := a.chatDraftStatePath(sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// chatDraftFiles reads the draft folder (the same limits as a skill import).
func (a *App) chatDraftFiles(sid string) (map[string][]byte, error) {
	return skillstore.ReadTree(a.chatDraftDir(sid))
}

// skillFilesHash fingerprints a skill's files (paths and contents).
func skillFilesHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(h, "%s\x00%x\n", p, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeChatDraft replaces the draft folder with files (paths checked like a
// stage's; written through an os.Root so nothing lands outside it).
func (a *App) writeChatDraft(sid string, files map[string][]byte) error {
	dir := a.chatDraftDir(sid)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for p, data := range files {
		clean, err := skillpkg.CleanRel(p)
		if err != nil || clean != p || !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("unsafe skill file path %q", p)
		}
		rel := filepath.FromSlash(p)
		if err := root.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
			return err
		}
		if err := root.WriteFile(rel, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) versionNo(ctx context.Context, vid string) int {
	if vid == "" || a.Ver == nil {
		return 0
	}
	if v, err := a.Ver.GetVersion(ctx, vid); err == nil {
		return v.VersionNo
	}
	return 0
}

// draftSessionIdle: the session exists and isn't answering a turn (a turn may
// be changing the draft).
func (a *App) draftSessionIdle(ctx context.Context, sid string) (chatRow, error) {
	c, err := a.chatSession(ctx, sid)
	if err != nil {
		return c, err
	}
	if c.Status == "running" {
		return c, webui.ErrChatSessionBusy
	}
	return c, nil
}

// ChatDraft is the session's draft as the page shows it.
func (a *App) ChatDraft(ctx context.Context, sid string) (webui.ChatDraft, error) {
	c, err := a.chatSession(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, a.loadChatDraft(sid)), nil
}

func (a *App) chatDraftView(ctx context.Context, c chatRow, st chatDraftState) webui.ChatDraft {
	v := webui.ChatDraft{Editable: chatMode(c.PermissionMode) != "read", Adopted: []webui.ChatDraftVersion{}, Skills: []webui.ChatBinding{}}
	for _, b := range a.chatSessionSkills(ctx, c) {
		v.Skills = append(v.Skills, webui.ChatBinding{Kind: "skill", ID: b.SkillID, Name: a.skillName(b.SkillID)})
	}
	ids := make([]string, 0, len(st.Adopted))
	for id := range st.Adopted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v.Adopted = append(v.Adopted, webui.ChatDraftVersion{SkillID: id, SkillName: a.skillName(id), VersionID: st.Adopted[id], No: a.versionNo(ctx, st.Adopted[id]), Status: "promoted"})
	}
	if st.SkillID == "" {
		return v
	}
	v.Active, v.SkillID, v.SkillName = true, st.SkillID, a.skillName(st.SkillID)
	v.BaseVersionID, v.BaseNo = st.BaseVersionID, a.versionNo(ctx, st.BaseVersionID)
	if cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, st.SkillID); err == nil {
		v.CurrentNo = cur.VersionNo
	}
	files, err := a.chatDraftFiles(c.ID)
	if err == nil {
		v.Files = len(files)
		hash := skillFilesHash(files)
		if base, e := a.Skills.Files(ctx, st.BaseVersionID); e == nil {
			v.Changed = hash != skillFilesHash(base)
		}
		v.Unsaved = v.Changed && hash != st.SavedHash
	}
	if st.ProposalID != "" {
		sv := webui.ChatDraftVersion{SkillID: st.SkillID, SkillName: v.SkillName, VersionID: st.SavedVersionID, No: a.versionNo(ctx, st.SavedVersionID), ProposalID: st.ProposalID}
		_ = a.DB.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id=?`, st.ProposalID).Scan(&sv.Status)
		v.Saved = &sv
		if sv.Status == "pending" || sv.Status == "canary" {
			v.Affects = a.skillTaskUse(ctx, st.SkillID)
		}
	}
	return v
}

// skillTaskUse: the tasks adopting a new version of skillID would change
// (they follow the current version) and how many keep a pinned one.
func (a *App) skillTaskUse(ctx context.Context, skillID string) *webui.ChatDraftAffects {
	out := &webui.ChatDraftAffects{Follow: []string{}}
	if a.Tasks == nil {
		return out
	}
	list, err := a.Tasks.List(ctx)
	if err != nil {
		return out
	}
	for _, t := range list {
		if t.Task.Status == "archived" || t.Spec == nil {
			continue
		}
		for _, b := range t.Spec.SkillBindings {
			if b.SkillID != skillID {
				continue
			}
			if b.Mode == "pinned" {
				out.Pinned++
			} else {
				out.Follow = append(out.Follow, t.Task.Name)
			}
		}
	}
	sort.Strings(out.Follow)
	return out
}

// StartChatDraft copies the version the session's turns use of skillID (the
// run's pinned version, or the current one) into the draft folder. A draft
// of another skill is replaced; what was adopted is kept.
func (a *App) StartChatDraft(ctx context.Context, sid, skillID string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	base := ""
	found := false
	for _, b := range a.chatSessionSkills(ctx, c) {
		if b.SkillID == skillID {
			found, base = true, b.VersionID
		}
	}
	if !found {
		return webui.ChatDraft{}, fmt.Errorf("%w: bind the skill to the session first", webui.ErrChatInvalid)
	}
	if base == "" {
		cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
		if err != nil {
			return webui.ChatDraft{}, fmt.Errorf("%w: the skill has no version", webui.ErrChatInvalid)
		}
		base = cur.ID
	}
	files, err := a.Skills.Files(ctx, base)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	if len(files["SKILL.md"]) == 0 {
		return webui.ChatDraft{}, fmt.Errorf("%w: the skill has no SKILL.md", webui.ErrChatInvalid)
	}
	if err := a.writeChatDraft(sid, files); err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	st = chatDraftState{SkillID: skillID, BaseVersionID: base, Adopted: st.Adopted}
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st), nil
}

// DiscardChatDraft removes the draft (saved versions stay with the skill).
func (a *App) DiscardChatDraft(ctx context.Context, sid string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	if err := os.RemoveAll(a.chatDraftDir(sid)); err != nil {
		return webui.ChatDraft{}, err
	}
	st := chatDraftState{Adopted: a.loadChatDraft(sid).Adopted}
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st), nil
}

// SaveChatDraft stores the draft as a new version of the skill, waiting to be
// adopted (current is not moved; no notice is sent: the owner is on the page).
func (a *App) SaveChatDraft(ctx context.Context, sid, note string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	if st.SkillID == "" {
		return webui.ChatDraft{}, webui.ErrChatDraftNone
	}
	files, err := a.chatDraftFiles(sid)
	if err != nil {
		return webui.ChatDraft{}, fmt.Errorf("%w: %v", webui.ErrChatInvalid, err)
	}
	if len(files["SKILL.md"]) == 0 {
		return webui.ChatDraft{}, fmt.Errorf("%w: the draft has no SKILL.md", webui.ErrChatInvalid)
	}
	hash := skillFilesHash(files)
	if st.ProposalID != "" && st.SavedHash == hash {
		var status string
		if a.DB.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id=?`, st.ProposalID).Scan(&status) == nil && status == "pending" {
			return a.chatDraftView(ctx, c, st), nil // saved already
		}
	}
	if note == "" {
		note = "saved from session: " + c.Title
	}
	p, err := a.Optimize.Propose(ctx, st.SkillID, files, "session", "session", note)
	if err != nil {
		if strings.Contains(err.Error(), "identical to the current version") {
			return webui.ChatDraft{}, webui.ErrChatDraftUnchanged
		}
		return webui.ChatDraft{}, err
	}
	if p.Status == "rejected" {
		var failed []string
		for _, ch := range p.Checks {
			if !ch.OK {
				failed = append(failed, ch.Name)
			}
		}
		return webui.ChatDraft{}, fmt.Errorf("%w: %s", webui.ErrChatDraftChecks, strings.Join(failed, ", "))
	}
	st.ProposalID, st.SavedVersionID, st.SavedHash = p.ID, p.VersionID, hash
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st), nil
}

// AdoptChatDraft makes the saved version the skill's current one. Only on the
// owner's click: nothing in a turn or a save ever adopts.
func (a *App) AdoptChatDraft(ctx context.Context, sid string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	if st.SkillID == "" {
		return webui.ChatDraft{}, webui.ErrChatDraftNone
	}
	if st.ProposalID == "" {
		return webui.ChatDraft{}, webui.ErrChatDraftNotSaved
	}
	if err := a.Optimize.Promote(ctx, st.ProposalID); err != nil {
		return webui.ChatDraft{}, fmt.Errorf("%w: %v", webui.ErrChatInvalid, err)
	}
	if st.Adopted == nil {
		st.Adopted = map[string]string{}
	}
	st.Adopted[st.SkillID] = st.SavedVersionID
	st.BaseVersionID = st.SavedVersionID // the draft now starts from what is current
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	a.Log.Info("session adopted skill version", "session", sid, "skill", st.SkillID, "version", st.SavedVersionID)
	return a.chatDraftView(ctx, c, st), nil
}

// ChatTaskPreset is "save as task" from a session (or, fromRun, from the run
// it continues): the request (the last message, or the run's), the folder,
// the permission level, the provider, and every skill pinned: to the version
// adopted in the session, else the one the turns use now.
func (a *App) ChatTaskPreset(ctx context.Context, sid string, fromRun bool) (webui.ChatTaskPreset, error) {
	c, err := a.chatSession(ctx, sid)
	if err != nil {
		return webui.ChatTaskPreset{}, err
	}
	p := webui.ChatTaskPreset{Workspace: c.WorkspaceDir, RunMode: chatMode(c.PermissionMode), Provider: c.Provider, SkillBindings: []tasksvc.SkillBinding{}}
	if !fromRun {
		_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(r.prompt,'') FROM chat_session_turns t JOIN runs r ON r.id=t.run_id
WHERE t.session_id=? AND t.turn_index>0 ORDER BY t.turn_index DESC LIMIT 1`, sid).Scan(&p.Prompt)
	}
	if p.Prompt == "" && c.OriginRunID != "" {
		_ = a.DB.QueryRowContext(ctx, `SELECT prompt FROM runs WHERE id=?`, c.OriginRunID).Scan(&p.Prompt)
	}
	for i, b := range a.chatSessionSkills(ctx, c) {
		vid := b.VersionID
		if b.Mode != "pinned" || vid == "" {
			cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, b.SkillID)
			if err != nil {
				continue
			}
			vid = cur.ID
		}
		role := "auxiliary"
		if i == 0 {
			role = "primary"
		}
		p.SkillBindings = append(p.SkillBindings, tasksvc.SkillBinding{SkillID: b.SkillID, SkillName: a.skillName(b.SkillID), Mode: "pinned", VersionID: vid, Role: role})
	}
	if len(p.SkillBindings) > 0 && p.SkillBindings[0].Role != "primary" {
		p.SkillBindings[0].Role = "primary"
	}
	return p, nil
}

// chatSessionSkills are the skills a turn loads (chatTurnSkills), with a
// version adopted in this session in place of the one the run used: after
// "adopt" the session goes on with what it adopted.
func (a *App) chatSessionSkills(ctx context.Context, c chatRow) []tasksvc.SkillBinding {
	all := a.chatTurnSkills(ctx, c)
	adopted := a.loadChatDraft(c.ID).Adopted
	for i := range all {
		if v := adopted[all[i].SkillID]; v != "" && all[i].Mode == "pinned" {
			all[i].VersionID = v
		}
	}
	return all
}

// chatDraftStage: the draft's files and the binding that loads them, or nil.
func (a *App) chatDraftStage(c chatRow) (chatDraftState, map[string][]byte) {
	st := a.loadChatDraft(c.ID)
	if st.SkillID == "" {
		return st, nil
	}
	files, err := a.chatDraftFiles(c.ID)
	if err != nil || len(files["SKILL.md"]) == 0 {
		if err == nil {
			err = errors.New("no SKILL.md")
		}
		a.Log.Warn("session turn: skill draft not loaded", "session", c.ID, "error", err)
		return st, nil
	}
	return st, files
}

const chatDraftEditNote = "\nThe owner is tuning the skill %q in this session. Its draft is the folder %s: when asked to change the skill, edit the files there (SKILL.md and its other files) and nothing else of the skill. This turn loaded the draft as it was when the turn started; changes apply from the next turn. Saving the draft as a version and adopting it are the owner's buttons, not yours."

const chatDraftReadNote = "\nThe owner is tuning the skill %q in this session, from a draft. This session is read-only, so you cannot change the draft: describe the change to make, and the owner can switch the session to 「可修改」 or 「可执行命令」 to let you edit it."
