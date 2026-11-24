package capability

import "time"

// TimeLayout is the fixed-width UTC layout for every capability timestamp
// (capabilities, capability_versions, capability_events, capability_proposals,
// capability_bindings). Lists order by these columns as text and proposals
// compare expires_at as text; RFC3339Nano drops trailing zeros, so a whole
// second ("…:05Z") sorted after a later fraction of the same second
// ("…:05.3Z", '.' < 'Z'). Migration 051 pads rows written before this change.
const TimeLayout = "2006-01-02T15:04:05.000000000Z"

// Stamp formats t in TimeLayout.
func Stamp(t time.Time) string { return t.UTC().Format(TimeLayout) }

// Now is Stamp(time.Now()).
func Now() string { return Stamp(time.Now()) }

// sqlNow is the SQLite expression for the current time in TimeLayout
// (strftime's %f has millisecond precision; pad to nine digits).
const sqlNow = `(strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z')`
