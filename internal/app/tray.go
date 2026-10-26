package app

import (
	"context"
	"fmt"
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
		done <- c.Call("status", nil, nil)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("ipc: no answer within %s", timeout)
	}
}

// TrayStatus gathers the tray model. daemonErr is the result of the liveness
// check (PingIPC, or the daemon's Serve having returned); non-nil means down.
func (a *App) TrayStatus(ctx context.Context, daemonErr error) TrayStatus {
	st := TrayStatus{}
	if a.Sched != nil {
		now := time.Now()
		p, until := a.Sched.PauseState(now)
		st.Paused, st.Until = trayPauseEnd(p, until, a.Cfg.Location(), now)
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
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('failed','timeout') AND COALESCE(finished_at, created_at) >= ?`, since).Scan(&failed)
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
