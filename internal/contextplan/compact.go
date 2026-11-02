package contextplan

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Compact is an explicitly lossy extract, never used for skill/safety rules.
// The original stays in the workflow snapshot and in the node's staged artifact.
func Compact(body, ref string, maxTokens int) string {
	if Estimate([]byte(body)) <= maxTokens {
		return body
	}
	header := fmt.Sprintf("[Lossy context extract; original: %s; sha256: %s]\n", ref, Hash(body))
	footer := "\n[Omitted lines remain in the original artifact; inspect it before drawing conclusions.]"
	remaining := maxTokens - Estimate([]byte(header+footer))
	if remaining <= 0 {
		return header + footer
	}
	lines := strings.Split(body, "\n")
	chosen := map[int]bool{}
	for i, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "fail") || strings.Contains(l, "error") || strings.Contains(l, "panic") || strings.Contains(l, "exception") {
			for j := max(0, i-1); j <= min(len(lines)-1, i+2); j++ {
				chosen[j] = true
			}
		}
	}
	var out strings.Builder
	add := func(i int) {
		if remaining <= 0 {
			return
		}
		text := fmt.Sprintf("L%d: %s\n", i+1, lines[i])
		text = fit(text, remaining)
		out.WriteString(text)
		remaining -= Estimate([]byte(text))
	}
	// Failure evidence takes precedence over boilerplate at the head of a log.
	for i := range lines {
		if chosen[i] {
			add(i)
		}
	}
	for _, i := range []int{0, 1, len(lines) - 2, len(lines) - 1} {
		if i >= 0 && i < len(lines) && !chosen[i] {
			chosen[i] = true
			add(i)
		}
	}
	return header + out.String() + footer
}

func fit(s string, budget int) string {
	if Estimate([]byte(s)) <= budget {
		return s
	}
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if Estimate([]byte(s[:mid])) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	for lo > 0 && !utf8.ValidString(s[:lo]) {
		lo--
	}
	return s[:lo]
}
