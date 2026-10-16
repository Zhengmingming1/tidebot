package provider

import (
	"fmt"
	"os"
	"strings"
)

// SecretResolver returns the API key for a profile without logging it.
type SecretResolver func(profile Profile) (string, error)

// EnvFromOS looks up the key from profile.SecretEnv, then common fallbacks.
func EnvFromOS(profile Profile) (string, error) {
	candidates := []string{}
	if profile.SecretEnv != "" {
		candidates = append(candidates, profile.SecretEnv)
	}
	candidates = append(candidates,
		"TIDEBOT_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY",
	)
	for _, k := range candidates {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key found in env for provider %q (tried %v)", profile.Name, candidates)
}

// ChildEnv builds the environment block injected into the Claude Code subprocess.
// Secrets are included for the child only; callers must never log the returned values.
func ChildEnv(base []string, profile Profile, apiKey string) []string {
	// Start from parent env, then strip conflicting Anthropic vars so profile wins.
	strip := map[string]struct{}{
		"ANTHROPIC_BASE_URL":            {},
		"ANTHROPIC_AUTH_TOKEN":          {},
		"ANTHROPIC_API_KEY":             {},
		"ANTHROPIC_MODEL":               {},
		"ANTHROPIC_DEFAULT_OPUS_MODEL":  {},
		"ANTHROPIC_DEFAULT_SONNET_MODEL": {},
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": {},
		"ANTHROPIC_DEFAULT_FABLE_MODEL": {},
		"ANTHROPIC_SMALL_FAST_MODEL":    {},
		"CLAUDE_CODE_SUBAGENT_MODEL":    {},
	}
	out := make([]string, 0, len(base)+16)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, drop := strip[name]; drop {
			continue
		}
		out = append(out, kv)
	}

	set := func(k, v string) {
		if v == "" {
			return
		}
		out = append(out, k+"="+v)
	}

	if profile.BaseURL != "" {
		set("ANTHROPIC_BASE_URL", profile.BaseURL)
	}
	authEnv := profile.AuthEnv
	if authEnv == "" {
		authEnv = "ANTHROPIC_AUTH_TOKEN"
	}
	if apiKey != "" {
		set(authEnv, apiKey)
		// Avoid dual-key conflicts (Kimi docs: remove ANTHROPIC_API_KEY when using AUTH_TOKEN).
		if authEnv == "ANTHROPIC_AUTH_TOKEN" {
			// ensure API_KEY not re-added
		} else if authEnv == "ANTHROPIC_API_KEY" {
			// official anthropic path
		}
	}
	set("ANTHROPIC_MODEL", profile.Model)
	set("ANTHROPIC_DEFAULT_OPUS_MODEL", profile.OpusModel)
	set("ANTHROPIC_DEFAULT_SONNET_MODEL", profile.SonnetModel)
	set("ANTHROPIC_DEFAULT_HAIKU_MODEL", profile.HaikuModel)
	set("ANTHROPIC_DEFAULT_FABLE_MODEL", profile.FableModel)
	set("ANTHROPIC_SMALL_FAST_MODEL", profile.SmallFastModel)
	set("CLAUDE_CODE_SUBAGENT_MODEL", profile.SubagentModel)
	for k, v := range profile.ExtraEnv {
		if k == "" {
			continue
		}
		if _, blocked := strip[k]; blocked {
			continue // already handled above
		}
		// Never allow ExtraEnv to smuggle secrets into logs via unexpected keys —
		// values still go only to the child process.
		set(k, v)
	}
	return out
}

// MaskSecret replaces secret substrings for safe logging.
func MaskSecret(s, secret string) string {
	if secret == "" || s == "" {
		return s
	}
	if !strings.Contains(s, secret) {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// SafeSummary returns a loggable description of the profile (no secrets).
func SafeSummary(p Profile) string {
	base := p.BaseURL
	if base == "" {
		base = "(anthropic-default)"
	}
	return fmt.Sprintf("provider=%s preset=%s base_url=%s model=%s auth_env=%s",
		p.Name, p.Preset, base, p.Model, p.AuthEnv)
}
