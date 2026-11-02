package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/ipc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
	"testing"
)

func TestDaemonRecoveryFailuresReleaseOwnership(t *testing.T) {
	for _, kind := range []string{"runs", "workflows"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			evaluationTestIPC(t, a)
			ctx := context.Background()
			if kind == "runs" {
				if err := a.Store.InsertRun(ctx, db.Run{ID: "leftover", Status: "running", CreatedAt: nowUTC()}); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(`CREATE TRIGGER block_recovery BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT,'fixture failed recovery'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				w, err := a.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "recover", Nodes: []workflow.Node{{ID: "a", Kind: "condition", Condition: &workflow.Condition{Source: "input", Operator: "not_empty"}}}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('leftover',?,1,'running','','{}',?)`, w.ID, nowUTC()); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(`CREATE TRIGGER block_recovery BEFORE UPDATE ON workflow_runs BEGIN SELECT RAISE(ABORT,'fixture failed recovery'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if d, err := a.StartDaemon(ctx); err == nil {
				d.Stop()
				t.Fatal("started despite failed recovery")
			}
			ln, err := ipc.Listen(a.Cfg.IPCPath)
			if err != nil {
				t.Fatalf("IPC ownership leaked: %v", err)
			}
			ln.Close()
		})
	}
}
