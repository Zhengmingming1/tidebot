package webui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// runHit tells the runs list why a row matched the search text: which field, and a short
// one-line excerpt around the first hit. Parts alternate plain / matched text (even index
// plain, odd index matched), so the page escapes each piece and marks the odd ones without
// re-searching — the list only carries a 300-byte prompt preview, and a hit further in used
// to leave no trace on the row. Idea: CherryHQ/cherry-studio src/main/utils/searchSnippet.ts
// (a fragment around each hit, "..." where text was cut) and src/renderer/components/
// HighlightText.tsx; cline/cline apps/vscode/webview-ui/src/components/history/HistoryView.tsx
// (highlighted match regions in the history list).
type runHit struct {
	Field string   `json:"field"`           // prompt | error | task | id
	Parts []string `json:"parts,omitempty"` // empty for "task": the title already shows it
	Count int      `json:"count"`           // hits in that field (capped at hitCountMax)
}

const (
	hitBefore   = 8  // runes of context before the first hit: the cell is ~16 CJK characters wide at 1024px
	hitAfter    = 80 // runes after it; CSS ellipsis trims what doesn't fit
	hitCountMax = 99
)

// foldASCII lower-cases ASCII letters only, like SQLite's LIKE: byte offsets stay the same,
// so a hit found in the folded copy is at the same place in the original.
func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// findRunHit picks the first field that contains q, in the order the row reads best:
// prompt, error, task name, run id. nil when nothing matches (q empty, or the row matched
// some other way).
func findRunHit(d runDTO, q string) *runHit {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	fq := foldASCII(q)
	for _, f := range []struct{ name, text string }{{"prompt", d.Prompt}, {"error", d.Error}, {"task", d.TaskName}, {"id", d.ID}} {
		ft := foldASCII(f.text)
		at := strings.Index(ft, fq)
		if at < 0 {
			continue
		}
		h := &runHit{Field: f.name, Count: countHits(ft, fq)}
		if f.name != "task" {
			h.Parts = hitParts(f.text, ft, fq, at)
		}
		return h
	}
	return nil
}

func countHits(ft, fq string) int {
	n := 0
	for i := 0; n < hitCountMax; n++ {
		j := strings.Index(ft[i:], fq)
		if j < 0 {
			break
		}
		i += j + len(fq)
	}
	return n
}

// hitParts cuts the excerpt: from hitBefore runes before the first hit (but not past the
// start of its line) to hitAfter runes after it, on character boundaries, every hit inside
// marked, white space folded to single spaces, "…" where text was left out.
func hitParts(text, ft, fq string, at int) []string {
	start := at
	for n := 0; n < hitBefore && start > 0; n++ {
		_, sz := utf8.DecodeLastRuneInString(text[:start])
		if text[start-sz] == '\n' {
			break
		}
		start -= sz
	}
	end := at + len(fq)
	for n := 0; n < hitAfter && end < len(text); n++ {
		_, sz := utf8.DecodeRuneInString(text[end:])
		end += sz
	}
	// every hit that starts inside the excerpt is marked; one straddling the end is kept whole
	parts := []string{}
	plain := start
	for i := start; ; {
		k := strings.Index(ft[i:], fq)
		if k < 0 || i+k >= end {
			break
		}
		parts = append(parts, squash(text[plain:i+k]), squash(text[i+k:i+k+len(fq)]))
		i += k + len(fq)
		plain = i
		if i > end {
			end = i
		}
	}
	parts = append(parts, squash(text[plain:end]))
	if start > 0 {
		parts[0] = "…" + strings.TrimLeft(parts[0], " ")
	}
	if end < len(text) {
		parts[len(parts)-1] = strings.TrimRight(parts[len(parts)-1], " ") + "…"
	}
	return parts
}

// squash folds runs of white space (line breaks, tabs, CRLF) into one space, so the excerpt
// stays on one line on every platform.
func squash(s string) string {
	var b strings.Builder
	sp := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !sp {
				b.WriteByte(' ')
			}
			sp = true
			continue
		}
		sp = false
		b.WriteRune(r)
	}
	return b.String()
}
