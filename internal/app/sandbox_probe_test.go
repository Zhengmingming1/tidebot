package app

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

const userns = "bwrap: setting up uid map: Permission denied"

// bwrap on PATH but refused by the kernel (Ubuntu 24.04+ AppArmor, a container's
// seccomp profile): the status says so, a command run is refused up front with
// bubblewrap's own message, and the dependency check blocks the sandbox and Bash
// instead of reporting "Commands will run in the system sandbox".
func TestSandboxBrokenRefusesCommandRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bubblewrap is the Linux sandbox")
	}
	a := newTestApp(t)
	sandboxDeps(t, true)
	sandboxProbe = func(string) string { return userns }
	if st := a.SandboxStatus(); st.Status != guard.SandboxBroken || st.Error != userns || len(st.Missing) != 0 {
		t.Fatalf("status %+v", st)
	}
	_, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: t.TempDir(), RunMode: "exec"}, "", 0)
	if !errors.Is(err, ErrSandboxUnavailable) || !strings.Contains(err.Error(), userns) {
		t.Fatalf("err %v", err)
	}
	rep, err := a.dependencyPreflight(context.Background(), webui.DependencyCheckRequest{Kind: "draft", Prompt: "hi", Mode: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	var sb *webui.DependencyFinding
	for i, f := range rep.Findings {
		if f.Dependency == webui.DepTitleSandbox {
			sb = &rep.Findings[i]
		}
	}
	if sb == nil || sb.Level != "blocked" || sb.Reason != webui.DepReasonSandboxBroken || !strings.Contains(sb.Evidence, userns) || sb.Suggestion != webui.DepSuggestSandboxUserns {
		t.Fatalf("sandbox finding %+v", sb)
	}
	// read-only runs don't need the sandbox and still start
	if _, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: t.TempDir(), RunMode: "read"}, "", 0); err != nil {
		t.Fatalf("read-only run: %v", err)
	}
	// once bubblewrap starts again, command runs do too
	sandboxProbe = func(string) string { return "" }
	if st := a.SandboxStatus(); st.Status != guard.SandboxOK {
		t.Fatalf("after fix %+v", st)
	}
}

// The probe result is kept per bwrap path: a start for 10 minutes, a refusal for
// 15 seconds, so fixing the setting shows up on the next page open.
func TestCachedBwrapProbe(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	now := t0
	calls := map[string]int{}
	answer := map[string]string{"/usr/bin/bwrap": userns, "/opt/bwrap": ""}
	probe := cachedBwrapProbe(func(p string) string { calls[p]++; return answer[p] }, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if probe("/usr/bin/bwrap") != userns || probe("/opt/bwrap") != "" {
			t.Fatal("wrong answer")
		}
	}
	if calls["/usr/bin/bwrap"] != 1 || calls["/opt/bwrap"] != 1 {
		t.Fatalf("not cached: %v", calls)
	}
	answer["/usr/bin/bwrap"] = ""
	now = t0.Add(bwrapProbeRefuseFor + time.Second)
	if probe("/usr/bin/bwrap") != "" || calls["/usr/bin/bwrap"] != 2 {
		t.Fatalf("refusal kept too long: %v", calls)
	}
	now = t0.Add(bwrapProbeOKFor - time.Second)
	probe("/opt/bwrap")
	if calls["/opt/bwrap"] != 1 {
		t.Fatalf("start re-probed early: %v", calls)
	}
	now = now.Add(2 * time.Second)
	probe("/opt/bwrap")
	if calls["/opt/bwrap"] != 2 {
		t.Fatalf("start kept too long: %v", calls)
	}
}
