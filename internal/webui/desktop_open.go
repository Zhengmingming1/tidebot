package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"

	"github.com/Zhengmingming1/tidebot/internal/guard"

	"github.com/Zhengmingming1/tidebot/internal/osopen"
)

// handleDesktopOpen shows tidebot's own folders in the OS file manager (desktop app).
// GET → {"available": bool, "os": GOOS} (the page names the button after Finder /
// File Explorer / the file manager). POST {"target": "data"} opens the data folder;
// {"target": "workspace", "id": <task id>} opens that task's working folder (tidebot's
// default one is created first, as the next run would); {"target": "saved", "id": …}
// selects a file this app just saved through the native Save dialog (the id came back
// from /api/desktop/save). The page never sends a path, so it can't make the desktop
// open anything else.
func (s *Server) handleDesktopOpen(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"available": s.OpenPath != nil, "os": runtime.GOOS})
		return
	case http.MethodPost:
	default:
		writeErr(w, 405, "method")
		return
	}
	if s.OpenPath == nil {
		writeErr(w, 501, "not available in the browser")
		return
	}
	var in struct {
		Target string `json:"target"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	var path string
	reveal := false
	switch in.Target {
	case "data":
		if s.deps.Cfg != nil {
			path = s.deps.Cfg.DataDir
		}
	case "workspace":
		v, err := s.deps.Tasks.Get(r.Context(), in.ID)
		if in.ID == "" || err != nil {
			writeErr(w, 404, "task not found")
			return
		}
		ws := v.Task.WorkspaceDir
		if v.Spec != nil && v.Spec.WorkspaceDir != "" {
			ws = v.Spec.WorkspaceDir
		}
		var def bool
		path, def = s.taskWorkspace(v.Task.ID, ws)
		if def {
			_ = os.MkdirAll(path, 0o755)
		}
	case "saved":
		path, reveal = s.saved.get(in.ID), true
	default:
		writeErr(w, 400, "unknown target")
		return
	}
	if path == "" {
		writeErr(w, 404, "not found")
		return
	}
	if err := s.OpenPath(path, reveal); err != nil {
		if errors.Is(err, osopen.ErrNotFound) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 500, "could not open the file manager")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// taskWorkspace is the folder a task's runs work in: its own (ws) or, when it has none
// (or names the data folder), the per-task folder the runner falls back to
// (see app.prepareGuard). def reports the fallback.
func (s *Server) taskWorkspace(id, ws string) (path string, def bool) {
	cfg := s.deps.Cfg
	if cfg == nil {
		return ws, false
	}
	if ws == "" || ws == cfg.DataDir {
		return guard.TaskWorkspace(cfg.HomeDir, id), true
	}
	return ws, false
}

// savedFiles remembers the last files saved through the native dialog, by random id,
// so "Show in folder" can name one without the page ever sending a path.
type savedFiles struct {
	mu    sync.Mutex
	ids   []string
	paths map[string]string
}

const maxSavedFiles = 20

func (f *savedFiles) add(p string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.paths == nil {
		f.paths = map[string]string{}
	}
	f.paths[id] = p
	f.ids = append(f.ids, id)
	for len(f.ids) > maxSavedFiles {
		delete(f.paths, f.ids[0])
		f.ids = f.ids[1:]
	}
	return id
}

func (f *savedFiles) get(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paths[id]
}
