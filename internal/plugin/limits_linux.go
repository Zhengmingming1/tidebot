//go:build linux

package plugin

import "golang.org/x/sys/unix"

// applyLimits caps the plugin's address space with prlimit(2). This is weaker than a cgroup v2
// memory limit (virtual vs resident) and CPU percent is not enforced; documented in docs.
func applyLimits(pid int, l Limits) {
	if l.MemoryMB <= 0 {
		return
	}
	// Runtimes like Go and Node reserve far more address space than they use, so the cap is
	// generous: 16x the resident target, at least 8 GiB. It stops runaway growth, not normal use.
	lim := uint64(l.MemoryMB) * 16 << 20
	if lim < 8<<30 {
		lim = 8 << 30
	}
	_ = unix.Prlimit(pid, unix.RLIMIT_AS, &unix.Rlimit{Cur: lim, Max: lim}, nil)
}
