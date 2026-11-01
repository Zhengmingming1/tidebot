package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// What an edit run changed in its folder (git work trees; see internal/gitsnap).
// The run detail carries the file list (file_changes); the diff itself is only
// read when the page opens a file or downloads the patch.

type patchLine struct {
	Op   string `json:"op"` // " " | "+" | "-" | "@" (hunk header) | "\\" (no newline at end)
	A    int    `json:"a,omitempty"`
	B    int    `json:"b,omitempty"`
	Text string `json:"text"`
}

type patchFile struct {
	Lines  []patchLine `json:"lines"`
	Binary bool        `json:"binary,omitempty"`
	Cut    bool        `json:"cut,omitempty"` // more lines than shown
}

const (
	patchMaxLines = 4000
	patchMaxLine  = 2000 // runes per line
)

// handleRunChanges: GET /api/runs/{id}/changes → {"diffs": [...per file, in the
// order of file_changes.files], "patch": unified diff, "patch_cut": bool}.
func (s *Server) handleRunChanges(w http.ResponseWriter, r *http.Request, id string) {
	var patch, fc string
	if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT file_patch, file_changes FROM runs WHERE id=?`, id).Scan(&patch, &fc); err != nil {
		writeErr(w, 404, "run not found")
		return
	}
	writeJSON(w, 200, map[string]any{
		"diffs":     ParsePatch(patch),
		"patch":     patch,
		"patch_cut": strings.Contains(fc, `"patch_cut":true`),
	})
}

// UndoError is why "Undo changes" (or putting them back) was refused. Code is one of
// none (nothing recorded) | running | cut (patch over the size cap) | binary (an
// older run's patch without binary data) | state (already undone / not undone) |
// dir (folder gone or not allowed) | busy (an edit run is going there) | conflict
// (a file was changed since; Detail is git's reason) | file (path not one of the
// run's files) | many (more files than listed: one-by-one undo not offered).
type UndoError struct{ Code, Detail string }

func (e *UndoError) Error() string {
	if e.Detail != "" {
		return "cannot undo the run's changes (" + e.Code + "): " + e.Detail
	}
	return "cannot undo the run's changes (" + e.Code + ")"
}

// UndoResult is the state after an undo / put back: UndoneAt is set once the whole
// run is back out; otherwise UndoneFiles lists the files taken back one by one.
type UndoResult struct {
	UndoneAt    string
	UndoneFiles []string
}

// UndoCheck says which undo buttons of a run would work now (nothing written). Code
// set: undo isn't possible at all (none | running | cut | binary | dir | busy).
// Whole: the main button's action — "ok" | "conflict"; WholeFiles names the files
// edited since. Files: each listed file's own action (undo it, or put it back when
// taken back); a file left out wasn't checked.
type UndoCheck struct {
	Code       string                   `json:"code,omitempty"`
	Whole      string                   `json:"whole,omitempty"`
	WholeFiles []string                 `json:"whole_files,omitempty"`
	Files      map[string]UndoFileCheck `json:"files"`
}

// UndoFileCheck: one file's answer; Detail is git's reason when it would not apply.
type UndoFileCheck struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// undoCheck: GET /api/runs/{id}/undo-check → UndoCheck. Read-only (git apply --check).
func (s *Server) undoCheck(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.CheckRunUndo == nil {
		writeErr(w, 503, "not available")
		return
	}
	res, err := s.deps.CheckRunUndo(r.Context(), id)
	switch {
	case errors.Is(err, ErrRunNotFound):
		writeErr(w, 404, "run not found")
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		if res.Files == nil {
			res.Files = map[string]UndoFileCheck{}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, res)
	}
}

// ErrRunNotFound: no run with that id.
var ErrRunNotFound = errors.New("run not found")

// undoRunChanges: POST /api/runs/{id}/undo-changes {"redo": bool, "path"?: file} →
// {"changes_undone_at": t, "changes_undone_files": [...]}. Takes the run's patch (or
// one file's part of it) back out of its folder (redo: applies it again); every file
// is checked first and nothing is written unless all of them still match.
func (s *Server) undoRunChanges(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.UndoRunChanges == nil {
		writeErr(w, 503, "not available")
		return
	}
	var body struct {
		Redo bool   `json:"redo"`
		Path string `json:"path"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if len(body.Path) > 4096 || strings.ContainsRune(body.Path, 0) {
		writeErr(w, 400, "bad request")
		return
	}
	res, err := s.deps.UndoRunChanges(r.Context(), id, body.Redo, body.Path)
	var ue *UndoError
	switch {
	case errors.As(err, &ue):
		writeJSON(w, 409, map[string]any{"error": ue.Error(), "code": ue.Code, "detail": ue.Detail})
	case errors.Is(err, ErrRunNotFound):
		writeErr(w, 404, "run not found")
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		files := res.UndoneFiles
		if files == nil {
			files = []string{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "changes_undone_at": res.UndoneAt, "changes_undone_files": files})
	}
}

// ParsePatch splits `git diff-tree -p` output into one entry per file section
// ("diff --git …"), each with numbered lines. A section cut off by the size cap
// just ends early.
func ParsePatch(p string) []patchFile {
	out := []patchFile{}
	if p == "" {
		return out
	}
	p = strings.ReplaceAll(p, "\r\n", "\n")
	var cur *patchFile
	var a, b int
	inHunk := false
	for _, ln := range strings.Split(strings.TrimSuffix(p, "\n"), "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			out = append(out, patchFile{Lines: []patchLine{}})
			cur, inHunk = &out[len(out)-1], false
			continue
		}
		if cur == nil {
			continue
		}
		if !inHunk || strings.HasPrefix(ln, "@@ ") {
			switch {
			case strings.HasPrefix(ln, "Binary files ") || strings.HasPrefix(ln, "GIT binary patch"):
				cur.Binary = true
			case strings.HasPrefix(ln, "@@ "):
				a, b = hunkStart(ln)
				inHunk = true
				cur.add(patchLine{Op: "@", Text: ln})
			}
			continue
		}
		if ln == "" { // an empty context line some tools write without its space
			ln = " "
		}
		l := patchLine{Op: ln[:1], Text: clipRunes(ln[1:], patchMaxLine)}
		switch l.Op {
		case " ":
			l.A, l.B = a, b
			a++
			b++
		case "-":
			l.A = a
			a++
		case "+":
			l.B = b
			b++
		case "\\":
			l.Text = strings.TrimSpace(ln[1:])
		default:
			continue
		}
		cur.add(l)
	}
	return out
}

func (f *patchFile) add(l patchLine) {
	if len(f.Lines) >= patchMaxLines {
		f.Cut = true
		return
	}
	f.Lines = append(f.Lines, l)
}

// hunkStart reads "@@ -a[,n] +b[,m] @@".
func hunkStart(h string) (int, int) {
	f := strings.Fields(h)
	if len(f) < 3 {
		return 0, 0
	}
	num := func(s string) int {
		s = strings.TrimLeft(s, "-+")
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[:i]
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	return num(f[1]), num(f[2])
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
