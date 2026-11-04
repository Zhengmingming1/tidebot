package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type CapabilityDependencyPreferenceRequest struct {
	Name          string `json:"name"`
	Path          string `json:"path,omitempty"`
	PreferManaged bool   `json:"prefer_managed"`
}
type CapabilityDependencyPreferenceAPI interface {
	CapabilityDependencyPreferenceRequest(context.Context, string, CapabilityDependencyPreferenceRequest) (any, error)
}

// Registered only behind the owner's session and CSRF guard. No model IPC tool
// exposes this interface, even though cap_check can read its resulting choices.
func (s *Server) handleCapabilityDependencyPreferences(w http.ResponseWriter, r *http.Request) {
	api, ok := s.deps.Capabilities.(CapabilityDependencyPreferenceAPI)
	if !ok {
		writeErr(w, 404, "unavailable")
		return
	}
	var req CapabilityDependencyPreferenceRequest
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut, http.MethodDelete:
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "invalid dependency preference")
			return
		}
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	out, err := api.CapabilityDependencyPreferenceRequest(r.Context(), r.Method, req)
	if err != nil {
		chatErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}
