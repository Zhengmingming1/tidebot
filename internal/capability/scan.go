package capability

import (
	"bytes"
	"path"
	"regexp"
	"strings"
)

type scanRule struct {
	id, severity, note string
	re                 *regexp.Regexp
}

var scanRules = []scanRule{
	{"agent-config-write", "caution", "Mentions agent configuration or an agent installer; inspect before installation.", regexp.MustCompile(`(?i)(\.claude[/\\]|\.codex[/\\]|\.cursor[/\\]|\.gemini[/\\]|\.agents[/\\]|\.config/opencode|\.mcp\.json|CLAUDE\.md|AGENTS\.md|GEMINI\.md|npx\s+skills\s+add|claude\s+(mcp\s+add|plugin\s+install)|dws\s+skill\s+setup)`)},
	{"execution-chain", "high", "Downloads or decodes code for execution.", regexp.MustCompile(`(?i)(curl|wget).*(\|\s*(sh|bash|zsh)|chmod)|iex\s*\(|base64.*(\|\s*(sh|bash)|eval)|\beval\s`)},
	{"credential-access", "high", "References credential stores or environment exfiltration.", regexp.MustCompile(`(?i)(~[/\\]\.(ssh|aws)|security\s+find-(generic|internet)-password|printenv.*(curl|wget)|env\s*\|.*(curl|wget))`)},
	{"persistence", "high", "May create persistent background or shell startup behavior.", regexp.MustCompile(`(?i)(crontab|launchctl|launchagents|systemctl\s+enable|currentversion[/\\]+run|\.(bashrc|zshrc|profile)\b)`)},
	{"claude-execution", "caution", "Agent metadata can change tool access or trigger execution.", regexp.MustCompile("(?i)(allowed-tools\\s*:|disable-model-invocation\\s*:|statusLine|hooks\\s*[\"']?\\s*:|!`)")},
	{"prompt-injection", "high", "Contains instruction override, concealment, or hidden characters.", regexp.MustCompile(`(?i)(ignore\s+(all\s+)?(previous|prior)\s+instructions|do not tell the user|don't tell the user|忽略.{0,8}指令|不要告诉用户|[\x{200b}\x{200c}\x{200d}\x{feff}])`)},
	{"unlocked-command", "caution", "Package command is unpinned or automatically installs packages.", regexp.MustCompile(`(?i)(npx|uvx).*(@latest|--yes|\s-y\s)`)},
}

// ScanFiles is deterministic and bounded. Findings are warnings, not proof that
// upstream content is safe. It must never execute the text being inspected.
func ScanFiles(files map[string][]byte) Scan {
	out := Scan{Scanner: ScannerVersion, Verdict: "safe", Findings: []Finding{}}
	if _, _, err := HashFiles(files); err != nil {
		out.Verdict = "high"
		out.Findings = append(out.Findings, Finding{ID: "snapshot-limit", Severity: "high", Note: err.Error()})
		return out
	}
	for _, p := range sortedPaths(files) {
		b := files[p]
		if bytes.ContainsRune(b, 0) {
			out.Findings = append(out.Findings, Finding{ID: "binary-content", File: p, Severity: "caution", Note: "Binary content requires separate integrity and provenance review."})
			if out.Verdict == "safe" {
				out.Verdict = "caution"
			}
		}
		for _, rule := range scanRules {
			matches := rule.re.FindAllIndex(b, 32)
			for _, m := range matches {
				if len(out.Findings) >= 512 {
					out.Verdict = "high"
					out.Findings = append(out.Findings, Finding{ID: "finding-limit", Severity: "high", Note: "Finding limit exceeded; review source manually."})
					return out
				}
				out.Findings = append(out.Findings, Finding{ID: rule.id, File: p, Line: bytes.Count(b[:m[0]], []byte{'\n'}) + 1, Note: rule.note, Severity: rule.severity})
				if rule.severity == "high" {
					out.Verdict = "high"
				} else if out.Verdict == "safe" {
					out.Verdict = "caution"
				}
			}
		}
	}
	return out
}

type Policy struct {
	Allowed                    bool   `json:"allowed"`
	ReviewRequired             bool   `json:"review_required"`
	SecondConfirmationRequired bool   `json:"second_confirmation_required"`
	Reason                     string `json:"reason,omitempty"`
}

func InstallPolicy(trust, verdict string, allowHighRisk bool) Policy {
	p := Policy{Allowed: true}
	if verdict != "safe" && verdict != "caution" && verdict != "high" {
		return Policy{Reason: "Unknown scan verdict"}
	}
	switch trust {
	case "official":
		p.ReviewRequired = verdict == "high"
	case "verified":
		p.ReviewRequired = verdict != "safe"
		p.SecondConfirmationRequired = verdict == "high"
	case "community":
		p.ReviewRequired = true
		if verdict == "high" && !allowHighRisk {
			p.Allowed = false
			p.Reason = "High-risk community sources are disabled"
		}
	case "unknown":
		p.ReviewRequired = true
		p.SecondConfirmationRequired = verdict == "caution"
		if verdict == "high" {
			p.Allowed = false
			p.Reason = "High-risk unknown sources cannot be installed"
		}
	default:
		return Policy{Reason: "Unknown trust level"}
	}
	return p
}

// InferRequirements only infers interpreter requirements. Exact package locks
// must come from reviewed metadata, never guessed or installed from README text.
func InferRequirements(files map[string][]byte) Requirements {
	r := Requirements{}
	python, node := false, false
	for p := range files {
		switch strings.ToLower(path.Ext(p)) {
		case ".py":
			python = true
		case ".js", ".mjs", ".cjs", ".ts":
			node = true
		}
	}
	if python {
		r.Runtimes = append(r.Runtimes, ToolRequirement{Name: "python3", Check: []string{"--version"}})
	}
	if node {
		r.Runtimes = append(r.Runtimes, ToolRequirement{Name: "node", Check: []string{"--version"}})
	}
	return r
}
