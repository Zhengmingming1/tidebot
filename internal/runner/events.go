package runner

import (
	"encoding/json"
	"fmt"
)

// Event is a single stream-json line from Claude Code.
type Event struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Raw     json.RawMessage `json:"-"`

	// Common / result fields (present depending on type).
	SessionID     string   `json:"session_id,omitempty"`
	Result        string   `json:"result,omitempty"`
	IsError       bool     `json:"is_error,omitempty"`
	DurationMS    int64    `json:"duration_ms,omitempty"`
	DurationAPIMS int64    `json:"duration_api_ms,omitempty"`
	NumTurns      int      `json:"num_turns,omitempty"`
	TotalCostUSD  float64  `json:"total_cost_usd,omitempty"`
	Usage         *Usage   `json:"usage,omitempty"`
	ModelUsage    json.RawMessage `json:"modelUsage,omitempty"`
	UUID          string   `json:"uuid,omitempty"`

	// system/init
	Model        string          `json:"model,omitempty"`
	Tools        []string        `json:"tools,omitempty"`
	CWD          string          `json:"cwd,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

// Usage captures token counts from a result event.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ParseEvent decodes one NDJSON line into Event.
func ParseEvent(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("parse stream-json: %w", err)
	}
	e.Raw = append(json.RawMessage(nil), line...)
	return e, nil
}

// IsResult reports whether this is the terminal result event.
func (e Event) IsResult() bool {
	return e.Type == "result"
}

// IsInit reports system/init.
func (e Event) IsInit() bool {
	return e.Type == "system" && e.Subtype == "init"
}
