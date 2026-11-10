package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/optimize"
)

// P5: automatic analysis and canary are opt-in. A freshly imported skill starts "off"
// and is never analyzed however many runs it has; a skill the user switched to
// propose keeps that setting when a new version arrives.
func TestOptimizeOffByDefault(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "fresh")
	st, err := a.Optimize.GetSettings(ctx, sid)
	if err != nil || st.Mode != "off" {
		t.Fatalf("new skill mode %+v %v", st, err)
	}
	if ok, _ := a.Optimize.ShouldAnalyze(ctx, sid, time.Now().Add(30*24*time.Hour)); ok {
		t.Fatal("off skill analyzed")
	}
	other := importTestSkill(t, a, "chosen")
	if err := a.Optimize.SetSettings(ctx, other, optimize.Settings{Mode: "propose", Ratio: 0.2, Runs: 10}); err != nil {
		t.Fatal(err)
	}
	importTestSkill(t, a, "chosen")
	if st, _ := a.Optimize.GetSettings(ctx, other); st.Mode != "propose" {
		t.Fatalf("user setting lost %+v", st)
	}
}
