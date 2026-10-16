//go:build windows

package ipc

import (
	"fmt"
	"net"
)

// Listen is a Phase-1 stub on Windows: named pipes will replace this.
// For now return a clear error so callers know to use WSL or wait for pipe support.
func Listen(path string) (net.Listener, error) {
	return nil, fmt.Errorf("windows named pipe IPC not implemented yet (path=%s); use WSL or wait for phase1b", path)
}

// Dial is a Phase-1 stub on Windows.
func Dial(path string) (net.Conn, error) {
	return nil, fmt.Errorf("windows named pipe IPC not implemented yet (path=%s)", path)
}
