package webui

import (
	"context"
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"io"
	"net/http"
)

type CapabilityTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}
type CapabilityBindingAPI interface {
	CapabilityBindingRequest(context.Context, string, capability.Binding) (any, error)
	CapabilityBindingTargets(context.Context) ([]CapabilityTarget, error)
}

func (s *Server) handleCapabilityBindings(w http.ResponseWriter, r *http.Request) {
	if s.deps.CapabilityBindings == nil {
		writeErr(w, 404, "unavailable")
		return
	}
	if r.URL.Path == "/api/capability-bindings/targets" && r.Method == "GET" {
		v, e := s.deps.CapabilityBindings.CapabilityBindingTargets(r.Context())
		if e != nil {
			chatErr(w, e)
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.URL.Path != "/api/capability-bindings" {
		writeErr(w, 404, "not found")
		return
	}
	b := capability.Binding{CapabilityID: r.URL.Query().Get("capability_id")}
	switch r.Method {
	case "GET":
	case "PUT", "DELETE":
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&b) != nil || dec.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "invalid binding")
			return
		}
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	v, e := s.deps.CapabilityBindings.CapabilityBindingRequest(r.Context(), r.Method, b)
	if e != nil {
		chatErr(w, e)
		return
	}
	writeJSON(w, 200, v)
}
