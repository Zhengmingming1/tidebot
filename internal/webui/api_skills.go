package webui

import (
	"encoding/json"

	"io"

	"net/http"

	"os"

	"strings"

	"github.com/Zhengmingming1/tidebot/internal/auth"

	"github.com/Zhengmingming1/tidebot/internal/version"
)

// ---------- skills ----------

type skillDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VersionID string `json:"version_id"`
	VersionNo int    `json:"version_no"`
	UpdatedAt string `json:"updated_at"`
	Files     int    `json:"files"`
	UsedBy    int    `json:"used_by"`
	// Description is SKILL.md's frontmatter description (first 200 characters); the
	// button form uses it to prefill a button's subtitle.
	Description string `json:"description,omitempty"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Skills.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []skillDTO{}
	for _, sk := range list {
		d := skillDTO{ID: sk.ID, Name: sk.Name}
		if sk.CurrentVersion != nil {
			d.VersionID = sk.CurrentVersion.ID
			d.VersionNo = sk.CurrentVersion.VersionNo
			d.UpdatedAt = sk.CurrentVersion.CreatedAt
			_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM version_files WHERE version_id=?`, d.VersionID).Scan(&d.Files)
			d.Description = s.skillDescription(r.Context(), d.VersionID)
		}
		out = append(out, d)
	}
	// used_by: tasks whose current spec binds this skill
	tasks, _ := s.deps.Tasks.List(r.Context())
	for i := range out {
		for _, t := range tasks {
			if t.Spec == nil {
				continue
			}
			for _, b := range t.Spec.SkillBindings {
				if b.SkillID == out[i].ID {
					out[i].UsedBy++
				}
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSkillSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/skills/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	switch parts[1] {
	case "versions":
		vers, err := s.deps.Ver.ListVersions(r.Context(), version.EntitySkill, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, _ := s.deps.Ver.GetPointer(r.Context(), version.EntitySkill, id, version.PointerCurrent)
		can, _ := s.deps.Ver.GetPointer(r.Context(), version.EntitySkill, id, version.PointerCanary)
		out := []versionDTO{}
		for _, v := range vers {
			out = append(out, versionDTO{ID: v.ID, No: v.VersionNo, ParentID: v.ParentVersionID.String, Source: v.Source, Message: v.Message.String, CreatedAt: v.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"versions": out, "current": cur.String, "canary": can.String})
	case "diff":
		s.handleSkillDiff(w, r, id)
	case "files":
		s.handleSkillFiles(w, r, id)
	case "file":
		s.handleSkillFile(w, r, id)
	case "export":
		s.handleSkillExport(w, r, id)
	case "rollback":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				VersionID string `json:"version_id"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
			if body.VersionID == "" {
				writeErr(w, 400, "version_id required")
				return
			}
			if err := s.deps.Skills.Rollback(r.Context(), id, body.VersionID, "user"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
	default:
		writeErr(w, 404, "not found")
	}
}

func (s *Server) handleSkillsImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Dir     string `json:"dir"`
		StageID string `json:"stage_id"`
		Mode    string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil || (body.Dir == "" && body.StageID == "") {
		writeErr(w, 400, "stage_id or dir required")
		return
	}
	if body.StageID != "" {
		s.importStaged(w, r, body.StageID, body.Name, body.Mode)
		return
	}
	if st, err := os.Stat(body.Dir); err != nil || !st.IsDir() {
		writeErr(w, 400, "dir not found")
		return
	}
	sk, err := s.deps.Skills.ImportDir(r.Context(), body.Name, body.Dir, "user")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": sk.ID, "name": sk.Name})
}
