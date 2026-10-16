package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is a concrete provider configuration used when spawning Claude Code.
type Profile struct {
	Name           string            `json:"name" yaml:"name"`
	Preset         string            `json:"preset,omitempty" yaml:"preset,omitempty"`
	BaseURL        string            `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	AuthEnv        string            `json:"auth_env,omitempty" yaml:"auth_env,omitempty"`
	SecretEnv      string            `json:"secret_env,omitempty" yaml:"secret_env,omitempty"` // env var holding the API key
	Model          string            `json:"model,omitempty" yaml:"model,omitempty"`
	OpusModel      string            `json:"opus_model,omitempty" yaml:"opus_model,omitempty"`
	SonnetModel    string            `json:"sonnet_model,omitempty" yaml:"sonnet_model,omitempty"`
	HaikuModel     string            `json:"haiku_model,omitempty" yaml:"haiku_model,omitempty"`
	FableModel     string            `json:"fable_model,omitempty" yaml:"fable_model,omitempty"`
	SmallFastModel string            `json:"small_fast_model,omitempty" yaml:"small_fast_model,omitempty"`
	SubagentModel  string            `json:"subagent_model,omitempty" yaml:"subagent_model,omitempty"`
	ExtraEnv       map[string]string `json:"extra_env,omitempty" yaml:"extra_env,omitempty"`
}

// FileDocument is the on-disk providers.yaml shape.
type FileDocument struct {
	Providers []Profile `json:"providers" yaml:"providers"`
}

// FromPreset builds a Profile from a built-in preset.
func FromPreset(p Preset) Profile {
	secretEnv := p.SecretEnv
	if secretEnv == "" {
		secretEnv = "TIDEBOT_API_KEY_" + strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_"))
	}
	extra := map[string]string{}
	for k, v := range p.ExtraEnv {
		extra[k] = v
	}
	return Profile{
		Name:           p.ID,
		Preset:         p.ID,
		BaseURL:        p.BaseURL,
		AuthEnv:        p.AuthEnv,
		SecretEnv:      secretEnv,
		Model:          p.DefaultModel,
		OpusModel:      p.OpusModel,
		SonnetModel:    p.SonnetModel,
		HaikuModel:     p.HaikuModel,
		FableModel:     p.FableModel,
		SmallFastModel: p.SmallFastModel,
		SubagentModel:  p.SubagentModel,
		ExtraEnv:       extra,
	}
}

// Resolve merges preset defaults with profile overrides.
func (p Profile) Resolve() (Profile, error) {
	out := p
	if p.Preset != "" {
		preset, ok := LookupPreset(p.Preset)
		if !ok {
			return Profile{}, fmt.Errorf("unknown preset %q", p.Preset)
		}
		base := FromPreset(preset)
		if out.Name == "" {
			out.Name = base.Name
		}
		if out.BaseURL == "" {
			out.BaseURL = base.BaseURL
		}
		if out.AuthEnv == "" {
			out.AuthEnv = base.AuthEnv
		}
		if out.SecretEnv == "" {
			out.SecretEnv = base.SecretEnv
		}
		if out.Model == "" {
			out.Model = base.Model
		}
		if out.OpusModel == "" {
			out.OpusModel = base.OpusModel
		}
		if out.SonnetModel == "" {
			out.SonnetModel = base.SonnetModel
		}
		if out.HaikuModel == "" {
			out.HaikuModel = base.HaikuModel
		}
		if out.FableModel == "" {
			out.FableModel = base.FableModel
		}
		if out.SmallFastModel == "" {
			out.SmallFastModel = base.SmallFastModel
		}
		if out.SubagentModel == "" {
			out.SubagentModel = base.SubagentModel
		}
		if out.SecretEnv == "" {
			out.SecretEnv = base.SecretEnv
		}
		if len(out.ExtraEnv) == 0 && len(base.ExtraEnv) > 0 {
			out.ExtraEnv = map[string]string{}
			for k, v := range base.ExtraEnv {
				out.ExtraEnv[k] = v
			}
		} else if len(base.ExtraEnv) > 0 {
			merged := map[string]string{}
			for k, v := range base.ExtraEnv {
				merged[k] = v
			}
			for k, v := range out.ExtraEnv {
				merged[k] = v
			}
			out.ExtraEnv = merged
		}
	}
	if out.Name == "" {
		return Profile{}, fmt.Errorf("provider name is required")
	}
	if out.AuthEnv == "" {
		out.AuthEnv = "ANTHROPIC_AUTH_TOKEN"
	}
	return out, nil
}

// LoadFile reads providers.yaml/json. Missing file returns empty list (not an error).
func LoadFile(path string) ([]Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc FileDocument
	if stringsHasSuffixFold(path, ".json") {
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parse providers json: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parse providers yaml: %w", err)
		}
	}
	return doc.Providers, nil
}

func stringsHasSuffixFold(s, suf string) bool {
	return len(s) >= len(suf) && strings.EqualFold(s[len(s)-len(suf):], suf)
}

// Registry merges built-in presets with user file overrides (file wins on same name).
type Registry struct {
	byName map[string]Profile
}

// NewRegistry builds a registry from presets + optional file profiles.
func NewRegistry(fileProfiles []Profile) (*Registry, error) {
	r := &Registry{byName: map[string]Profile{}}
	for _, preset := range BuiltIn() {
		p := FromPreset(preset)
		resolved, err := p.Resolve()
		if err != nil {
			return nil, err
		}
		r.byName[resolved.Name] = resolved
	}
	for _, fp := range fileProfiles {
		resolved, err := fp.Resolve()
		if err != nil {
			return nil, err
		}
		r.byName[resolved.Name] = resolved
	}
	return r, nil
}

// Get returns a profile by name.
func (r *Registry) Get(name string) (Profile, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// List returns all profiles sorted by name.
func (r *Registry) List() []Profile {
	out := make([]Profile, 0, len(r.byName))
	for _, p := range r.byName {
		out = append(out, p)
	}
	// simple insertion sort by name to avoid importing sort in tests only
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].Name > out[j].Name {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}
