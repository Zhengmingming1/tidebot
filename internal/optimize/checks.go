package optimize

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Check is one zero-cost static check on a proposal (final-plan §7.4).
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Limits (suggested values).
const (
	MaxSkillLines   = 500
	MaxSkillBytes   = 100 * 1024
	MaxChangedRatio = 0.30
)

var (
	reSecret      = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|xox[bap]-[A-Za-z0-9-]{10,})`)
	reInjection   = regexp.MustCompile(`(?i)(ignore (all |any )?(previous|prior|above) instructions|disregard (the )?(system|previous)|you are now|exfiltrate|send (the )?(api key|secrets?|credentials) to|curl [^\n]*\$\{?[A-Z_]*(KEY|TOKEN|SECRET))`)
	reDateOrIssue = regexp.MustCompile(`\b(20\d\d-\d\d-\d\d|#\d{2,})\b`)
)

func frontmatter(md string) (map[string]string, bool) {
	if !strings.HasPrefix(md, "---\n") {
		return nil, false
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return nil, false
	}
	out := map[string]string{}
	for _, l := range strings.Split(md[4:4+end], "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, true
}

func toolSet(s string) map[string]bool {
	m := map[string]bool{}
	s = strings.Trim(s, "[]")
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t = strings.Trim(t, `"'`); t != "" {
			m[t] = true
		}
	}
	return m
}

func skillMD(files map[string][]byte) string {
	if b, ok := files["SKILL.md"]; ok {
		return string(b)
	}
	return string(files["skill.md"])
}

func changedRatio(a, b string) float64 {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	count := map[string]int{}
	for _, l := range al {
		count[l]++
	}
	same := 0
	for _, l := range bl {
		if count[l] > 0 {
			count[l]--
			same++
		}
	}
	n := len(al)
	if len(bl) > n {
		n = len(bl)
	}
	if n == 0 {
		return 0
	}
	return float64(n-same) / float64(n)
}

// StaticChecks runs all checks. risk is low|medium|high; touchesPerms marks permission changes.
func StaticChecks(oldFiles, newFiles map[string][]byte) (checks []Check, risk string, touchesPerms bool) {
	risk = "low"
	bump := func(r string) {
		if r == "high" || (r == "medium" && risk == "low") {
			risk = r
		}
	}
	md := skillMD(newFiles)
	fm, ok := frontmatter(md)
	fmOK := ok && fm["name"] != "" && fm["description"] != ""
	checks = append(checks, Check{"frontmatter", fmOK, "needs --- name/description ---"})

	lines := strings.Count(md, "\n") + 1
	sizeOK := lines <= MaxSkillLines && len(md) <= MaxSkillBytes
	checks = append(checks, Check{"size", sizeOK, fmt.Sprintf("%d lines, %d bytes", lines, len(md))})

	ratio := changedRatio(skillMD(oldFiles), md)
	diffOK := len(oldFiles) == 0 || ratio <= MaxChangedRatio
	checks = append(checks, Check{"diff_size", diffOK, fmt.Sprintf("%.0f%% of lines changed", ratio*100)})
	if ratio > 0.15 {
		bump("medium")
	}

	oldFM, _ := frontmatter(skillMD(oldFiles))
	oldTools, newTools := toolSet(oldFM["allowed-tools"]), toolSet(fm["allowed-tools"])
	var added []string
	for t := range newTools {
		if !oldTools[t] {
			added = append(added, t)
		}
	}
	sort.Strings(added)
	var newScripts []string
	for p := range newFiles {
		if _, existed := oldFiles[p]; !existed && !strings.EqualFold(path.Ext(p), ".md") {
			newScripts = append(newScripts, p)
		}
	}
	sort.Strings(newScripts)
	mcp := strings.Contains(md, "mcp__") && !strings.Contains(skillMD(oldFiles), "mcp__")
	touchesPerms = len(added) > 0 || len(newScripts) > 0 || mcp
	checks = append(checks, Check{"permissions", !touchesPerms, strings.TrimSpace(fmt.Sprintf("new tools %v new files %v", added, newScripts))})
	if touchesPerms {
		bump("high")
	}

	var all strings.Builder
	for _, b := range newFiles {
		all.Write(b)
		all.WriteByte('\n')
	}
	sec := reSecret.FindString(all.String())
	checks = append(checks, Check{"secrets", sec == "", redact(sec)})
	inj := reInjection.FindString(all.String())
	checks = append(checks, Check{"injection", inj == "", inj})
	if inj != "" {
		bump("high")
	}
	gen := reDateOrIssue.FindString(md)
	checks = append(checks, Check{"general_rules", gen == "", "avoid dates / issue numbers: " + gen})
	return checks, risk, touchesPerms
}

// Passed reports whether every blocking check passed (general_rules is advisory).
func Passed(checks []Check) bool {
	for _, c := range checks {
		if !c.OK && c.Name != "general_rules" && c.Name != "permissions" {
			return false
		}
	}
	return true
}

func redact(s string) string {
	if len(s) <= 6 {
		return s
	}
	return s[:4] + "…"
}
