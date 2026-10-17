// Package netproxy decides which proxy tidebot's own HTTP traffic uses
// (self-update, provider tests, official plugins, and the Claude Code child).
//
// Precedence: the Settings → 网络 field > environment (HTTPS_PROXY / HTTP_PROXY /
// ALL_PROXY / NO_PROXY) > the OS proxy settings. GUI apps on macOS and Windows
// don't inherit a shell's HTTPS_PROXY, so the OS lookup matters there:
// macOS reads `scutil --proxy`, Windows the WinINET (IE) registry values,
// Linux GNOME's gsettings. PAC scripts are not evaluated; we report that so the
// user can set a manual proxy instead.
package netproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Modes for Settings.Mode.
const (
	ModeAuto   = "auto"   // env, then OS settings
	ModeManual = "manual" // Settings.URL
	ModeOff    = "off"    // direct
)

// Settings are what the user picks in Settings → 网络.
type Settings struct {
	Mode   string `json:"mode"`
	URL    string `json:"url,omitempty"`    // manual: http://, https://, socks5://, socks5h://
	Bypass string `json:"bypass,omitempty"` // manual: comma separated hosts / .suffix / CIDR
}

// System is one OS-level proxy configuration.
type System struct {
	HTTP, HTTPS, SOCKS string   // host:port (empty when unset)
	Bypass             []string // host patterns
	PAC                string   // PAC URL, when auto-config is on (not evaluated)
	Source             string   // "scutil", "registry", "gsettings"
}

// Effective is the resolved choice, shown in the UI.
type Effective struct {
	Mode   string   `json:"mode"`
	Source string   `json:"source"` // settings | env | system:scutil | system:registry | system:gsettings | none
	HTTPS  string   `json:"https,omitempty"`
	HTTP   string   `json:"http,omitempty"`
	Bypass []string `json:"bypass,omitempty"`
	PAC    string   `json:"pac,omitempty"`
	Note   string   `json:"note,omitempty"`
}

var (
	mu       sync.RWMutex
	cur      = Settings{Mode: ModeAuto}
	sysCache *System
	sysAt    time.Time
	// LookupSystem is the OS lookup; replaced in tests.
	LookupSystem = lookupSystem
	// Getenv is replaced in tests.
	Getenv = os.Getenv
)

// Set installs the user's settings (called at start and on save).
func Set(s Settings) error {
	if err := Validate(s); err != nil {
		return err
	}
	if s.Mode == "" {
		s.Mode = ModeAuto
	}
	mu.Lock()
	cur = s
	sysCache = nil
	mu.Unlock()
	return nil
}

// Get returns the current settings.
func Get() Settings { mu.RLock(); defer mu.RUnlock(); return cur }

// Validate checks a settings value.
func Validate(s Settings) error {
	switch s.Mode {
	case "", ModeAuto, ModeOff:
		return nil
	case ModeManual:
		_, err := ParseProxyURL(s.URL)
		return err
	}
	return errors.New("proxy mode must be auto, manual or off")
}

// ParseProxyURL accepts host:port or a URL with scheme http, https, socks5 or socks5h.
func ParseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("proxy URL is not valid (example: http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy scheme must be http, https, socks5 or socks5h")
	}
	if u.Port() == "" {
		return nil, errors.New("proxy URL needs a port")
	}
	return u, nil
}

