package workflow

import (
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"math"
	"regexp"
	"strings"
)

var nodeID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,47}$`)
var placeholder = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// Validate normalizes defaults and rejects ambiguous or unbounded execution graphs.
func Validate(d *Definition) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || len([]rune(d.Name)) > 80 {
		return fmt.Errorf("workflow name is required (max 80 characters)")
	}
	if len(d.Description) > 4096 {
		return fmt.Errorf("description exceeds 4096 bytes")
	}
	if len(d.Nodes) == 0 || len(d.Nodes) > MaxNodes {
		return fmt.Errorf("workflow needs 1-%d nodes", MaxNodes)
	}
	if d.MaxParallel == 0 {
		d.MaxParallel = 2
	}
	if d.MaxParallel < 1 || d.MaxParallel > 4 {
		return fmt.Errorf("parallelism must be 1-4")
	}
	if d.TimeoutMinutes == 0 {
		d.TimeoutMinutes = 30
	}
	if d.TimeoutMinutes < 1 || d.TimeoutMinutes > 1440 {
		return fmt.Errorf("timeout must be 1-1440 minutes")
	}
	nodes := map[string]*Node{}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.RetryMax < 0 || n.RetryMax > 5 || n.RetryBackoffMS < 0 || n.RetryBackoffMS > 60000 {
			return fmt.Errorf("node %s: retry_max must be 0-5 and retry_backoff_ms 0-60000", n.ID)
		}
		if n.Kind == "condition" && (n.RetryMax != 0 || n.RetryBackoffMS != 0) {
			return fmt.Errorf("condition node %s cannot retry", n.ID)
		}
		if p := n.Position; p != nil && (math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || p.X < 0 || p.Y < 0 || p.X > 10000 || p.Y > 10000) {
			return fmt.Errorf("node %s: canvas position must be within 0-10000", n.ID)
		}
		if !nodeID.MatchString(n.ID) || nodes[n.ID] != nil {
			return fmt.Errorf("invalid or duplicate node id: %s", n.ID)
		}
		if n.Name == "" {
			n.Name = n.ID
		}
		if len([]rune(n.Name)) > 80 {
			return fmt.Errorf("node %s name exceeds 80 characters", n.ID)
		}
		if n.Join == "" {
			n.Join = "all"
		}
		if n.Join != "all" && n.Join != "any" {
			return fmt.Errorf("node %s: join must be all or any", n.ID)
		}
		if err := tasksvc.ValidateSkillSetSource(n.SkillSetSource); err != nil {
			return fmt.Errorf("node %s: %w", n.ID, err)
		}
		switch n.Kind {
		case "skill":
			if strings.TrimSpace(n.Prompt) == "" || len(n.Prompt) > 32<<10 {
				return fmt.Errorf("node %s needs a prompt (max 32 KB)", n.ID)
			}
			if len(n.SkillBindings) == 0 {
				return fmt.Errorf("node %s needs a primary skill", n.ID)
			}
			var err error
			n.SkillBindings, err = tasksvc.NormalizeBindings(n.SkillBindings)
			if err != nil {
				return fmt.Errorf("node %s: %w", n.ID, err)
			}
			if n.Condition != nil {
				return fmt.Errorf("skill node %s cannot have a condition", n.ID)
			}
		case "condition":
			if n.Condition == nil || len(n.Condition.Source) > 32<<10 || len(n.Condition.Value) > 4096 {
				return fmt.Errorf("node %s needs a bounded condition", n.ID)
			}
			if len(n.SkillBindings) > 0 || n.SkillSetSource != nil {
				return fmt.Errorf("condition node %s cannot run skills", n.ID)
			}
			switch n.Condition.Operator {
			case "equals", "not_equals", "contains", "not_contains", "empty", "not_empty":
			default:
				return fmt.Errorf("node %s: unknown condition operator", n.ID)
			}
		default:
			return fmt.Errorf("node %s: kind must be skill or condition", n.ID)
		}
		nodes[n.ID] = n
	}
	for _, n := range d.Nodes {
		seen := map[string]bool{}
		for _, need := range n.Needs {
			parent := nodes[need.NodeID]
			if parent == nil || parent.ID == n.ID || seen[need.NodeID] {
				return fmt.Errorf("node %s: missing, self or duplicate dependency %s", n.ID, need.NodeID)
			}
			seen[need.NodeID] = true
			if parent.Kind == "condition" {
				if need.Branch != "true" && need.Branch != "false" {
					return fmt.Errorf("node %s: select true or false branch of %s", n.ID, parent.ID)
				}
			} else if need.Branch != "" {
				return fmt.Errorf("node %s: branch requires a condition node", n.ID)
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var walk func(string) error
	walk = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("workflow contains a cycle at %s", id)
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, need := range nodes[id].Needs {
			if err := walk(need.NodeID); err != nil {
				return err
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for id := range nodes {
		if err := walk(id); err != nil {
			return err
		}
	}
	for _, n := range d.Nodes {
		ancestors := map[string]bool{}
		var collect func(string)
		collect = func(id string) {
			for _, need := range nodes[id].Needs {
				if !ancestors[need.NodeID] {
					ancestors[need.NodeID] = true
					collect(need.NodeID)
				}
			}
		}
		collect(n.ID)
		text := n.Prompt
		if n.Kind == "condition" {
			text = n.Condition.Source
		}
		rest := placeholder.ReplaceAllStringFunc(text, func(s string) string { return "" })
		if strings.Contains(rest, "{{") {
			return fmt.Errorf("node %s: malformed template", n.ID)
		}
		for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
			key := strings.TrimSpace(m[1])
			if key == "input" {
				continue
			}
			id, ok := stepRef(key)
			if !ok || !ancestors[id] {
				return fmt.Errorf("node %s: template reference %q must name an ancestor output", n.ID, key)
			}
		}
	}
	return nil
}
func stepRef(key string) (string, bool) {
	if !strings.HasPrefix(key, "steps.") || !strings.HasSuffix(key, ".output") {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(key, "steps."), ".output")
	return id, nodeID.MatchString(id)
}
func render(text, input string, states map[string]*NodeRun, join string) (string, error) {
	// Enforce the budget before each append. Expanding a small template with
	// thousands of references must not allocate hundreds of MB before rejection.
	var chunks []string
	total := 0
	appendText := func(s string) error {
		if len(s) > MaxPromptBytes-total {
			return fmt.Errorf("rendered prompt exceeds %d KB", MaxPromptBytes>>10)
		}
		if s != "" {
			chunks = append(chunks, s)
			total += len(s)
		}
		return nil
	}
	for {
		match := placeholder.FindStringIndex(text)
		if match == nil {
			if err := appendText(text); err != nil {
				return "", err
			}
			if len(chunks) == 0 {
				return "", nil
			}
			if len(chunks) == 1 {
				return chunks[0], nil
			}
			var out strings.Builder
			out.Grow(total)
			for _, chunk := range chunks {
				out.WriteString(chunk)
			}
			return out.String(), nil
		}
		if err := appendText(text[:match[0]]); err != nil {
			return "", err
		}
		key := strings.TrimSpace(text[match[0]+2 : match[1]-2])
		value := input
		if key != "input" {
			id, ok := stepRef(key)
			if !ok {
				return "", fmt.Errorf("unknown template %s", key)
			}
			n := states[id]
			if n == nil {
				return "", fmt.Errorf("missing output %s", id)
			}
			if n.Status == "skipped" && join == "any" {
				value = ""
			} else if n.Status != "succeeded" {
				return "", fmt.Errorf("output %s is unavailable (%s)", id, n.Status)
			} else {
				value = n.Output
			}
		}
		if err := appendText(value); err != nil {
			return "", err
		}
		text = text[match[1]:]
	}
}

func testCondition(c Condition, value string) bool {
	switch c.Operator {
	case "equals":
		return value == c.Value
	case "not_equals":
		return value != c.Value
	case "contains":
		return strings.Contains(value, c.Value)
	case "not_contains":
		return !strings.Contains(value, c.Value)
	case "empty":
		return strings.TrimSpace(value) == ""
	case "not_empty":
		return strings.TrimSpace(value) != ""
	}
	return false
}
