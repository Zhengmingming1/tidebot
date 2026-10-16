//go:build !windows

package claudebin

import (
	"os/exec"
	"syscall"
)

// setNoTTY puts the child in its own session so an interactive shell can't grab our terminal.
func setNoTTY(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
