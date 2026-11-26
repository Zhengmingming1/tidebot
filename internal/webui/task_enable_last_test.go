package webui

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func execAll(t *testing.T, c *client, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := c.s.deps.DB.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// TestTaskFormEnablesLastPausesFirst: saving the task form turns a task off before it
// writes anything else and on only after everything else is saved. A save that fails
// half way (here: tags cannot be written) leaves a new task never active and an
// existing paused task still paused; a pause that comes with a new schedule lands
// before the schedule, so the new fire time never belongs to an active task.
func TestTaskFormEnablesLastPausesFirst(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	execAll(t, c, `CREATE TABLE seen_active (task_id TEXT)`,
		`CREATE TRIGGER seen_active_ins AFTER INSERT ON tasks WHEN NEW.status='active' BEGIN INSERT INTO seen_active VALUES (NEW.id); END`,
		`CREATE TRIGGER seen_active_upd AFTER UPDATE OF status ON tasks WHEN NEW.status='active' BEGIN INSERT INTO seen_active VALUES (NEW.id); END`,
		`CREATE TRIGGER fail_tags BEFORE INSERT ON task_tags BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	everActive := func(id string) int {
		t.Helper()
		var n int
		if err := c.s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM seen_active WHERE task_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// a new scheduled task asked to be on, whose tags fail: never active, archived
	form := map[string]any{"name": "日报", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true, "tags": []string{"日报"}}
	if code, e := c.json(http.MethodPost, "/api/tasks", form); code != 400 {
		t.Fatalf("create with failing tags %d %v", code, e)
	}
	var id string
	var n int
	if err := c.s.deps.DB.QueryRowContext(ctx, `SELECT id FROM tasks WHERE name='日报'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.s.deps.Tasks.Get(ctx, id); v.Task.Status != "archived" || v.Task.CronExpr != "" {
		t.Fatalf("failed create left %q %q", v.Task.Status, v.Task.CronExpr)
	}
	if n = everActive(id); n != 0 {
		t.Fatalf("half-made task was active %d times", n)
	}

	// without the fault: on, with its tags; a task created off is paused by the user
	execAll(t, c, `DROP TRIGGER fail_tags`)
	code, on := c.json(http.MethodPost, "/api/tasks", form)
	if code != 201 {
		t.Fatalf("create %d %v", code, on)
	}
	onID := on["id"].(string)
	if v, _ := c.s.deps.Tasks.Get(ctx, onID); v.Task.Status != "active" || v.Task.PauseKind != "" || v.Task.PausedAt.Valid || !v.Task.NextRunAt.Valid {
		t.Fatalf("enabled task %+v", v.Task)
	}
	if err := c.s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_tags WHERE task_id=?`, onID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tags %d %v", n, err)
	}
	code, off := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "周报", "prompt": "p", "provider": "anthropic", "enabled": false})
	if code != 201 {
		t.Fatalf("create off %d %v", code, off)
	}
	offID := off["id"].(string)
	if v, _ := c.s.deps.Tasks.Get(ctx, offID); v.Task.Status != "paused" || v.Task.PauseKind != tasksvc.PauseUser || !v.Task.PausedAt.Valid {
		t.Fatalf("task created off %+v", v.Task)
	}
	if n = everActive(offID); n != 0 {
		t.Fatalf("task created off was active %d times", n)
	}

	// switching a paused task on in a save that fails half way leaves it paused
	execAll(t, c, `CREATE TRIGGER fail_tags BEFORE INSERT ON task_tags BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	if code, e := c.json(http.MethodPut, "/api/tasks/"+offID, map[string]any{"name": "周报", "prompt": "p", "provider": "anthropic", "enabled": true, "tags": []string{"周报"}}); code != 400 {
		t.Fatalf("enable with failing tags %d %v", code, e)
	}
	if v, _ := c.s.deps.Tasks.Get(ctx, offID); v.Task.Status != "paused" || v.Task.PauseKind != tasksvc.PauseUser {
		t.Fatalf("failed enable left %q %q", v.Task.Status, v.Task.PauseKind)
	}
	if n = everActive(offID); n != 0 {
		t.Fatalf("failed enable made it active %d times", n)
	}
	execAll(t, c, `DROP TRIGGER fail_tags`)

	// turning an active task off with a new schedule: the pause lands first
	execAll(t, c, `CREATE TABLE form_steps (seq INTEGER PRIMARY KEY AUTOINCREMENT, step TEXT)`,
		`CREATE TRIGGER step_status AFTER UPDATE OF status ON tasks WHEN NEW.status<>OLD.status BEGIN INSERT INTO form_steps(step) VALUES ('status:'||NEW.status); END`,
		`CREATE TRIGGER step_cron AFTER UPDATE OF cron_expr ON tasks WHEN NEW.cron_expr<>OLD.cron_expr BEGIN INSERT INTO form_steps(step) VALUES ('cron'); END`)
	if code, e := c.json(http.MethodPut, "/api/tasks/"+onID, map[string]any{"name": "日报", "prompt": "p", "provider": "anthropic", "cron": "*/5 * * * *", "enabled": false}); code != 200 {
		t.Fatalf("pause with new schedule %d %v", code, e)
	}
	rows, err := c.s.deps.DB.QueryContext(ctx, `SELECT step FROM form_steps ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		steps = append(steps, s)
	}
	if !reflect.DeepEqual(steps, []string{"status:paused", "cron"}) {
		t.Fatalf("form steps %v, want the pause before the new schedule", steps)
	}
	if v, _ := c.s.deps.Tasks.Get(ctx, onID); v.Task.Status != "paused" || v.Task.CronExpr != "*/5 * * * *" {
		t.Fatalf("paused task %+v", v.Task)
	}
}
