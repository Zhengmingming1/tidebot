package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func aiWait(t *testing.T, a *App, jobID string, want ...string) map[string]any {
	t.Helper()
	var v map[string]any
	waitFor(t, 30*time.Second, func() bool {
		r, err := a.CallExt(context.Background(), "plugins.ai.status", json.RawMessage(`{"job_id":"`+jobID+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		v = r.(map[string]any)
		for _, w := range want {
			if v["status"] == w {
				return true
			}
		}
		if v["status"] == "failed" {
			t.Fatalf("job failed: %v", v["error"])
		}
		return false
	})
	return v
}

func TestRedactSecrets(t *testing.T) {
	in := "watch repo x; my token: ghp_abcdefghijklmnopqrstuvwxyz123456 and password=hunter2 key sk-ant-abcdefghijklmnopqrstuv"
	out, n := RedactSecrets(in)
	if n < 3 || strings.Contains(out, "hunter2") || strings.Contains(out, "ghp_") || strings.Contains(out, "sk-ant") || !strings.Contains(out, "watch repo x") {
		t.Fatalf("%d %s", n, out)
	}
}

func TestAIPluginBuilderEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for the generated demo plugin")
	}
	a := newTestApp(t)
	ctx := context.Background()
	argsFile := t.TempDir() + "/args"
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	r, err := a.CallExt(ctx, "plugins.ai.draft", json.RawMessage(`{"description":"当 GitHub 仓库发布新版本时触发任务, token sk-ant-abcdefghijklmnopqrstuvwxyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	job := r.(map[string]any)["id"].(string)
	v := aiWait(t, a, job, "spec_ready")
	spec := v["spec"].(map[string]any)
	if spec["id"] != "github-release-watch" || spec["type"] != "trigger" || v["redacted"].(float64) < 1 {
		t.Fatalf("%v", v)
	}
	if strings.Contains(v["description"].(string), "sk-ant") {
		t.Fatal("secret reached the prompt")
	}
	if _, err := a.CallExt(ctx, "plugins.ai.install", json.RawMessage(`{"job_id":"`+job+`"}`)); err == nil {
		t.Fatal("install before generate")
	}
	if _, err := a.CallExt(ctx, "plugins.ai.generate", json.RawMessage(`{"job_id":"`+job+`"}`)); err != nil {
		t.Fatal(err)
	}
	v = aiWait(t, a, job, "review")
	b, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(b), "--disallowedTools") || !strings.Contains(string(b), "Bash") || !strings.Contains(string(b), "--max-budget-usd") || strings.Contains(string(b), "--mcp-config") {
		t.Fatalf("build args %s", b)
	}
	if v["passed"] != true {
		t.Fatalf("self-check: %v", toJSONStr(v["checks"]))
	}
	names := toJSONStr(v["checks"])
	for _, c := range []string{"manifest", "schema", "start", "handshake", "enumerate"} {
		if !strings.Contains(names, `"`+c+`"`) {
			t.Fatalf("missing check %s: %s", c, names)
		}
	}
	perms := v["permissions"].([]any)
	if len(perms) != 2 {
		t.Fatalf("perms %v", perms)
	}
	// Grant only one item: the other stays off (nothing is auto-granted).
	if _, err := a.CallExt(ctx, "plugins.ai.install", json.RawMessage(`{"job_id":"`+job+`","permissions":["network:api.github.com"]}`)); err != nil {
		t.Fatal(err)
	}
	in, _ := a.Plugins.Get(ctx, "github-release-watch")
	if in.Source != "ai-generated" || in.Current == nil || len(in.Current.Approved) != 1 || in.Current.Version != "0.1.0" {
		t.Fatalf("%+v", in.Current)
	}
	// Iterate: new immutable version, history kept, rollback works.
	r, err = a.CallExt(ctx, "plugins.ai.iterate", json.RawMessage(`{"id":"github-release-watch","message":"也包含预发布版本(可选)"}`))
	if err != nil {
		t.Fatal(err)
	}
	job2 := r.(map[string]any)["id"].(string)
	v = aiWait(t, a, job2, "review")
	if v["passed"] != true || !strings.Contains(toJSONStr(v["files"]), "include_prereleases") || !strings.Contains(toJSONStr(v["files"]), `"modified"`) {
		t.Fatalf("%v", toJSONStr(v))
	}
	if _, err := a.CallExt(ctx, "plugins.ai.install", json.RawMessage(`{"job_id":"`+job2+`","permissions":["network:api.github.com"]}`)); err != nil {
		t.Fatal(err)
	}
	in, _ = a.Plugins.Get(ctx, "github-release-watch")
	if in.Current.Version != "0.1.1" || len(in.Versions) != 2 {
		t.Fatalf("v %s n=%d", in.Current.Version, len(in.Versions))
	}
	old := ""
	for _, x := range in.Versions {
		if x.Version == "0.1.0" {
			old = x.ID
		}
	}
	if _, err := a.CallExt(ctx, "plugins.rollback", json.RawMessage(`{"id":"github-release-watch","version_id":"`+old+`"}`)); err != nil {
		t.Fatal(err)
	}
	in, _ = a.Plugins.Get(ctx, "github-release-watch")
	if in.Current.Version != "0.1.0" {
		t.Fatal("rollback")
	}
	msgs, _ := a.CallExt(ctx, "plugins.ai.messages", json.RawMessage(`{"id":"github-release-watch"}`))
	if len(msgs.([]map[string]string)) != 4 {
		t.Fatalf("messages %v", msgs)
	}
}

func TestAIPluginBuilderFailedCheckBlocksInstall(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed")
	}
	a := newTestApp(t)
	ctx := context.Background()
	t.Setenv("TIDEBOT_MOCK_AI_BREAK", "1")
	r, _ := a.CallExt(ctx, "plugins.ai.draft", json.RawMessage(`{"description":"watch GitHub releases for a repo"}`))
	job := r.(map[string]any)["id"].(string)
	aiWait(t, a, job, "spec_ready")
	_, _ = a.CallExt(ctx, "plugins.ai.generate", json.RawMessage(`{"job_id":"`+job+`"}`))
	v := aiWait(t, a, job, "review")
	if v["passed"] == true {
		t.Fatal("broken plugin passed")
	}
	if _, err := a.CallExt(ctx, "plugins.ai.install", json.RawMessage(`{"job_id":"`+job+`","permissions":[]}`)); err == nil {
		t.Fatal("installed a plugin that failed self-check")
	}
}

func TestAIPluginBuilderCancel(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	t.Setenv("TIDEBOT_MOCK_STEPS", "20")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	r, _ := a.CallExt(ctx, "plugins.ai.draft", json.RawMessage(`{"description":"watch GitHub releases for a repo"}`))
	job := r.(map[string]any)["id"].(string)
	time.Sleep(500 * time.Millisecond)
	if _, err := a.CallExt(ctx, "plugins.ai.cancel", json.RawMessage(`{"job_id":"`+job+`"}`)); err != nil {
		t.Fatal(err)
	}
	aiWait(t, a, job, "canceled")
}
