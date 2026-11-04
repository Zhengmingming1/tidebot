package app

import (
	"context"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func installedCap(t *testing.T) (*App, capability.Record) {
	t.Helper()
	a, _, sid := capFixture(t)
	ctx := context.Background()
	v, e := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	p := v.(capabilityProposalView)
	v, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	return a, v.(capability.Record)
}
func TestCapabilityRuntimeExplicitOwnersAndTamper(t *testing.T) {
	a, r := installedCap(t)
	ctx := context.Background()
	for _, kind := range []string{"task", "workflow_node", "button"} {
		t.Run(kind, func(t *testing.T) {
			owner := "owner_" + kind
			if e := a.caps().Bind(ctx, capability.Binding{OwnerKind: kind, OwnerID: owner, CapabilityID: r.ID}); e != nil {
				t.Fatal(e)
			}
			opts := runner.Options{EnvBase: []string{"PATH=/usr/bin"}}
			c := ctx
			if kind == "task" {
				opts.TaskID = owner
			} else {
				c = context.WithValue(ctx, capabilityOwnerKey{}, capabilityOwner{kind, owner})
			}
			clean, e := a.applyCapabilityRuntime(c, &opts)
			if e != nil {
				t.Fatal(e)
			}
			defer clean()
			if len(opts.ContextArgs) == 0 {
				t.Fatal("bound skill not staged")
			}
			unbound := runner.Options{EnvBase: []string{"PATH=/usr/bin"}}
			cleanup, e := a.applyCapabilityRuntime(ctx, &unbound)
			if e != nil {
				t.Fatal(e)
			}
			cleanup()
			if len(unbound.ContextArgs) != 0 {
				t.Fatal("capability leaked into unbound run")
			}
		})
	}
	p := filepath.Join(a.Cfg.DataDir, "capabilities", r.ID, r.VersionID, "src", "SKILL.md")
	if e := os.Chmod(p, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := a.applyCapabilityRuntime(ctx, &runner.Options{TaskID: "owner_task"}); e == nil {
		t.Fatal("modified snapshot accepted")
	}
}
func TestCapabilityEnvironmentAndToolScoping(t *testing.T) {
	for _, n := range []string{"PATH", "HOME", "LD_PRELOAD", "CLAUDE_CONFIG_DIR", "ANTHROPIC_API_KEY", "NODE_OPTIONS", "BASH_ENV", "bad-key"} {
		if allowedCapabilityEnv(n) {
			t.Errorf("accepted reserved name %s", n)
		}
	}
	if !allowedCapabilityEnv("DWS_CLIENT_SECRET") {
		t.Fatal("ordinary scoped secret rejected")
	}
	env := setCapabilityEnv([]string{"A=1", "X=old", "B=2", "X=other"}, "X", "new")
	if strings.Join(env, ";") != "A=1;B=2;X=new" {
		t.Fatal(env)
	}
	opts := runner.Options{ExtraArgs: []string{"--allowedTools", "Read"}}
	appendCapabilityTools(&opts, []string{"Bash(dws:*)"})
	if opts.ExtraArgs[1] != "Read,Bash(dws:*)" {
		t.Fatal(opts.ExtraArgs)
	}
}
func TestCapabilityBindingTargets(t *testing.T) {
	a := newTestApp(t)
	targets, e := a.CapabilityBindingTargets(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if targets == nil {
		t.Fatal("nil target collection")
	}
}
func TestCapabilityRuntimeQueuedRevocation(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprint(revoked), func(t *testing.T) {
			a, r := installedCap(t)
			ctx := context.Background()
			b := capability.Binding{OwnerKind: "button", OwnerID: "review", CapabilityID: r.ID}
			if e := a.caps().Bind(ctx, b); e != nil {
				t.Fatal(e)
			}
			ctx = context.WithValue(ctx, capabilityOwnerKey{}, capabilityOwner{"button", "review"})
			opts := runner.Options{RunID: "run_capcheck", EnvBase: []string{"PATH=/usr/bin"}}
			cleanup, e := a.applyCapabilityRuntime(ctx, &opts)
			if e != nil {
				t.Fatal(e)
			}
			defer cleanup()
			if _, e = a.DB.Exec("INSERT INTO runs(id,created_at) VALUES(?,?)", opts.RunID, nowUTC()); e != nil {
				t.Fatal(e)
			}
			if revoked {
				if e = a.caps().Unbind(ctx, b); e != nil {
					t.Fatal(e)
				}
			}
			e = opts.AtStart(ctx, &opts)
			if revoked && e == nil {
				t.Fatal("revoked queued binding launched")
			}
			if !revoked && e != nil {
				t.Fatal(e)
			}
			var count int
			a.DB.QueryRow("SELECT count(*) FROM capability_run_bindings WHERE run_id=?", opts.RunID).Scan(&count)
			if revoked && count != 0 {
				t.Fatal("revoked binding recorded")
			}
			if !revoked && count != 1 {
				t.Fatal("run version not recorded")
			}
		})
	}
}
func TestCapabilityRuntimeExistingSkillDedup(t *testing.T) {
	a, r := installedCap(t)
	ctx := context.Background()
	if e := a.caps().Bind(ctx, capability.Binding{OwnerKind: "button", OwnerID: "review", CapabilityID: r.ID}); e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, capabilityOwnerKey{}, capabilityOwner{"button", "review"})
	c := r.Lock.Components[0]
	for _, version := range []string{c.VersionID, "different"} {
		opts := runner.Options{ContextRequest: &contextplan.Request{Materials: []contextplan.Material{{ID: "skill:" + c.SkillID, Kind: "skill", Version: version}}}}
		cleanup, e := a.applyCapabilityRuntime(ctx, &opts)
		if cleanup != nil {
			cleanup()
		}
		if version == c.VersionID {
			if e != nil {
				t.Fatal(e)
			}
			if len(opts.ContextArgs) != 0 {
				t.Fatal("same skill was staged twice")
			}
		} else if e == nil {
			t.Fatal("conflicting task/capability skill versions accepted")
		}
	}
}
func TestCapabilityExportDefaultsCurrentVersion(t *testing.T) {
	a, r := installedCap(t)
	v, e := a.CapabilityRequest(context.Background(), "export", webui.CapabilityRequest{CapabilityID: r.ID})
	if e != nil {
		t.Fatal(e)
	}
	lock, files, e := capability.ParseBundle(v.([]byte))
	if e != nil {
		t.Fatal(e)
	}
	if lock.ID != r.ID || len(files) == 0 {
		t.Fatal("wrong export")
	}
}
