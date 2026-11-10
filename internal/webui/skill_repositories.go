package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

type repositoryGit interface {
	Scan(context.Context, string, string, string, string) (skillrepo.Snapshot, error)
	ReadSkill(context.Context, string, string, string) (map[string][]byte, error)
	RemoveCache(context.Context, string) error
}
type repositoryStage struct {
	RepositoryID string
	Revision     int
	Commit       string
	Candidate    skillrepo.Candidate
	Current      string
	Expires      time.Time
}
type repositoryHTTPState struct {
	mu     sync.Mutex
	git    repositoryGit
	locks  map[string]chan struct{}
	stages map[string]repositoryStage
}

func (s *Server) repositoryClient() repositoryGit {
	s.repositories.mu.Lock()
	defer s.repositories.mu.Unlock()
	if s.repositories.git == nil {
		s.repositories.git = skillrepo.New(filepath.Join(s.deps.Cfg.DataDir, "skill-repositories"))
	}
	return s.repositories.git
}
func (s *Server) lockRepository(ctx context.Context, key string) (func(), error) {
	s.repositories.mu.Lock()
	if s.repositories.locks == nil {
		s.repositories.locks = map[string]chan struct{}{}
	}
	ch := s.repositories.locks[key]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.repositories.locks[key] = ch
	}
	s.repositories.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Typed repository errors share the UI catalog with parsed workflow errors.
var repositoryErrorCodes = []string{
	"repository_error", "repo_git_missing", "repo_authentication", "repo_ssh_host_key", "repo_certificate", "repo_network", "repo_ref_not_found", "repo_invalid_url", "repo_invalid_ref", "repo_invalid_path", "repo_cache_limit", "repo_scan_limit", "repo_skill_size_limit", "repo_unsupported_file", "repo_unsafe_path", "repo_path_collision", "repo_lfs_unsupported", "repo_invalid_frontmatter", "repo_snapshot_missing", "repo_skill_missing", "repo_git_failed", "repo_conflict", "repo_name_conflict", "repo_not_found", "repo_canceled", "repo_timeout", "repo_invalid_id", "repo_invalid_snapshot", "repo_path_not_found",
}

