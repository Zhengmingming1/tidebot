package webui

import (
	"context"
	"net/http"
	"strings"
	"unicode"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// Final answer compared with the task's previous successful answer, line by line, with the
// changed words marked inside a changed line. Idea: dgtlmoon/changedetection.io
// changedetectionio/diff/__init__.py (render_nested_line_diff: a changed line pair keeps its
// line colour and the changed tokens get a darker one; a line with nothing in common is
// shown whole) and diff/tokenizers/natural_text.py (words and whitespace as tokens).
const (
	ansDiffMaxLines  = 1000 // changed middle (both sides) diffed exactly; more is shown as replaced
	ansDiffMaxTokens = 200  // per line for the word marks
)

type ansSeg struct {
	T string `json:"t"`
	X bool   `json:"x,omitempty"` // changed
}

type ansDiffLine struct {
	Op   string   `json:"op"` // " ", "+", "-"
	Text string   `json:"text"`
	A    int      `json:"a,omitempty"`
	B    int      `json:"b,omitempty"`
	Segs []ansSeg `json:"segs,omitempty"`
}

type ansDiffDTO struct {
	Prev    *answerSameDTO `json:"prev"`
	Same    bool           `json:"same"` // equal once whitespace is ignored
	Added   int            `json:"added"`
	Removed int            `json:"removed"`
	Coarse  bool           `json:"coarse,omitempty"` // too many changed lines: shown as replaced
	Lines   []ansDiffLine  `json:"lines"`
}

func (s *Server) answerDiff(w http.ResponseWriter, r *http.Request, id string) {
	var cur string
	if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT result_text FROM runs WHERE id=?`, id).Scan(&cur); err != nil {
		writeErr(w, 404, "run not found")
		return
	}
	pid, prev, at, err := s.prevAnswer(r.Context(), id)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	out := ansDiffDTO{Lines: []ansDiffLine{}}
	if pid == "" || cur == "" {
		writeJSON(w, 200, out)
		return
	}
	out.Prev = &answerSameDTO{RunID: pid, At: at}
	out.Same = db.AnswerKey(prev) == db.AnswerKey(cur)
	out.Lines, out.Coarse = AnswerDiff(prev, cur)
	for _, l := range out.Lines {
		switch l.Op {
		case "+":
			out.Added++
		case "-":
			out.Removed++
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) prevAnswer(ctx context.Context, runID string) (id, text, at string, err error) {
	return (&db.Store{DB: s.deps.DB}).PrevAnswer(ctx, runID)
}

func ansLines(s string) []string {
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// AnswerDiff returns every line of the new answer plus the removed lines of the old one, in
// order (the page folds long unchanged stretches). Trailing spaces don't count as a change.
// A changed middle of more than ansDiffMaxLines lines is shown as removed-then-added
// (coarse) instead of running the quadratic-memory diff on it.
func AnswerDiff(oldText, newText string) ([]ansDiffLine, bool) {
	a, b := ansLines(oldText), ansLines(newText)
	key := func(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i, l := range a {
		ka[i] = key(l)
	}
	for i, l := range b {
		kb[i] = key(l)
	}
	pre := 0
	for pre < len(a) && pre < len(b) && ka[pre] == kb[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && ka[len(a)-1-suf] == kb[len(b)-1-suf] {
		suf++
	}
	out := make([]ansDiffLine, 0, len(b)+8)
	for i := 0; i < pre; i++ {
		out = append(out, ansDiffLine{Op: " ", Text: b[i], A: i + 1, B: i + 1})
	}
	ma, mb := ka[pre:len(a)-suf], kb[pre:len(b)-suf]
	coarse := len(ma)+len(mb) > ansDiffMaxLines
	var mid []diffLine
	if coarse {
		for i := range ma {
			mid = append(mid, diffLine{Op: "-", A: i + 1})
		}
		for j := range mb {
			mid = append(mid, diffLine{Op: "+", B: j + 1})
		}
	} else {
		mid = LineDiff(ma, mb)
	}
	for _, l := range mid {
		d := ansDiffLine{Op: l.Op}
		if l.A > 0 {
			d.A = pre + l.A
		}
		if l.B > 0 {
			d.B = pre + l.B
		}
		if l.Op == "-" {
			d.Text = a[d.A-1]
		} else {
			d.Text = b[d.B-1]
		}
		out = append(out, d)
	}
	for i := 0; i < suf; i++ {
		ai, bi := len(a)-suf+i, len(b)-suf+i
		out = append(out, ansDiffLine{Op: " ", Text: b[bi], A: ai + 1, B: bi + 1})
	}
	markWords(out)
	return out, coarse
}

// markWords pairs the removed and added lines of each change (first with first, …) and marks
// the tokens that differ. A pair with no word in common keeps no marks: the line colour says it.
func markWords(lines []ansDiffLine) {
	for i := 0; i < len(lines); {
		if lines[i].Op == " " {
			i++
			continue
		}
		var del, add []int
		j := i
		for ; j < len(lines) && lines[j].Op != " "; j++ {
			if lines[j].Op == "-" {
				del = append(del, j)
			} else {
				add = append(add, j)
			}
		}
		for k := 0; k < len(del) && k < len(add); k++ {
			lines[del[k]].Segs, lines[add[k]].Segs = wordSegs(lines[del[k]].Text, lines[add[k]].Text)
		}
		i = j
	}
}

func wordSegs(a, b string) ([]ansSeg, []ansSeg) {
	ta, tb := wordTokens(a), wordTokens(b)
	if len(ta) > ansDiffMaxTokens || len(tb) > ansDiffMaxTokens || len(ta) == 0 || len(tb) == 0 {
		return nil, nil
	}
	ops := LineDiff(ta, tb)
	shared := false
	for _, o := range ops {
		if o.Op == " " && strings.IndexFunc(o.Text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			shared = true
			break
		}
	}
	if !shared {
		return nil, nil
	}
	var sa, sb []ansSeg
	add := func(s []ansSeg, t string, x bool) []ansSeg {
		// whitespace between two changed tokens joins them into one mark
		if n := len(s); n > 0 && (s[n-1].X == x || (strings.TrimSpace(t) == "" && s[n-1].X)) {
			s[n-1].T += t
			return s
		}
		return append(s, ansSeg{T: t, X: x})
	}
	for _, o := range ops {
		switch o.Op {
		case " ":
			sa, sb = add(sa, ta[o.A-1], false), add(sb, tb[o.B-1], false)
		case "-":
			sa = add(sa, ta[o.A-1], true)
		case "+":
			sb = add(sb, tb[o.B-1], true)
		}
	}
	return trimMarks(sa), trimMarks(sb)
}

// trimMarks moves trailing whitespace out of a mark so only the words are highlighted.
func trimMarks(s []ansSeg) []ansSeg {
	var out []ansSeg
	for _, g := range s {
		if g.X {
			t := strings.TrimRightFunc(g.T, unicode.IsSpace)
			if rest := g.T[len(t):]; rest != "" {
				if t != "" {
					out = append(out, ansSeg{T: t, X: true})
				}
				if n := len(out); n > 0 && !out[n-1].X {
					out[n-1].T += rest
				} else {
					out = append(out, ansSeg{T: rest})
				}
				continue
			}
		}
		if n := len(out); n > 0 && out[n-1].X == g.X {
			out[n-1].T += g.T
			continue
		}
		out = append(out, g)
	}
	return out
}

// wordTokens splits a line into words (letters, digits, '.', ',' inside numbers), runs of
// whitespace and single other characters; each Han / kana / Hangul character is its own
// token, so a changed Chinese word is marked without its whole sentence.
func wordTokens(s string) []string {
	var out []string
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		j := i + 1
		switch {
		case unicode.IsSpace(r):
			for j < len(rs) && unicode.IsSpace(rs[j]) {
				j++
			}
		case cjkRune(r):
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			for j < len(rs) {
				c := rs[j]
				if (unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_') && !cjkRune(c) {
					j++
				} else if (c == '.' || c == ',') && j+1 < len(rs) && unicode.IsDigit(rs[j-1]) && unicode.IsDigit(rs[j+1]) {
					j++ // 1,299.50 stays one token
				} else {
					break
				}
			}
		}
		out = append(out, string(rs[i:j]))
		i = j
	}
	return out
}

func cjkRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
