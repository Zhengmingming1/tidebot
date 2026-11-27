package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// Skill drafts in a session (P3): the owner tunes a skill without leaving the
// session. "Edit as draft" copies a skill's files into the session's own
// folder; while the draft exists every turn loads it instead of the stored
// version, and in a session that may change files the model edits it there.
// "Save as version" stores the draft as a new version without making it
// current; "Adopt" (always the owner's click, never automatic) makes it
// current. Saving as a task pins the version adopted in the session.

// ChatDraftItem is one skill draft of a session.
type ChatDraftItem struct {
	Active        bool   `json:"active"`
	SkillID       string `json:"skill_id,omitempty"`
	SkillName     string `json:"skill_name,omitempty"`
	BaseVersionID string `json:"base_version_id,omitempty"`
	BaseNo        int    `json:"base_no,omitempty"`
	CurrentNo     int    `json:"current_no,omitempty"` // the skill's current version now
	// CurrentVersionID: sent back by "adopt" when the owner agreed to replace a
	// current version other than the one the draft started from.
	CurrentVersionID string            `json:"current_version_id,omitempty"`
	Files            int               `json:"files,omitempty"`
	Changed          bool              `json:"changed"` // differs from the version it started from
	Unsaved          bool              `json:"unsaved"` // differs from the last saved version (or none saved)
	Saved            *ChatDraftVersion `json:"saved,omitempty"`
	// Affects: who changes when the saved version is adopted.
	Affects *ChatDraftAffects `json:"affects,omitempty"`
}

// ChatDraft is a session's skill drafts and what it adopted. A session may
// tune several of its skills at once, one draft each (Drafts, in the order
// they were started). The embedded item is the draft the request was about
// (start, save, adopt), else the first one, so a page that knows only one
// draft keeps working.
type ChatDraft struct {
	ChatDraftItem
	Drafts []ChatDraftItem `json:"drafts"`
	// Editable: the session's level lets the model change the drafts (edit / exec).
	Editable bool `json:"editable"`
	// Adopted: versions adopted in this session (pinned by "save as task").
	Adopted []ChatDraftVersion `json:"adopted"`
	// Skills: what a draft can be started from (the skills the turns load).
	Skills []ChatBinding `json:"skills"`
}

// ChatDraftVersion is a version saved or adopted in a session.
type ChatDraftVersion struct {
	SkillID    string `json:"skill_id"`
	SkillName  string `json:"skill_name,omitempty"`
	VersionID  string `json:"version_id"`
	No         int    `json:"no"`
	ProposalID string `json:"proposal_id,omitempty"`
	Status     string `json:"status,omitempty"` // pending | promoted | rejected …
}

// ChatDraftAffects names the tasks adopting would change.
type ChatDraftAffects struct {
	Follow []string `json:"follow"` // tasks that use the current version
	Pinned int      `json:"pinned"` // tasks pinned to a version (unchanged)
}

// ChatTaskPreset is what "save as task" fills in from a session or a run.
type ChatTaskPreset struct {
	Prompt        string                 `json:"prompt"`
	Workspace     string                 `json:"workspace"`
	RunMode       string                 `json:"run_mode"`
	Effort        string                 `json:"effort"`
	Provider      string                 `json:"provider"`
	SkillBindings []tasksvc.SkillBinding `json:"skill_bindings"`
	// WorkspaceProblem is a CheckRunDir problem for the session's folder
	// (moved, deleted, a link into a sensitive folder). "save as task" does
	// not prefill an unsafe path; a missing folder is still filled in so the
	// form can warn.
	WorkspaceProblem string `json:"workspace_problem,omitempty"`
}

// chatDrafter is optional: an app with skill drafts in sessions.
type chatDrafter interface {
	ChatDraft(ctx context.Context, id string) (ChatDraft, error)
	StartChatDraft(ctx context.Context, id, skillID string) (ChatDraft, error)
	// skillID "" in discard, save and adopt: the session's only draft
	// (ErrChatDraftWhich when it has several).
	DiscardChatDraft(ctx context.Context, id, skillID string) (ChatDraft, error)
	SaveChatDraft(ctx context.Context, id, skillID, note string) (ChatDraft, error)
	AdoptChatDraft(ctx context.Context, id, skillID, expectCurrent string) (ChatDraft, error)
	ChatTaskPreset(ctx context.Context, id string, fromRun bool) (ChatTaskPreset, error)
}

