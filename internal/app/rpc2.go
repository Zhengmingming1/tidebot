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
	srv.Register("guard.check", a.rpcGuardCheck)
	srv.Register("guard.list", a.rpcGuardList)
	srv.Register("guard.approve", a.rpcGuardDecide(true))
	srv.Register("guard.deny", a.rpcGuardDecide(false))
	srv.Register("guard.scan", a.rpcGuardScan)
	srv.Register("notify.channels", a.rpcNotifyChannels)
	srv.Register("notify.add_channel", a.rpcNotifyAddChannel)
	srv.Register("notify.remove_channel", a.rpcNotifyRemoveChannel)
	srv.Register("notify.add_rule", a.rpcNotifyAddRule)
	srv.Register("notify.test", a.rpcNotifyTest)
	srv.Register("notify.settings", a.rpcNotifySettings)
	srv.Register("amendments.consolidation", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, _ := decode[struct {
			TaskID string `json:"task_id"`
		}](raw)
		return a.Amend.Consolidation(ctx, p.TaskID)
	})
	srv.Register("amendments.consolidate", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, _ := decode[struct {
			TaskID string `json:"task_id"`
			Hash   string `json:"hash"`
		}](raw)
		vid, err := a.Amend.ApplyConsolidation(ctx, p.TaskID, p.Hash, "user")
		return map[string]string{"version_id": vid}, err
	})
	a.registerRPC3(srv)
	a.registerSkillRPC(func(n string, h func(context.Context, json.RawMessage) (any, error)) { srv.Register(n, h) })
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
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	if _, ok := keys["notify_answer"]; !ok { // a caller that doesn't know the field keeps it
		in.NotifyAnswer = cur.NotifyAnswer
	}
	if _, ok := keys["notify_changed"]; !ok {
		in.NotifyChanged = cur.NotifyChanged
	}
	in.ConsecutiveFailures = cur.ConsecutiveFailures
	if err := a.Policy.Put(ctx, in); err != nil {
		return nil, err
	}
	return a.Policy.Get(ctx, in.TaskID), nil
}
