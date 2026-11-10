package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"sync"
	"sync/atomic"
	"time"
)

type Service struct {
	DB      *sql.DB
	Execute Executor
	// Prepare validates references when saving and freezes versions when starting.
	Prepare func(context.Context, Definition) (Definition, error)
	OnError func(error)
	// Admit runs when a run is about to start, with the frozen snapshot: it may refuse
	// (e.g. the folder is busy) and returns what to release when the run ends.
	Admit func(ctx context.Context, rid string, d Definition) (release func(), err error)
	// OverBudget reports, before each node starts, why the run must not go on
	// ("" = go on). Only called for definitions with a budget.
	OverBudget func(ctx context.Context, rid string, d Definition) string
	mu         sync.Mutex
	active     map[string]context.CancelCauseFunc
	done       map[string]chan struct{}
	closing    bool
	wg         sync.WaitGroup
	retries    atomic.Uint64
}

func New(db *sql.DB, execute Executor) *Service {
	return &Service{DB: db, Execute: execute, active: map[string]context.CancelCauseFunc{}, done: map[string]chan struct{}{}}
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Service) check(ctx context.Context, d *Definition) error {
	if err := Validate(d); err != nil {
		return err
	}
	for _, n := range d.Nodes {
		if source := n.SkillSetSource; source != nil {
			var count int
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM versions WHERE id=? AND entity_type='skill_set' AND entity_id=?`, source.VersionID, source.SetID).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("node %s: skill set source version does not belong to set", n.ID)
			}
		}
	}
	if s.Prepare != nil {
		_, err := s.Prepare(ctx, *d)
		return err
	}
	return nil
}
func (s *Service) Save(ctx context.Context, wid string, expected int, d Definition) (Workflow, error) {
	d.Nodes = append([]Node(nil), d.Nodes...)
	for i := range d.Nodes {
		d.Nodes[i].SkillSetState = ""
	}
	if err := s.check(ctx, &d); err != nil {
		return Workflow{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return Workflow{}, err
	}
	at := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workflow{}, err
	}
	defer tx.Rollback()
	next := 1
	if wid == "" {
		wid = id.New("wf")
		_, err = tx.ExecContext(ctx, `INSERT INTO workflows(id,name,description,version,created_at,updated_at) VALUES(?,?,?,?,?,?)`, wid, d.Name, d.Description, next, at, at)
	} else {
		if expected < 1 {
			return Workflow{}, ErrConflict
		}
		next = expected + 1
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE workflows SET name=?,description=?,version=?,updated_at=? WHERE id=? AND version=? AND deleted=0`, d.Name, d.Description, next, at, wid, expected)
		if err == nil {
			n, e := res.RowsAffected()
			if e != nil {
				return Workflow{}, e
			}
			if n != 1 {
				return Workflow{}, ErrConflict
			}
		}
	}
	if err != nil {
		return Workflow{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES(?,?,?,?)`, wid, next, string(raw), at); err != nil {
		return Workflow{}, err
	}
	if err = tx.Commit(); err != nil {
		return Workflow{}, err
	}
	return s.Get(ctx, wid)
}
func (s *Service) Get(ctx context.Context, wid string) (Workflow, error) {
	var w Workflow
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT w.id,w.name,w.description,w.version,w.created_at,w.updated_at,v.definition_json FROM workflows w JOIN workflow_versions v ON v.workflow_id=w.id AND v.version=w.version WHERE w.id=? AND w.deleted=0`, wid).Scan(&w.ID, &w.Name, &w.Description, &w.Version, &w.CreatedAt, &w.UpdatedAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	err = json.Unmarshal([]byte(raw), &w.Definition)
	return w, err
}
func (s *Service) List(ctx context.Context) ([]Workflow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT w.id,w.name,w.description,w.version,w.created_at,w.updated_at,v.definition_json FROM workflows w JOIN workflow_versions v ON v.workflow_id=w.id AND v.version=w.version WHERE w.deleted=0 ORDER BY w.updated_at DESC,w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		var w Workflow
		var raw string
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Version, &w.CreatedAt, &w.UpdatedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &w.Definition); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Service) Delete(ctx context.Context, wid string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE workflows SET deleted=1,updated_at=? WHERE id=? AND deleted=0`, now(), wid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Service) Start(ctx context.Context, wid, input, provider string) (string, error) {
	return s.StartWith(ctx, StartOptions{WorkflowID: wid, Input: input, Provider: provider})
}

// GetVersion returns one saved version of a workflow (the workflow must still exist).
func (s *Service) GetVersion(ctx context.Context, wid string, version int) (Workflow, error) {
	w, err := s.Get(ctx, wid)
	if err != nil || version == 0 || version == w.Version {
		return w, err
	}
	var raw string
	err = s.DB.QueryRowContext(ctx, `SELECT definition_json FROM workflow_versions WHERE workflow_id=? AND version=?`, wid, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return w, fmt.Errorf("workflow version %d not found", version)
	}
	if err != nil {
		return w, err
	}
	w.Version = version
	w.Definition = Definition{}
	err = json.Unmarshal([]byte(raw), &w.Definition)
	return w, err
}

// StartWith starts a run of a workflow version (a task's pinned one, or the current).
func (s *Service) StartWith(ctx context.Context, o StartOptions) (string, error) {
	input, provider, wid := o.Input, o.Provider, o.WorkflowID
	if len(input) > MaxOutputBytes {
		return "", fmt.Errorf("workflow input exceeds %d KB", MaxOutputBytes>>10)
	}
	w, err := s.GetVersion(ctx, wid, o.Version)
	if err != nil {
		return "", err
	}
	d := w.Definition
	if err = Validate(&d); err != nil {
		return "", err
	}
	if s.Prepare != nil {
		d, err = s.Prepare(ctx, d)
		if err != nil {
			return "", err
		}
	}
	if err = Validate(&d); err != nil {
		return "", err
	}
	snapshot, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return "", ErrClosing
	}
	if s.Execute == nil {
		return "", errors.New("workflow executor unavailable")
	}
	rid := id.New("wfr")
	at := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,provider,snapshot_json,created_at) VALUES(?,?,?,'queued',?,?,?,?)`, rid, wid, w.Version, input, provider, string(snapshot), at); err != nil {
		return "", err
	}
	for _, n := range d.Nodes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status) VALUES(?,?,'pending')`, rid, n.ID); err != nil {
			return "", err
		}
	}
	release := func() {}
	if s.Admit != nil {
		r, err := s.Admit(ctx, rid, d)
		if err != nil {
			return "", err
		}
		if r != nil {
			release = r
		}
	}
	if err = tx.Commit(); err != nil {
		release()
		return "", err
	}
	runCtx, cancel := context.WithCancelCause(context.WithValue(context.Background(), runInfoKey{}, RunInfo{RunID: rid, TaskID: o.TaskID, Bucket: o.Bucket, Def: d}))
	s.active[rid] = cancel
	finished := make(chan struct{})
	s.done[rid] = finished
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			release()
			cancel(nil)
			s.mu.Lock()
			delete(s.active, rid)
			delete(s.done, rid)
			s.mu.Unlock()
			close(finished)
		}()
		s.run(runCtx, rid, d, input, provider)
	}()
	return rid, nil
}
func (s *Service) GetRun(ctx context.Context, rid string) (Run, error) {
	var r Run
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT id,workflow_id,workflow_version,status,input,provider,snapshot_json,error,created_at,started_at,finished_at FROM workflow_runs WHERE id=?`, rid).Scan(&r.ID, &r.WorkflowID, &r.WorkflowVersion, &r.Status, &r.Input, &r.Provider, &raw, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(raw), &r.Snapshot); err != nil {
		return r, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT n.node_id,CASE WHEN n.status='running' AND child.status='queued' THEN 'queued' ELSE n.status END,n.child_run_id,n.output,n.error,n.branch,n.started_at,n.finished_at FROM workflow_run_nodes n LEFT JOIN runs child ON child.id=n.child_run_id WHERE n.workflow_run_id=? ORDER BY n.node_id`, rid)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Nodes = []NodeRun{}
	for rows.Next() {
		var n NodeRun
		if err = rows.Scan(&n.NodeID, &n.Status, &n.ChildRunID, &n.Output, &n.Error, &n.Branch, &n.StartedAt, &n.FinishedAt); err != nil {
			return r, err
		}
		r.Nodes = append(r.Nodes, n)
	}
	return r, rows.Err()
}
func (s *Service) ListRuns(ctx context.Context, wid string) ([]Run, error) {
	// History is a summary; large node outputs and complete snapshots belong to
	// GetRun, otherwise polling 100 graphs could repeatedly transfer hundreds of MB.
	q := `SELECT id,workflow_id,workflow_version,status,substr(input,1,240),provider,error,created_at,started_at,finished_at,json_extract(snapshot_json,'$.name') FROM workflow_runs`
	args := []any{}
	if wid != "" {
		q += ` WHERE workflow_id=?`
		args = append(args, wid)
	}
	q += ` ORDER BY created_at DESC,id DESC LIMIT 100`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var name sql.NullString
		if err = rows.Scan(&r.ID, &r.WorkflowID, &r.WorkflowVersion, &r.Status, &r.Input, &r.Provider, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt, &name); err != nil {
			return nil, err
		}
		r.Snapshot.Name = name.String
		r.Nodes = []NodeRun{}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) Stop(rid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.active[rid]; cancel != nil {
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		var status string
		if err := s.DB.QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, rid).Scan(&status); err != nil {
			return err
		}
		if status != "queued" && status != "running" {
			return ErrNotActive
		}
		cancel(ErrCanceled)
		return nil
	}
	return ErrNotActive
}

// WaitRun blocks until a run this service started has finished (at once when it is
// not running here). Canceling ctx stops waiting, not the run.
func (s *Service) WaitRun(ctx context.Context, rid string) error {
	s.mu.Lock()
	ch := s.done[rid]
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) BeginShutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	for _, cancel := range s.active {
		cancel(ErrInterrupted)
	}
}
func (s *Service) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Recover(ctx context.Context) error {
	// Never interrupt executions owned by this live service.
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) != 0 {
		return errors.New("cannot recover workflows while executions are active")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_run_nodes SET status='interrupted',error='workflow interrupted before completion',finished_at=? WHERE status IN ('pending','running','queued') AND workflow_run_id IN (SELECT id FROM workflow_runs WHERE status IN ('queued','running'))`, at); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status='interrupted',error='workflow interrupted before completion; rerun explicitly',finished_at=? WHERE status IN ('queued','running')`, at); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) putNode(rid string, n NodeRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `UPDATE workflow_run_nodes SET status=?,child_run_id=?,output=?,error=?,branch=?,started_at=?,finished_at=? WHERE workflow_run_id=? AND node_id=?`, n.Status, n.ChildRunID, n.Output, n.Error, n.Branch, n.StartedAt, n.FinishedAt, rid, n.NodeID)
	return err
}
func (s *Service) putRun(rid, status, message string, started bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q := `UPDATE workflow_runs SET status=?,error=?,finished_at=? WHERE id=?`
	if started {
		q = `UPDATE workflow_runs SET status=?,error=?,started_at=? WHERE id=?`
	}
	_, err := s.DB.ExecContext(ctx, q, status, message, now(), rid)
	return err
}
func (s *Service) report(err error) {
	if err != nil && s.OnError != nil {
		s.OnError(err)
	}
}
