package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func TestCCStateMapping(t *testing.T) {
	cases := []struct {
		found, compat bool
		running       int
		health, want  string
	}{
		{false, true, 0, "ok", CCMissing},
		{false, false, 3, "failing", CCMissing}, // not found wins
		{true, false, 0, "ok", CCWarning},
		{true, true, 2, "failing", CCWarning}, // warning beats running
		{true, true, 2, "ok", CCRunning},
		{true, true, 0, "unknown", CCReady},
		{true, true, 0, "ok", CCReady},
	}
	for _, c := range cases {
		if got, _ := CCState(c.found, c.compat, c.running, c.health); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
	if _, r := CCState(true, false, 0, "failing"); len(r) != 2 || r[0] != "version_old" || r[1] != "provider_failing" {
		t.Fatalf("reasons %v", r)
	}
}

func TestClaudeVersionParsing(t *testing.T) {
	for in, want := range map[string]string{
		"2.1.3 (Claude Code)":        "2.1.3",
		"claude-code 2.0.14\n":       "2.0.14",
		"Claude Code v1.0.9":         "",
		"2.0.0-beta.1 (Claude Code)": "2.0.0-beta.1",
		"":                           "",
	} {
		if got := parseClaudeVersion(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	if ClaudeCompatible("1.9.9") || !ClaudeCompatible("2.0.0") || !ClaudeCompatible("10.0.0") || !ClaudeCompatible("") {
		t.Fatal("compatibility check wrong")
	}
}

func TestProviderErrorLike(t *testing.T) {
	for _, m := range []string{"API Error: 401 Unauthorized", "invalid api key", "dial tcp: connection refused", "429 rate limit", "Overloaded"} {
		if !ProviderErrorLike(m) {
			t.Errorf("should be provider error: %q", m)
		}
	}
	for _, m := range []string{"", "tests failed: 3 of 10", "file not found: report.md", "exit status 1"} {
		if ProviderErrorLike(m) {
			t.Errorf("should not be provider error: %q", m)
		}
	}
}

func TestParseVmRSS(t *testing.T) {
	if got := runner.ParseVmRSS("Name:\tnode\nVmRSS:\t  20480 kB\nThreads: 9\n"); got != 20480*1024 {
		t.Fatalf("got %d", got)
	}
	if runner.ParseVmRSS("Name: x\n") != 0 {
		t.Fatal("missing VmRSS should be 0")
	}
}

func resetCCCache() {
	ccCache.Lock()
	ccCache.at = time.Time{}
	ccCache.Unlock()
}

func TestClaudeStatusDetection(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	fake := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho '2.1.3 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.Cfg.ClaudeBin = fake
	resetCCCache()
	st := a.ClaudeStatus(ctx, true)
	if st.Version != "2.1.3" || !st.Compatible || !st.Found || st.Path == "" || !filepath.IsAbs(st.Path) || st.MinVersion != MinClaudeVersion {
		t.Fatalf("mock claude should be found: %+v", st)
	}
	if st.State != CCReady || st.Provider.Name != a.Cfg.DefaultProvider || st.Provider.Health != "unknown" {
		t.Fatalf("want ready/unknown: %+v", st)
	}
	// A failed connection test turns it amber; the result is never a live probe.
	a.recordProviderTest(ctx, a.Cfg.DefaultProvider, map[string]any{"ok": false, "latency_ms": int64(120), "error": "401"})
	st = a.ClaudeStatus(ctx, false)
	if st.State != CCWarning || st.Provider.Health != "failing" || st.Provider.Source != "test" || st.Provider.LatencyMS != 120 {
		t.Fatalf("want warning from test: %+v", st)
	}
	// A newer successful run for that provider clears it.
	later := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	_, _ = a.DB.Exec(`INSERT INTO runs(id,status,provider_name,created_at,finished_at) VALUES('ccr','succeeded',?,?,?)`, a.Cfg.DefaultProvider, later, later)
	resetCCCache()
	if st = a.ClaudeStatus(ctx, false); st.State != CCReady || st.Provider.Source != "run" {
		t.Fatalf("want ready from run: %+v", st)
	}
	// Old version.
	_ = os.WriteFile(fake, []byte("#!/bin/sh\necho '1.0.40 (Claude Code)'\n"), 0o755)
	if st = a.ClaudeStatus(ctx, true); st.State != CCWarning || st.Reasons[0] != "version_old" {
		t.Fatalf("want version warning: %+v", st)
	}
	// Missing binary.
	a.Cfg.ClaudeBin = filepath.Join(t.TempDir(), "no-such-claude")
	if st = a.ClaudeStatus(ctx, true); st.State != CCMissing || st.Found {
		t.Fatalf("want missing: %+v", st)
	}
}
