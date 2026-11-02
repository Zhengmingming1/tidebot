// Package workflow validates and executes versioned, bounded skill DAGs.
package workflow

import (
	"context"
	"errors"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

const MaxNodes = 32
const MaxOutputBytes = 128 << 10
const MaxPromptBytes = 256 << 10

var (
	ErrNotFound    = errors.New("workflow not found")
	ErrConflict    = errors.New("workflow changed; reload before saving")
	ErrNotActive   = errors.New("workflow run is not active")
	ErrClosing     = errors.New("workflow service is stopping")
	ErrCanceled    = errors.New("workflow stopped by user")
	ErrInterrupted = errors.New("workflow interrupted by shutdown")
)

type Definition struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	MaxParallel    int    `json:"max_parallel"`
	TimeoutMinutes int    `json:"timeout_minutes"`
	Nodes          []Node `json:"nodes"`
}
type Node struct {
	// SkillSetState is derived before freezing bindings in a run snapshot.
	SkillSetState  string                  `json:"skill_set_state,omitempty"`
	SkillSetSource *tasksvc.SkillSetSource `json:"skill_set_source,omitempty"`
	Position       *Position               `json:"position,omitempty"`
	ID             string                  `json:"id"`
	Name           string                  `json:"name,omitempty"`
	Kind           string                  `json:"kind"`
	Prompt         string                  `json:"prompt,omitempty"`
	SkillBindings  []tasksvc.SkillBinding  `json:"skill_bindings,omitempty"`
	Needs          []Dependency            `json:"needs,omitempty"`
	Join           string                  `json:"join"`
	Condition      *Condition              `json:"condition,omitempty"`
	// Retries are opt-in and only apply to transient failures. Owners must use
	// idempotent skills when enabling them; recovered runs are never replayed.
	RetryMax       int `json:"retry_max,omitempty"`
	RetryBackoffMS int `json:"retry_backoff_ms,omitempty"`
}

// Position is presentation metadata; it never determines dependency order.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Dependency struct {
	NodeID string `json:"node_id"`
	Branch string `json:"branch,omitempty"`
}
type Condition struct {
	Source   string `json:"source"`
	Operator string `json:"operator"`
	Value    string `json:"value,omitempty"`
}
type Workflow struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Version     int        `json:"version"`
	Definition  Definition `json:"definition"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
}
type Run struct {
	ID              string     `json:"id"`
	WorkflowID      string     `json:"workflow_id"`
	WorkflowVersion int        `json:"workflow_version"`
	Status          string     `json:"status"`
	Input           string     `json:"input"`
	Provider        string     `json:"provider"`
	Snapshot        Definition `json:"snapshot"`
	Error           string     `json:"error"`
	CreatedAt       string     `json:"created_at"`
	StartedAt       string     `json:"started_at"`
	FinishedAt      string     `json:"finished_at"`
	Nodes           []NodeRun  `json:"nodes"`
}
type NodeRun struct {
	NodeID     string `json:"node_id"`
	Status     string `json:"status"`
	ChildRunID string `json:"child_run_id"`
	Output     string `json:"output"`
	Error      string `json:"error"`
	Branch     string `json:"branch"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// Executor must honor cancellation, preserve the run's permission limits and report
// the child run ID before it starts waiting for a global runner slot.
type Executor func(context.Context, Node, string, string, func(string)) (string, error)
