package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

// Tray icon states.
const (
	TrayIdle    = "idle"
	TrayRunning = "running"
	TrayError   = "error"
)

// TrayProblem is one line in the tray's "problems" section (shown only when non-empty).
// Key is an i18n key under desktop.p.*; N is its numeric argument when it has one.
type TrayProblem struct {
	Key string
	N   int
}

// TrayStatus is what the desktop tray renders. Pure data so it can be tested
// without a GUI; the desktop shell polls it every couple of seconds.
type TrayStatus struct {
	Problems []TrayProblem
	Running  int
	Paused   bool
	Until    string // "15:04" (or "01-02 15:04" on another day) when a timed pause ends
	Icon     string
}

// Signature changes whenever the menu needs rebuilding.
func (t TrayStatus) Signature() string {
	var b strings.Builder
	for _, p := range t.Problems {
		fmt.Fprintf(&b, "%s:%d;", p.Key, p.N)
	}
	fmt.Fprintf(&b, "r%d p%v u%s i%s", t.Running, t.Paused, t.Until, t.Icon)
	return b.String()
}

// PingIPC checks that the core answers on its IPC socket within timeout.
func (a *App) PingIPC(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		c, err := ipc.DialClient(a.Cfg.IPCPath)
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		done <- c.Call("ping", nil, nil)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("ipc: no answer within %s", timeout)
	}
}

// TrayRev is a cheap "might the tray look different?" token: SQLite's total_changes() on the
// store's only connection (moves on any write: runs, usage, budgets, settings incl. language),
// the minute (the "failed in the last hour" window and the budget day roll over with time),
// liveness, the pause state and the last disk reading (both in memory). Same token → the
// tray would render the same, so the 2 s poll skips TrayStatus and its queries. A failed read
// returns a token that never repeats, so the tray recomputes rather than going stale.
func (a *App) TrayRev(ctx context.Context, daemonErr error, now time.Time) string {
	var n int64
	if err := a.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n); err != nil {
		return "err." + strconv.FormatInt(now.UnixNano(), 10)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d.%d.%t", n, now.Unix()/60, daemonErr != nil)
	if a.Sched != nil {
		p, until := a.Sched.PauseState(now)
		fmt.Fprintf(&b, ".p%t.%d", p, until.Unix())
	}
	if d := a.lastDisk(); d != nil {
		fmt.Fprintf(&b, ".d%t.%d", d.Low, d.Free>>20)
	}
	return b.String()
}

// TrayGate remembers the last TrayRev token: Changed is true the first time and whenever
// the token moved. Not safe for concurrent use (the tray polls from one goroutine).
type TrayGate struct{ last string }

func (g *TrayGate) Changed(tok string) bool {
	if tok != "" && tok == g.last {
		return false
	}
	g.last = tok
	return true
}

// TrayStatus gathers the tray model. daemonErr is the result of the liveness
// check (PingIPC, or the daemon's Serve having returned); non-nil means down.
func (a *App) TrayStatus(ctx context.Context, daemonErr error) TrayStatus {
	st := TrayStatus{}
	if a.Sched != nil {
		now := time.Now()
		p, until := a.Sched.PauseState(now)
		st.Paused, st.Until = trayPauseEnd(p, until, a.Loc(), now)
	}
	if daemonErr != nil {
		st.Problems = append(st.Problems, TrayProblem{Key: "desktop.p.daemon_down"})
	}
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&st.Running)
	if sum, err := a.Cost.Summary(ctx, ""); err == nil {
		for _, b := range sum.Budgets {
			if b.Scope == "global" && b.Period == "day" && b.MaxUSD > 0 && b.UsedUSD >= b.MaxUSD {
				st.Problems = append(st.Problems, TrayProblem{Key: "desktop.p.budget_out"})
				break
			}
		}
	}
	var failed int
	since := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	// = COALESCE(finished_at, created_at) >= since, split so both halves are index ranges.
	_ = a.DB.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(1) FROM runs WHERE status IN ('failed','timeout') AND finished_at >= ?) +
 (SELECT COUNT(1) FROM runs WHERE status IN ('failed','timeout') AND finished_at IS NULL AND created_at >= ?)`, since, since).Scan(&failed)
	if failed > 0 {
		st.Problems = append(st.Problems, TrayProblem{Key: "desktop.p.failed", N: failed})
	}
	if d := a.lastDisk(); d != nil && d.Low {
		st.Problems = append(st.Problems, TrayProblem{Key: "desktop.p.disk_low", N: int(d.Free >> 20)})
	}
	switch {
	case len(st.Problems) > 0:
		st.Icon = TrayError
	case st.Running > 0:
		st.Icon = TrayRunning
	default:
		st.Icon = TrayIdle
	}
	return st
}

// trayPauseEnd formats a timed pause's end for the tray tooltip in the scheduling timezone.
func trayPauseEnd(paused bool, until time.Time, loc *time.Location, now time.Time) (bool, string) {
	if !paused || until.IsZero() {
		return paused, ""
	}
	u, n := until.In(loc), now.In(loc)
	if u.Year() == n.Year() && u.YearDay() == n.YearDay() {
		return true, u.Format("15:04")
	}
	return true, u.Format("01-02 15:04")
}
