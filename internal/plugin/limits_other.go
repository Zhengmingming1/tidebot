//go:build !linux

package plugin

// applyLimits is a no-op outside Linux (Windows Job Objects / macOS rlimit: not implemented).
func applyLimits(pid int, l Limits) {}
