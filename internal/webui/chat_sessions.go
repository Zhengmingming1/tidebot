package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ChatSession is one session (会话) as the API shows it.
type ChatSession struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Origin         string  `json:"origin"` // run (chat, run_fix later)
	OriginRunID    string  `json:"origin_run_id"`
	WorkspaceDir   string  `json:"workspace_dir"`
	PermissionMode string  `json:"permission_mode"` // read
	Provider       string  `json:"provider"`
	Turns          int     `json:"turns"` // turns sent (the origin run is turn 0)
	CostUSD        float64 `json:"cost_usd"`
	WebAccess      bool    `json:"web_access"`
	Status         string  `json:"status"` // idle | running | closed
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// ChatTurn is one turn: what was asked and the run that answered.
type ChatTurn struct {
	Index     int     `json:"index"`
	RunID     string  `json:"run_id"`
	Message   string  `json:"message"`
	Status    string  `json:"status"`
	Answer    string  `json:"answer"`
	Truncated bool    `json:"answer_truncated,omitempty"`
	Error     string  `json:"error,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	CreatedAt string  `json:"created_at"`
}

// ChatSessionDetail is a session with its turns, oldest first.
type ChatSessionDetail struct {
	ChatSession
	TurnList []ChatTurn `json:"turn_list"`
}

// ChatSessionCreate starts a session from a finished run.
type ChatSessionCreate struct {
	RunID     string `json:"run_id"`
	Title     string `json:"title"`
	WebAccess bool   `json:"web_access"`
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
}

// Session errors; the API answers them with a stable "code".
var (
	ErrChatSessionNotFound = errors.New("session not found")
	ErrChatRunNotFound     = errors.New("run not found")
	ErrChatRunNotFinished  = errors.New("the run has not finished")
	ErrChatSessionBusy     = errors.New("the session is answering a turn")
	ErrChatTurnLimit       = errors.New("the session has reached its turn limit")
	ErrChatInvalid         = errors.New("invalid session request")
)

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
		{ErrChatTurnLimit, 409, "turn_limit"},
		{ErrChatInvalid, 400, "invalid"},
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
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil || strings.TrimSpace(body.RunID) == "" {
			writeJSON(w, 400, map[string]string{"error": "run_id required", "code": "invalid"})
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

// handleChatSessionSub: GET/DELETE /api/chat-sessions/{id}, POST /api/chat-sessions/{id}/turns.
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
		d, err := api.GetChatSession(r.Context(), id)
		if err != nil {
			chatErr(w, err)
			return
		}
		if d.TurnList == nil {
			d.TurnList = []ChatTurn{}
		}
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
	case sub == "" || sub == "turns":
		writeErr(w, 405, "method")
	default:
		writeErr(w, 404, "not found")
	}
}