// Draft errors (codes for the page).
var (
	ErrChatDraftNone      = errors.New("the session has no skill draft")
	ErrChatDraftWhich     = errors.New("the session has several skill drafts: say which skill")
	ErrChatDraftUnchanged = errors.New("the draft is the same as the skill's current version")
	ErrChatDraftChecks    = errors.New("the draft did not pass the skill checks")
	ErrChatDraftNotSaved  = errors.New("save the draft as a version first")
	// ErrChatDraftStale: the skill's current version changed (not the one the
	// draft started from, nor the one the page showed): adopting would replace it.
	ErrChatDraftStale = errors.New("the skill's current version changed since the draft started")
)

func chatDraftErr(w http.ResponseWriter, err error) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrChatDraftNone, 409, "no_draft"},
		{ErrChatDraftWhich, 400, "draft_which"},
		{ErrChatDraftUnchanged, 409, "draft_unchanged"},
		{ErrChatDraftChecks, 422, "draft_checks"},
		{ErrChatDraftNotSaved, 409, "draft_not_saved"},
		{ErrChatDraftStale, 409, "draft_stale"},
	} {
		if errors.Is(err, c.err) {
			writeJSON(w, c.status, map[string]string{"error": err.Error(), "code": c.code})
			return
		}
	}
	chatErr(w, err)
}

// handleChatDraft: GET/POST/DELETE /api/chat-sessions/{id}/draft[?skill_id=],
// POST …/draft/save, POST …/draft/adopt (body skill_id), GET …/task-preset[?from=run].
// Login and CSRF come from the route (withAuthCSRF).
func (s *Server) handleChatDraft(w http.ResponseWriter, r *http.Request, id, sub string) {
	d, ok := s.deps.ChatSessions.(chatDrafter)
	if !ok {
		writeErr(w, 404, "not available")
		return
	}
	ctx := r.Context()
	var (
		out any
		err error
	)
	switch {
	case sub == "task-preset" && r.Method == http.MethodGet:
		out, err = d.ChatTaskPreset(ctx, id, r.URL.Query().Get("from") == "run")
	case sub == "draft" && r.Method == http.MethodGet:
		out, err = d.ChatDraft(ctx, id)
	case sub == "draft" && r.Method == http.MethodPost:
		var body struct {
			SkillID string `json:"skill_id"`
		}
		if e := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); e != nil || strings.TrimSpace(body.SkillID) == "" {
			writeJSON(w, 400, map[string]string{"error": "skill_id required", "code": "invalid"})
			return
		}
		out, err = d.StartChatDraft(ctx, id, strings.TrimSpace(body.SkillID))
	case sub == "draft" && r.Method == http.MethodDelete:
		out, err = d.DiscardChatDraft(ctx, id, strings.TrimSpace(r.URL.Query().Get("skill_id")))
	case sub == "draft/save" && r.Method == http.MethodPost:
		var body struct {
			SkillID string `json:"skill_id"`
			Note    string `json:"note"`
		}
		if e := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); e != nil && !errors.Is(e, io.EOF) || len(body.Note) > 2000 || len(body.SkillID) > 200 {
			writeJSON(w, 400, map[string]string{"error": "bad note", "code": "invalid"})
			return
		}
		out, err = d.SaveChatDraft(ctx, id, strings.TrimSpace(body.SkillID), strings.TrimSpace(body.Note))
	case sub == "draft/adopt" && r.Method == http.MethodPost:
		var body struct {
			SkillID       string `json:"skill_id"`
			ExpectCurrent string `json:"expect_current"`
		}
		if e := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); e != nil && !errors.Is(e, io.EOF) {
			writeJSON(w, 400, map[string]string{"error": "bad body", "code": "invalid"})
			return
		}
		out, err = d.AdoptChatDraft(ctx, id, strings.TrimSpace(body.SkillID), strings.TrimSpace(body.ExpectCurrent))
	case sub == "task-preset" || sub == "draft" || sub == "draft/save" || sub == "draft/adopt":
		writeErr(w, 405, "method")
		return
	default:
		writeErr(w, 404, "not found")
		return
	}
	if err != nil {
		chatDraftErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}
