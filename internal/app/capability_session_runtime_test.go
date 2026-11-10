package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func bindSessionCapability(t *testing.T, a *App, r capability.Record) string {
	t.Helper()
	s, err := a.CreateChatSession(context.Background(), webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"capabilities": []string{r.ID}})
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json=? WHERE id=?`, string(raw), s.ID); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func TestSessionCapabilityRuntimeEnvironmentAndSkillDedup(t *testing.T) {
	a, r := installedCap(t)
	ctx := context.Background()
	files, err := a.Skills.Files(ctx, r.Lock.Components[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	lock := r.Lock
	lock.Requires.Secrets = []capability.SecretRequirement{{Name: "FIXTURE_CLI_TOKEN"}}
	lock.Permissions.Tools = []string{"Read"}
	toolDir := t.TempDir()
	lock.Resolved = map[string]capability.Resolved{"fixture": {Path: filepath.Join(toolDir, "fixture"), Source: "system", Status: "ready"}}
	r, err = a.caps().Install(ctx, lock, files)
	if err != nil {
		t.Fatal(err)
	}
	a.Secrets = secrets.NewMemory()
	if err = a.Secrets.Set("cap:"+r.ID+":FIXTURE_CLI_TOKEN", "fixture-private-value"); err != nil {
		t.Fatal(err)
	}
	sid := bindSessionCapability(t, a, r)
	row, err := a.chatSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := a.stageChatSkills(ctx, row)
	if st == nil {
		t.Fatal("no staged skill")
	}
	defer st.Close()
	opts := runner.Options{RunMode: "read", KeepSession: true, EnvBase: []string{"HOME=/fixture/home", "PATH=/fixture/base", "SYSTEM_FIXTURE=retained"}, ContextArgs: []string{"--disallowedTools", "Bash,Write,Edit"}, ContextRequest: st.contextRequest(a.modernClaude(), false)}
	cleanup, err := a.applySessionCapabilityMCP(ctx, &opts, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	env := strings.Join(opts.EnvBase, "\n")
	for _, want := range []string{"HOME=/fixture/home", "SYSTEM_FIXTURE=retained", "FIXTURE_CLI_TOKEN=fixture-private-value", "PATH=" + toolDir + string(os.PathListSeparator) + "/fixture/base"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing environment %s", want)
		}
	}
	if len(opts.ContextArgs) != 2 || len(opts.MCPServers) != 0 {
		t.Fatalf("duplicate skill or unauthorized tools: %v", opts.ContextArgs)
	}
	if opts.AtStart == nil {
		t.Fatal("skill-only capability not pinned")
	}
}

func TestSessionCapabilityRuntimeQueuedChanges(t *testing.T) {
	for _, change := range []string{"none", "unbind", "close", "version", "tamper"} {
		t.Run(change, func(t *testing.T) {
			a, r := installedCap(t)
			ctx := context.Background()
			sid := bindSessionCapability(t, a, r)
			opts := runner.Options{RunID: "session_runtime", KeepSession: true, RunMode: "read"}
			cleanup, err := a.applySessionCapabilityMCP(ctx, &opts, sid)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if _, err = a.DB.Exec(`INSERT INTO runs(id,created_at) VALUES(?,?)`, opts.RunID, nowUTC()); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "unbind":
				_, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json='{}' WHERE id=?`, sid)
			case "close":
				_, err = a.DB.Exec(`UPDATE chat_sessions SET status='closed' WHERE id=?`, sid)
			case "version":
				files, e := a.Skills.Files(ctx, r.Lock.Components[0].VersionID)
				if e != nil {
					t.Fatal(e)
				}
				_, err = a.caps().Install(ctx, r.Lock, files)
			case "tamper":
				p := filepath.Join(a.Cfg.DataDir, "capabilities", r.ID, r.VersionID, "src", "SKILL.md")
				if err = os.Chmod(p, 0600); err == nil {
					err = os.WriteFile(p, []byte("tampered"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = opts.AtStart(ctx, &opts)
			if (change == "none") != (err == nil) {
				t.Fatalf("change %s: %v", change, err)
			}
			var count int
			if err = a.DB.QueryRow(`SELECT count(*) FROM capability_run_bindings WHERE run_id=?`, opts.RunID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if change == "none" {
				want = 1
			}
			if count != want {
				t.Fatalf("pins %d, want %d", count, want)
			}
		})
	}
}

func TestSendChatTurnPinsSkillOnlyCapability(t *testing.T) {
	chatHome(t)
	a, r := installedCap(t)
	sid := bindSessionCapability(t, a, r)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	started, err := a.SendChatTurn(context.Background(), sid, "Read the attached instructions")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, sid, 1)
	var version string
	if err = a.DB.QueryRow(`SELECT version_id FROM capability_run_bindings WHERE run_id=? AND capability_id=?`, started.RunID, r.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != r.VersionID {
		t.Fatal("wrong snapshot")
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(argAfter(args, "--disallowedTools"), "Bash") {
		t.Fatal("read-only guard lost")
	}
	for _, flag := range []string{"--plugin-dir", "--add-dir"} {
		n := 0
		for _, arg := range args {
			if arg == flag {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("capability skill staged twice: %v", args)
		}
	}
}
