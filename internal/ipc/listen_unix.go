//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"time"
)

// Listen opens a Unix domain socket at path with mode 0600 (owner-only).
// A socket file left by a crashed core is replaced; one that still answers belongs
// to a live core (agentd and the desktop app share the path) and is refused, so two
// schedulers never run against the same database. Windows gets the same guarantee
// from the first-instance named pipe.
func Listen(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("ipc path required")
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w (%s)", ErrInUse, path)
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
