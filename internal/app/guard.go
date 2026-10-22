package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// EventConfirmNeeded: a dangerous tool call was blocked and waits for the owner.
const EventConfirmNeeded = "confirm_needed"

// ConfirmTimeout: pending confirmations expire after this (suggested 30 min).
const ConfirmTimeout = 30 * time.Minute

var externalTriggers = map[string]bool{"webhook": true, "email": true, "im": true, "file_change": true, "plugin": true}

// applyGuard sets the per-task workspace, tool allow-list and the PreToolUse hook.
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
	exe := a.HookExe
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe != "" && !a.Cfg.GuardDisabled {
		p := filepath.Join(a.Cfg.DataDir, "guard-settings.json")
		hook := fmt.Sprintf("%q hook pretool", exe)
		if err := os.WriteFile(p, guard.SettingsJSON(hook), 0o600); err == nil {
			opts.ExtraArgs = append(opts.ExtraArgs, "--settings", p)
		}
	}
	base := opts.EnvBase
	if base == nil {
		base = os.Environ()
	}
	opts.EnvBase = append(append([]string{}, base...),
		"TIDEBOT_IPC="+a.Cfg.IPCPath, "TIDEBOT_TASK_ID="+opts.TaskID, "TIDEBOT_GUARD_WORKSPACE="+opts.WorkspaceDir)
	return nil
}

// Confirmation is a blocked dangerous call.
type Confirmation struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	TaskID    string `json:"task_id"`
	Tool      string `json:"tool"`
	Input     string `json:"input"`
	Rule      string `json:"rule"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// GuardCheck is called by the hook for a call that matched a rule. An approved confirmation
// for the same task + call is consumed and allows it once; otherwise a pending one is recorded.
func (a *App) GuardCheck(ctx context.Context, runID, taskID, tool, key, rule, reason string) (deny bool, err error) {
	res, err := a.DB.ExecContext(ctx, `UPDATE pending_confirmations SET status='used', decided_at=?
WHERE id = (SELECT id FROM pending_confirmations WHERE task_id=? AND input=? AND status='approved' LIMIT 1)`, nowUTC(), taskID, key)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			return false, nil
		}
	}
	var exists int
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending_confirmations WHERE task_id=? AND input=? AND status='pending'`, taskID, key).Scan(&exists)
	if exists == 0 {
		cid := id.New("cfm")
		if _, err := a.DB.ExecContext(ctx, `INSERT INTO pending_confirmations(id, run_id, task_id, tool, input, rule, status, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			cid, runID, taskID, tool, key, rule, "pending", nowUTC()); err != nil {
			return true, err
		}
		a.Events.Emit(Event{Kind: EventConfirmNeeded, TaskID: taskID, TaskName: a.taskName(taskID), Detail: rule + ": " + truncateStr(key, 120)})
	}
	return true, nil
}

// ListConfirmations returns confirmations, expiring stale pending ones first.
func (a *App) ListConfirmations(ctx context.Context, status string) ([]Confirmation, error) {
	cut := time.Now().UTC().Add(-ConfirmTimeout).Format(time.RFC3339)
	_, _ = a.DB.ExecContext(ctx, `UPDATE pending_confirmations SET status='expired', decided_at=? WHERE status='pending' AND created_at < ?`, nowUTC(), cut)
	q := `SELECT id, run_id, task_id, tool, input, rule, status, created_at FROM pending_confirmations`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	rows, err := a.DB.QueryContext(ctx, q+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Confirmation
	for rows.Next() {
		var c Confirmation
		if err := rows.Scan(&c.ID, &c.RunID, &c.TaskID, &c.Tool, &c.Input, &c.Rule, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DecideConfirmation approves or denies; approving re-runs the task once.
func (a *App) DecideConfirmation(ctx context.Context, cid string, approve bool) error {
	st := "denied"
	if approve {
		st = "approved"
	}
	res, err := a.DB.ExecContext(ctx, `UPDATE pending_confirmations SET status=?, decided_at=? WHERE id=? AND status='pending'`, st, nowUTC(), cid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no pending confirmation with that id")
	}
	if approve {
		var taskID string
		_ = a.DB.QueryRowContext(ctx, `SELECT task_id FROM pending_confirmations WHERE id=?`, cid).Scan(&taskID)
		if taskID != "" {
			_, err := a.startTaskAsync(taskID, "production")
			return err
		}
	}
	return nil
}

func (a *App) rpcGuardCheck(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		RunID  string `json:"run_id"`
		TaskID string `json:"task_id"`
		Tool   string `json:"tool"`
		Key    string `json:"key"`
		Rule   string `json:"rule"`
		Reason string `json:"reason"`
	}](raw)
	if err != nil {
		return nil, err
	}
	deny, err := a.GuardCheck(ctx, p.RunID, p.TaskID, p.Tool, p.Key, p.Rule, p.Reason)
	return guard.Verdict{Deny: deny, RuleID: p.Rule}, err
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func (a *App) rpcGuardList(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		Status string `json:"status"`
	}](raw)
	return a.ListConfirmations(ctx, p.Status)
}

func (a *App) rpcGuardDecide(approve bool) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct {
			ID string `json:"id"`
		}](raw)
		if err != nil || p.ID == "" {
			return nil, errors.New("id required")
		}
		return map[string]bool{"ok": true}, a.DecideConfirmation(ctx, p.ID, approve)
	}
}

func (a *App) rpcGuardScan(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Dir string `json:"dir"`
	}](raw)
	if err != nil || p.Dir == "" {
		return nil, errors.New("dir required")
	}
	return guard.ScanWorkspace(p.Dir), nil
}
