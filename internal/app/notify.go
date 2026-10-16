package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/notify"
)

var urgentEvents = map[string]bool{EventConfirmNeeded: true, EventBudgetBreached: true, EventBreakerTripped: true}

// EventToMessage renders an event in the configured language.
func (a *App) EventToMessage(e Event) (notify.Message, bool) {
	name := e.TaskName
	if name == "" {
		name = i18n.T("desktop.quick_task")
	}
	m := notify.Message{Event: e.Kind, TaskID: e.TaskID, Urgent: urgentEvents[e.Kind]}
	switch e.Kind {
	case EventRunFinished:
		if e.TaskID == "" || a.Policy == nil || !a.Policy.Get(context.Background(), e.TaskID).NotifySuccess {
			return m, false // success is silent unless enabled per task
		}
		m.Title, m.Body = i18n.T("desktop.n.finished", name), i18n.T("desktop.n.finished_body", e.CostUSD)
	case EventRunFailed:
		m.Title, m.Body = i18n.T("desktop.n.failed", name), e.Detail
	case EventApprovalNeeded:
		m.Title, m.Body = i18n.T("desktop.n.approval", name), i18n.T("desktop.n.approval_body", e.Count)
	case EventBudgetBreached:
		m.Title, m.Body = i18n.T("desktop.n.budget", name), e.Detail
	case EventBreakerTripped:
		m.Title, m.Body = i18n.T("notify.breaker", name), e.Detail
	case EventConfirmNeeded:
		m.Title, m.Body = i18n.T("notify.confirm", name), e.Detail
	default:
		m.Title, m.Body = e.Kind+": "+name, e.Detail
	}
	return m, true
}

// runNotifier forwards bus events to channels and flushes the digest daily.
func (a *App) runNotifier(ctx context.Context) {
	ch := a.Events.Subscribe(128)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastDigest := ""
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if m, ok := a.EventToMessage(e); ok {
				quiet, _ := a.settingString(ctx, "notify.quiet")
				if _, err := a.Notify.Dispatch(ctx, m, quiet); err != nil {
					a.Log.Warn("notify", "error", err)
				}
			}
		case now := <-tick.C:
			h := 20
			if v, _ := a.settingString(ctx, "notify.digest_hour"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < 24 {
					h = n
				}
			}
			local := now.In(a.Cfg.Location())
			day := local.Format("2006-01-02")
			if local.Hour() == h && lastDigest != day {
				lastDigest = day
				if _, err := a.Notify.FlushDigest(ctx); err != nil {
					a.Log.Warn("digest", "error", err)
				}
			}
		}
	}
}

// settingString reads a JSON-string setting (falls back to the raw value).
func (a *App) settingString(ctx context.Context, key string) (string, error) {
	raw, err := a.Auth.GetSetting(ctx, key)
	if err != nil || raw == "" {
		return "", err
	}
	var s string
	if json.Unmarshal([]byte(raw), &s) == nil {
		return s, nil
	}
	return raw, nil
}

func (a *App) rpcNotifyChannels(ctx context.Context, _ json.RawMessage) (any, error) {
	return a.Notify.Channels(ctx)
}

func (a *App) rpcNotifyAddChannel(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Kind   string            `json:"kind"`
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
		Secret string            `json:"secret"`
	}](raw)
	if err != nil {
		return nil, err
	}
	return a.Notify.AddChannel(ctx, p.Kind, p.Name, p.Config, p.Secret)
}

func (a *App) rpcNotifyRemoveChannel(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		ID string `json:"id"`
	}](raw)
	if p.ID == "" {
		return nil, errors.New("id required")
	}
	return map[string]bool{"ok": true}, a.Notify.RemoveChannel(ctx, p.ID)
}

func (a *App) rpcNotifyAddRule(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		Event     string `json:"event"`
		TaskID    string `json:"task_id"`
		ChannelID string `json:"channel_id"`
	}](raw)
	if p.Event == "" || p.ChannelID == "" {
		return nil, errors.New("event and channel_id required")
	}
	rid, err := a.Notify.AddRule(ctx, p.Event, p.TaskID, p.ChannelID)
	return map[string]string{"id": rid}, err
}

func (a *App) rpcNotifyTest(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		ChannelID string `json:"channel_id"`
	}](raw)
	chs, err := a.Notify.Channels(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range chs {
		if c.ID == p.ChannelID {
			return map[string]bool{"ok": true}, a.Notify.Send(ctx, c, notify.Message{Event: "test", Title: i18n.T("notify.test")})
		}
	}
	return nil, errors.New("channel not found")
}

func (a *App) rpcNotifySettings(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		Quiet      *string `json:"quiet"`
		DigestHour *int    `json:"digest_hour"`
	}](raw)
	if p.Quiet != nil {
		b, _ := json.Marshal(*p.Quiet)
		if err := a.Auth.SetSetting(ctx, "notify.quiet", string(b)); err != nil {
			return nil, err
		}
	}
	if p.DigestHour != nil {
		if *p.DigestHour < 0 || *p.DigestHour > 23 {
			return nil, errors.New("digest_hour 0..23")
		}
		b, _ := json.Marshal(strconv.Itoa(*p.DigestHour))
		_ = a.Auth.SetSetting(ctx, "notify.digest_hour", string(b))
	}
	q, _ := a.settingString(ctx, "notify.quiet")
	h, _ := a.settingString(ctx, "notify.digest_hour")
	return map[string]string{"quiet": q, "digest_hour": h}, nil
}