func repositoryScanErrorCode(err error) string {
	var ge *skillrepo.Error
	code := "repository_error"
	if errors.As(err, &ge) {
		code = "repo_" + ge.Code
	}
	if errors.Is(err, context.Canceled) {
		code = "repo_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "repo_timeout"
	}
	for _, known := range repositoryErrorCodes {
		if code == known {
			return code
		}
	}
	return "repository_error"
}
func (s *Server) repositoryError(w http.ResponseWriter, err error) {
	status, code := 400, "repository_error"
	var gitError *skillrepo.Error
	var conflict *skillstore.RepositoryNameConflict
	switch {
	case errors.As(err, &gitError):
		code = "repo_" + gitError.Code
	case errors.As(err, &conflict):
		writeJSON(w, 409, map[string]any{"error": err.Error(), "code": "repo_name_conflict", "suggested_name": conflict.SuggestedName, "error_info": errInfo{Code: "repo_name_conflict"}})
		return
	case errors.Is(err, skillstore.ErrRepositoryConflict):
		status, code = 409, "repo_conflict"
	case errors.Is(err, skillstore.ErrRepositoryNotFound), errors.Is(err, sql.ErrNoRows):
		status, code = 404, "repo_not_found"
	case errors.Is(err, context.Canceled):
		status, code = 408, "repo_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = 408, "repo_timeout"
	}
	writeJSON(w, status, map[string]any{"error": err.Error(), "code": code, "error_info": errInfo{Code: code}})
}
func (s *Server) handleSkillRepositories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out, err := s.deps.Skills.ListRepositories(r.Context())
		if err != nil {
			s.repositoryError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		s.saveRepository(w, r, "")
	default:
		writeErr(w, 405, "GET or POST only")
	}
}
func (s *Server) saveRepository(w http.ResponseWriter, r *http.Request, repoID string) {
	var in skillstore.Repository
	if decodeManifestRequest(w, r, &in) != nil {
		writeErr(w, 400, "invalid repository JSON")
		return
	}
	in.ID = repoID
	u, err := skillrepo.ValidateURL(in.URL)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	in.URL = u
	in.Ref = strings.TrimSpace(in.Ref)
	if err = skillrepo.ValidateRef(in.Ref); err != nil {
		s.repositoryError(w, err)
		return
	}
	in.Root, err = skillrepo.ValidateRoot(strings.TrimSpace(in.Root))
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	if in.Root == "." {
		in.Root = ""
	}
	unlock, err := s.lockRepository(r.Context(), repoID)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	defer unlock()
	out, err := s.deps.Skills.SaveRepository(r.Context(), in)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	status := 200
	if repoID == "" {
		status = 201
	}
	writeJSON(w, status, out)
}
func (s *Server) handleSkillRepositorySub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/skill-repositories/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !idRe.MatchString(parts[0]) {
		writeErr(w, 404, "not found")
		return
	}
	repoID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodPut {
		s.saveRepository(w, r, repoID)
		return
	}
	allowed := len(parts) == 1 && (r.Method == http.MethodGet || r.Method == http.MethodDelete) || len(parts) == 2 && ((parts[1] == "skills" && r.Method == http.MethodGet) || ((parts[1] == "refresh" || parts[1] == "preview" || parts[1] == "install" || parts[1] == "clear-cache") && r.Method == http.MethodPost))
	if !allowed {
		writeErr(w, 405, "method not allowed")
		return
	}
	unlock, err := s.lockRepository(r.Context(), repoID)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	defer unlock()
	repo, err := s.deps.Skills.GetRepository(r.Context(), repoID)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, repo)
			return
		}
		// Remove files first; a failed cleanup leaves the visible record available for retry.
		if err = s.repositoryClient().RemoveCache(r.Context(), repoID); err == nil {
			err = s.deps.Skills.DeleteRepository(r.Context(), repoID)
		}
		if err != nil {
			s.repositoryError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	switch parts[1] {
	case "refresh":
		snap, e := s.repositoryClient().Scan(r.Context(), repo.ID, repo.URL, repo.Ref, repo.Root)
		if e != nil {
			// Cancellation must not erase the last successful directory or its time.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.deps.Skills.RecordRepositoryScan(ctx, repo.ID, repo.Revision, "", nil, e.Error(), repositoryScanErrorCode(e))
			s.repositoryError(w, e)
			return
		}
		data, e := json.Marshal(snap.Candidates)
		if e == nil {
			e = s.deps.Skills.RecordRepositoryScan(r.Context(), repo.ID, repo.Revision, snap.Commit, data, "")
		}
		if e != nil {
			s.repositoryError(w, e)
			return
		}
		repo, e = s.deps.Skills.GetRepository(r.Context(), repo.ID)
		if e != nil {
			s.repositoryError(w, e)
			return
		}
		writeJSON(w, 200, repo)
	case "skills":
		candidates, e := s.repositoryCandidates(r.Context(), repo)
		if e != nil {
			s.repositoryError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"repository": repo, "candidates": candidates})
	case "preview":
		s.previewRepositorySkill(w, r, repo)
	case "install":
		s.installRepositorySkill(w, r, repo)
	case "clear-cache":
		if err = s.repositoryClient().RemoveCache(r.Context(), repoID); err != nil {
			s.repositoryError(w, err)
			return
		}
		s.repositories.mu.Lock()
		for key, p := range s.repositories.stages {
			if p.RepositoryID == repoID {
				delete(s.repositories.stages, key)
			}
		}
		s.repositories.mu.Unlock()
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

type repositoryCandidate struct {
	skillrepo.Candidate
	Source  *skillstore.RepositorySource `json:"source,omitempty"`
	Removed bool                         `json:"removed,omitempty"`
}

func (s *Server) repositoryCandidates(ctx context.Context, repo skillstore.Repository) ([]repositoryCandidate, error) {
	var candidates []skillrepo.Candidate
	if err := json.Unmarshal(repo.Candidates, &candidates); err != nil {
		return nil, err
	}
	sources, err := s.deps.Skills.ListRepositorySources(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	byPath := map[string]*skillstore.RepositorySource{}
	for i := range sources {
		source := &sources[i]
		byPath[source.Path] = source
	}
	out := []repositoryCandidate{}
	for _, c := range candidates {
		if c.Path == "." {
			c.Path = ""
		}
		source := byPath[c.Path]
		if source != nil {
			source.UpdateAvailable = source.UpdateAvailable || source.TreeHash != c.TreeHash
			delete(byPath, c.Path)
		}
		out = append(out, repositoryCandidate{Candidate: c, Source: source})
	}
	// A removed upstream directory remains visible instead of resembling an uninstalled skill.
	for _, source := range byPath {
		source.SourceRemoved = true
		out = append(out, repositoryCandidate{Candidate: skillrepo.Candidate{Path: source.Path, Name: source.Name, Error: "source directory is no longer present"}, Source: source, Removed: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (s *Server) previewRepositorySkill(w http.ResponseWriter, r *http.Request, repo skillstore.Repository) {
	var in struct {
		Path   string `json:"path"`
		Commit string `json:"commit"`
	}
	if decodeManifestRequest(w, r, &in) != nil {
		writeErr(w, 400, "invalid preview JSON")
		return
	}
	if in.Path == "." {
		in.Path = ""
	}
	if in.Commit == "" || in.Commit != repo.Commit {
		s.repositoryError(w, skillstore.ErrRepositoryConflict)
		return
	}
	candidates, err := s.repositoryCandidates(r.Context(), repo)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	var found *repositoryCandidate
	for i := range candidates {
		if candidates[i].Path == in.Path {
			found = &candidates[i]
			break
		}
	}
	if found == nil || found.Error != "" {
		s.repositoryError(w, fmt.Errorf("skill directory is missing or cannot be installed"))
		return
	}
	files, err := s.repositoryClient().ReadSkill(r.Context(), repo.ID, in.Commit, in.Path)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	tree := []previewFile{}
	var size int64
	for path, data := range files {
		tree = append(tree, previewFile{Path: path, Size: int64(len(data))})
		size += int64(len(data))
	}
	sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
	old := map[string][]byte{}
	current := ""
	usages := []repositoryUsage{}
	if found.Source != nil {
		current = found.Source.CurrentVersionID
		old, err = s.deps.Skills.Files(r.Context(), current)
		if err == nil {
			usages, err = s.repositoryUsages(r.Context(), found.Source.SkillID)
		}
		if err != nil {
			s.repositoryError(w, err)
			return
		}
	}
	name := found.Name
	if found.Source != nil {
		name = found.Source.Name
	}
	stageID := id.New("repo_stage")
	s.repositories.mu.Lock()
	if s.repositories.stages == nil {
		s.repositories.stages = map[string]repositoryStage{}
	}
	for key, p := range s.repositories.stages {
		if time.Now().After(p.Expires) {
			delete(s.repositories.stages, key)
		}
	}
	if len(s.repositories.stages) >= 256 {
		s.repositories.mu.Unlock()
		s.repositoryError(w, fmt.Errorf("too many active previews; wait for older previews to expire"))
		return
	}
	s.repositories.stages[stageID] = repositoryStage{RepositoryID: repo.ID, Revision: repo.Revision, Commit: in.Commit, Candidate: found.Candidate, Current: current, Expires: time.Now().Add(30 * time.Minute)}
	s.repositories.mu.Unlock()
	writeJSON(w, 200, map[string]any{"review": capability.ReviewImport(files), "stage_id": stageID, "path": in.Path, "commit": in.Commit, "name": name, "description": found.Description, "skill_md": string(files["SKILL.md"]), "tree": tree, "files": len(files), "size": size, "warning": found.Warning, "source": found.Source, "expected_current_version_id": current, "diff": DiffFiles(old, files, 3), "used_by": usages})
}
func (s *Server) installRepositorySkill(w http.ResponseWriter, r *http.Request, repo skillstore.Repository) {
	var in struct {
		StageID  string `json:"stage_id"`
		Path     string `json:"path"`
		Commit   string `json:"commit"`
		Name     string `json:"name"`
		Copy     bool   `json:"copy"`
		Expected string `json:"expected_current_version_id"`
		// ConfirmRisk: the user saw the high-risk findings and chose to install anyway
		ConfirmRisk bool `json:"confirm_risk"`
	}
	if decodeManifestRequest(w, r, &in) != nil {
		writeErr(w, 400, "invalid install JSON")
		return
	}
	s.repositories.mu.Lock()
	p, ok := s.repositories.stages[in.StageID]
	s.repositories.mu.Unlock()
	if !ok || time.Now().After(p.Expires) || p.RepositoryID != repo.ID || p.Revision != repo.Revision || p.Current != in.Expected {
		s.repositoryError(w, skillstore.ErrRepositoryConflict)
		return
	}
	files, err := s.repositoryClient().ReadSkill(r.Context(), repo.ID, p.Commit, p.Candidate.Path)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	if !writeImportRisk(w, files, in.ConfirmRisk) {
		return
	}
	out, err := s.deps.Skills.InstallRepositorySkill(r.Context(), skillstore.RepositoryInstallInput{RepositoryID: repo.ID, RepositoryRevision: repo.Revision, Path: p.Candidate.Path, Commit: p.Commit, TreeHash: p.Candidate.TreeHash, Name: in.Name, ExpectedCurrentVersionID: p.Current, Files: files, Copy: in.Copy})
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	s.repositories.mu.Lock()
	delete(s.repositories.stages, in.StageID)
	s.repositories.mu.Unlock()
	writeJSON(w, 200, out)
}
func (s *Server) handleRepositorySource(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	source, err := s.deps.Skills.RepositorySourceForSkill(r.Context(), skillID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, nil)
		return
	}
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	writeJSON(w, 200, source)
}
func (s *Server) handleRepositoryApply(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	var in struct {
		VersionID string `json:"version_id"`
		Expected  string `json:"expected_current_version_id"`
	}
	if decodeManifestRequest(w, r, &in) != nil {
		writeErr(w, 400, "invalid apply JSON")
		return
	}
	if err := s.deps.Skills.ApplyRepositoryVersion(r.Context(), skillID, in.VersionID, in.Expected); err != nil {
		s.repositoryError(w, err)
		return
	}
	source, err := s.deps.Skills.RepositorySourceForSkill(r.Context(), skillID)
	if err != nil {
		s.repositoryError(w, err)
		return
	}
	writeJSON(w, 200, source)
}

type repositoryUsage struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

func (s *Server) repositoryUsages(ctx context.Context, skillID string) ([]repositoryUsage, error) {
	out := []repositoryUsage{}
	add := func(kind, id, name string, bindings []tasksvc.SkillBinding) {
		for _, b := range bindings {
			if b.SkillID == skillID {
				mode := b.Mode
				if mode == "" {
					mode = "follow"
				}
				out = append(out, repositoryUsage{kind, id, name, mode})
			}
		}
	}
	if s.deps.Tasks != nil {
		tasks, err := s.deps.Tasks.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			if t.Spec != nil && t.Task.Status != "archived" {
				add("task", t.Task.ID, t.Task.Name, t.Spec.SkillBindings)
			}
		}
	}
	sets, err := s.deps.Skills.ListSets(ctx)
	if err != nil {
		return nil, err
	}
	for _, set := range sets {
		add("skill_set", set.ID, set.Name, set.SkillBindings)
	}
	if s.deps.Workflows != nil {
		flows, err := s.deps.Workflows.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range flows {
			for _, n := range f.Definition.Nodes {
				add("workflow_node", f.ID, f.Name+" / "+n.Name, n.SkillBindings)
			}
		}
	}
	if s.deps.Auth != nil {
		for _, b := range s.loadButtons(ctx) {
			if b.SkillID == skillID {
				mode := "follow"
				if b.SkillVerID != "" {
					mode = "pinned"
				}
				out = append(out, repositoryUsage{"button", b.ID, b.Label, mode})
			}
		}
	}
	return out, nil
}
