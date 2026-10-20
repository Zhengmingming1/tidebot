package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// ---- provider form: preset, name, auth and models from a pasted Base URL ----

// handleProviderInfer: POST /api/providers/infer {base_url, key_prefix, env} → guess.
// key_prefix is at most 8 characters (the client never sends the key); env carries only
// non-secret ANTHROPIC_* model variables parsed from a pasted snippet.
func (s *Server) handleProviderInfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL   string            `json:"base_url"`
		KeyPrefix string            `json:"key_prefix"`
		Env       map[string]string `json:"env"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if len(body.KeyPrefix) > 8 {
		body.KeyPrefix = body.KeyPrefix[:8]
	}
	out := inferProvider(body.BaseURL, body.KeyPrefix, body.Env)
	if name, _ := out["name"].(string); name != "" && s.deps.Providers != nil {
		taken := map[string]bool{}
		for _, v := range s.deps.Providers.ListProviders(r.Context()) {
			taken[v.Name] = true
		}
		out["name"] = freeName(name, taken)
	}
	writeJSON(w, 200, out)
}

func inferProvider(baseURL, keyPrefix string, env map[string]string) map[string]any {
	presets := provider.BuiltIn()
	m := provider.MatchBaseURL(baseURL, presets)
	if m.Preset == nil && strings.TrimSpace(baseURL) == "" {
		if id := provider.KeyPreset(keyPrefix); id != "" {
			for i := range presets {
				if presets[i].ID == id {
					m = provider.Match{Preset: &presets[i], Name: id}
				}
			}
		}
	}
	out := map[string]any{"base_url": m.BaseURL, "name": m.Name, "fixed": m.Fixed, "preset": "", "display": ""}
	models := map[string]string{}
	if p := m.Preset; p != nil {
		out["preset"], out["display"], out["auth_env"] = p.ID, p.DisplayName, p.AuthEnv
		models["model"], models["small_fast_model"] = p.DefaultModel, p.SmallFastModel
		models["opus_model"], models["sonnet_model"], models["haiku_model"] = p.OpusModel, p.SonnetModel, p.HaikuModel
	}
	for k, f := range map[string]string{"ANTHROPIC_MODEL": "model", "ANTHROPIC_SMALL_FAST_MODEL": "small_fast_model",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "opus_model", "ANTHROPIC_DEFAULT_SONNET_MODEL": "sonnet_model", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "haiku_model"} {
		if v := strings.TrimSpace(env[k]); v != "" && len(v) <= 128 {
			models[f] = v
		}
	}
	for k, v := range models {
		if v == "" {
			delete(models, k)
		}
	}
	out["models"] = models
	return out
}

// freeName returns name, or name-2, name-3… when a profile with that name already exists.
func freeName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for i := 2; i < 100; i++ {
		base := name
		if suf := "-" + strconv.Itoa(i); len(base)+len(suf) > 40 {
			base = base[:40-len(suf)]
		}
		if n := base + "-" + strconv.Itoa(i); !taken[n] {
			return n
		}
	}
	return name
}
