// Package config loads tidebot configuration from YAML/JSON files and env overrides.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	// Windows has no system zoneinfo: embed it so a configured timezone (or $TZ) loads
	// the same on macOS, Windows and Linux.
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

// DefaultShutdownGrace: see Config.ShutdownGrace.
const DefaultShutdownGrace = 5 * time.Second

// Config is the runtime configuration for agentd / agentctl.
type Config struct {
	HomeDir          string        `json:"home_dir" yaml:"home_dir"`
	DataDir          string        `json:"data_dir" yaml:"data_dir"`
	DBPath           string        `json:"db_path" yaml:"db_path"`
	LogLevel         string        `json:"log_level" yaml:"log_level"`
	LogFormat        string        `json:"log_format" yaml:"log_format"`
	ClaudeBin        string        `json:"claude_bin" yaml:"claude_bin"`
	MaxConcurrency   int           `json:"max_concurrency" yaml:"max_concurrency"`
	DefaultTimeout   time.Duration `json:"default_timeout" yaml:"default_timeout"`
	DefaultProvider  string        `json:"default_provider" yaml:"default_provider"`
	ProvidersFile    string        `json:"providers_file" yaml:"providers_file"`
	WebUIEnabled     bool          `json:"webui_enabled" yaml:"webui_enabled"`
	NoWebUI          bool          `json:"no_webui" yaml:"no_webui"` // CLI override
	WebUIAddr        string        `json:"webui_addr" yaml:"webui_addr"`
	WebUIAllowLAN    bool          `json:"webui_allow_lan" yaml:"webui_allow_lan"`
	WebUITLSCert     string        `json:"webui_tls_cert" yaml:"webui_tls_cert"`
	WebUITLSKey      string        `json:"webui_tls_key" yaml:"webui_tls_key"`
	WebUIAllowHTTP   bool          `json:"webui_allow_http" yaml:"webui_allow_http"` // explicit: LAN without TLS
	WebUIIdleMinutes int           `json:"webui_idle_minutes" yaml:"webui_idle_minutes"`
	Timezone         string        `json:"timezone" yaml:"timezone"` // IANA name for UI display; empty = system
	SchedulerTick    time.Duration `json:"scheduler_tick" yaml:"scheduler_tick"`
	GuardDisabled    bool          `json:"guard_disabled" yaml:"guard_disabled"` // skip the PreToolUse danger hook
	IPCPath          string        `json:"ipc_path" yaml:"ipc_path"`
	// ShutdownGrace is how long quitting waits for running runs to finish on
	// their own before stopping them (they're recorded as canceled either way).
	// The default keeps grace + the 3s write wait under Docker's 10s stop timeout.
	ShutdownGrace time.Duration `json:"shutdown_grace" yaml:"shutdown_grace"`
}

// Default returns a Config with sensible Phase-0 defaults.
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	appHome := filepath.Join(home, ".tidebot")
	return Config{
		HomeDir:          appHome,
		DataDir:          filepath.Join(appHome, "data"),
		DBPath:           filepath.Join(appHome, "data", "tidebot.db"),
		LogLevel:         "info",
		LogFormat:        "text",
		ClaudeBin:        "claude",
		MaxConcurrency:   2,
		DefaultTimeout:   30 * time.Minute,
		DefaultProvider:  "anthropic",
		ProvidersFile:    filepath.Join(appHome, "providers.yaml"),
		WebUIEnabled:     false,
		WebUIAddr:        "127.0.0.1:7420",
		WebUIIdleMinutes: 30,
		SchedulerTick:    15 * time.Second,
		ShutdownGrace:    DefaultShutdownGrace,
		IPCPath:          filepath.Join(appHome, "agentd.sock"),
	}
}

