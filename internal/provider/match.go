package provider

import (
	"net/url"
	"regexp"
	"strings"
)

// Match is what a pasted Base URL (and optionally an API key prefix) tells us about a
// provider, so the add-provider form can fill itself in.
type Match struct {
	Preset  *Preset // nil: no built-in preset matched
	BaseURL string  // cleaned URL to store ("" = Anthropic default endpoint)
	Name    string  // suggested profile name
	Fixed   bool    // BaseURL was rewritten (scheme added, /v1/messages dropped, preset path completed)
}

// MatchBaseURL cleans a pasted Base URL and finds the built-in preset it belongs to.
// Same host and the preset path as a prefix wins; a bare host of a known provider is
// completed to that preset's Anthropic-compatible URL.
func MatchBaseURL(raw string, presets []Preset) Match {
	s := strings.Trim(strings.TrimSpace(raw), `"'<>`)
	if s == "" {
		return Match{}
	}
	if !strings.Contains(s, "://") {
		if strings.HasPrefix(s, "localhost") || strings.HasPrefix(s, "127.0.0.1") {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Match{BaseURL: strings.TrimSpace(raw)}
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	p := strings.TrimRight(u.Path, "/")
	for _, suf := range []string{"/v1/messages", "/messages", "/v1"} {
		if strings.HasSuffix(p, suf) {
			p = strings.TrimSuffix(p, suf)
			break
		}
	}
	host := strings.ToLower(u.Host)
	clean := u.Scheme + "://" + host + p
	m := Match{BaseURL: clean, Fixed: clean != strings.TrimRight(strings.TrimSpace(raw), "/"), Name: NameFromHost(host)}
	if host == "api.anthropic.com" {
		for i := range presets {
			if presets[i].ID == "anthropic" {
				m.Preset, m.BaseURL, m.Name = &presets[i], "", "anthropic"
				return m
			}
		}
	}
	best, bestLen, hostOnly := -1, -1, -1
	for i, pr := range presets {
		if pr.BaseURL == "" {
			continue
		}
		pu, err := url.Parse(pr.BaseURL)
		if err != nil || strings.ToLower(pu.Host) != host {
			continue
		}
		pp := strings.TrimRight(pu.Path, "/")
		if p == pp || strings.HasPrefix(p, pp+"/") {
			if len(pp) > bestLen {
				best, bestLen = i, len(pp)
			}
		} else if hostOnly < 0 && (p == "" || p == "/") {
			hostOnly = i
		}
	}
	if best < 0 && hostOnly >= 0 {
		best = hostOnly
		m.BaseURL, m.Fixed = presets[best].BaseURL, true
	}
	if best >= 0 {
		m.Preset, m.Name = &presets[best], presets[best].ID
		if p == strings.TrimRight(mustPath(presets[best].BaseURL), "/") {
			m.BaseURL = presets[best].BaseURL // keep the preset's exact form (some need a trailing slash)
		}
	}
	return m
}

func mustPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

var nameJunk = map[string]bool{"api": true, "www": true, "open": true, "gateway": true, "com": true, "cn": true, "net": true, "org": true, "io": true, "ai": true, "dev": true, "app": true, "co": true, "coding": true, "llm": true}
var nameBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// NameFromHost turns a host into a profile name: the first label that isn't a generic
// word (api, open, coding, TLDs…). api.foo-ai.com → foo-ai, open.bigmodel.cn → bigmodel,
// 127.0.0.1:3425 → local-3425.
func NameFromHost(host string) string {
	host = strings.ToLower(host)
	h, port := host, ""
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		h, port = host[:i], host[i+1:]
	}
	if h == "localhost" || h == "127.0.0.1" || h == "[::1]" {
		if port != "" {
			return "local-" + port
		}
		return "local"
	}
	parts := []string{}
	for _, l := range strings.Split(h, ".") {
		if l != "" && !nameJunk[l] {
			parts = append(parts, l)
		}
	}
	if len(parts) == 0 {
		parts = strings.Split(h, ".")[:1]
	}
	n := nameBad.ReplaceAllString(parts[0], "-")
	n = strings.Trim(n, "-._")
	if len(n) > 40 {
		n = n[:40]
	}
	if n == "" {
		n = "custom"
	}
	return n
}

// KeyPreset guesses a preset from the first characters of an API key. Only the prefix
// is ever looked at; callers should not send more than that.
func KeyPreset(prefix string) string {
	switch {
	case strings.HasPrefix(prefix, "sk-ant-"):
		return "anthropic"
	}
	return ""
}
