package optimize

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Analysis is the analyzer run's JSON output (constrained with --json-schema).
type Analysis struct {
	Category           string            `json:"category"` // efficiency | logic | robustness | trigger | cleanup
	Rationale          string            `json:"rationale"`
	EvidenceRunIDs     []string          `json:"evidence_run_ids"`
	ExpectedBenefit    string            `json:"expected_benefit"`
	Risk               string            `json:"risk"`
	TouchesPermissions bool              `json:"touches_permissions"`
	Files              map[string]string `json:"files"` // full new content per changed file
}

// AnalyzerSchema is passed to `claude --json-schema`.
const AnalyzerSchema = `{"type":"object","required":["category","rationale","files"],"properties":{` +
	`"category":{"enum":["efficiency","logic","robustness","trigger","cleanup"]},` +
	`"rationale":{"type":"string"},"evidence_run_ids":{"type":"array","items":{"type":"string"}},` +
	`"expected_benefit":{"type":"string"},"risk":{"enum":["low","medium","high"]},` +
	`"touches_permissions":{"type":"boolean"},"files":{"type":"object","additionalProperties":{"type":"string"}}}}`

// AnalyzerTools: read-only, no network.
var AnalyzerTools = []string{"Read", "Grep", "Glob"}

// BuildAnalyzerPrompt assembles metrics and the current skill. userInstruction is set for
// "write this into the skill" requests. External payloads and requirement items are never included.
func BuildAnalyzerPrompt(skillName string, files map[string][]byte, metrics string, userInstruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You improve the Claude Code skill %q. Return JSON only.\n", skillName)
	b.WriteString("Rules: derive general rules from failures; do not list specific cases, dates or issue numbers; " +
		"do not widen allowed-tools, add scripts or network access; if unavoidable set touches_permissions=true and risk=high. " +
		"Return the full new content of each changed file in `files`.\n\n")
	if userInstruction != "" {
		fmt.Fprintf(&b, "Owner instruction (primary goal):\n%s\n\n", userInstruction)
	}
	if metrics != "" {
		fmt.Fprintf(&b, "Run metrics (production runs of current version, next-run-only runs excluded):\n%s\n\n", metrics)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "=== %s ===\n%s\n", p, files[p])
	}
	return b.String()
}

// ParseAnalysis extracts the JSON object from the analyzer result text.
func ParseAnalysis(text string) (*Analysis, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, errors.New("analyzer returned no JSON")
	}
	var a Analysis
	if err := json.Unmarshal([]byte(text[start:end+1]), &a); err != nil {
		return nil, fmt.Errorf("analyzer JSON: %w", err)
	}
	if len(a.Files) == 0 {
		return nil, errors.New("analyzer proposed no change")
	}
	return &a, nil
}

// Apply merges changed files over the current ones.
func (a *Analysis) Apply(cur map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range cur {
		out[k] = v
	}
	for k, v := range a.Files {
		out[k] = []byte(v)
	}
	return out
}
