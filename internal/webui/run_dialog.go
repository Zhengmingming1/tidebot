package webui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// RunRequest is what the Run dialog starts: a prompt, optionally with a skill and a
// working folder. Without a folder the run works in a scratch folder of tidebot's;
// with one it is read-only unless Mode is "edit".
type RunRequest struct {
	Prompt    string `json:"prompt"`
	Provider  string `json:"provider"`
	SkillID   string `json:"skill_id"`
	VersionID string `json:"version_id"`
	Workspace string `json:"workspace"`
	Mode      string `json:"mode"` // "read" (default) | "edit"
}

// ErrUnknownSkill: the skill (or version) asked for doesn't exist.
var ErrUnknownSkill = errors.New("skill not found")

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

const runPromptMax = 8000

func (b *RunRequest) validate() string {
	b.Prompt = strings.TrimSpace(b.Prompt)
	if utf8.RuneCountInString(b.Prompt) > runPromptMax {
		return "prompt too long"
	}
	if b.SkillID != "" && !idRe.MatchString(b.SkillID) || b.VersionID != "" && !idRe.MatchString(b.VersionID) {
		return "bad skill id"
	}
	if len(b.Workspace) > maxWorkspacePath || strings.ContainsRune(b.Workspace, 0) {
		return "working folder: path too long"
	}
	switch b.Mode {
	case "", "read", "edit":
	default:
		return "mode must be read or edit"
	}
	return ""
}

// runDirCheck is the Run dialog's view of a typed folder: the real path, any
// problem (see guard.CheckRunDir) and, for git work trees, the branch and how many
// paths have uncommitted changes.
func (s *Server) runDirCheck(ctx context.Context, raw string) map[string]any {
	out := map[string]any{"path": "", "exists": false, "problem": ""}
	p := guard.ExpandHome(raw)
	if p == "" {
		return out
	}
	if len(p) > maxWorkspacePath || strings.ContainsRune(p, 0) {
		out["problem"] = "too_long"
		return out
	}
	home, _ := os.UserHomeDir()
	appHome := ""
	if s.deps.Cfg != nil {
		appHome = s.deps.Cfg.HomeDir
	}
	real, prob := guard.CheckRunDir(p, home, appHome)
	if prob != "" {
		out["problem"] = prob
		if prob != guard.DirNotFound && prob != guard.DirRelative {
			out["exists"] = true
		}
		return out
	}
	out["path"], out["exists"] = real, true
	if g := gitState(ctx, real); g != nil {
		out["git"] = g
	}
	return out
}

// gitState reads a folder's git branch and count of changed paths without
// changing anything: no index refresh (GIT_OPTIONAL_LOCKS=0), no fsmonitor or
// hooks, 2s cap. nil when it isn't a work tree or
// git isn't installed.
func gitState(ctx context.Context, dir string) map[string]any {
	if !hasGitDir(dir) {
		return nil
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false",
		"-c", "core.hooksPath="+os.DevNull, "status", "--porcelain=v1", "--branch", "--no-renames", "--ignore-submodules=all")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	branch, dirty := "", 0
	for _, ln := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(ln, "## "):
			branch = strings.TrimPrefix(ln, "## ")
			if i := strings.Index(branch, "..."); i >= 0 {
				branch = branch[:i]
			}
			branch = strings.TrimPrefix(branch, "No commits yet on ")
			if i := strings.IndexByte(branch, ' '); i >= 0 {
				branch = branch[:i]
			}
		case strings.TrimSpace(ln) != "":
			dirty++
		}
	}
	return map[string]any{"branch": branch, "dirty": dirty}
}

// hasGitDir: dir or a parent holds .git (a folder or a worktree's file).
func hasGitDir(dir string) bool {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}

// handleRunDefaults: GET /api/runs/defaults?skill_id= → what the Run dialog fills in:
// the folder, mode and provider of the last run with this skill (else the last run
// with a folder), and up to 4 recent folders that are still allowed.
func (s *Server) handleRunDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	ctx := r.Context()
	skill := r.URL.Query().Get("skill_id")
	if skill != "" && !idRe.MatchString(skill) {
		writeErr(w, 400, "bad skill id")
		return
	}
	type last struct {
		Workspace string `json:"workspace"`
		Mode      string `json:"mode"`
		Provider  string `json:"provider"`
	}
	out := map[string]any{"last": nil, "recent_dirs": []string{}}
	appHome := ""
	if s.deps.Cfg != nil {
		appHome = s.deps.Cfg.HomeDir
	}
	q := `SELECT workspace_dir, run_mode, COALESCE(provider_name,'') FROM runs
WHERE trigger_kind='quick' AND workspace_dir<>'' AND run_mode<>''`
	pick := func(extra string, args ...any) *last {
		var l last
		if err := s.deps.DB.QueryRowContext(ctx, q+extra+` ORDER BY created_at DESC LIMIT 1`, args...).Scan(&l.Workspace, &l.Mode, &l.Provider); err != nil {
			return nil
		}
		return &l
	}
	var l *last
	if skill != "" {
		l = pick(` AND skill_id=?`, skill)
	}
	if l == nil {
		l = pick("")
	}
	if l != nil {
		out["last"] = l
	}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT workspace_dir, MAX(created_at) m, MAX(skill_id=?) k FROM runs
WHERE trigger_kind='quick' AND workspace_dir<>'' AND run_mode<>'' GROUP BY workspace_dir ORDER BY k DESC, m DESC LIMIT 12`, skill)
	if err == nil {
		var dirs []string
		for rows.Next() {
			var d, m string
			var k int
			if rows.Scan(&d, &m, &k) != nil {
				continue
			}
			if appHome != "" && guard.Within(d, appHome) {
				continue
			}
			if fi, err := os.Stat(d); err == nil && fi.IsDir() && len(dirs) < 4 {
				dirs = append(dirs, d)
			}
		}
		rows.Close()
		if dirs != nil {
			out["recent_dirs"] = dirs
		}
	}
	writeJSON(w, 200, out)
}
