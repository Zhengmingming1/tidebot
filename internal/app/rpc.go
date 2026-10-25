package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"database/sql"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/ipc"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// RegisterRPC wires JSON-RPC methods onto the IPC server.
func (a *App) RegisterRPC(srv *ipc.Server) {
	srv.Register("status", a.rpcStatus)
	srv.Register("providers.list", a.rpcProvidersList)
	srv.Register("tasks.list", a.rpcTasksList)
	srv.Register("tasks.get", a.rpcTasksGet)
	srv.Register("tasks.create", a.rpcTasksCreate)
	srv.Register("tasks.pause", a.rpcTasksPause)
	srv.Register("tasks.resume", a.rpcTasksResume)
	srv.Register("tasks.run", a.rpcTasksRun)
	srv.Register("tasks.versions", a.rpcTasksVersions)
	srv.Register("tasks.rollback", a.rpcTasksRollback)
	srv.Register("tasks.tag", a.rpcTasksTag)
	srv.Register("runs.list", a.rpcRunsList)
	srv.Register("runs.stop", a.rpcRunsStop)
	srv.Register("amendments.propose", a.rpcAmendPropose)
	srv.Register("amendments.list", a.rpcAmendList)
	srv.Register("amendments.confirm", a.rpcAmendConfirm)
	srv.Register("amendments.reject", a.rpcAmendReject)
	srv.Register("amendments.resolve", a.rpcAmendResolve)
	srv.Register("skills.import", a.rpcSkillsImport)
	srv.Register("skills.list", a.rpcSkillsList)
	srv.Register("skills.rollback", a.rpcSkillsRollback)
	srv.Register("skills.pin", a.rpcSkillsPin)
	srv.Register("spend.summary", a.rpcSpendSummary)
	srv.Register("spend.set_budget", a.rpcSpendSetBudget)
	srv.Register("webui.status", a.rpcWebUIStatus)
	srv.Register("webui.set", a.rpcWebUISet)
	srv.Register("auth.set_password", a.rpcAuthSetPassword)
	srv.Register("auth.totp_setup", a.rpcAuthTOTPSetup)
	srv.Register("auth.totp_disable", a.rpcAuthTOTPDisable)
	a.registerRPC2(srv)
}

func (a *App) rpcStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	active, _ := a.Store.CountActiveRuns(ctx)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return map[string]any{
		"ok":               true,
		"db":               a.Cfg.DBPath,
		"ipc":              a.Cfg.IPCPath,
		"max_concurrency":  a.Cfg.MaxConcurrency,
		"active_runs":      active,
		"default_provider": a.Cfg.DefaultProvider,
		"webui":            a.WebUI != nil && a.WebUI.Enabled(),
		"webui_configured": a.Cfg.EffectiveWebUI(),
		"heap_alloc_mib":   float64(ms.HeapAlloc) / (1024 * 1024),
		"go_version":       runtime.Version(),
		"time":             time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (a *App) rpcProvidersList(ctx context.Context, _ json.RawMessage) (any, error) {
	_ = a.SeedBuiltinProviders(ctx)
	type row struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		AuthEnv string `json:"auth_env"`
	}
	var out []row
	for _, p := range a.Registry.List() {
		out = append(out, row{Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, AuthEnv: p.AuthEnv})
	}
	return out, nil
}

func (a *App) rpcTasksList(ctx context.Context, _ json.RawMessage) (any, error) {
	return a.Tasks.List(ctx)
}

func (a *App) rpcTasksGet(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	return a.Tasks.Get(ctx, p.ID)
}

func (a *App) rpcTasksCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var p tasksvc.CreateInput
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Actor == "" {
		p.Actor = "user"
	}
	return a.Tasks.Create(ctx, p)
}

func (a *App) rpcTasksPause(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	return nil, a.Tasks.SetStatus(ctx, p.ID, "paused", "user")
}

func (a *App) rpcTasksResume(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	return nil, a.Tasks.SetStatus(ctx, p.ID, "active", "user")
}

