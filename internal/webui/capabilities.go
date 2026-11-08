package webui

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// CapabilityAPI separates owner-only decisions from the model's proposal tools.
type CapabilityAPI interface {
	CapabilityRequest(context.Context, string, CapabilityRequest) (any, error)
}
type CapabilityRequest struct {
	LocalChoices        map[string]string `json:"local_choices,omitempty"`
	SourceID            string            `json:"source_id,omitempty"`
	SourceKind          string            `json:"source_kind,omitempty"`
	Name                string            `json:"name,omitempty"`
	Providers           string            `json:"providers,omitempty"`
	DefaultEnabled      bool              `json:"default_enabled,omitempty"`
	Component           string            `json:"component,omitempty"`
	PreparationID       string            `json:"preparation_id,omitempty"`
	ClientID            string            `json:"client_id,omitempty"`
	RegisterClient      bool              `json:"register_client,omitempty"`
	Scopes              []string          `json:"scopes,omitempty"`
	Code                string            `json:"-"`
	State               string            `json:"-"`
	OAuthError          string            `json:"-"`
	RunID               string            `json:"run_id,omitempty"`
	SkillID             string            `json:"skill_id,omitempty"`
	CapabilityID        string            `json:"capability_id,omitempty"`
	VersionID           string            `json:"version_id,omitempty"`
	Bundle              json.RawMessage   `json:"bundle,omitempty"`
	InstallDependencies bool              `json:"install_dependencies,omitempty"`
	Source              string            `json:"source,omitempty"`
	Query               string            `json:"query,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	ProposalID          string            `json:"proposal_id,omitempty"`
	Since               string            `json:"-"`
	Reason              string            `json:"reason,omitempty"`
	Path                string            `json:"path,omitempty"`
	Reviewed            bool              `json:"reviewed,omitempty"`
	SecondaryConfirm    bool              `json:"secondary_confirm,omitempty"`
}
type CapabilityError struct {
	Status        int
	Code, Message string
}

func (e *CapabilityError) Error() string { return e.Message }
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.deps.Capabilities == nil {
		writeErr(w, 404, "not available")
		return
	}
	route := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/capabilities"), "/")
	req := CapabilityRequest{Providers: r.URL.Query().Get("providers"), RunID: r.URL.Query().Get("run_id"), VersionID: r.URL.Query().Get("version_id"), Source: r.URL.Query().Get("source"), Query: r.URL.Query().Get("q"), SessionID: r.URL.Query().Get("session_id"), Path: r.URL.Query().Get("path"), Since: r.URL.Query().Get("since")}
	action := ""
	switch {
	case route == "sources" && r.Method == http.MethodGet:
		action = "sources"
	case route == "sources" && r.Method == http.MethodPost:
		action = "source_save"
	case route == "" && r.Method == http.MethodGet:
		action = "list"
	case route == "run-bindings" && r.Method == http.MethodGet:
		action = "run_bindings"
	case route == "defaults" && r.Method == http.MethodGet:
		action = "default_get"
	case route == "storage" && r.Method == http.MethodGet:
		action = "storage"
	case route == "storage/gc" && r.Method == http.MethodPost:
		action = "storage_gc"
	case route == "registry/search" && r.Method == http.MethodGet:
		action = "registry_search"
	case route == "search" && r.Method == http.MethodGet:
		action = "search"
	case (route == "inspect" || route == "check" || route == "test" || route == "marketplace" || route == "source-skills") && r.Method == http.MethodPost:
		action = route
	case route == "proposals" && r.Method == http.MethodGet:
		action = "proposals"
	case (route == "recover" || route == "skill-optimize") && r.Method == http.MethodPost:
		action = route
	case route == "import" && r.Method == http.MethodPost:
		action = "import"
	case route == "proposals" && r.Method == http.MethodPost:
		action = "propose"
	default:
		parts := strings.Split(route, "/")
		if len(parts) == 3 && parts[0] == "sources" && parts[1] != "" && r.Method == http.MethodPost && (parts[2] == "delete" || parts[2] == "refresh") {
			req.SourceID = parts[1]
			action = "source_" + parts[2]
		}
		if len(parts) == 2 && parts[0] != "" && parts[1] == "login" {
			req.CapabilityID = parts[0]
			if r.Method == http.MethodPost {
				action = "cli_login_start"
			}
			if r.Method == http.MethodGet {
				action = "cli_login_status"
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		if len(parts) == 3 && parts[0] != "" && parts[1] == "login" && parts[2] == "cancel" && r.Method == http.MethodPost {
			req.CapabilityID = parts[0]
			action = "cli_login_cancel"
		}

		if len(parts) == 3 && parts[0] != "" && parts[1] == "oauth" && r.Method == http.MethodPost && (parts[2] == "prepare" || parts[2] == "start" || parts[2] == "revoke") {
			req.CapabilityID = parts[0]
			action = "oauth_" + parts[2]
		}
		if (len(parts) == 4 || len(parts) == 5 && parts[4] == "complete") && parts[0] != "" && parts[1] == "oauth" && parts[2] == "callback" && parts[3] != "" && r.Method == http.MethodGet {
			req.CapabilityID = parts[0]
			req.PreparationID = parts[3]
			req.Code = r.URL.Query().Get("code")
			req.State = r.URL.Query().Get("state")
			req.OAuthError = r.URL.Query().Get("error")
			action = "oauth_callback"
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
		}
		if len(parts) == 2 && parts[0] != "" {
			req.CapabilityID = parts[0]
			if r.Method == http.MethodGet && (parts[1] == "versions" || parts[1] == "events" || parts[1] == "export") {
				action = parts[1]
			}
			if r.Method == http.MethodPost && (parts[1] == "rollback" || parts[1] == "uninstall" || parts[1] == "update" || parts[1] == "credentials_clear" || parts[1] == "gc" || parts[1] == "default" || parts[1] == "deactivate" || parts[1] == "rescan") {
				action = parts[1]
				if action == "default" {
					action = "default_set"
				}
			}
		}
		if len(parts) == 3 && parts[0] == "proposals" && parts[1] != "" {
			req.ProposalID = parts[1]
			if (parts[2] == "confirm" || parts[2] == "dismiss") && r.Method == http.MethodPost {
				action = parts[2]
			}
			if (parts[2] == "files" || parts[2] == "file") && r.Method == http.MethodGet {
				action = parts[2]
			}
		}
	}
	if action == "" {
		writeErr(w, 404, "not found")
		return
	}
	if r.Method == http.MethodPost {
		sourceID := req.SourceID
		proposalID := req.ProposalID
		capabilityID := req.CapabilityID
		limit := int64(16 << 10)
		if action == "import" {
			limit = 96 << 20
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		if dec.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "one JSON object required")
			return
		}
		req.SourceID = sourceID
		req.ProposalID = proposalID
		req.CapabilityID = capabilityID
	}
	out, err := s.deps.Capabilities.CapabilityRequest(r.Context(), action, req)
	if err != nil {
		var e *CapabilityError
		if errors.As(err, &e) {
			writeJSON(w, e.Status, map[string]string{"code": e.Code, "error": e.Message})
		} else {
			chatErr(w, err)
		}
		return
	}
	if action == "export" {
		if raw, ok := out.([]byte); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", `attachment; filename="capability-bundle.json"`)
			w.WriteHeader(200)
			_, _ = w.Write(raw)
			return
		}
	}
	status := 200
	if action == "propose" || action == "update" || action == "rescan" || action == "import" {
		status = 201
	}
	writeJSON(w, status, out)
}

// A top-level provider redirect cannot carry the Strict owner cookie. This inert
// landing page creates a same-origin navigation without changing cookie policy.
// Only the subsequent authenticated handler can exchange the authorization code.
func (s *Server) handleCapabilityRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/capabilities/"), "/")
	if r.Method == http.MethodGet && len(parts) == 4 && parts[0] != "" && parts[1] == "oauth" && parts[2] == "callback" && parts[3] != "" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		q := url.Values{}
		for _, key := range []string{"code", "state", "error"} {
			v := r.URL.Query().Get(key)
			if len(v) > 8192 {
				writeErr(w, 400, "invalid OAuth callback")
				return
			}
			if v != "" {
				q.Set(key, v)
			}
		}
		target := &url.URL{Path: "/api/capabilities/" + parts[0] + "/oauth/callback/" + parts[3] + "/complete", RawQuery: q.Encode()}
		page := template.Must(template.New("oauth").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>OAuth</title></head><body><a href="{{.}}">Continue connecting your account / 继续连接账号</a></body></html>`))
		_ = page.Execute(w, target.String())
		return
	}
	s.withAuthCSRF(s.handleCapabilities)(w, r)
}
