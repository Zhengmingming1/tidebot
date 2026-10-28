package webui

import (
	"context"
	"time"
)

// taskListBeats is how many recent runs the task list shows per task.
const taskListBeats = 10

// enrichTaskList fills the list-only run fields (last status and time, the last
// taskListBeats runs, runs today) with two queries for the whole list instead of two
// per task (the recent runs read per task from the index, see recentBeats). "Today" starts at local midnight in the display time zone, the same day the
// home page KPIs count, not the UTC date.
func (s *Server) enrichTaskList(ctx context.Context, out []taskDTO) error {
	if len(out) == 0 {
		return nil
	}
	at := make(map[string]int, len(out))
	for i := range out {
		at[out[i].ID] = i
	}
	beats, err := s.recentBeats(ctx, "t.status <> 'archived'", taskListBeats)
	if err != nil {
		return err
	}
	for tid, bs := range beats {
		if i, ok := at[tid]; ok {
			out[i].Beats = bs
			last := bs[len(bs)-1] // oldest first
			out[i].LastStatus, out[i].LastRunAt = last.Status, last.At
		}
	}
	for i := range out {
		out[i].FailStreak = failStreak(out[i].Beats)
		out[i].Failing = out[i].FailStreak > 0
	}
	now := time.Now().In(s.loc())
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc()).UTC().Format(time.RFC3339)
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT task_id, COUNT(1) FROM runs WHERE task_id IS NOT NULL AND created_at >= ? GROUP BY task_id`, day)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tid string
		var n int
		if err := rows.Scan(&tid, &n); err != nil {
			return err
		}
		if i, ok := at[tid]; ok {
			out[i].RunsToday = n
		}
	}
	return rows.Err()
}

// failStreak walks the runs newest first, skipping those without a result of their own
// (running, queued, stopped, skipped), and counts failed / timed-out runs until the first
// success. 0 means the task is not failing. beats come oldest first.
func failStreak(beats []beat) int {
	n := 0
	for i := len(beats) - 1; i >= 0; i-- {
		switch beats[i].Status {
		case "failed", "timeout":
			n++
		case "succeeded":
			return n
		}
	}
	return n
}
