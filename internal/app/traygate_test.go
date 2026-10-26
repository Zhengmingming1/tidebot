package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/diskspace"
)

func TestTrayGate(t *testing.T) {
	var g TrayGate
	if !g.Changed("a") {
		t.Fatal("first token must count as a change")
	}
	if g.Changed("a") {
		t.Fatal("same token again is no change")
	}
	if !g.Changed("b") || g.Changed("b") || !g.Changed("a") {
		t.Fatal("a moved token is a change, once")
	}
	if !g.Changed("") || !g.Changed("") {
		t.Fatal("an empty token always recomputes")
	}
}

func TestTrayRev(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 16, 0, 10, 0, time.UTC)
	rev := func() string { return a.TrayRev(ctx, nil, now) }

	r0 := rev()
	// Reading the tray, pinging the core and asking again must not move the token,
	// or the gate would never skip anything.
	_ = a.TrayStatus(ctx, nil)
	_ = a.TrayStatus(ctx, nil)
	if r := rev(); r != r0 {
		t.Fatalf("reads moved the token: %s → %s", r0, r)
	}
	if r := a.TrayRev(ctx, nil, now.Add(40*time.Second)); r != r0 {
		t.Fatalf("same minute should keep the token: %s → %s", r0, r)
	}

	steps := []struct {
		name string
		do   func()
		at   time.Time
		err  error
	}{
		{"next minute", nil, now.Add(time.Minute), nil},
		{"daemon down", nil, now, errors.New("down")},
		{"run started", func() {
			_, _ = a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('rg1','running',?)`, now.Format(time.RFC3339))
		}, now, nil},
		{"run failed", func() {
			_, _ = a.DB.Exec(`UPDATE runs SET status='failed', finished_at=? WHERE id='rg1'`, now.Format(time.RFC3339))
		}, now, nil},
		{"language", func() {
			b, _ := json.Marshal("en")
			_ = a.Auth.SetSetting(ctx, "language", string(b))
		}, now, nil},
		{"spend", func() {
			_, _ = a.DB.Exec(`INSERT INTO cost_ledger(run_id, bucket, usd, day, month, created_at) VALUES('rg1','default',1,'2026-10-26','2026-10',?)`, now.Format(time.RFC3339))
		}, now, nil},
		{"paused", func() { a.Sched.PauseUntil(true, time.Time{}) }, now, nil},
		{"pause end set", func() { a.Sched.PauseUntil(true, now.Add(time.Hour)) }, now, nil},
		{"resumed", func() { a.Sched.PauseUntil(false, time.Time{}) }, now, nil},
		{"disk reading", func() {
			a.disk.mu.Lock()
			a.disk.last = &DiskStatus{Info: diskspace.Info{Free: 900 << 20}, Low: true}
			a.disk.mu.Unlock()
		}, now, nil},
		{"disk freed", func() {
			a.disk.mu.Lock()
			a.disk.last = &DiskStatus{Info: diskspace.Info{Free: 9000 << 20}}
			a.disk.mu.Unlock()
		}, now, nil},
	}
	prev := r0
	for _, s := range steps {
		if s.do != nil {
			s.do()
		}
		r := a.TrayRev(ctx, s.err, s.at)
		if r == prev {
			t.Fatalf("%s: token did not move (%s)", s.name, r)
		}
		if s.at.Equal(now) && s.err == nil {
			prev = r
		}
	}

	// A timed pause that ran out is over: the token says so even before the scheduler ticks.
	a.Sched.PauseUntil(true, now.Add(time.Minute))
	during := a.TrayRev(ctx, nil, now)
	after := a.TrayRev(ctx, nil, now.Add(time.Minute))
	if during == after {
		t.Fatal("a pause ending must move the token")
	}
	a.Sched.PauseUntil(false, time.Time{})
}

// The gate in front of TrayStatus: with nothing happening, polls don't recompute;
// every visible change still reaches the tray on the next poll.
func TestTrayGateFollowsStatus(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	var g TrayGate
	recompute := 0
	var st TrayStatus
	poll := func(alive error) {
		if g.Changed(a.TrayRev(ctx, alive, now)) {
			recompute++
			st = a.TrayStatus(ctx, alive)
		}
	}
	for i := 0; i < 30; i++ {
		poll(nil)
	}
	if recompute != 1 || st.Icon != TrayIdle {
		t.Fatalf("idle polls should compute once, got %d (%+v)", recompute, st)
	}
	_, _ = a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('rg2','running',?)`, now.UTC().Format(time.RFC3339))
	poll(nil)
	if st.Icon != TrayRunning || st.Running != 1 {
		t.Fatalf("a started run must reach the tray: %+v", st)
	}
	poll(errors.New("down"))
	if len(st.Problems) == 0 || st.Problems[0].Key != "desktop.p.daemon_down" {
		t.Fatalf("daemon down must reach the tray: %+v", st)
	}
	poll(nil)
	if len(st.Problems) != 0 {
		t.Fatalf("daemon back must clear the problem: %+v", st)
	}
	_, _ = a.DB.Exec(`UPDATE runs SET status='failed', finished_at=? WHERE id='rg2'`, now.UTC().Format(time.RFC3339))
	poll(nil)
	if st.Icon != TrayError || st.Running != 0 {
		t.Fatalf("a failed run must reach the tray: %+v", st)
	}
	n := recompute
	for i := 0; i < 30; i++ {
		poll(nil)
	}
	if recompute != n {
		t.Fatalf("idle again: no recompute expected, got %d more", recompute-n)
	}
}

func TestPingRPCIsLight(t *testing.T) {
	a := newTestApp(t)
	out, err := a.rpcPing(context.Background(), nil)
	if err != nil || fmt.Sprint(out) != "map[ok:true]" {
		t.Fatalf("ping: %v %v", out, err)
	}
	// The probe must not write: the tray gate relies on idle pings leaving the token alone.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := a.StartDaemon(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	waitFor(t, 5*time.Second, func() bool { return a.PingIPC(time.Second) == nil })
	time.Sleep(500 * time.Millisecond) // start-up writes (disk reading, schedules) settle
	now := time.Now()
	r0 := a.TrayRev(ctx, nil, now)
	for i := 0; i < 5; i++ {
		if err := a.PingIPC(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if r := a.TrayRev(ctx, nil, now); r != r0 {
		t.Fatalf("pings moved the token: %s → %s", r0, r)
	}
}

// go test ./internal/app -run '^$' -bench Tray -benchtime 200x
func BenchmarkTrayPoll(b *testing.B) {
	a := newTestApp(b)
	ctx := context.Background()
	now := time.Now().UTC()
	tx, _ := a.DB.Begin()
	for i := 0; i < 20000; i++ {
		at := now.Add(-time.Duration(i) * 3 * time.Minute).Format(time.RFC3339)
		st := []string{"succeeded", "failed", "succeeded", "timeout", "succeeded"}[i%5]
		_, _ = tx.Exec(`INSERT INTO runs(id, status, created_at, finished_at) VALUES(?,?,?,?)`, fmt.Sprintf("b%d", i), st, at, at)
		_, _ = tx.Exec(`INSERT INTO cost_ledger(run_id, bucket, usd, day, month, created_at) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("b%d", i), "default", 0.01, at[:10], at[:7], at)
	}
	_ = tx.Commit()
	b.Run("status", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = a.TrayStatus(ctx, nil)
		}
	})
	b.Run("rev", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = a.TrayRev(ctx, nil, now)
		}
	})
	b.Run("rpc-status", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = a.rpcStatus(ctx, nil)
		}
	})
	b.Run("rpc-ping", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = a.rpcPing(ctx, nil)
		}
	})
}

// The split "failed in the last hour" query keeps COALESCE(finished_at, created_at) semantics.
func TestTrayRecentFailuresWindow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	rows := []struct {
		id, status, created string
		finished            any
	}{
		{"w1", "failed", f(-3 * time.Hour), f(-10 * time.Minute)}, // started long ago, failed recently: counts
		{"w2", "timeout", f(-5 * time.Minute), nil},               // no finish time, recent: counts
		{"w3", "failed", f(-2 * time.Hour), nil},                  // no finish time, old: not counted
		{"w4", "failed", f(-3 * time.Hour), f(-2 * time.Hour)},    // finished 2 h ago: not counted
		{"w5", "succeeded", f(-time.Minute), f(-time.Minute)},     // not a failure
		{"w6", "stopped", f(-time.Minute), f(-time.Minute)},       // stopped by hand isn't a failure
	}
	for _, r := range rows {
		if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at, finished_at) VALUES(?,?,?,?)`, r.id, r.status, r.created, r.finished); err != nil {
			t.Fatal(err)
		}
	}
	st := a.TrayStatus(ctx, nil)
	if len(st.Problems) != 1 || st.Problems[0].Key != "desktop.p.failed" || st.Problems[0].N != 2 {
		t.Fatalf("want 2 recent failures: %+v", st)
	}
}

// The tray's and the budgets' time-range queries use an index, not a full table scan.
func TestTrayQueriesUseIndexes(t *testing.T) {
	a := newTestApp(t)
	plan := func(q string, args ...any) string {
		rows, err := a.DB.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return fmt.Sprint(out)
	}
	for _, c := range []struct{ q, idx string }{
		{`SELECT COUNT(1) FROM runs WHERE status IN ('failed','timeout') AND finished_at >= ?`, "idx_runs_status_finished"},
		{`SELECT COALESCE(SUM(usd),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ?`, "idx_cost_created"},
		{`SELECT COALESCE(SUM(usd),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ? AND task_id=?`, "idx_cost_task_created"},
	} {
		args := []any{"2026-10-26T00:00:00Z", "2026-10-27T00:00:00Z", "t1"}[:countQ(c.q)]
		if p := plan(c.q, args...); !containsAll(p, c.idx) {
			t.Errorf("%s\nplan %s, want %s", c.q, p, c.idx)
		}
	}
}

func countQ(q string) int {
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
		}
	}
	return n
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}
