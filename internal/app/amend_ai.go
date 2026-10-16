package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/amend"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// ClaudeStructurer uses Claude Code with a strict JSON prompt to structure amendments.
// Enable with TIDEBOT_AMEND_AI=1. Keep prompts tiny to limit cost.
type ClaudeStructurer struct {
	App *App
}

func (c *ClaudeStructurer) Structure(ctx context.Context, taskID string, spec *tasksvc.Spec, utterance string) (amend.StructuredProposal, error) {
	var constraints []string
	if spec != nil {
		for _, x := range spec.Constraints {
			constraints = append(constraints, fmt.Sprintf("%s:%s", x.ID, x.Text))
		}
	}
	prompt := fmt.Sprintf(`You structure task requirement amendments. Reply with ONLY a JSON object, no markdown:
{"items":[{"op":"add|modify|delete","scope":"permanent|next_run","target_spec_item":"","content":"...","conflict_with":[]}]}
Existing constraints: %s
User said: %s`, strings.Join(constraints, "; "), utterance)

	res, err := c.App.RunPromptEx(ctx, runner.Options{
		Prompt: prompt, WorkspaceDir: c.App.Cfg.DataDir, TriggerKind: "amend_structure", TaskID: taskID,
		ExtraArgs: []string{
			"--permission-mode", "dontAsk",
			"--permission-prompts", "none",
			"--tools", "",
			"--max-budget-usd", "0.03",
			"--effort", "low",
		},
	}, c.App.Cfg.DefaultProvider, 2*time.Minute)
	if err != nil {
		return amend.StructuredProposal{}, err
	}
	text := strings.TrimSpace(res.ResultText)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var prop amend.StructuredProposal
	if err := json.Unmarshal([]byte(text), &prop); err != nil {
		return amend.StructuredProposal{}, fmt.Errorf("parse structured amendment: %w (text=%q)", err, truncate(text, 200))
	}
	return prop, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
