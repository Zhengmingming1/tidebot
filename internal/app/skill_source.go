package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func (a *App) sourceStart(source *tasksvc.SkillSetSource, declared []tasksvc.SkillBinding, next func(string)) func(string) {
	return func(runID string) {
		if err := a.Skills.RecordRunSource(context.Background(), runID, source, declared); err != nil {
			a.Log.Error("record composition source", "run_id", runID, "error", err)
			a.Runner.Cancel(runID)
		}
		if next != nil {
			next(runID)
		}
	}
}
