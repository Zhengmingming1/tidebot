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
	// WorkspaceDir is the folder every node works in ("" = none: read-only nodes get
	// an empty folder each, edit / exec nodes share a scratch folder per run).
	WorkspaceDir string `json:"workspace_dir,omitempty"`
	// RunMode is the permission level of every node: read | edit | exec. "" = a
	// workflow saved before the three levels, which runs read-only as it always did.
	RunMode string `json:"run_mode,omitempty"`
	// Budget caps the whole run's cost in BudgetCurrency (0 = no cap, the default):
	// once reached no further node starts, and a node starts capped at what is left.
	Budget         float64 `json:"budget,omitempty"`
	BudgetCurrency string  `json:"budget_currency,omitempty"`
	Nodes          []Node  `json:"nodes"`
}

// Writes reports whether nodes may change files in a shared folder: they then run
// one at a time ("one change at a time in a folder").
func (d Definition) Writes() bool { return d.RunMode == "edit" || d.RunMode == "exec" }

type Node struct {
	Context          *ContextPolicy    `json:"context,omitempty"`
	ContextRunID     string            `json:"-"`
	ContextArtifacts []ContextArtifact `json:"-"`
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

// RunInfo describes the workflow run an executor call belongs to (RunFrom).
type RunInfo struct {
	RunID  string
	TaskID string // the task that started the run ("" = started by hand)
	Bucket string // the task's cost bucket ("" = production)
	Def    Definition
}

type runInfoKey struct{}

// RunFrom returns the run an executor call belongs to.
func RunFrom(ctx context.Context) (RunInfo, bool) {
	ri, ok := ctx.Value(runInfoKey{}).(RunInfo)
	return ri, ok
}

// StartOptions starts a workflow run (StartWith).
type StartOptions struct {
	WorkflowID string
	Version    int // 0 = the current version
	Input      string
	Provider   string
	TaskID     string
	Bucket     string
}

// ErrBudget stops a run whose total cost reached the workflow's budget.
var ErrBudget = errors.New("workflow budget reached")

// Executor must honor cancellation, preserve the run's permission limits and report
// the child run ID before it starts waiting for a global runner slot.
type Executor func(context.Context, Node, string, string, func(string)) (string, error)
