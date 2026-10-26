package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// Settings › General › Scheduling timezone: timezone.get lists zones sorted by offset
// with the automatic zone; timezone.set validates, persists, is used by the scheduler,
// budgets, notifications and the WebUI at once, and moves upcoming runs to the new
// wall clock; "" goes back to automatic.
func TestSchedulingTimezone(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.Cfg.Timezone = "Asia/Shanghai" // automatic = config file
	a.applyLoc(a.resolveLoc(ctx))
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "tz", Prompt: "hi", CronExpr: "0 9 * * *", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	nextHour := func(loc *time.Location) int {
		tk, err := a.Store.GetTask(ctx, id)
		if err != nil || !tk.NextRunAt.Valid {
			t.Fatalf("next run: %v", err)
		}
		at, err := time.Parse(time.RFC3339, tk.NextRunAt.String)
		if err != nil {
			t.Fatal(err)
		}
		return at.In(loc).Hour()
	}
	call := func(method string, in any) (TZView, error) {
		raw, _ := json.Marshal(in)
		out, err := a.CallExt(ctx, method, raw)
		if err != nil {
			return TZView{}, err
		}
		b, _ := json.Marshal(out)
		var v TZView
		_ = json.Unmarshal(b, &v)
		return v, nil
	}
	v, err := call("timezone.get", map[string]any{})
	if err != nil || v.Zone != "" || v.Active != "Asia/Shanghai" || v.Auto != "Asia/Shanghai" || v.AutoOffset != 480 {
		t.Fatalf("get %+v %v", v, err)
	}
	if len(v.Zones) < 300 {
		t.Fatalf("zones %d", len(v.Zones))
	}
	for i := 1; i < len(v.Zones); i++ {
		if v.Zones[i-1].Offset > v.Zones[i].Offset {
			t.Fatalf("not sorted at %s", v.Zones[i].ID)
		}
	}
	sh, _ := time.LoadLocation("Asia/Shanghai")
	if h := nextHour(sh); h != 9 {
		t.Fatalf("before: %d", h)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"zone": "Mars/Base"}, map[string]any{"zone": "Local"}, map[string]any{"zone": 8}} {
		if _, err := call("timezone.set", bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	v, err = call("timezone.set", map[string]any{"zone": "America/New_York"})
	if err != nil || v.Zone != "America/New_York" || v.Active != "America/New_York" || v.Moved != 1 {
		t.Fatalf("set %+v %v", v, err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	if h := nextHour(ny); h != 9 {
		t.Fatalf("after: next run at %d:00 New York", h)
	}
	for name, got := range map[string]string{"sched": a.Sched.Location().String(), "cost": a.Cost.Location().String(), "app": a.Loc().String()} {
		if got != "America/New_York" {
			t.Fatalf("%s zone %s", name, got)
		}
	}
	// Survives a restart: the stored zone wins over the automatic one.
	a.applyLoc(sh)
	if a.resolveLoc(ctx).String() != "America/New_York" {
		t.Fatal("not persisted")
	}
	a.applyLoc(ny)
	// Same zone again moves nothing; "" is automatic again.
	if v, _ := call("timezone.set", map[string]any{"zone": "America/New_York"}); v.Moved != 0 {
		t.Fatalf("same zone moved %d", v.Moved)
	}
	v, err = call("timezone.set", map[string]any{"zone": ""})
	if err != nil || v.Zone != "" || v.Active != "Asia/Shanghai" || v.Moved != 1 {
		t.Fatalf("auto %+v %v", v, err)
	}
	if h := nextHour(sh); h != 9 {
		t.Fatalf("back to auto: %d", h)
	}
}
