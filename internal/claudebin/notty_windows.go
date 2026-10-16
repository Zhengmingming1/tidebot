//go:build windows

package claudebin

import "os/exec"

func setNoTTY(cmd *exec.Cmd) {}
