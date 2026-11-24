package app

import "github.com/Zhengmingming1/tidebot/internal/capability"

// chatNow is the time written to chat_sessions (created_at, updated_at) and
// chat_session_turns.created_at: the same fixed-width layout as capability
// times (capability.TimeLayout), because installing a capability in a session
// also stamps chat_sessions.updated_at. The session list orders by updated_at
// as text; with nowUTC (RFC3339, whole seconds) next to the capability layout a
// session touched at "…:05Z" sorted above one touched later at "…:05.4…Z", and
// two sessions touched in the same second fell back to id (creation) order.
// Migration 052 pads rows written before this change.
func chatNow() string { return capability.Now() }
