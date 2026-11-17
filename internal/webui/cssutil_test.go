package webui

import (
	"regexp"
	"strings"
	"testing"
)

// cssBlockBody returns the contents of the first rule whose selector contains
// sel (whitespace around commas is ignored). Used so contrast, appearance and
// layout tests can read the shipped, minified stylesheet.
func cssBlockBody(t *testing.T, css, sel string) string {
	t.Helper()
	body, ok := findCSSBlock(css, sel)
	if !ok {
		t.Fatalf("CSS block not found: %s", sel)
	}
	return body
}

func findCSSBlock(css, sel string) (string, bool) {
	parts := strings.Split(sel, ",")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(strings.TrimSpace(p))
	}
	pat := regexp.MustCompile(`(?m)(?:^|[}\n{])` + strings.Join(parts, `\s*,\s*`) + `\s*(?:,[^{]*)?\{`)
	loc := pat.FindStringIndex(css)
	if loc == nil {
		return "", false
	}
	// The match ends at '{'.
	return cssBraceInner(css, loc[1]-1)
}

func cssBraceInner(css string, openIdx int) (string, bool) {
	if openIdx < 0 || openIdx >= len(css) || css[openIdx] != '{' {
		return "", false
	}
	depth := 0
	var inStr byte
	for i := openIdx; i < len(css); i++ {
		c := css[i]
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = c
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[openIdx+1 : i], true
			}
		}
	}
	return "", false
}

func cssAtRuleBody(css, atPattern string) []string {
	re := regexp.MustCompile(atPattern)
	var bodies []string
	for _, loc := range re.FindAllStringIndex(css, -1) {
		j := strings.IndexByte(css[loc[0]:], '{')
		if j < 0 {
			continue
		}
		if body, ok := cssBraceInner(css, loc[0]+j); ok {
			bodies = append(bodies, body)
		}
	}
	return bodies
}
