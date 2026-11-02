package webui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// decodeManifestRequest rejects oversized and trailing payloads before a mutation.
func decodeManifestRequest(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("expected one JSON object")
	}
	return nil
}

func (s *Server) handleSkillSetPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	var in struct {
		Bindings []tasksvc.SkillBinding `json:"skill_bindings"`
	}
	if err := decodeManifestRequest(w, r, &in); err != nil {
		writeErr(w, 400, "bad or oversized json")
		return
	}
	out, err := s.deps.Skills.PreviewDependencies(r.Context(), in.Bindings)
	if err != nil {
		s.setError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSkillManifest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/skill-manifests/")
	if !idRe.MatchString(id) {
		writeErr(w, 404, "skill not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := s.deps.Skills.GetSkillManifest(r.Context(), id, r.URL.Query().Get("version_id"))
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPut:
		var in struct {
			Expected string                   `json:"expected_version_id"`
			Manifest skillstore.SkillManifest `json:"manifest"`
		}
		if err := decodeManifestRequest(w, r, &in); err != nil {
			writeErr(w, 400, "bad or oversized json")
			return
		}
		out, err := s.deps.Skills.SaveSkillManifest(r.Context(), id, in.Expected, in.Manifest)
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
	default:
		writeErr(w, 405, "GET or PUT only")
	}
}
