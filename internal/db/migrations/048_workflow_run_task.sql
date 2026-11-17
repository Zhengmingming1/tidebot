-- Which task started this workflow run ('' = started by hand from the
-- workflow page). Node runs already carry task_id for cost; the workflow
-- run itself did not, so the history and execution pages could not show
-- the task. Existing rows are filled from a child run when one has a task.
ALTER TABLE workflow_runs ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_workflow_runs_task ON workflow_runs(task_id, created_at DESC) WHERE task_id <> '';

UPDATE workflow_runs
SET task_id = (
  SELECT r.task_id
  FROM workflow_run_nodes n
  JOIN runs r ON r.id = n.child_run_id
  WHERE n.workflow_run_id = workflow_runs.id
    AND COALESCE(r.task_id, '') <> ''
  LIMIT 1
)
WHERE task_id = ''
  AND EXISTS (
    SELECT 1
    FROM workflow_run_nodes n
    JOIN runs r ON r.id = n.child_run_id
    WHERE n.workflow_run_id = workflow_runs.id
      AND COALESCE(r.task_id, '') <> ''
  );
