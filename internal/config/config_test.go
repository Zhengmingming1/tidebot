package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultValidate(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrency != 2 {
		t.Fatalf("expected concurrency 2, got %d", cfg.MaxConcurrency)
	}
}

func TestLoadYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "log_level: debug\nmax_concurrency: 3\ndefault_timeout: 5m\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TIDEBOT_LOG_LEVEL", "TIDEBOT_HOME", "TIDEBOT_CLAUDE_BIN", "TIDEBOT_MAX_CONCURRENCY", "TIDEBOT_DEFAULT_PROVIDER", "TIDEBOT_WEBUI", "TIDEBOT_CONFIG"} {
		t.Setenv(k, "")
	}
	t.Setenv("TIDEBOT_CLAUDE_BIN", "/tmp/fake-claude")
	t.Setenv("TIDEBOT_MAX_CONCURRENCY", "4")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level=%s", cfg.LogLevel)
	}
	if cfg.ClaudeBin != "/tmp/fake-claude" {
		t.Fatalf("claude_bin=%s", cfg.ClaudeBin)
	}
	if cfg.MaxConcurrency != 4 {
		t.Fatalf("env should override file: got %d", cfg.MaxConcurrency)
	}
	if cfg.DefaultTimeout != 5*time.Minute {
		t.Fatalf("timeout=%v", cfg.DefaultTimeout)
	}
}

func TestValidateRejectsBadConcurrency(t *testing.T) {
	cfg := Default()
	cfg.MaxConcurrency = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveWebUI(t *testing.T) {
	cfg := Default()
	cfg.WebUIEnabled = true
	if !cfg.EffectiveWebUI() {
		t.Fatal("expected enabled")
	}
	cfg.NoWebUI = true
	if cfg.EffectiveWebUI() {
		t.Fatal("--no-webui should win")
	}
}

func TestShutdownGraceDefaultAndEnv(t *testing.T) {
	t.Setenv("TIDEBOT_HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShutdownGrace != DefaultShutdownGrace {
		t.Fatalf("default grace %v", cfg.ShutdownGrace)
	}
	t.Setenv("TIDEBOT_SHUTDOWN_GRACE", "20s")
	if cfg, _ = Load(""); cfg.ShutdownGrace != 20*time.Second {
		t.Fatalf("env grace %v", cfg.ShutdownGrace)
	}
	t.Setenv("TIDEBOT_SHUTDOWN_GRACE", "-1s")
	if cfg, _ = Load(""); cfg.ShutdownGrace != 0 {
		t.Fatalf("negative grace %v", cfg.ShutdownGrace)
	}
}
