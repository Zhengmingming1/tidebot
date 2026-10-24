package webui

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Limits for the diff view: anything bigger is reported as "too large" instead of diffed.
const (
	diffMaxFileBytes = 256 << 10
	diffMaxLines     = 6000
)

type diffLine struct {
	Op   string `json:"op"` // " ", "+", "-"
	Text string `json:"text"`
	A    int    `json:"a,omitempty"` // 1-based line in the old file
	B    int    `json:"b,omitempty"` // 1-based line in the new file
}

type fileDiff struct {
	Path    string     `json:"path"`
	Status  string     `json:"status"` // added|removed|modified|same|binary|too_large
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []diffLine `json:"lines,omitempty"`
}

// LineDiff is a Myers O(ND) line diff. It returns the full edit script; callers trim context.
func LineDiff(a, b []string) []diffLine {
	n, m := len(a), len(b)
	max := n + m
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := false
	for d := 0; d <= max && !found; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	// backtrack
	var out []diffLine
	x, y := n, m
	for d := len(trace) - 1; d >= 0 && (x > 0 || y > 0); d-- {
		vv := trace[d]
		k := x - y
		var pk int
		if d == 0 {
			for x > 0 && y > 0 {
				out = append(out, diffLine{Op: " ", Text: a[x-1], A: x, B: y})
				x--
				y--
			}
			break
		}
		if k == -d || (k != d && vv[off+k-1] < vv[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := vv[off+pk]
		py := px - pk
		for x > px && y > py {
			out = append(out, diffLine{Op: " ", Text: a[x-1], A: x, B: y})
			x--
			y--
		}
		if x == px {
			out = append(out, diffLine{Op: "+", Text: b[y-1], B: y})
			y--
		} else {
			out = append(out, diffLine{Op: "-", Text: a[x-1], A: x})
			x--
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// trimContext keeps ctx unchanged lines around each change; gaps become a single "@" marker.
func trimContext(lines []diffLine, ctx int) []diffLine {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op != " " {
			for j := i - ctx; j <= i+ctx; j++ {
				if j >= 0 && j < len(lines) {
					keep[j] = true
				}
			}
		}
	}
	var out []diffLine
	gap := false
	for i, l := range lines {
		if keep[i] {
			if gap && len(out) > 0 {
				out = append(out, diffLine{Op: "@"})
			}
			gap = false
			out = append(out, l)
		} else {
			gap = true
		}
	}
	if gap && len(out) > 0 {
		out = append(out, diffLine{Op: "@"})
	}
	return out
}

func splitLines(b []byte) []string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func isText(b []byte) bool { return utf8.Valid(b) && !bytes.ContainsRune(b, 0) }

// DiffFiles compares two file maps (path → content).
func DiffFiles(oldF, newF map[string][]byte, ctx int) []fileDiff {
	paths := map[string]bool{}
	for p := range oldF {
		paths[p] = true
	}
	for p := range newF {
		paths[p] = true
	}
	var keys []string
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	out := []fileDiff{}
	for _, p := range keys {
		a, inA := oldF[p]
		b, inB := newF[p]
		fd := fileDiff{Path: p}
		switch {
		case inA && inB && bytes.Equal(a, b):
			fd.Status = "same"
			out = append(out, fd)
			continue
		case !inA:
			fd.Status = "added"
		case !inB:
			fd.Status = "removed"
		default:
			fd.Status = "modified"
		}
		if (inA && !isText(a)) || (inB && !isText(b)) {
			fd.Status = "binary"
			out = append(out, fd)
			continue
		}
		if len(a) > diffMaxFileBytes || len(b) > diffMaxFileBytes {
			fd.Status = "too_large"
			out = append(out, fd)
			continue
		}
		la, lb := splitLines(a), splitLines(b)
		if len(la) > diffMaxLines || len(lb) > diffMaxLines {
			fd.Status = "too_large"
			out = append(out, fd)
			continue
		}
		lines := LineDiff(la, lb)
		for _, l := range lines {
			if l.Op == "+" {
				fd.Added++
			} else if l.Op == "-" {
				fd.Removed++
			}
		}
		fd.Lines = trimContext(lines, ctx)
		out = append(out, fd)
	}
	return out
}

func (s *Server) skillVersionOf(r *http.Request, skillID, vid string) bool {
	if vid == "" || len(vid) > 64 {
		return false
	}
	v, err := s.deps.Ver.GetVersion(r.Context(), vid)
	return err == nil && v != nil && v.EntityType == version.EntitySkill && v.EntityID == skillID
}

// GET /api/skills/<id>/diff?a=<version>&b=<version>[&ctx=3]
func (s *Server) handleSkillDiff(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	q := r.URL.Query()
	a, b := q.Get("a"), q.Get("b")
	if !s.skillVersionOf(r, skillID, a) || !s.skillVersionOf(r, skillID, b) {
		writeErr(w, 400, "a and b must be versions of this skill")
		return
	}
	ctx := 3
	if q.Get("ctx") == "all" {
		ctx = 1 << 30
	}
	fa, err := s.deps.Skills.Files(r.Context(), a)
	if err != nil {
		writeErr(w, 500, "cannot read version files")
		return
	}
	fb, err := s.deps.Skills.Files(r.Context(), b)
	if err != nil {
		writeErr(w, 500, "cannot read version files")
		return
	}
	files := DiffFiles(fa, fb, ctx)
	add, rem := 0, 0
	for _, f := range files {
		add += f.Added
		rem += f.Removed
	}
	writeJSON(w, 200, map[string]any{"a": a, "b": b, "files": files, "added": add, "removed": rem})
}

// GET /api/skills/<id>/files?v=<version> lists paths and sizes (no content).
func (s *Server) handleSkillFiles(w http.ResponseWriter, r *http.Request, skillID string) {
	v := r.URL.Query().Get("v")
	if !s.skillVersionOf(r, skillID, v) {
		writeErr(w, 400, "v must be a version of this skill")
		return
	}
	fs, err := s.deps.Skills.Files(r.Context(), v)
	if err != nil {
		writeErr(w, 500, "cannot read version files")
		return
	}
	type fe struct {
		Path string `json:"path"`
		Size int    `json:"size"`
	}
	out := []fe{}
	for p, b := range fs {
		out = append(out, fe{p, len(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, 200, out)
}

// skillFileView is one file of a version as the file browser shows it. Text comes back
// as stored (CRLF kept); the client escapes it. Binary and very large files carry no text.
type skillFileView struct {
	Path  string `json:"path"`
	Size  int    `json:"size"`
	Kind  string `json:"kind"`           // text | binary | too_large
	Text  string `json:"text,omitempty"` // only for kind=text
	Lines int    `json:"lines"`
	EOL   string `json:"eol,omitempty"` // lf | crlf | mixed ("" when there is no line break)
}

func viewSkillFile(path string, b []byte) skillFileView {
	fv := skillFileView{Path: path, Size: len(b)}
	switch {
	case !isText(b):
		fv.Kind = "binary"
		return fv
	case len(b) > diffMaxFileBytes:
		fv.Kind = "too_large"
		return fv
	}
	fv.Kind = "text"
	fv.Text = string(b)
	crlf := bytes.Count(b, []byte("\r\n"))
	lf := bytes.Count(b, []byte("\n"))
	switch {
	case lf == 0:
	case crlf == lf:
		fv.EOL = "crlf"
	case crlf == 0:
		fv.EOL = "lf"
	default:
		fv.EOL = "mixed"
	}
	if len(b) > 0 {
		fv.Lines = lf
		if b[len(b)-1] != '\n' {
			fv.Lines++
		}
	}
	return fv
}

// GET /api/skills/<id>/file?v=<version>&path=<file> returns one file of that version for
// reading. The path must be a key of the version's own file list, so nothing outside the
// stored version can be named (no "..", no absolute paths).
func (s *Server) handleSkillFile(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	q := r.URL.Query()
	v, p := q.Get("v"), q.Get("path")
	if !s.skillVersionOf(r, skillID, v) {
		writeErr(w, 400, "v must be a version of this skill")
		return
	}
	if p == "" || len(p) > 1024 {
		writeErr(w, 400, "path required")
		return
	}
	fs, err := s.deps.Skills.Files(r.Context(), v)
	if err != nil {
		writeErr(w, 500, "cannot read version files")
		return
	}
	b, ok := fs[p]
	if !ok {
		writeErr(w, 404, "file not in this version")
		return
	}
	writeJSON(w, 200, viewSkillFile(p, b))
}
