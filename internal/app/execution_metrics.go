package app

import (
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
	"io"
)

func writeExecutionMetrics(w io.Writer, m runner.Metrics, wf workflow.Metrics) {
	values := []struct {
		name, help, kind string
		value            float64
	}{
		{"executions_started_total", "Executions started in this process.", "counter", float64(m.Started)},
		{"executions_finished_total", "Execution requests finished in this process.", "counter", float64(m.Finished)},
		{"executions_failed_total", "Failed or timed out execution requests.", "counter", float64(m.Failed)},
		{"executions_canceled_total", "Canceled execution requests.", "counter", float64(m.Canceled)},
		{"executions_skipped_total", "Execution requests skipped before starting.", "counter", float64(m.Skipped)},
		{"executions_rejected_total", "Execution requests rejected during shutdown.", "counter", float64(m.Rejected)},
		{"execution_queue_wait_seconds_total", "Time spent waiting for runner slots.", "counter", m.QueueSeconds},
		{"execution_queue_wait_total", "Requests that waited for a runner slot.", "counter", float64(m.Queued)},
		{"execution_seconds_total", "Total execution duration.", "counter", m.ExecutionSeconds},
		{"execution_persistence_errors_total", "Execution state persistence failures.", "counter", float64(m.PersistenceErrors)},
		{"executions_active", "Accepted requests still in progress.", "gauge", float64(m.Active)},
		{"execution_slots_busy", "Runner slots currently occupied.", "gauge", float64(m.Running)},
		{"execution_queue_depth", "Requests waiting for runner slots.", "gauge", float64(m.Waiting)},
		{"workflows_active", "Workflows currently in progress.", "gauge", float64(wf.Active)},
		{"workflow_node_retries_total", "Workflow node retry backoffs initiated.", "counter", float64(wf.Retries)},
	}
	for _, v := range values {
		fmt.Fprintf(w, "# HELP tidebot_%s %s\n# TYPE tidebot_%s %s\ntidebot_%s %g\n", v.name, v.help, v.name, v.kind, v.name, v.value)
	}
}