func (a *App) rpcTasksRun(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	task, err := a.Store.GetTask(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	// Started by hand (agentctl tasks run): like Run now in the WebUI, not held back
	// by power saving and allowed on a paused task.
	return nil, a.runTaskTrigger(ctx, *task, "manual", cost.BucketProduction)
}

func (a *App) rpcTasksVersions(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	vers, err := a.Ver.ListVersions(ctx, version.EntityTask, p.ID)
	if err != nil {
		return nil, err
	}
	cur, _ := a.Ver.GetPointer(ctx, version.EntityTask, p.ID, version.PointerCurrent)
	return map[string]any{"versions": vers, "current": cur.String}, nil
}

func (a *App) rpcTasksRollback(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.VersionID == "" {
		return nil, fmt.Errorf("id and version_id required")
	}
	return nil, a.Tasks.Rollback(ctx, p.ID, p.VersionID, "user", p.Reason)
}

func (a *App) rpcTasksTag(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID        string `json:"id"`
		Tag       string `json:"tag"`
		VersionID string `json:"version_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Tag == "" || p.VersionID == "" {
		return nil, fmt.Errorf("id, tag, version_id required")
	}
	return nil, a.Tasks.Tag(ctx, p.ID, p.Tag, p.VersionID, "user")
}

func (a *App) rpcRunsList(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TaskID string `json:"task_id"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(params, &p)
	return a.Store.ListRuns(ctx, p.TaskID, p.Limit)
}

func (a *App) rpcRunsStop(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	was, err := a.StopRun(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": p.ID, "was": was}, nil
}

func (a *App) rpcAmendPropose(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TaskID    string `json:"task_id"`
		Utterance string `json:"utterance"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" || p.Utterance == "" {
		return nil, fmt.Errorf("task_id and utterance required")
	}
	return a.Amend.Propose(ctx, p.TaskID, p.Utterance, "user")
}

func (a *App) rpcAmendList(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" {
		return nil, fmt.Errorf("task_id required")
	}
	return a.Amend.List(ctx, p.TaskID, p.Status)
}

func (a *App) rpcAmendConfirm(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID    string   `json:"id"`
		Batch string   `json:"batch"`
		Only  []string `json:"only"`
	}
	if err := json.Unmarshal(params, &p); err != nil || (p.ID == "" && p.Batch == "") {
		return nil, fmt.Errorf("id or batch required")
	}
	if p.Batch != "" {
		return a.Amend.ConfirmBatch(ctx, p.Batch, p.Only, "user")
	}
	ids := strings.Split(p.ID, ",")
	if len(ids) > 1 {
		return a.Amend.ConfirmItems(ctx, ids, "user")
	}
	return a.Amend.Confirm(ctx, p.ID, "user")
}

func (a *App) rpcAmendReject(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID    string   `json:"id"`
		Batch string   `json:"batch"`
		Only  []string `json:"only"`
	}
	if err := json.Unmarshal(params, &p); err != nil || (p.ID == "" && p.Batch == "") {
		return nil, fmt.Errorf("id or batch required")
	}
	if p.Batch != "" {
		n, err := a.Amend.RejectBatch(ctx, p.Batch, p.Only)
		return map[string]int{"rejected": n}, err
	}
	return nil, a.Amend.Reject(ctx, p.ID)
}

func (a *App) rpcAmendResolve(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID         string `json:"id"`
		Resolution string `json:"resolution"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id and resolution required")
	}
	return nil, a.Amend.ResolveConflict(ctx, p.ID, p.Resolution)
}

func (a *App) rpcSkillsImport(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
		Dir  string `json:"dir"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Dir == "" {
		return nil, fmt.Errorf("dir required")
	}
	if _, err := os.Stat(p.Dir); err != nil {
		return nil, err
	}
	return a.Skills.ImportDir(ctx, p.Name, p.Dir, "user")
}

func (a *App) rpcSkillsList(ctx context.Context, _ json.RawMessage) (any, error) {
	return a.Skills.List(ctx)
}

func (a *App) rpcSkillsRollback(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.VersionID == "" {
		return nil, fmt.Errorf("id and version_id required")
	}
	return nil, a.Skills.Rollback(ctx, p.ID, p.VersionID, "user")
}

func (a *App) rpcSkillsPin(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TaskID         string `json:"task_id"`
		SkillID        string `json:"skill_id"`
		SkillVersionID string `json:"skill_version_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" || p.SkillID == "" || p.SkillVersionID == "" {
		return nil, fmt.Errorf("task_id, skill_id, skill_version_id required")
	}
	return nil, a.Skills.PinOnTask(ctx, p.TaskID, p.SkillID, p.SkillVersionID, "user")
}

func (a *App) rpcSpendSummary(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Day string `json:"day"`
	}
	_ = json.Unmarshal(params, &p)
	return a.Cost.Summary(ctx, p.Day)
}

func (a *App) rpcSpendSetBudget(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Scope     string  `json:"scope"`
		ScopeID   string  `json:"scope_id"`
		Period    string  `json:"period"`
		Bucket    string  `json:"bucket"`
		MaxUSD    float64 `json:"max_usd"`
		MaxTokens int64   `json:"max_tokens"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Scope == "" {
		p.Scope = "global"
	}
	if p.Period == "" {
		p.Period = "day"
	}
	b := cost.Budget{
		Scope: p.Scope, ScopeID: p.ScopeID, Period: p.Period, Bucket: p.Bucket,
		MaxUSD:    sql.NullFloat64{Float64: p.MaxUSD, Valid: p.MaxUSD > 0},
		MaxTokens: sql.NullInt64{Int64: p.MaxTokens, Valid: p.MaxTokens > 0},
		Action:    "pause_notify", WarnAtPct: 80,
	}
	return nil, a.Cost.UpsertBudget(ctx, b)
}

func (a *App) rpcWebUIStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	_ = ctx
	return map[string]any{
		"enabled":    a.WebUI != nil && a.WebUI.Enabled(),
		"addr":       a.Cfg.WebUIAddr,
		"configured": a.Cfg.WebUIEnabled,
	}, nil
}

func (a *App) rpcWebUISet(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if a.WebUI == nil {
		return nil, fmt.Errorf("webui not initialized")
	}
	if err := a.WebUI.SetEnabled(ctx, p.Enabled); err != nil {
		return nil, err
	}
	return map[string]any{"enabled": a.WebUI.Enabled()}, nil
}

func (a *App) rpcAuthSetPassword(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Password == "" {
		return nil, fmt.Errorf("password required")
	}
	return nil, a.Auth.SetPassword(ctx, p.Password)
}

func (a *App) rpcAuthTOTPSetup(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Code   string `json:"code"`
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Secret == "" {
		sec, uri, err := auth.GenerateTOTPSecret("tidebot", "owner")
		if err != nil {
			return nil, err
		}
		return map[string]any{"secret": sec, "uri": uri, "note": "confirm with auth.totp_setup code+secret"}, nil
	}
	if err := a.Auth.EnableTOTPWithCode(ctx, p.Secret, p.Code); err != nil {
		if errors.Is(err, auth.ErrBadTOTP) {
			return nil, fmt.Errorf("invalid totp code")
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) rpcAuthTOTPDisable(ctx context.Context, _ json.RawMessage) (any, error) {
	return nil, a.Auth.DisableTOTP(ctx)
}
