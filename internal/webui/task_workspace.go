package webui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// maxWorkspacePath caps a typed working folder (Windows long paths are ~32K, but nobody types that).
const maxWorkspacePath = 1024

// Problems with a typed working folder. The page shows them in its own language; the
// save answer carries the English text.
var (
	errWsRelative = errors.New("working folder: use a full path (e.g. /Users/me/project or C:\\Users\\me\\project)")
	errWsRoot     = errors.New("working folder: a drive or filesystem root is too broad")
	errWsInternal = errors.New("working folder: can't be inside tidebot's own data folder")
	errWsNotDir   = errors.New("working folder: that path is a file, not a folder")
	errWsTooLong  = errors.New("working folder: path too long")
)

func wsProblem(err error) string {
	switch err {
	case errWsRelative:
		return "relative"
	case errWsRoot:
		return "root"
	case errWsInternal:
		return "internal"
	case errWsNotDir:
		return "not_dir"
	case errWsTooLong:
		return "too_long"
	}
	return ""
}

// normalizeWorkspace turns what the user typed or picked into the folder a task keeps.
// "" means the per-task default. Accepted: absolute paths, "~" / "~/x" / "~\x", and
// paths pasted with surrounding quotes (Explorer's "Copy as path"). The result is
// cleaned with the OS separator. Rejected: relative paths, a filesystem / drive root,
// anything inside tidebot's home (keys, database) other than the task's own default
// folder or the shared data folder, which both mean "default", and existing files.
func (s *Server) normalizeWorkspace(raw, taskID string) (string, error) {
	p := strings.TrimSpace(raw)
	if len(p) >= 2 && (p[0] == '"' && p[len(p)-1] == '"' || p[0] == '\'' && p[len(p)-1] == '\'') {
		p = strings.TrimSpace(p[1 : len(p)-1])
	}
	if p == "" {
		return "", nil
	}
	if len(p) > maxWorkspacePath || strings.ContainsRune(p, 0) {
		return "", errWsTooLong
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errWsRelative
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", errWsRelative
	}
	p = filepath.Clean(p)
	if filepath.Dir(p) == p {
		return "", errWsRoot
	}
	if cfg := s.deps.Cfg; cfg != nil && cfg.HomeDir != "" {
		if samePath(p, cfg.DataDir) || (taskID != "" && samePath(p, guard.TaskWorkspace(cfg.HomeDir, taskID))) {
			return "", nil
		}
		if within(p, cfg.HomeDir) {
			return "", errWsInternal
		}
	}
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return "", errWsNotDir
	}
	return p, nil
}

// caseFoldFS: macOS and Windows file systems ignore case by default.
var caseFoldFS = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// samePath compares cleaned paths; case-insensitively where the file system usually is.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if caseFoldFS {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within: p is dir or below it.
func within(p, dir string) bool {
	dir = filepath.Clean(dir)
	if caseFoldFS {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// handleWorkspaceCheck: GET /api/tasks/workspace-check?path=…&id=… → what saving this
// folder would do, so the form can say it before Save:
// {"default": <folder used when empty>, "path": <cleaned>, "exists", "problem"}.
// Without an id the default is shown as <home>/workspaces/… (the id is made on create).
func (s *Server) handleWorkspaceCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id != "" {
		if _, err := s.deps.Tasks.Get(r.Context(), id); err != nil {
			id = ""
		}
	}
	out := map[string]any{"default": "", "path": "", "exists": false, "problem": ""}
	if cfg := s.deps.Cfg; cfg != nil && cfg.HomeDir != "" {
		if id != "" {
			out["default"] = guard.TaskWorkspace(cfg.HomeDir, id)
		} else {
			out["default"] = filepath.Join(cfg.HomeDir, "workspaces") + string(filepath.Separator) + "…"
		}
	}
	p, err := s.normalizeWorkspace(q.Get("path"), id)
	if err != nil {
		out["problem"] = wsProblem(err)
		writeJSON(w, 200, out)
		return
	}
	out["path"] = p
	if p != "" {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out["exists"] = true
		}
	}
	writeJSON(w, 200, out)
}

// handlePickFolder: GET → {"available"}; POST {"start": <folder>} → {"path"} or
// {"canceled": true} from the native folder dialog (desktop app only, 501 elsewhere).
// start only sets where the dialog opens; it is used when it is an existing folder.
func (s *Server) handlePickFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]bool{"available": s.PickFolder != nil})
		return
	case http.MethodPost:
	default:
		writeErr(w, 405, "method")
		return
	}
	if s.PickFolder == nil {
		writeErr(w, 501, "not available in the browser")
		return
	}
	var body struct {
		Start string `json:"start"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body)
	start := ""
	if st := strings.TrimSpace(body.Start); st != "" && len(st) <= maxWorkspacePath && filepath.IsAbs(st) {
		if fi, err := os.Stat(st); err == nil && fi.IsDir() {
			start = filepath.Clean(st)
		}
	}
	p, err := s.PickFolder(start)
	if err != nil {
		writeErr(w, 500, "dialog failed")
		return
	}
	if p == "" {
		writeJSON(w, 200, map[string]bool{"canceled": true})
		return
	}
	writeJSON(w, 200, map[string]string{"path": p})
}
