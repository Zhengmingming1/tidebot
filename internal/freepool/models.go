// Package freepool exposes Kilo's anonymous, zero-priced tool-capable models to a
// local Anthropic client. It never sends a user's provider credentials upstream.
package freepool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Compare decimal prices exactly: floating-point underflow must not turn a
// positive (very small) price into a free model.
var zeroPrice = regexp.MustCompile(`^-?0(?:\.0+)?(?:[eE][+-]?[0-9]+)?$`)

const (
	PluginID        = "kilo-free-pool"
	ProviderName    = "kilo-free"
	DefaultModel    = "kilo-auto/free"
	upstreamBase    = "https://ai-gateway.kilo.ai/api/v1"
	catalogTTL      = 5 * time.Minute
	maxCatalogBytes = 4 << 20
)

// Model is a currently free, text-output model advertising tool support. Unknown
// data-retention policy is treated conservatively, not as a no-training promise.
type Model struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	ContextLength     int      `json:"context_length"`
	MaxOutputTokens   int      `json:"max_output_tokens"`
	MayTrainOnPrompts bool     `json:"may_train_on_prompts"`
	DataPolicyKnown   bool     `json:"data_policy_known"`
	InputModalities   []string `json:"input_modalities"`
}

type catalogModel struct {
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	IsFree        bool                       `json:"isFree"`
	Training      *bool                      `json:"mayTrainOnYourPrompts"`
	Pricing       map[string]json.RawMessage `json:"pricing"`
	Parameters    []string                   `json:"supported_parameters"`
	ContextLength int                        `json:"context_length"`
	Architecture  struct {
		Input  []string `json:"input_modalities"`
		Output []string `json:"output_modalities"`
	} `json:"architecture"`
	Top struct {
		ContextLength int `json:"context_length"`
		MaxTokens     int `json:"max_completion_tokens"`
	} `json:"top_provider"`
}

func contains(values []string, wanted string) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}
func zeroPricing(prices map[string]json.RawMessage) bool {
	if len(prices) == 0 || len(prices["prompt"]) == 0 || len(prices["completion"]) == 0 {
		return false
	}
	for _, raw := range prices {
		s := strings.TrimSpace(string(raw))
		if strings.HasPrefix(s, "\"") {
			if json.Unmarshal(raw, &s) != nil {
				return false
			}
		}
		if !zeroPrice.MatchString(s) {
			return false
		}
	}
	return true
}
func parseModels(body []byte) ([]Model, error) {
	var doc struct {
		Data []catalogModel `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("invalid free model catalog")
	}
	if len(doc.Data) == 0 || len(doc.Data) > 5000 {
		return nil, errors.New("empty or oversized free model catalog")
	}
	out := []Model{}
	seen := map[string]bool{}
	for _, m := range doc.Data {
		if !m.IsFree || !zeroPricing(m.Pricing) || !contains(m.Parameters, "tools") || !contains(m.Architecture.Input, "text") || !contains(m.Architecture.Output, "text") {
			continue
		}
		if m.ID == "" || len(m.ID) > 256 || strings.ContainsAny(m.ID, "\x00\r\n\t ") {
			return nil, errors.New("invalid free model identifier")
		}
		if seen[m.ID] {
			return nil, errors.New("duplicate free model identifier")
		}
		seen[m.ID] = true
		training := true
		if m.Training != nil {
			training = *m.Training
		}
		contextLength := m.ContextLength
		if contextLength == 0 {
			contextLength = m.Top.ContextLength
		}
		out = append(out, Model{ID: m.ID, Name: m.Name, ContextLength: contextLength, MaxOutputTokens: m.Top.MaxTokens, MayTrainOnPrompts: training, DataPolicyKnown: m.Training != nil, InputModalities: append([]string(nil), m.Architecture.Input...)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == DefaultModel {
			return true
		}
		if out[j].ID == DefaultModel {
			return false
		}
		return out[i].ID < out[j].ID
	})
	if len(out) == 0 {
		return nil, errors.New("no free models with tool support are currently available")
	}
	return out, nil
}
func cloneModels(in []Model) []Model {
	out := append([]Model(nil), in...)
	for i := range out {
		out[i].InputModalities = append([]string(nil), out[i].InputModalities...)
	}
	return out
}

// Models refreshes a bounded public catalog. Expired or failed catalog reads never
// authorize a request using a stale price. Callers enforce plugin permissions.
func (g *Gateway) Models(ctx context.Context, force bool) ([]Model, error) {
	ctx, cancel := g.requestContext(ctx)
	defer cancel()
	select {
	case g.catalogGate <- struct{}{}:
		defer func() { <-g.catalogGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !force && time.Now().Before(g.catalogExpires) && len(g.models) > 0 {
		return cloneModels(g.models), nil
	}
	// Clear first so a failed refresh cannot extend an old free-price decision.
	g.models = nil
	g.catalogExpires = time.Time{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.upstream+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return nil, errors.New("cannot fetch the free model catalog")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("free model catalog unavailable (HTTP %d)", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxCatalogBytes+1))
	if err != nil || len(body) > maxCatalogBytes {
		return nil, errors.New("invalid or oversized free model catalog")
	}
	models, err := parseModels(body)
	if err != nil {
		return nil, err
	}
	g.models = models
	g.catalogExpires = time.Now().Add(catalogTTL)
	return cloneModels(models), nil
}
func (g *Gateway) selectedModel(ctx context.Context, id string) (Model, error) {
	models, err := g.Models(ctx, false)
	if err != nil {
		return Model{}, err
	}
	for _, m := range models {
		if m.ID == id {
			return m, nil
		}
	}
	return Model{}, errors.New("selected model is not currently free with tool support")
}
