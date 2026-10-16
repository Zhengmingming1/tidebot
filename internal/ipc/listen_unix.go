//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
)

// Listen opens a Unix domain socket at path with mode 0600 (owner-only).
func Listen(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("ipc path required")
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

// Dial connects to a Unix domain socket.
func Dial(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
