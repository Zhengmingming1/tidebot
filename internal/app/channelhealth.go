package app

import (
	"strconv"

	"github.com/Zhengmingming1/tidebot/internal/notify"
)

// A notification channel that failed notify.DownAfter sends in a row, and the same channel
// working again. They reach the bell, the desktop and every other channel, never the
// failing one itself.
const (
	EventChannelDown = "channel_down"
	EventChannelUp   = "channel_up"
)

// wireChannelHealth turns notify's down / up flips into bus events.
func (a *App) wireChannelHealth() {
	a.Notify.OnHealth = func(h notify.Health) {
		e := Event{Kind: EventChannelUp, ChannelID: h.Channel.ID, ChannelKind: h.Channel.Kind, TaskName: h.Channel.Name}
		if h.Down {
			e.Kind, e.Count, e.Detail = EventChannelDown, h.Streak, h.Err
		}
		a.Events.Emit(e)
	}
}

// channelMessage fills m for the two channel events.
func channelMessage(e Event, m *notify.Message, t func(string, ...any) string) {
	m.TaskID = "" // not a task: per-task rules must not match
	m.SkipChannel = e.ChannelID
	m.Params["kind"] = e.ChannelKind
	if e.Kind == EventChannelDown {
		m.Params["n"] = strconv.Itoa(e.Count)
		m.Title, m.Body = t("notify.channel_down", e.TaskName, e.Count), e.Detail
		return
	}
	m.Params["detail"] = ""
	m.Title, m.Body = t("notify.channel_up", e.TaskName), t("notify.channel_up_body")
}
