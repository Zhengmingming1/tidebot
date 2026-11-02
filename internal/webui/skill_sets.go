package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"io"
	"net/http"
	"strings"
)

func (s *Server) setError(w http.ResponseWriter, err error) {
	var partial *skillstore.PartialSetImportError
	if errors.As(err, &partial) {
		writeJSON(w, 400, map[string]any{"error": err.Error(), "partial": true, "imported_skill_ids": partial.ImportedSkillIDs})
		return
	}
	code := 400
	if errors.Is(err, sql.ErrNoRows) {
		code = 404
	}
	if errors.Is(err, skillstore.ErrSetConflict) {
		code = 409
	}
	writeErr(w, code, err.Error())
}
func (s *Server) saveSkillSet(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Name     string                 `json:"name"`
		Bindings []tasksvc.SkillBinding `json:"skill_bindings"`
		Expected string                 `json:"expected_version_id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil {
		writeErr(w, 400, "bad json")
		return
	}
	out, err := s.deps.Skills.SaveSet(r.Context(), id, in.Name, in.Expected, in.Bindings)
	if err != nil {
		s.setError(w, err)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	writeJSON(w, status, out)
}
func (s *Server) handleSkillSets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out, err := s.deps.Skills.ListSets(r.Context())
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		s.saveSkillSet(w, r, "")
	default:
		writeErr(w, 405, "GET or POST only")
	}
}
func (s *Server) handleSkillSetSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/skill-sets/"), "/")
	if len(parts) < 1 || !idRe.MatchString(parts[0]) {
		writeErr(w, 404, "skill set not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet {
		data, err := s.deps.Skills.ExportSet(r.Context(), id)
		if err != nil {
			s.setError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="tidebot-skill-set.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if len(parts) != 1 {
		writeErr(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := s.deps.Skills.GetSet(r.Context(), id)
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPut:
		s.saveSkillSet(w, r, id)
	case http.MethodDelete:
		if err := s.deps.Skills.DeleteSet(r.Context(), id); err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "GET, PUT or DELETE only")
	}
}
func (s *Server) handleSkillSetImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, skillpkg.MaxArchiveBytes+(1<<20))
	if r.ParseMultipartForm(8<<20) != nil {
		writeErr(w, 400, "invalid or oversized archive")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "choose a skill set ZIP")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, skillpkg.MaxArchiveBytes+1))
	if err != nil {
		writeErr(w, 400, "cannot read archive")
		return
	}
	out, err := s.deps.Skills.ImportSet(r.Context(), data)
	if err != nil {
		s.setError(w, err)
		return
	}
	writeJSON(w, 201, out)
}
