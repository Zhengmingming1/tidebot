package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type CapabilitySecretStatus struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional"`
	Set      bool   `json:"set"`
}
type CapabilitySecretAPI interface {
	CapabilitySecretStatus(context.Context, string) ([]CapabilitySecretStatus, error)
	SetCapabilitySecret(context.Context, string, string, string) error
}

func (s *Server) handleCapabilitySecrets(w http.ResponseWriter, r *http.Request) {
	if s.deps.CapabilitySecrets == nil {
		writeErr(w, 404, "unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "GET":
		out, e := s.deps.CapabilitySecrets.CapabilitySecretStatus(r.Context(), r.URL.Query().Get("capability_id"))
		if e != nil {
			writeErr(w, 400, "capability unavailable")
			return
		}
		writeJSON(w, 200, out)
	case "POST":
		var in struct {
			CapabilityID string `json:"capability_id"`
			Name         string `json:"name"`
			Value        string `json:"value"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "invalid secret request")
			return
		}
		if e := s.deps.CapabilitySecrets.SetCapabilitySecret(r.Context(), in.CapabilityID, in.Name, in.Value); e != nil {
			writeErr(w, 400, "could not save declared capability secret")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}
