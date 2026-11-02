package webui

import (
	"context"

	"encoding/json"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"io"

	"net/http"

	"regexp"

	"strings"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// ---------- buttons ----------

type buttonDTO struct {
	ID          string        `json:"id"`
	Label       string        `json:"label"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	TaskID      string        `json:"task_id,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	Color       string        `json:"color,omitempty"`
	SkillID     string        `json:"skill_id,omitempty"`
	SkillVerID  string        `json:"skill_version_id,omitempty"`
	Params      []buttonParam `json:"params,omitempty"`
}

type buttonParam struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

var buttonColorRe = regexp.MustCompile(`^(|#[0-9a-fA-F]{6})$`)
var paramNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,31}$`)

// expandParams replaces {{name}} with the param default.
func expandParams(prompt string, ps []buttonParam) string {
	for _, p := range ps {
		prompt = strings.ReplaceAll(prompt, "{{"+p.Name+"}}", p.Default)
	}
	return prompt
}

var buttonIDRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

func (s *Server) loadButtons(ctx context.Context) []buttonDTO {
	raw, _ := s.deps.Auth.GetSetting(ctx, "buttons")
	out := []buttonDTO{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) handleButtons(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.loadButtons(r.Context()))
	case http.MethodPut, http.MethodPost:
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var list []buttonDTO
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&list); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			taken := map[string]bool{}
			for _, b := range list {
				if b.ID != "" {
					if taken[b.ID] {
						writeErr(w, 400, "duplicate button id "+b.ID)
						return
					}
					taken[b.ID] = true
				}
			}
			for i := range list {
				b := &list[i]
				if b.ID == "" {
					b.ID = NewButtonID(b.Label, taken)
					taken[b.ID] = true
				}
				if !buttonIDRe.MatchString(b.ID) || strings.TrimSpace(b.Label) == "" || (b.TaskID == "" && b.Prompt == "") {
					writeErr(w, 400, "each button needs a label and a task or a prompt")
					return
				}
				if !buttonColorRe.MatchString(b.Color) || len(b.Params) > 10 {
					writeErr(w, 400, "bad color or too many params")
					return
				}
				for _, p := range b.Params {
					if !paramNameRe.MatchString(p.Name) || len(p.Default) > 500 {
						writeErr(w, 400, "param names: letters, digits, _")
						return
					}
				}
			}
			bs, _ := json.Marshal(list)
			if err := s.deps.Auth.SetSetting(r.Context(), "buttons", string(bs)); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, list)
		})).ServeHTTP(w, r)
	default:
		writeErr(w, 405, "method")
	}
}

func (s *Server) handleButtonRun(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/buttons/"), "/")
	if len(parts) != 2 || parts[1] != "run" || r.Method != http.MethodPost {
		writeErr(w, 404, "not found")
		return
	}
	label, runID, err := s.runButton(r.Context(), parts[0])
	if err != nil {
		code := 500
		if label == "" {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	out := map[string]any{"ok": true, "started": label}
	if runID != "" {
		out["run_id"] = runID
	}
	writeJSON(w, 202, out)
}

func (s *Server) handleQuickRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	var body RunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Prompt) == "" {
		writeErr(w, 400, "prompt required")
		return
	}
	if msg := body.validate(); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.deps.Skills.ValidateSetSource(r.Context(), body.SkillSetSource); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if body.SkillSetSource != nil && body.SkillID == "" && len(body.SkillBindings) == 0 {
		writeErr(w, 400, "composition source requires skill bindings")
		return
	}
	var runID string
	var err error
	switch {
	case body.SkillID == "" && len(body.SkillBindings) == 0 && strings.TrimSpace(body.Workspace) == "":
		if s.deps.QuickRun == nil {
			writeErr(w, 500, "execution not available")
			return
		}
		runID, err = s.deps.QuickRun(body.Prompt, body.Provider)
	case s.deps.StartRun == nil:
		writeErr(w, 500, "execution not available")
		return
	default:
		runID, err = s.deps.StartRun(body)
	}
	if err != nil {
		var de *guard.DirError
		switch {
		case errors.As(err, &de):
			writeJSON(w, 400, map[string]string{"error": err.Error(), "problem": de.Problem})
		case errors.Is(err, guard.ErrDirBusy):
			writeJSON(w, 409, map[string]string{"error": err.Error(), "problem": "busy"})
		case errors.Is(err, ErrUnknownSkill):
			writeErr(w, 404, err.Error())
		default:
			writeErr(w, 500, err.Error())
		}
		return
	}
	s.writeStarted(w, runID)
}

// handleRecentPrompts lists the last distinct quick-run prompts (newest first).
func (s *Server) handleRecentPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	list, err := s.deps.Store.RecentQuickPrompts(r.Context(), 6)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"prompts": list})
}