func envConfig() *httpproxy.Config {
	c := &httpproxy.Config{
		HTTPProxy:  firstEnv("HTTP_PROXY", "http_proxy"),
		HTTPSProxy: firstEnv("HTTPS_PROXY", "https_proxy"),
		NoProxy:    firstEnv("NO_PROXY", "no_proxy"),
	}
	if all := firstEnv("ALL_PROXY", "all_proxy"); all != "" {
		if c.HTTPSProxy == "" {
			c.HTTPSProxy = all
		}
		if c.HTTPProxy == "" {
			c.HTTPProxy = all
		}
	}
	if c.HTTPProxy == "" && c.HTTPSProxy == "" {
		return nil
	}
	return c
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func system() *System {
	mu.Lock()
	defer mu.Unlock()
	if sysCache != nil && time.Since(sysAt) < time.Minute {
		return sysCache
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s := LookupSystem(ctx)
	if s == nil {
		s = &System{}
	}
	sysCache, sysAt = s, time.Now()
	return s
}

// config builds the httpproxy config for the current settings.
func config() (*httpproxy.Config, Effective) {
	s := Get()
	switch s.Mode {
	case ModeOff:
		return nil, Effective{Mode: ModeOff, Source: "none"}
	case ModeManual:
		u, _ := ParseProxyURL(s.URL)
		if u == nil {
			return nil, Effective{Mode: ModeManual, Source: "none"}
		}
		return &httpproxy.Config{HTTPProxy: u.String(), HTTPSProxy: u.String(), NoProxy: s.Bypass},
			Effective{Mode: ModeManual, Source: "settings", HTTPS: Redact(u.String()), HTTP: Redact(u.String()), Bypass: splitList(s.Bypass)}
	}
	if c := envConfig(); c != nil {
		return c, Effective{Mode: ModeAuto, Source: "env", HTTPS: Redact(c.HTTPSProxy), HTTP: Redact(c.HTTPProxy), Bypass: splitList(c.NoProxy)}
	}
	sys := system()
	c := sys.Config()
	e := Effective{Mode: ModeAuto, Source: "none", PAC: sys.PAC}
	if c != nil {
		e.Source = "system:" + sys.Source
		e.HTTPS, e.HTTP, e.Bypass = Redact(c.HTTPSProxy), Redact(c.HTTPProxy), sys.Bypass
	}
	if sys.PAC != "" && c == nil {
		e.Note = "pac"
	}
	return c, e
}

// Config turns an OS configuration into an httpproxy config (nil when no proxy).
func (s *System) Config() *httpproxy.Config {
	if s == nil {
		return nil
	}
	c := &httpproxy.Config{NoProxy: strings.Join(s.Bypass, ",")}
	if s.HTTPS != "" {
		c.HTTPSProxy = "http://" + s.HTTPS
	}
	if s.HTTP != "" {
		c.HTTPProxy = "http://" + s.HTTP
	}
	if s.SOCKS != "" {
		if c.HTTPSProxy == "" {
			c.HTTPSProxy = "socks5://" + s.SOCKS
		}
		if c.HTTPProxy == "" {
			c.HTTPProxy = "socks5://" + s.SOCKS
		}
	}
	if c.HTTPSProxy == "" && c.HTTPProxy != "" {
		c.HTTPSProxy = c.HTTPProxy // most system proxies (Clash, V2Ray…) tunnel HTTPS via CONNECT
	}
	if c.HTTPProxy == "" && c.HTTPSProxy == "" {
		return nil
	}
	return c
}

// Resolve reports the effective proxy configuration.
func Resolve() Effective { _, e := config(); return e }

// ProxyFunc is for http.Transport.Proxy.
func ProxyFunc(req *http.Request) (*url.URL, error) {
	c, _ := config()
	if c == nil {
		return nil, nil
	}
	return c.ProxyFunc()(req.URL)
}

// ProxyFor returns the proxy used for a URL (nil = direct).
func ProxyFor(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	c, _ := config()
	if c == nil {
		return nil
	}
	p, _ := c.ProxyFunc()(u)
	return p
}

// Env returns HTTPS_PROXY/HTTP_PROXY/NO_PROXY (and lower-case copies) for child
// processes, so Claude Code and plugins use the same route. Empty when direct.
func Env() []string {
	c, _ := config()
	if c == nil {
		return nil
	}
	var out []string
	add := func(k, v string) {
		if v != "" {
			out = append(out, k+"="+v, strings.ToLower(k)+"="+v)
		}
	}
	add("HTTPS_PROXY", c.HTTPSProxy)
	add("HTTP_PROXY", c.HTTPProxy)
	if strings.HasPrefix(c.HTTPSProxy, "socks5") {
		add("ALL_PROXY", c.HTTPSProxy)
	}
	add("NO_PROXY", c.NoProxy)
	return out
}

// Transport returns a proxy-aware transport with sane timeouts.
func Transport() *http.Transport {
	return &http.Transport{
		Proxy:                 ProxyFunc,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// Client is a proxy-aware client; timeout 0 means none (use the context).
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: Transport(), Timeout: timeout}
}

// Redact hides user:password in a proxy URL.
func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
