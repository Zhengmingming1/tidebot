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

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A session's skill drafts (P3, see webui/chat_drafts.go). A draft is a copy
// of one skill version's files in the session's own folder
// (<data>/chat-sessions/<id>/drafts/<skill>), never in a skill's stored
// version or any folder Claude Code reads on its own. A session may tune
// several of its skills at once, one draft per skill. While a draft exists
// every turn stages it in place of the version it started from; in an edit or
// command session each draft folder is an --add-dir, so the model can change
// it. Saving stores the files as a new version (a "session" proposal: current
// is not moved); adopting moves current to it and only ever happens on the
// owner's click. The state (which skills, the versions they started from,
// what was saved and adopted) is a small JSON file next to the drafts;
// deleting the session removes both.

// chatDraftEntry is one skill's draft.
type chatDraftEntry struct {
	SkillID        string `json:"skill_id"`
	BaseVersionID  string `json:"base_version_id,omitempty"`
	ProposalID     string `json:"proposal_id,omitempty"`
	SavedVersionID string `json:"saved_version_id,omitempty"`
	SavedHash      string `json:"saved_hash,omitempty"`
	// Dir: the draft folder, relative to the session folder ("draft" for a
	// draft started before a session could hold several).
	Dir string `json:"dir"`
}

type chatDraftState struct {
	// The single draft of an older draft.json (read only; see loadChatDraft).
	SkillID        string `json:"skill_id,omitempty"`
	BaseVersionID  string `json:"base_version_id,omitempty"`
	ProposalID     string `json:"proposal_id,omitempty"`
	SavedVersionID string `json:"saved_version_id,omitempty"`
	SavedHash      string `json:"saved_hash,omitempty"`
	// Drafts in the order they were started, at most one per skill.
	Drafts []chatDraftEntry `json:"drafts,omitempty"`
	// Adopted: skill id → the version adopted in this session.
	Adopted map[string]string `json:"adopted,omitempty"`
}

// draft returns the index of skillID's draft, or -1.
func (st *chatDraftState) draft(skillID string) int {
	for i := range st.Drafts {
		if st.Drafts[i].SkillID == skillID {
			return i
		}
	}
	return -1
}

// pick is the draft a save / adopt / discard is about: skillID's, or with
// skillID "" the only one.
func (st *chatDraftState) pick(skillID string) (int, error) {
	if skillID == "" {
		switch len(st.Drafts) {
		case 0:
			return -1, webui.ErrChatDraftNone
		case 1:
			return 0, nil
		default:
			return -1, webui.ErrChatDraftWhich
		}
	}
	if i := st.draft(skillID); i >= 0 {
		return i, nil
	}
	return -1, webui.ErrChatDraftNone
}

// chatDraftMu serializes draft changes (start, save, adopt, discard).
var chatDraftMu sync.Mutex

func (a *App) chatSessionDir(sid string) string {
	return filepath.Join(a.chatSessionsDir(), safeChatID(sid))
}

// chatDraftsDir holds a session's drafts (one folder per skill).
func (a *App) chatDraftsDir(sid string) string {
	return filepath.Join(a.chatSessionDir(sid), "drafts")
}

// chatLegacyDraftDir is where the one draft of an older session lives.
func (a *App) chatLegacyDraftDir(sid string) string {
	return filepath.Join(a.chatSessionDir(sid), "draft")
}

// chatDraftDir is the folder of skillID's draft ("" when it has none).
func (a *App) chatDraftDir(sid, skillID string) string {
	st := a.loadChatDraft(sid)
	if i := st.draft(skillID); i >= 0 {
		return a.chatDraftEntryDir(sid, st.Drafts[i])
	}
	return ""
}

func (a *App) chatDraftEntryDir(sid string, e chatDraftEntry) string {
	rel := filepath.FromSlash(e.Dir)
	if rel == "" || !filepath.IsLocal(rel) {
		rel = filepath.Join("drafts", safeChatID(e.SkillID))
	}
	return filepath.Join(a.chatSessionDir(sid), rel)
}