// Load reads optional config file (YAML or JSON) then applies TIDEBOT_* env overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = os.Getenv("TIDEBOT_CONFIG")
	}
	if path == "" {
		// Try default locations.
		for _, candidate := range []string{
			filepath.Join(cfg.HomeDir, "config.yaml"),
			filepath.Join(cfg.HomeDir, "config.yml"),
			filepath.Join(cfg.HomeDir, "config.json"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path != "" {
		if err := loadFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}
	applyEnv(&cfg)
	if cfg.ShutdownGrace < 0 {
		cfg.ShutdownGrace = 0
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(b, cfg); err != nil {
			return fmt.Errorf("parse config json: %w", err)
		}
	case ".yaml", ".yml", "":
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return fmt.Errorf("parse config yaml: %w", err)
		}
	default:
		// Try YAML first, then JSON.
		if err := yaml.Unmarshal(b, cfg); err != nil {
			if err2 := json.Unmarshal(b, cfg); err2 != nil {
				return fmt.Errorf("parse config: yaml=%v json=%v", err, err2)
			}
		}
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("TIDEBOT_HOME"); v != "" {
		cfg.HomeDir = v
		cfg.DataDir = filepath.Join(v, "data")
		if os.Getenv("TIDEBOT_DB") == "" {
			cfg.DBPath = filepath.Join(cfg.DataDir, "tidebot.db")
		}
		if os.Getenv("TIDEBOT_PROVIDERS_FILE") == "" {
			cfg.ProvidersFile = filepath.Join(v, "providers.yaml")
		}
		if os.Getenv("TIDEBOT_IPC") == "" {
			cfg.IPCPath = filepath.Join(v, "agentd.sock")
		}
	}
	if v := os.Getenv("TIDEBOT_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("TIDEBOT_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("TIDEBOT_LOG_FORMAT"); v != "" {
		cfg.LogFormat = v
	}
	if v := os.Getenv("TIDEBOT_CLAUDE_BIN"); v != "" {
		cfg.ClaudeBin = v
	}
	if v := os.Getenv("TIDEBOT_MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrency = n
		}
	}
	if v := os.Getenv("TIDEBOT_DEFAULT_PROVIDER"); v != "" {
		cfg.DefaultProvider = v
	}
	if v := os.Getenv("TIDEBOT_PROVIDERS_FILE"); v != "" {
		cfg.ProvidersFile = v
	}
	if v := os.Getenv("TIDEBOT_DEFAULT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DefaultTimeout = d
		}
	}
	if v := os.Getenv("TIDEBOT_SCHEDULER_TICK"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SchedulerTick = d
		}
	}
	if v := os.Getenv("TIDEBOT_SHUTDOWN_GRACE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ShutdownGrace = d
		}
	}
	if v := os.Getenv("TIDEBOT_WEBUI"); v != "" {
		cfg.WebUIEnabled = parseBool(v)
	}
	if v := os.Getenv("TIDEBOT_WEBUI_ADDR"); v != "" {
		cfg.WebUIAddr = v
	}
	if v := os.Getenv("TIDEBOT_WEBUI_ALLOW_LAN"); v != "" {
		cfg.WebUIAllowLAN = parseBool(v)
	}
	if v := os.Getenv("TIDEBOT_WEBUI_ALLOW_HTTP"); v != "" {
		cfg.WebUIAllowHTTP = parseBool(v)
	}
	if v := os.Getenv("TIDEBOT_IPC"); v != "" {
		cfg.IPCPath = v
	}
	if v := os.Getenv("TIDEBOT_TIMEZONE"); v != "" {
		cfg.Timezone = v
	}
}

// Location resolves the display timezone: config, $TZ, /etc/timezone, /etc/localtime link, else Local.
func (c Config) Location() *time.Location {
	cands := []string{c.Timezone, os.Getenv("TZ")}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		cands = append(cands, strings.TrimSpace(string(b)))
	}
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(l, "zoneinfo/"); i >= 0 {
			cands = append(cands, l[i+len("zoneinfo/"):])
		}
	}
	for _, n := range cands {
		if n == "" {
			continue
		}
		if loc, err := time.LoadLocation(n); err == nil {
			return loc
		}
	}
	return time.Local
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Validate checks required fields and ranges.
func (c Config) Validate() error {
	if c.MaxConcurrency < 1 {
		return fmt.Errorf("max_concurrency must be >= 1")
	}
	if c.DefaultTimeout < time.Second {
		return fmt.Errorf("default_timeout must be >= 1s")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	return nil
}

// EnsureDirs creates HomeDir and DataDir if missing.
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.HomeDir, c.DataDir, filepath.Dir(c.DBPath), filepath.Dir(c.IPCPath)} {
		if d == "" || d == "." {
			continue
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// EffectiveWebUI returns whether HTTP WebUI should listen, honoring --no-webui.
func (c Config) EffectiveWebUI() bool {
	if c.NoWebUI {
		return false
	}
	return c.WebUIEnabled
}
