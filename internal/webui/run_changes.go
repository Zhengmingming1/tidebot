package webui

import (
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
