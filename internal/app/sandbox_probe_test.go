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
	// when the cause is known: the run's error and the finding give the setting to change
	fix := "sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0"
	sandboxDiag = func(string) (string, string) { return guard.SandboxCauseAppArmor, fix }
	if st := a.SandboxStatus(); st.Cause != guard.SandboxCauseAppArmor || st.Fix != fix {
		t.Fatalf("diagnosed status %+v", st)
	}
	if _, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: t.TempDir(), RunMode: "exec"}, "", 0); !errors.Is(err, ErrSandboxUnavailable) || !strings.Contains(err.Error(), fix) {
		t.Fatalf("err %v", err)
	}
	// the diagnosis gets bubblewrap's message (a refused nested start reads differently)
	nested := guard.BwrapNestedPrefix + "bwrap: No permissions to create a new namespace"
	sandboxProbe = func(string) string { return nested }
	var gotErr string
	sandboxDiag = func(e string) (string, string) {
		gotErr = e
		return guard.SandboxCauseAppArmorBwrap, "sudo apparmor_parser -R x"
	}
	if st := a.SandboxStatus(); st.Status != guard.SandboxBroken || gotErr != nested || st.Cause != guard.SandboxCauseAppArmorBwrap {
		t.Fatalf("nested status %+v (diagnosed %q)", st, gotErr)
	}
	sandboxProbe = func(string) string { return userns }
	for cause, want := range map[string]string{guard.SandboxCauseAppArmor: webui.DepSuggestSandboxAppArmor, guard.SandboxCauseContainer: webui.DepSuggestSandboxContainer,
		guard.SandboxCauseAppArmorBwrap: webui.DepSuggestSandboxAppArmorBwrap, guard.SandboxCauseNested: webui.DepSuggestSandboxNested} {
		c, f := cause, fix
		if c == guard.SandboxCauseContainer {
			f = ""
		}
		sandboxDiag = func(string) (string, string) { return c, f }
		rep, err := a.dependencyPreflight(context.Background(), webui.DependencyCheckRequest{Kind: "draft", Prompt: "hi", Mode: "exec"})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, f := range rep.Findings {
			if f.Dependency == webui.DepTitleSandbox {
				got = f.Suggestion
			}
		}
		if got != want {
			t.Fatalf("%s suggestion %q, want %q", cause, got, want)
		}
	}
	// the diagnosis is only read when bubblewrap was refused
	diagCalls := 0
	sandboxDiag = func(string) (string, string) { diagCalls++; return "", "" }
	// read-only runs don't need the sandbox and still start
	if _, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: t.TempDir(), RunMode: "read"}, "", 0); err != nil {
		t.Fatalf("read-only run: %v", err)
	}
	// once bubblewrap starts again, command runs do too
	sandboxProbe = func(string) string { return "" }
	if st := a.SandboxStatus(); st.Status != guard.SandboxOK || st.Cause != "" || st.Fix != "" || diagCalls != 0 {
		t.Fatalf("after fix %+v (diagnosed %d times)", st, diagCalls)
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
