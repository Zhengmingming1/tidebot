// Package contextplan plans bounded, authorized context without model calls.
package contextplan

import "context"

const PolicyVersion = "contextplan/v1"
const EstimatorVersion = "ascii4-unicode1/v1"

// Budget describes an owner-selected policy, not a claim about a provider's model.
// A zero WindowTokens means unknown; MaxInputTokens still bounds initial context.
type Budget struct {
	WindowTokens   int `json:"window_tokens,omitempty"`
	MaxInputTokens int `json:"max_input_tokens,omitempty"`
	OutputReserve  int `json:"output_reserve,omitempty"`
	ToolReserve    int `json:"tool_reserve,omitempty"`
	SystemReserve  int `json:"system_reserve,omitempty"`
}

type Material struct {
	Mode     string `json:"mode,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Scope    string `json:"scope"`
	Version  string `json:"version,omitempty"`
	Hash     string `json:"sha256,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Required bool   `json:"required"`
	Priority int    `json:"priority,omitempty"`
	Order    int    `json:"order,omitempty"`
	Tokens   int    `json:"estimated_tokens"`
	Body     string `json:"-"`
	// Access must be explicitly established by the adapter, before Load runs.
	Allowed bool                                  `json:"-"`
	Load    func(context.Context) (string, error) `json:"-"`
}

type Omitted struct {
	ID          string `json:"id"`
	Reason      string `json:"reason"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}
type Request struct {
	Scope     string
	NodeID    string
	Provider  string
	Model     string
	Budget    Budget
	Materials []Material
}
type Plan struct {
	ID              string     `json:"id"`
	Policy          string     `json:"policy"`
	Estimator       string     `json:"estimator"`
	Scope           string     `json:"scope"`
	NodeID          string     `json:"node_id,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	Model           string     `json:"model,omitempty"`
	Budget          Budget     `json:"budget"`
	BudgetTokens    int        `json:"budget_tokens"`
	EstimatedTokens int        `json:"estimated_tokens"`
	Materials       []Material `json:"materials"`
	Omitted         []Omitted  `json:"omitted"`
	Warnings        []string   `json:"warnings"`
}

type Planner interface {
	Plan(context.Context, Request) (Plan, error)
}
type Deterministic struct{}
