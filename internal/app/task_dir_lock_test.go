package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// taskInFolder makes a task working in dir at the given permission level.
func taskInFolder(t *testing.T, a *App, name, dir, mode string) string {
	t.Helper()
	ctx := context.Background()
	tid := mkTask(t, a, name)
	v, err := a.Tasks.Get(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	sp := *v.Spec
	sp.WorkspaceDir, sp.RunMode = dir, mode
	if _, err := a.Tasks.UpdateSpec(ctx, tid, sp, "user", "folder"); err != nil {
		t.Fatal(err)
	}
	return tid
}

// A task that may change its folder takes the same one-at-a-time hold as the Run
// dialog, sessions and workflows: while another run changes the folder, a manual
// run is refused before it has a run row (the page says the folder is busy),
// whatever path (a link) the task saved; a read-only task still runs.
func TestTaskRunHoldsItsFolder(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	ctx := context.Background()
	dir := t.TempDir()
	real := editLockKey(dir)
	if !lockEditDir(real) { // a session turn / Run dialog run is changing the folder
		t.Fatal("folder already held")
	}
	held := true
	defer func() {
		if held {
			unlockEditDir(real)
		}
	}()
	saved := dir
	if runtime.GOOS != "windows" {
		saved = filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dir, saved); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"", "edit", "exec"} {
		tid := taskInFolder(t, a, "w-"+mode, saved+string(filepath.Separator), mode)
		v, _ := a.Tasks.Get(ctx, tid)
		if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); !errors.Is(err, guard.ErrDirBusy) {
			t.Fatalf("mode %q: err %v", mode, err)
		}
		if n := runCount(a, tid); n != 0 {
			t.Fatalf("mode %q: %d run rows for a refused run", mode, n)
		}
		if _, err := a.startTaskAsync(tid, "production"); !errors.Is(err, guard.ErrDirBusy) {
			t.Fatalf("mode %q run now: %v", mode, err)
		}
	}
	ro := taskInFolder(t, a, "r", saved, "read")
	v, _ := a.Tasks.Get(ctx, ro)
	if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); err != nil {
		t.Fatalf("read-only task: %v", err)
	}
	unlockEditDir(real)
	held = false

	// Free again: the task runs, and lets go of the folder when it ends.
	tid := taskInFolder(t, a, "after", saved, "edit")
	v, _ = a.Tasks.Get(ctx, tid)
	if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); err != nil {
		t.Fatal(err)
	}
	if !lockEditDir(real) {
		t.Fatal("task run kept the folder")
	}
	unlockEditDir(real)
}

// A scheduled run whose folder is busy waits until it is free and runs then: no
// failed run, no retry used, no breaker strike. Paused meanwhile, it never runs.
func TestScheduledTaskWaitsForBusyFolder(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	old := dirBusyPoll
	dirBusyPoll = 20 * time.Millisecond
	t.Cleanup(func() { dirBusyPoll = old })
	ctx := context.Background()
	dir := t.TempDir()
	real := editLockKey(dir)
	tid := taskInFolder(t, a, "cron", dir, "exec")
	task, _ := a.Store.GetTask(ctx, tid)

	if !lockEditDir(real) {
		t.Fatal("folder already held")
	}
	done := make(chan struct{})
	go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
	time.Sleep(150 * time.Millisecond)
	if n := runCount(a, tid); n != 0 {
		unlockEditDir(real)
		t.Fatalf("%d run rows while the folder was busy", n)
	}
	unlockEditDir(real)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("scheduled run never went after the folder was freed")
	}
	var ok, other int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status='succeeded'`, tid).Scan(&ok)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status<>'succeeded'`, tid).Scan(&other)
	if ok != 1 || other != 0 || a.Policy.Get(ctx, tid).ConsecutiveFailures != 0 {
		t.Fatalf("succeeded=%d other=%d fails=%d", ok, other, a.Policy.Get(ctx, tid).ConsecutiveFailures)
	}

	// Paused while it waited: it stops waiting and never runs.
	if !lockEditDir(real) {
		t.Fatal("folder held")
	}
	defer unlockEditDir(real)
	done = make(chan struct{})
	go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
	time.Sleep(60 * time.Millisecond)
	if err := a.Tasks.SetStatus(ctx, tid, "paused", "user"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("paused task kept waiting for its folder")
	}
	if n := runCount(a, tid); n != 1 {
		t.Fatalf("paused task ran: %d runs", n)
	}
}

// One task's runs share their folder's hold (its overlap policy decides how many
// go); a session, a Run dialog run, a workflow or another task is kept out
// until the last of them ends.
func TestTaskRunsShareFolderHold(t *testing.T) {
	dir := editLockKey(t.TempDir())
	if !lockEditDirAs(dir, "task:a") || !lockEditDirAs(dir, "task:a") {
		t.Fatal("same task refused")
	}
	if lockEditDir(dir) || lockEditDirAs(dir, "task:b") {
		t.Fatal("other run let in beside a task's runs")
	}
	unlockEditDir(dir)
	if lockEditDir(dir) {
		t.Fatal("let in while a task run still holds the folder")
	}
	unlockEditDir(dir)
	if !lockEditDir(dir) {
		t.Fatal("folder not freed after the task's last run")
	}
	if lockEditDirAs(dir, "task:a") || lockEditDir(dir) {
		t.Fatal("exclusive hold shared")
	}
	unlockEditDir(dir)
}
