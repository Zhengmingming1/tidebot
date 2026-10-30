package provider

import (
	"fmt"
	"os"
	"strings"
)

// SecretResolver returns the API key for a profile without logging it.
type SecretResolver func(profile Profile) (string, error)

// NormalizeSecretEnv parses "env:VAR" / "VAR" into the OS environment variable name.
func NormalizeSecretEnv(secretEnv string) string {
	s := strings.TrimSpace(secretEnv)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "env:") {
		return strings.TrimSpace(s[4:])
	}
	return s
}

// EnvFromOS looks up the key from profile.SecretEnv (supports "env:NAME"), then common fallbacks.
func EnvFromOS(profile Profile) (string, error) {
	candidates := []string{}
	if name := NormalizeSecretEnv(profile.SecretEnv); name != "" {
		candidates = append(candidates, name)
	}
	candidates = append(candidates,
		"TIDEBOT_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY",
	)
	seen := map[string]struct{}{}
	var tried []string
	for _, k := range candidates {
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		tried = append(tried, k)
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key found in env for provider %q (tried %v)", profile.Name, tried)
}

// ChildEnv builds the environment block injected into the Claude Code subprocess.
// Secrets are included for the child only; callers must never log the returned values.
func ChildEnv(base []string, profile Profile, apiKey string) []string {
	strip := map[string]struct{}{
		"ANTHROPIC_BASE_URL":              {},
		"ANTHROPIC_AUTH_TOKEN":            {},
		"ANTHROPIC_API_KEY":               {},
		"ANTHROPIC_MODEL":                 {},
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    {},
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  {},
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   {},
		"ANTHROPIC_DEFAULT_FABLE_MODEL":   {},
		"ANTHROPIC_SMALL_FAST_MODEL":      {},
		"CLAUDE_CODE_SUBAGENT_MODEL":      {},
		"CLAUDE_CODE_EFFORT_LEVEL":        {},
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": {},
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":  {},
		// The owner's Claude subscription token (claude setup-token) is never passed
		// on: runs use the provider's key only.
		"CLAUDE_CODE_OAUTH_TOKEN": {},
	}
	// Also strip the source secret env name so the child only sees AuthEnv.
	if name := NormalizeSecretEnv(profile.SecretEnv); name != "" {
		strip[name] = struct{}{}
	}
	for k := range profile.ExtraEnv {
		strip[k] = struct{}{}
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
	return fmt.Sprintf("provider=%s preset=%s base_url=%s model=%s auth_env=%s secret_env=%s",
		p.Name, p.Preset, base, p.Model, p.AuthEnv, p.SecretEnv)
}
