package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// registerRPC2 wires phase 2–5 methods (reach them with `agentctl call <method> '<json>'`).
func (a *App) registerRPC2(srv *ipc.Server) {
	srv.Register("policy.get", a.rpcPolicyGet)
	srv.Register("policy.set", a.rpcPolicySet)
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (a *App) rpcPolicyGet(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		TaskID string `json:"task_id"`
	}](raw)
	if err != nil || p.TaskID == "" {
		return nil, errors.New("task_id required")
	}
	return a.Policy.Get(ctx, p.TaskID), nil
}

func (a *App) rpcPolicySet(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := decode[policy.Policy](raw)
	if err != nil || in.TaskID == "" {
		return nil, errors.New("task_id required")
	}
	if _, err := a.Store.GetTask(ctx, in.TaskID); err != nil {
		return nil, errors.New("task not found")
	}
	cur := a.Policy.Get(ctx, in.TaskID)
	in.ConsecutiveFailures = cur.ConsecutiveFailures
	if err := a.Policy.Put(ctx, in); err != nil {
		return nil, err
	}
	return a.Policy.Get(ctx, in.TaskID), nil
}
