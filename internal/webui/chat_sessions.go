package webui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ChatSession is one session (会话) as the API shows it.
type ChatSession struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Origin       string `json:"origin"` // run | chat (run_fix later)
	OriginRunID  string `json:"origin_run_id"`
	WorkspaceDir string `json:"workspace_dir"`
	// WorkspaceProblem is a CheckRunDir problem for WorkspaceDir when it is
	// the owner's folder (moved, deleted, or a link into a sensitive folder).
	// Empty when the folder is fine, unset, or a tidebot folder. Filled when
	// opening a session so the directory tag can show it before Send.
	WorkspaceProblem string  `json:"workspace_problem,omitempty"`
	PermissionMode   string  `json:"permission_mode"` // read | edit | exec
	Effort           string  `json:"effort"`          // "" (not passed) | low | medium | high; the next turns use it
	Provider         string  `json:"provider"`
	Turns            int     `json:"turns"`     // turns sent (the origin run is turn 0)
	TurnHint         int     `json:"turn_hint"` // from this many turns the page shows a soft "long session" hint (no limit)
	CostUSD          float64 `json:"cost_usd"`  // display currency, like runs' cost_usd
	WebAccess        bool    `json:"web_access"`
	Status           string  `json:"status"` // idle | running | closed
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
	// Bindings are the skills and capabilities bound to the session: part of
	// every later turn (read-only like the rest of the session).
	Bindings []ChatBinding `json:"bindings"`
}

// ChatBinding is one thing a session is bound to.
type ChatBinding struct {
	Kind    string `json:"kind"` // skill | capability
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing,omitempty"` // deleted since; not used by turns
}

// ChatBindingsInput replaces a session's bindings.
type ChatBindingsInput struct {
	Skills       []string `json:"skills"`
	Capabilities []string `json:"capabilities"`
}

