package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

var externalTriggers = map[string]bool{"webhook": true, "email": true, "im": true, "file_change": true, "plugin": true}

// applyGuard sets the per-task workspace and tool allow-list. (The PreToolUse
// hook it used to install never ran: tidebot's runs are --bare, which skips
// hooks. Deny rules and, for command runs, the OS sandbox replace it.)
func (a *App) applyGuard(ctx context.Context, opts *runner.Options) error {
	if opts.TaskID != "" && (opts.WorkspaceDir == "" || opts.WorkspaceDir == a.Cfg.DataDir) {
		opts.WorkspaceDir = guard.TaskWorkspace(a.Cfg.HomeDir, opts.TaskID)
	}
	if opts.WorkspaceDir != "" {
		if err := os.MkdirAll(opts.WorkspaceDir, 0o755); err != nil {
			return err
		}
	}
	var tools []string
	if opts.TaskID != "" && a.Policy != nil {
		for _, t := range strings.Split(a.Policy.Get(ctx, opts.TaskID).AllowedTools, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tools = append(tools, t)
			}
		}
	}
	if len(tools) == 0 && opts.TaskID != "" && a.Tasks != nil {
		if v, err := a.Tasks.Get(ctx, opts.TaskID); err == nil && v.Spec != nil {
			tools = v.Spec.AllowedTools
		}
	}
	if len(tools) == 0 && externalTriggers[opts.TriggerKind] {
		tools = guard.ExternalTools
	}
	if len(tools) > 0 {
		opts.ExtraArgs = append(opts.ExtraArgs, "--allowedTools", strings.Join(tools, ","))
	}
	base := opts.EnvBase
	if base == nil {
		base = os.Environ()
	}
	opts.EnvBase = append(append([]string{}, base...),
		"TIDEBOT_IPC="+a.Cfg.IPCPath, "TIDEBOT_TASK_ID="+opts.TaskID)
	return nil
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func (a *App) rpcGuardScan(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Dir string `json:"dir"`
	}](raw)
	if err != nil || p.Dir == "" {
		return nil, errors.New("dir required")
	}
	return guard.ScanWorkspace(p.Dir), nil
}
