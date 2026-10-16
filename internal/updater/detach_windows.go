//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	const detached, newGroup = 0x00000008, 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detached | newGroup}
}
