package tasksvc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func TestPauseReason(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	svc := New(store, version.New(sqlDB), nil)
	ctx := context.Background()
	tv, err := svc.Create(ctx, CreateInput{Name: "n", Prompt: "p", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	get := func() *db.Task {
		t.Helper()
		tk, err := store.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	ps := &policy.Store{DB: sqlDB}
	for i := 0; i < policy.BreakerThreshold; i++ {
		if _, _, err := ps.RecordOutcome(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}

	// A breaker pause records kind, detail and time; the text goes in as a parameter.
	evil := "x'); DROP TABLE tasks; --"
	if err := svc.Pause(ctx, id, PauseBreaker, evil); err != nil {
		t.Fatal(err)
	}
	tk := get()
	if tk.Status != "paused" || tk.PauseKind != PauseBreaker || tk.PauseDetail != evil || !tk.PausedAt.Valid {
		t.Fatalf("breaker pause %+v", tk)
	}
	first := tk.PausedAt.String

	// Pausing again (by hand, or another trip) keeps the first reason and time.
	if err := svc.SetStatus(ctx, id, "paused", "user"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.PauseKind != PauseBreaker || tk.PausedAt.String != first {
		t.Fatalf("second pause relabelled %+v", tk)
	}
	// The list carries it too.
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].Task.PauseKind != PauseBreaker {
		t.Fatalf("list %+v %v", list, err)
	}

	// Resuming clears the reason and the failure count.
	if err := svc.SetStatus(ctx, id, "active", "user"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.Status != "active" || tk.PauseKind != "" || tk.PauseDetail != "" || tk.PausedAt.Valid {
		t.Fatalf("after resume %+v", tk)
	}
	if n := ps.Get(ctx, id).ConsecutiveFailures; n != 0 {
		t.Fatalf("failures after resume = %d", n)
	}

	// Saving an active task as active again does not touch the count.
	if _, _, err := ps.RecordOutcome(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatus(ctx, id, "active", "webui"); err != nil {
		t.Fatal(err)
	}
	if n := ps.Get(ctx, id).ConsecutiveFailures; n != 1 {
		t.Fatalf("active→active reset the count: %d", n)
	}

	// A manual pause is "user"; a long detail is cut on a rune boundary.
	if err := svc.SetStatus(ctx, id, "paused", "user"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.PauseKind != PauseUser {
		t.Fatalf("manual pause %q", tk.PauseKind)
	}
	_ = svc.SetStatus(ctx, id, "active", "user")
	if err := svc.Pause(ctx, id, PausePlugin, strings.Repeat("插", 200)); err != nil {
		t.Fatal(err)
	}
	if tk = get(); len(tk.PauseDetail) > 300 || !utf8.ValidString(tk.PauseDetail) {
		t.Fatalf("detail %d bytes valid=%v", len(tk.PauseDetail), utf8.ValidString(tk.PauseDetail))
	}

	// Archiving clears it; unknown ids and statuses behave as before.
	if err := svc.SetStatus(ctx, id, "archived", "x"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.PauseKind != "" || tk.PausedAt.Valid {
		t.Fatalf("archived keeps reason %+v", tk)
	}
	if err := svc.SetStatus(ctx, "task_nope", "paused", "user"); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
	if err := svc.SetStatus(ctx, id, "deleted", "user"); err == nil {
		t.Fatal("bad status accepted")
	}
}
