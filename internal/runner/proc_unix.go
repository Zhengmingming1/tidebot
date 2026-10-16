//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// setupProcess puts the child in its own process group and kills the whole group on cancel.
func setupProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func afterStart(cmd *exec.Cmd) func() { return func() {} }
