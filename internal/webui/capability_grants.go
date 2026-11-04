package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type CapabilityGrantRequest struct {
	SessionID     string   `json:"session_id"`
	CapabilityID  string   `json:"capability_id"`
	VersionID     string   `json:"version_id"`
	Component     string   `json:"component"`
	DiscoveryHash string   `json:"discovery_hash"`
	Tools         []string `json:"tools"`
}
type CapabilityGrantAPI interface {
	CapabilityGrantRequest(context.Context, string, CapabilityGrantRequest) (any, error)
}

func (s *Server) handleCapabilityGrants(w http.ResponseWriter, r *http.Request) {
	api, ok := s.deps.Capabilities.(CapabilityGrantAPI)
	if !ok {
		writeErr(w, 404, "unavailable")
		return
	}
	req := CapabilityGrantRequest{SessionID: r.URL.Query().Get("session_id")}
	switch r.Method {
	case "GET":
	case "PUT", "DELETE":
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "invalid grant")
			return
		}
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	result, err := api.CapabilityGrantRequest(r.Context(), r.Method, req)
	if err != nil {
		chatErr(w, err)
		return
	}
	writeJSON(w, 200, result)
}
