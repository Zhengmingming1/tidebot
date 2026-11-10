//go:build !windows

package runner

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// setupProcess puts the child in its own process group and kills the whole group on cancel.
// With grace > 0, cancel asks Claude Code to stop (SIGTERM) first so it can clean up, and
// the group is killed once grace runs out (see Options.StopGrace). The returned func is
// called after Wait: it ends the grace timer and kills what is left of the group.
func setupProcess(cmd *exec.Cmd, grace time.Duration) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if grace <= 0 {
		cmd.Cancel = killGroup
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	canceled := false
	var mu sync.Mutex
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		mu.Lock()
		canceled = true
		mu.Unlock()
		go func() {
			select {
			case <-time.After(grace):
				_ = killGroup()
			case <-done:
			}
		}()
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return func() {
		once.Do(func() {
			close(done)
			mu.Lock()
			c := canceled
			mu.Unlock()
			if c {
				_ = killGroup() // commands that outlived Claude Code
			}
		})
	}
}

func afterStart(cmd *exec.Cmd) func() { return func() {} }