// ChatTurn is one turn: what was asked and the run that answered.
type ChatTurn struct {
	Index     int      `json:"index"`
	RunID     string   `json:"run_id"`
	Message   string   `json:"message"`
	Status    string   `json:"status"`
	Answer    string   `json:"answer"`
	Truncated bool     `json:"answer_truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
	ErrorInfo *errInfo `json:"error_info,omitempty"`
	CostUSD   float64  `json:"cost_usd"` // display currency
	CreatedAt string   `json:"created_at"`
	// Effort is the --effort this turn's run actually used ("" = default, also
	// for a deleted run or a stored value that is not low/medium/high). The
	// session's own effort can change between turns; this one never does.
	Effort string `json:"effort,omitempty"`
	// Provider and Model are what this turn's run was started with (the run
	// row's provider_name / model; Model "" = the provider's default model).
	// Empty for a deleted run.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Changes is how much this turn changed in the owner's folder, same
	// summary as a run list row. Nil when nothing was recorded. The file
	// list itself stays on the run detail.
	Changes *runChangeSum `json:"changes,omitempty"`
}

// TurnChanges builds the summary stored on a turn (files is -1 when nothing
// was recorded). The session page uses this for the badge and undo button.
func TurnChanges(files, add, del, undoneN int, undone, patchCut bool) *runChangeSum {
	if files < 0 {
		return nil
	}
	return &runChangeSum{Files: files, Add: add, Del: del, Undone: undone, UndoneN: undoneN, PatchCut: patchCut}
}

// ChatSessionDetail is a session with its turns, oldest first.
type ChatSessionDetail struct {
	ChatSession
	TurnList []ChatTurn `json:"turn_list"`
	// Sig fingerprints this answer; GET …?since=<sig> answers {"unchanged":true}
	// while it still matches (see chatSessionSigner).
	Sig string `json:"sig,omitempty"`
	// CapProposals: with ?caps=1, the session's extension proposals as
	// GET /api/capabilities/proposals?session_id= would answer (see capProposalsEmbed).
	CapProposals any `json:"cap_proposals,omitempty"`
}

// chatModeSetter is optional: an app that lets the owner change a session's
// permission level (PUT /api/chat-sessions/{id}/mode).
type chatModeSetter interface {
	SetChatMode(ctx context.Context, id, mode string) (ChatSession, error)
}

// chatEffortSetter is optional: an app that lets the owner change a session's
// --effort (PUT /api/chat-sessions/{id}/effort).
type chatEffortSetter interface {
	SetChatEffort(ctx context.Context, id, effort string) (ChatSession, error)
}

// chatSessionSigner is optional: an app that can fingerprint a session cheaply
// lets the session page's 1.5 s poll skip unchanged sessions.
type chatSessionSigner interface {
	ChatSessionSig(ctx context.Context, id string) (string, error)
}

// ChatSessionCreate starts a session from a finished run, or a blank one
// (Origin "chat", no run).
type ChatSessionCreate struct {
	RunID     string `json:"run_id"`
	Origin    string `json:"origin,omitempty"`
	Title     string `json:"title"`
	WebAccess bool   `json:"web_access"`
	// PermissionMode: read | edit | exec; "" = the run's level (blank: read).
	PermissionMode string `json:"permission_mode,omitempty"`
	// Effort: nil = the run's effort (blank: default); "" = default; low | medium | high.
	Effort *string `json:"effort,omitempty"`
}

// ChatTurnStarted is the answer to sending a turn.
type ChatTurnStarted struct {
	Index int    `json:"index"`
	RunID string `json:"run_id,omitempty"`
}

// ChatSessionAPI is what the session endpoints need from the app.
type ChatSessionAPI interface {
	CreateChatSession(ctx context.Context, req ChatSessionCreate) (ChatSession, error)
	ListChatSessions(ctx context.Context, limit int) ([]ChatSession, error)
	GetChatSession(ctx context.Context, id string) (ChatSessionDetail, error)
	SendChatTurn(ctx context.Context, id, message string) (ChatTurnStarted, error)
	DeleteChatSession(ctx context.Context, id string) error
	SetChatBindings(ctx context.Context, id string, in ChatBindingsInput) (ChatSession, error)
}

// Session errors; the API answers them with a stable "code".
var (
	ErrChatSessionNotFound = errors.New("session not found")
	ErrChatRunNotFound     = errors.New("run not found")
	ErrChatRunNotFinished  = errors.New("the run has not finished")
	ErrChatSessionBusy     = errors.New("the session is answering a turn")
	ErrChatInvalid         = errors.New("invalid session request")
)

// runChatRef: the session a run belongs to (turn 0 = the run it continues).
type runChatRef struct {
	ID          string `json:"id"`
	Turn        int    `json:"turn"`
	OriginRunID string `json:"origin_run_id"`
}

// runChatRef finds the session a run is a turn of, or nil.
func (s *Server) runChatRef(ctx context.Context, runID string) *runChatRef {
	if s.deps.ChatSessions == nil {
		return nil
	}
	var c runChatRef
	err := s.deps.DB.QueryRowContext(ctx, `SELECT t.session_id, t.turn_index, cs.origin_run_id FROM chat_session_turns t
  JOIN chat_sessions cs ON cs.id=t.session_id WHERE t.run_id=? ORDER BY t.turn_index LIMIT 1`, runID).Scan(&c.ID, &c.Turn, &c.OriginRunID)
	if err != nil {
		return nil
	}
	return &c
}

// chatSessionDetail: the body of GET /api/chat-sessions/{id} (turn list never null, error
// codes attached); sig is the fingerprint taken before the read, "" when unknown.
func chatSessionDetail(ctx context.Context, api ChatSessionAPI, id, sig string) (ChatSessionDetail, error) {
	d, err := api.GetChatSession(ctx, id)
	if err != nil {
		return d, err
	}
	if d.TurnList == nil {
		d.TurnList = []ChatTurn{}
	}
	attachTurnErrInfo(d.TurnList)
	d.Sig = sig
	return d, nil
}

// runChatEmbed: for GET /api/runs/{id}?chat=1, the session a finished run starts (its
// turn 0), as GET /api/chat-sessions/{id} would answer, so the run page draws the thread
// with the run instead of one round trip later. nil when there is none or it can't be read
// (the page then asks for it itself, as before).
func (s *Server) runChatEmbed(ctx context.Context, ref *runChatRef, status string) *ChatSessionDetail {
	api := s.deps.ChatSessions
	if api == nil || ref == nil || ref.Turn != 0 {
		return nil
	}
	switch status {
	case "succeeded", "failed", "timeout", "canceled":
	default:
		return nil
	}
	sig := ""
	if sg, ok := api.(chatSessionSigner); ok {
		if v, err := sg.ChatSessionSig(ctx, ref.ID); err == nil {
			sig = v
		}
	}
	d, err := chatSessionDetail(ctx, api, ref.ID, sig)
	if err != nil {
		return nil
	}
	// the run page shows the session's proposal cards too: they come along
	d.CapProposals = s.capProposalsFor(ctx, ref.ID, "")
	return &d
}

// capProposalsEmbed: for GET /api/chat-sessions/{id}?caps=1[&cap_since=<sig>], the
// session's extension proposals, answered exactly as GET
// /api/capabilities/proposals?session_id=<id>&since=<sig> would ({proposals, sig} or
// {unchanged, sig}). The session page polls the session every 1.5 s while a turn is
// answered and used to ask for the proposals right after each poll; carried here, one
// request does both and the cards draw with the thread instead of a round trip later.
// nil (the page asks itself, as before) when not asked for, not available, or failed.
func (s *Server) capProposalsEmbed(r *http.Request, id string) any {
	q := r.URL.Query()
	if q.Get("caps") != "1" {
		return nil
	}
	return s.capProposalsFor(r.Context(), id, q.Get("cap_since"))
}

func (s *Server) capProposalsFor(ctx context.Context, id, since string) any {
	if s.deps.Capabilities == nil {
		return nil
	}
	v, err := s.deps.Capabilities.CapabilityRequest(ctx, "proposals", CapabilityRequest{SessionID: id, Since: since})
	if err != nil || v == nil {
		return nil
	}
	return v
}

// ChatMessageMax caps one turn's message (bytes).
const ChatMessageMax = 16 << 10

func chatErr(w http.ResponseWriter, err error) {
	codes := []struct {
		err    error
		status int
		code   string
	}{
		{ErrChatSessionNotFound, 404, "not_found"},
		{ErrChatRunNotFound, 404, "run_not_found"},
		{ErrChatRunNotFinished, 409, "run_not_finished"},
		{ErrChatSessionBusy, 409, "busy"},
		{ErrChatInvalid, 400, "invalid"},
		{guard.ErrDirBusy, 409, "dir_busy"}, // another run is changing the session's folder
	}
	// the session's folder can't be used now (moved, deleted, not allowed): the
	// same problem codes as the Run dialog's folder check
	var de *guard.DirError
	if errors.As(err, &de) {
		writeJSON(w, 400, map[string]string{"error": err.Error(), "code": "dir", "problem": de.Problem})
		return
	}
	for _, c := range codes {
		if errors.Is(err, c.err) {
			writeJSON(w, c.status, map[string]string{"error": err.Error(), "code": c.code})
			return
		}
	}
	writeErrInfo(w, 500, err.Error())
}

// handleChatSessions: GET lists sessions (newest first), POST creates one from a run.
func (s *Server) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	api := s.deps.ChatSessions
	if api == nil {
		writeErr(w, 404, "not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		list, err := api.ListChatSessions(r.Context(), limit)
		if err != nil {
			chatErr(w, err)
			return
		}
		if list == nil {
			list = []ChatSession{}
		}
		writeJSON(w, 200, map[string]any{"sessions": list})
	case http.MethodPost:
		var body ChatSessionCreate
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil || strings.TrimSpace(body.RunID) == "" && body.Origin != "chat" || body.Origin != "" && body.Origin != "chat" && body.Origin != "run" ||
			body.PermissionMode != "" && body.PermissionMode != "read" && body.PermissionMode != "edit" && body.PermissionMode != "exec" {
			writeJSON(w, 400, map[string]string{"error": "run_id required", "code": "invalid"})
			return
		}
		if body.Effort != nil && !validChatEffort(*body.Effort) {
			writeJSON(w, 400, map[string]string{"error": "effort must be empty, low, medium or high", "code": "invalid"})
			return
		}
		ses, err := api.CreateChatSession(r.Context(), body)
		if err != nil {
			chatErr(w, err)
			return
		}
		writeJSON(w, 201, ses)
	default:
		writeErr(w, 405, "GET or POST")
	}
}

// handleChatSessionSub: GET/DELETE /api/chat-sessions/{id}, POST /api/chat-sessions/{id}/turns,
// PUT /api/chat-sessions/{id}/bindings, PUT /api/chat-sessions/{id}/mode,
// PUT /api/chat-sessions/{id}/effort.
func (s *Server) handleChatSessionSub(w http.ResponseWriter, r *http.Request) {
	api := s.deps.ChatSessions
	if api == nil {
		writeErr(w, 404, "not available")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/chat-sessions/")
	id, sub, _ := strings.Cut(rest, "/")
	if id == "" || strings.ContainsAny(id, `\.`) {
		writeErr(w, 404, "not found")
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		// The fingerprint is taken before the read: a change in between only
		// makes the next poll fetch again, never hides one.
		sig := ""
		if sg, ok := api.(chatSessionSigner); ok {
			v, err := sg.ChatSessionSig(r.Context(), id)
			if errors.Is(err, ErrChatSessionNotFound) {
				chatErr(w, err)
				return
			}
			if err == nil {
				sig = v
			}
		}
		if since := r.URL.Query().Get("since"); sig != "" && since == sig {
			out := map[string]any{"unchanged": true, "sig": sig}
			if cp := s.capProposalsEmbed(r, id); cp != nil {
				out["cap_proposals"] = cp
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, out)
			return
		}
		d, err := chatSessionDetail(r.Context(), api, id, sig)
		if err != nil {
			chatErr(w, err)
			return
		}
		d.CapProposals = s.capProposalsEmbed(r, id)
		writeJSON(w, 200, d)
	case sub == "" && r.Method == http.MethodDelete:
		if err := api.DeleteChatSession(r.Context(), id); err != nil {
			chatErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case sub == "turns" && r.Method == http.MethodPost:
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4*ChatMessageMax)).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
			writeJSON(w, 400, map[string]string{"error": "message required", "code": "invalid"})
			return
		}
		if len(body.Message) > ChatMessageMax {
			writeJSON(w, 400, map[string]string{"error": "message too long", "code": "too_long"})
			return
		}
		st, err := api.SendChatTurn(r.Context(), id, body.Message)
		if err != nil {
			chatErr(w, err)
			return
		}
		out := map[string]any{"ok": true, "index": st.Index}
		if st.RunID != "" {
			out["run_id"] = st.RunID
			if n := s.queuePos(st.RunID); n > 0 {
				out["queue_pos"] = n
			}
		}
		writeJSON(w, 202, out)
	case sub == "bindings" && r.Method == http.MethodPut:
		var body ChatBindingsInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad bindings", "code": "invalid"})
			return
		}
		ses, err := api.SetChatBindings(r.Context(), id, body)
		if err != nil {
			chatErr(w, err)
			return
		}
		writeJSON(w, 200, ses)
	case sub == "mode" && r.Method == http.MethodPut:
		ms, ok := api.(chatModeSetter)
		if !ok {
			writeErr(w, 404, "not available")
			return
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || body.Mode != "read" && body.Mode != "edit" && body.Mode != "exec" {
			writeJSON(w, 400, map[string]string{"error": "mode must be read, edit or exec", "code": "invalid"})
			return
		}
		ses, err := ms.SetChatMode(r.Context(), id, body.Mode)
		if err != nil {
			chatErr(w, err)
			return
		}
		writeJSON(w, 200, ses)
	case sub == "effort" && r.Method == http.MethodPut:
		es, ok := api.(chatEffortSetter)
		if !ok {
			writeErr(w, 404, "not available")
			return
		}
		var body struct {
			Effort *string `json:"effort"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || body.Effort == nil || !validChatEffort(*body.Effort) {
			writeJSON(w, 400, map[string]string{"error": "effort must be empty, low, medium or high", "code": "invalid"})
			return
		}
		ses, err := es.SetChatEffort(r.Context(), id, *body.Effort)
		if err != nil {
			chatErr(w, err)
			return
		}
		writeJSON(w, 200, ses)
	case sub == "draft" || strings.HasPrefix(sub, "draft/") || sub == "task-preset":
		s.handleChatDraft(w, r, id, sub)
	case sub == "" || sub == "turns" || sub == "bindings" || sub == "mode" || sub == "effort":
		writeErr(w, 405, "method")
	default:
		writeErr(w, 404, "not found")
	}
}

// validChatEffort: "" (not passed) or one of Claude Code's --effort levels.
func validChatEffort(e string) bool {
	return e == "" || e == "low" || e == "medium" || e == "high"
}

func attachTurnErrInfo(turns []ChatTurn) {
	for i := range turns {
		turns[i].ErrorInfo = explainError(turns[i].Error)
	}
}
