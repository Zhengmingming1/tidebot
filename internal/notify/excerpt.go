package notify

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Chat robots get the start of a run's final answer, not all of it: a short excerpt reads well
// in a group chat, stays far below every robot's size limit (WeCom text: 2048 bytes) and the
// full answer is one click away on the run page. Email gets the whole answer.
const (
	ExcerptRunes = 360
	ExcerptLines = 8
)

var (
	mdImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	mdStrong   = regexp.MustCompile(`(\*\*|__)(\S(?:.*?\S)?)(\*\*|__)`)
	mdStrike   = regexp.MustCompile(`~~(\S(?:.*?\S)?)~~`)
	mdEm       = regexp.MustCompile(`(^|[\s(])\*(\S(?:[^*]*\S)?)\*`)
	mdCode     = regexp.MustCompile("`([^`]+)`")
	mdHeading  = regexp.MustCompile(`^#{1,6}\s+`)
	mdBullet   = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	mdTask     = regexp.MustCompile(`^(\s*)[-*+]\s+\[( |x|X)\]\s+`)
	mdRule     = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	mdTableSep = regexp.MustCompile(`^[\s|:-]*-[\s|:-]*$`) // "|---|:-:|" (the line holds a "|", checked below)
	mdQuote    = regexp.MustCompile(`^\s*>\s?`)
	mdSpaces   = regexp.MustCompile(`[ \t]{2,}`)
)

// AnswerExcerpt turns the start of a Markdown answer into plain text for a chat message:
// headings, emphasis, code fences, quotes and table rules lose their marks, bullets become
// "•", links keep their address ("text (https://…)"). At most ExcerptLines non-empty lines and
// ExcerptRunes characters are kept; cut reports that something was left out.
func AnswerExcerpt(md string) (text string, cut bool) {
	md = strings.ReplaceAll(strings.ReplaceAll(md, "\r\n", "\n"), "\r", "\n")
	var out []string
	runes, fence, blank := 0, false, false
	lines := strings.Split(strings.TrimSpace(md), "\n")
	for _, ln := range lines {
		t := strings.TrimRight(ln, " \t")
		if s := strings.TrimSpace(t); strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			fence = !fence
			continue
		}
		if !fence {
			if t = flattenMarkdownLine(t); t == "\x00" { // a rule or a table's |---| line: gone
				continue
			}
		}
		if strings.TrimSpace(t) == "" {
			blank = len(out) > 0
			continue
		}
		if len(out) == ExcerptLines {
			return strings.Join(out, "\n"), true
		}
		n := utf8.RuneCountInString(t)
		if runes+n > ExcerptRunes {
			if room := ExcerptRunes - runes; room >= 24 || len(out) == 0 {
				out = append(out, strings.TrimRight(string([]rune(t)[:max(room, 1)]), " ")+"…")
			} else if len(out) > 0 {
				out[len(out)-1] += " …"
			}
			return strings.Join(out, "\n"), true
		}
		if blank && len(out) > 0 { // keep paragraph breaks, but never two blank lines
			out = append(out, "")
		}
		blank = false
		out = append(out, t)
		runes += n
	}
	return strings.Join(out, "\n"), false
}

func flattenMarkdownLine(t string) string {
	if mdRule.MatchString(t) || (strings.Contains(t, "|") && mdTableSep.MatchString(t)) {
		return "\x00"
	}
	for mdQuote.MatchString(t) {
		t = mdQuote.ReplaceAllString(t, "")
	}
	t = mdHeading.ReplaceAllString(t, "")
	if m := mdTask.FindStringSubmatch(t); m != nil {
		box := "☐ "
		if m[2] != " " {
			box = "☑ "
		}
		t = m[1] + box + t[len(m[0]):]
	} else {
		t = mdBullet.ReplaceAllString(t, "$1• ")
	}
	if s := strings.TrimSpace(t); strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|") && len(s) > 1 {
		cells := strings.Split(strings.Trim(s, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		t = strings.Join(cells, " · ")
	}
	t = mdImage.ReplaceAllString(t, "$1")
	t = mdLink.ReplaceAllStringFunc(t, func(m string) string {
		p := mdLink.FindStringSubmatch(m)
		if (strings.HasPrefix(p[2], "https://") || strings.HasPrefix(p[2], "http://")) && p[1] != p[2] {
			return p[1] + " (" + p[2] + ")"
		}
		return p[1]
	})
	t = mdCode.ReplaceAllString(t, "$1")
	t = mdStrong.ReplaceAllString(t, "$2")
	t = mdStrike.ReplaceAllString(t, "$1")
	t = mdEm.ReplaceAllString(t, "$1$2")
	return mdSpaces.ReplaceAllString(t, " ")
}
