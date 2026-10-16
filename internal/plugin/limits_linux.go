//go:build linux

package plugin

import "golang.org/x/sys/unix"

// applyLimits caps the plugin's address space with prlimit(2). This is weaker than a cgroup v2
// memory limit (virtual vs resident) and CPU percent is not enforced; documented in docs.
func applyLimits(pid int, l Limits) {
	if l.MemoryMB <= 0 {
		return
	}
	// Go runtimes reserve lots of virtual memory; give 4x headroom over the resident target.
	lim := uint64(l.MemoryMB) * 4 << 20
	_ = unix.Prlimit(pid, unix.RLIMIT_AS, &unix.Rlimit{Cur: lim, Max: lim}, nil)
}
