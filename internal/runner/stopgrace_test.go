package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cleanupClaude writes mark when asked to stop (SIGTERM), like Claude Code
// removing the sandbox's placeholder files before it exits.
func cleanupClaude(t *testing.T, dir, mark string) string {
	t.Helper()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"trap 'echo cleaned > \"" + mark + "\"; kill $!; exit 143' TERM\n" +
		`echo '{"type":"assistant","message":{"id":"m1","model":"glm-5.3","usage":{"input_tokens":1,"output_tokens":1}}}'` + "\n" +
		"sleep 30 &\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestStopGraceLetsClaudeCleanUp(t *testing.T) {
	skipOnWindows(t)
	for _, tc := range []struct {
		grace   time.Duration
		cleaned bool
	}{{5 * time.Second, true}, {0, false}} {
		r, _, dir := drainRunner(t, 1)
		mark := filepath.Join(dir, "mark")
		opts := drainOpts(cleanupClaude(t, dir, mark), dir)
		opts.StopGrace = tc.grace
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan Result, 1)
		go func() {
			res, _ := r.RunOnce(ctx, opts)
			done <- res
		}()
		waitActive(t, r, 1)
		time.Sleep(300 * time.Millisecond) // the trap is set
		start := time.Now()
		cancel()
		var res Result
		select {
		case res = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("stop hung")
		}
		if took := time.Since(start); took > 4*time.Second {
			t.Fatalf("grace %v: stop took %v", tc.grace, took)
		}
		_, err := os.Stat(mark)
		if (err == nil) != tc.cleaned {
			t.Fatalf("grace %v: cleaned=%v (status %s)", tc.grace, err == nil, res.Status)
		}
		if res.Status != "canceled" {
			t.Fatalf("grace %v: status %s %s", tc.grace, res.Status, res.ErrorMessage)
		}
	}
}
