package webui

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSchedulerPreviewMatchesScheduler(t *testing.T) {
	c := login(t)
	sched := c.s.deps.Tasks.Sched
	from := time.Date(2026, 10, 16, 8, 0, 0, 0, c.s.loc())
	for _, expr := range []string{"0 9 * * 1-5", "*/15 * * * *", "30 8 1 * *", "0 18 * * 0"} {
		got, err := PreviewRuns(sched.NextRun, expr, from, 5)
		if err != nil {
			t.Fatal(expr, err)
		}
		cur := from
		for i := 0; i < 5; i++ {
			want, _ := sched.NextRun(expr, cur)
			if !got[i].Equal(want) {
				t.Fatalf("%s #%d: preview %v scheduler %v", expr, i, got[i], want)
			}
			cur = want
		}
	}
	// weekdays 9:00 → never Saturday/Sunday, always 09:00 in the scheduler zone
	got, _ := PreviewRuns(sched.NextRun, "0 9 * * 1-5", from, 10)
	for _, g := range got {
		if g.Weekday() == time.Saturday || g.Weekday() == time.Sunday || g.In(c.s.loc()).Hour() != 9 {
			t.Fatalf("bad weekday run %v", g)
		}
	}
	code, out := c.json(http.MethodGet, "/api/scheduler/preview?cron=0+9+*+*+1-5&tz=UTC&n=3", nil)
	if code != 200 || len(out["times"].([]any)) != 3 || out["timezone"] != "UTC" {
		t.Fatalf("preview %d %v", code, out)
	}
	if code, _ := c.json(http.MethodGet, "/api/scheduler/preview?cron=0+9+*+*+*&tz=Mars/Base", nil); code != 400 {
		t.Fatalf("bad tz %d", code)
	}
	if code, _ := c.json(http.MethodGet, "/api/scheduler/preview?cron="+strings.Repeat("1", 300), nil); code != 400 {
		t.Fatalf("long cron %d", code)
	}
}

func TestTaskBatch(t *testing.T) {
	c := login(t)
	ids := []string{}
	for _, n := range []string{"a", "b", "c"} {
		_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": n, "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
		ids = append(ids, tk["id"].(string))
	}
	if code, out := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": ids[:2], "action": "pause"}); code != 200 || out["done"].(float64) != 2 {
		t.Fatalf("pause %d %v", code, out)
	}
	_, a := c.json(http.MethodGet, "/api/tasks/"+ids[0], nil)
	if a["status"] != "paused" {
		t.Fatalf("not paused %v", a["status"])
	}
	if code, out := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": []string{ids[2], "nope"}, "action": "delete"}); code != 200 || out["done"].(float64) != 1 || len(out["failed"].([]any)) != 1 {
		t.Fatalf("delete %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": ids, "action": "explode"}); code != 400 {
		t.Fatalf("bad action %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": []string{}, "action": "pause"}); code != 400 {
		t.Fatalf("empty ids %d", code)
	}
}
