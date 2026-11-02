package runner

import "sync/atomic"

type executionMetrics struct {
	started, finished, failed, canceled, skipped, rejected atomic.Uint64
	queued, queueNS, durationMS, persistenceErrors         atomic.Uint64
}

// Metrics is a process-lifetime snapshot. Counts have no task/provider labels so
// cardinality stays bounded and diagnostics never contain prompts or secrets.
type Metrics struct {
	Active            int     `json:"active"`
	Running           int     `json:"running"`
	Waiting           int     `json:"waiting"`
	Started           uint64  `json:"started"`
	Finished          uint64  `json:"finished"`
	Failed            uint64  `json:"failed"`
	Canceled          uint64  `json:"canceled"`
	Skipped           uint64  `json:"skipped"`
	Rejected          uint64  `json:"rejected"`
	Queued            uint64  `json:"queued"`
	QueueSeconds      float64 `json:"queue_seconds"`
	ExecutionSeconds  float64 `json:"execution_seconds"`
	PersistenceErrors uint64  `json:"persistence_errors"`
}

func (m *executionMetrics) record(r Result, err error) {
	m.finished.Add(1)
	m.durationMS.Add(uint64(max(r.DurationMS, 0)))
	switch r.Status {
	case "canceled":
		m.canceled.Add(1)
	case "skipped":
		m.skipped.Add(1)
	case "failed", "timeout":
		m.failed.Add(1)
	default:
		if err != nil {
			m.failed.Add(1)
		}
	}
}

func (r *Runner) Metrics() Metrics {
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	r.slots.mu.Lock()
	running, waiting := r.slots.busy, len(r.slots.waiting)
	r.slots.mu.Unlock()
	m := &r.metrics
	return Metrics{Active: active, Running: running, Waiting: waiting, Started: m.started.Load(), Finished: m.finished.Load(), Failed: m.failed.Load(), Canceled: m.canceled.Load(), Skipped: m.skipped.Load(), Rejected: m.rejected.Load(), Queued: m.queued.Load(), QueueSeconds: float64(m.queueNS.Load()) / 1e9, ExecutionSeconds: float64(m.durationMS.Load()) / 1e3, PersistenceErrors: m.persistenceErrors.Load()}
}
