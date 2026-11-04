package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"testing"
	"time"
)

func TestTaskUnlimitedIgnoresConfiguredFallback(t *testing.T) {
	a := newTestApp(t)
	a.Cfg.DefaultTimeout = time.Nanosecond
	ctx := context.Background()
	if err := a.saveJSONSetting(ctx, setDiskMinFree, 0); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"manual", "cron"} {
		v, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: trigger, Prompt: "work"})
		if err != nil {
			t.Fatal(err)
		}
		if err = a.runTaskTrigger(ctx, v.Task, trigger, "production"); err != nil {
			t.Fatalf("%s inherited hidden timeout: %v", trigger, err)
		}
		if countRuns(t, a, v.Task.ID, "succeeded") != 1 {
			t.Fatalf("%s did not succeed", trigger)
		}
	}
}