// newDraftDir picks a free folder name for skillID's draft (skill ids that
// differ only in characters safeChatID replaces get a suffix).
func newDraftDir(st chatDraftState, skillID string) string {
	used := map[string]bool{}
	for _, e := range st.Drafts {
		used[e.Dir] = true
	}
	base := "drafts/" + safeChatID(skillID)
	dir := base
	for n := 2; used[dir]; n++ {
		dir = fmt.Sprintf("%s-%d", base, n)
	}
	return dir
}

func (a *App) chatDraftStatePath(sid string) string {
	return filepath.Join(a.chatSessionDir(sid), "draft.json")
}

// loadChatDraft reads draft.json; an older file's single draft (in the
// "draft" folder) comes back as the first of Drafts.
func (a *App) loadChatDraft(sid string) chatDraftState {
	var st chatDraftState
	if b, err := os.ReadFile(a.chatDraftStatePath(sid)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.SkillID != "" {
		if st.draft(st.SkillID) < 0 {
			st.Drafts = append([]chatDraftEntry{{SkillID: st.SkillID, BaseVersionID: st.BaseVersionID, ProposalID: st.ProposalID,
				SavedVersionID: st.SavedVersionID, SavedHash: st.SavedHash, Dir: "draft"}}, st.Drafts...)
		}
		st.SkillID, st.BaseVersionID, st.ProposalID, st.SavedVersionID, st.SavedHash = "", "", "", "", ""
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

// readDraftFiles reads a draft folder (the same limits as a skill import).
func readDraftFiles(dir string) (map[string][]byte, error) {
	return skillstore.ReadTree(dir)
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

// writeDraftFiles replaces a draft folder with files (paths checked like a
// stage's; written through an os.Root so nothing lands outside it).
func writeDraftFiles(dir string, files map[string][]byte) error {
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

// ChatDraft is the session's drafts as the page shows them.
func (a *App) ChatDraft(ctx context.Context, sid string) (webui.ChatDraft, error) {
	c, err := a.chatSession(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, a.loadChatDraft(sid), ""), nil
}

// chatDraftView: every draft, with focus's (else the first) also at the top.
func (a *App) chatDraftView(ctx context.Context, c chatRow, st chatDraftState, focus string) webui.ChatDraft {
	v := webui.ChatDraft{Editable: chatMode(c.PermissionMode) != "read", Drafts: []webui.ChatDraftItem{}, Adopted: []webui.ChatDraftVersion{}, Skills: []webui.ChatBinding{}}
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
	for _, e := range st.Drafts {
		it := a.chatDraftItem(ctx, c.ID, e)
		v.Drafts = append(v.Drafts, it)
		if e.SkillID == focus || !v.Active {
			v.ChatDraftItem = it
		}
	}
	return v
}

func (a *App) chatDraftItem(ctx context.Context, sid string, e chatDraftEntry) webui.ChatDraftItem {
	v := webui.ChatDraftItem{Active: true, SkillID: e.SkillID, SkillName: a.skillName(e.SkillID)}
	v.BaseVersionID, v.BaseNo = e.BaseVersionID, a.versionNo(ctx, e.BaseVersionID)
	if cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, e.SkillID); err == nil {
		v.CurrentNo, v.CurrentVersionID = cur.VersionNo, cur.ID
	}
	files, err := readDraftFiles(a.chatDraftEntryDir(sid, e))
	if err == nil {
		v.Files = len(files)
		hash := skillFilesHash(files)
		if base, e2 := a.Skills.Files(ctx, e.BaseVersionID); e2 == nil {
			v.Changed = hash != skillFilesHash(base)
		}
		v.Unsaved = v.Changed && hash != e.SavedHash
	}
	if e.ProposalID != "" {
		sv := webui.ChatDraftVersion{SkillID: e.SkillID, SkillName: v.SkillName, VersionID: e.SavedVersionID, No: a.versionNo(ctx, e.SavedVersionID), ProposalID: e.ProposalID}
		_ = a.DB.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id=?`, e.ProposalID).Scan(&sv.Status)
		v.Saved = &sv
		if sv.Status == "pending" || sv.Status == "canary" {
			v.Affects = a.skillTaskUse(ctx, e.SkillID)
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
// run's pinned version, or the current one) into a draft folder of its own.
// Drafts of the session's other skills are kept; a skill that already has a
// draft keeps it as it is (start again after discarding to begin anew).
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
	st := a.loadChatDraft(sid)
	if st.draft(skillID) >= 0 {
		return a.chatDraftView(ctx, c, st, skillID), nil
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
	e := chatDraftEntry{SkillID: skillID, BaseVersionID: base, Dir: newDraftDir(st, skillID)}
	if err := writeDraftFiles(a.chatDraftEntryDir(sid, e), files); err != nil {
		return webui.ChatDraft{}, err
	}
	st.Drafts = append(st.Drafts, e)
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st, skillID), nil
}

// DiscardChatDraft removes one draft (saved versions stay with the skill;
// the other drafts and what was adopted are kept).
func (a *App) DiscardChatDraft(ctx context.Context, sid, skillID string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	i, err := st.pick(skillID)
	if errors.Is(err, webui.ErrChatDraftNone) {
		return a.chatDraftView(ctx, c, st, ""), nil // gone already
	}
	if err != nil {
		return webui.ChatDraft{}, err
	}
	if err := os.RemoveAll(a.chatDraftEntryDir(sid, st.Drafts[i])); err != nil {
		return webui.ChatDraft{}, err
	}
	st.Drafts = append(st.Drafts[:i:i], st.Drafts[i+1:]...)
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st, ""), nil
}

// SaveChatDraft stores a draft as a new version of its skill, waiting to be
// adopted (current is not moved; no notice is sent: the owner is on the page).
func (a *App) SaveChatDraft(ctx context.Context, sid, skillID, note string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	i, err := st.pick(skillID)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	e := &st.Drafts[i]
	files, err := readDraftFiles(a.chatDraftEntryDir(sid, *e))
	if err != nil {
		return webui.ChatDraft{}, fmt.Errorf("%w: %v", webui.ErrChatInvalid, err)
	}
	if len(files["SKILL.md"]) == 0 {
		return webui.ChatDraft{}, fmt.Errorf("%w: the draft has no SKILL.md", webui.ErrChatInvalid)
	}
	hash := skillFilesHash(files)
	if e.ProposalID != "" && e.SavedHash == hash {
		var status string
		if a.DB.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id=?`, e.ProposalID).Scan(&status) == nil && status == "pending" {
			return a.chatDraftView(ctx, c, st, e.SkillID), nil // saved already
		}
	}
	if note == "" {
		note = "saved from session: " + c.Title
	}
	p, err := a.Optimize.Propose(ctx, e.SkillID, files, "session", "session", note)
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
	e.ProposalID, e.SavedVersionID, e.SavedHash = p.ID, p.VersionID, hash
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	return a.chatDraftView(ctx, c, st, e.SkillID), nil
}

// AdoptChatDraft makes a draft's saved version its skill's current one. Only
// on the owner's click: nothing in a turn or a save ever adopts. It replaces
// only the version the draft started from, or expectCurrent: the current
// version the page showed the owner (the confirmation says it gets replaced).
// Anything else (adopted in another session, edited or switched on the skill
// page since) is webui.ErrChatDraftStale and nothing changes, like the skill
// page's own edits.
func (a *App) AdoptChatDraft(ctx context.Context, sid, skillID, expectCurrent string) (webui.ChatDraft, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	c, err := a.draftSessionIdle(ctx, sid)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	st := a.loadChatDraft(sid)
	i, err := st.pick(skillID)
	if err != nil {
		return webui.ChatDraft{}, err
	}
	e := &st.Drafts[i]
	if e.ProposalID == "" {
		return webui.ChatDraft{}, webui.ErrChatDraftNotSaved
	}
	if err := a.Optimize.PromoteOver(ctx, e.ProposalID, e.BaseVersionID, expectCurrent); err != nil {
		if errors.Is(err, optimize.ErrStale) {
			return webui.ChatDraft{}, webui.ErrChatDraftStale
		}
		return webui.ChatDraft{}, fmt.Errorf("%w: %v", webui.ErrChatInvalid, err)
	}
	if st.Adopted == nil {
		st.Adopted = map[string]string{}
	}
	st.Adopted[e.SkillID] = e.SavedVersionID
	e.BaseVersionID = e.SavedVersionID // the draft now starts from what is current
	if err := a.storeChatDraft(sid, st); err != nil {
		return webui.ChatDraft{}, err
	}
	a.Log.Info("session adopted skill version", "session", sid, "skill", e.SkillID, "version", e.SavedVersionID)
	return a.chatDraftView(ctx, c, st, e.SkillID), nil
}

// ChatTaskPreset is "save as task" from a session (or, fromRun, from the run
// it continues): the request (the last message, or the run's), the folder,
// the permission level, the session's effort, the provider, and every skill pinned:
// to the version adopted in the session, else the one the turns use now.
func (a *App) ChatTaskPreset(ctx context.Context, sid string, fromRun bool) (webui.ChatTaskPreset, error) {
	c, err := a.chatSession(ctx, sid)
	if err != nil {
		return webui.ChatTaskPreset{}, err
	}
	p := webui.ChatTaskPreset{Workspace: c.WorkspaceDir, RunMode: chatMode(c.PermissionMode), Provider: c.Provider, Effort: c.Effort, SkillBindings: []tasksvc.SkillBinding{}}
	if prob := a.chatWorkspaceProblem(c.WorkspaceDir); prob != "" {
		p.WorkspaceProblem = prob
		if prob != guard.DirNotFound {
			// Don't prefill an unsafe path (a link into ~/.ssh, the home
			// folder, tidebot's own folder). A missing folder is left in so
			// the form can show it and the owner can pick another.
			p.Workspace = ""
		}
	}
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

// chatDraftStaged is a draft a turn loads: its skill, the version it
// started from, its folder and files.
type chatDraftStaged struct {
	SkillID, BaseVersionID, Dir string
	Files                       map[string][]byte
}

// chatDraftStage: the drafts whose files can be loaded, in the order they
// were started (one that can't be read is left out and logged).
func (a *App) chatDraftStage(c chatRow) []chatDraftStaged {
	var out []chatDraftStaged
	for _, e := range a.loadChatDraft(c.ID).Drafts {
		dir := a.chatDraftEntryDir(c.ID, e)
		files, err := readDraftFiles(dir)
		if err != nil || len(files["SKILL.md"]) == 0 {
			if err == nil {
				err = errors.New("no SKILL.md")
			}
			a.Log.Warn("session turn: skill draft not loaded", "session", c.ID, "skill", e.SkillID, "error", err)
			continue
		}
		out = append(out, chatDraftStaged{SkillID: e.SkillID, BaseVersionID: e.BaseVersionID, Dir: dir, Files: files})
	}
	return out
}

const chatDraftEditNote = "\nThe owner is tuning the skill %q in this session. Its draft is the folder %s: when asked to change the skill, edit the files there (SKILL.md and its other files) and nothing else of the skill. This turn loaded the draft as it was when the turn started; changes apply from the next turn. Saving the draft as a version and adopting it are the owner's buttons, not yours."

const chatDraftReadNote = "\nThe owner is tuning the skill %q in this session, from a draft. This session is read-only, so you cannot change the draft: describe the change to make, and the owner can switch the session to 「可修改」 or 「可执行命令」 to let you edit it."
