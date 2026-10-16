package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

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
	srv.Register("amendments.propose", a.rpcAmendPropose)
	srv.Register("amendments.list", a.rpcAmendList)
	srv.Register("amendments.confirm", a.rpcAmendConfirm)
	srv.Register("amendments.reject", a.rpcAmendReject)
	srv.Register("amendments.resolve", a.rpcAmendResolve)
	srv.Register("skills.import", a.rpcSkillsImport)
	srv.Register("skills.list", a.rpcSkillsList)
	srv.Register("skills.rollback", a.rpcSkillsRollback)
	srv.Register("skills.pin", a.rpcSkillsPin)
}

func (a *App) rpcStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	active, _ := a.Store.CountActiveRuns(ctx)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return map[string]any{
		"ok":              true,
		"db":              a.Cfg.DBPath,
		"ipc":             a.Cfg.IPCPath,
		"max_concurrency": a.Cfg.MaxConcurrency,
		"active_runs":     active,
		"default_provider": a.Cfg.DefaultProvider,
		"webui":           a.Cfg.EffectiveWebUI(),
		"heap_alloc_mib":  float64(ms.HeapAlloc) / (1024 * 1024),
		"go_version":      runtime.Version(),
		"time":            time.Now().UTC().Format(time.RFC3339),
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
	return nil, a.runTask(ctx, *task)
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
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
	}
	return a.Amend.Confirm(ctx, p.ID, "user")
}

func (a *App) rpcAmendReject(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id required")
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

