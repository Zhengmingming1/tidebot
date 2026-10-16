//go:build windows

package ipc

import (
	"fmt"
	"net"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
)

// PipeName maps the configured IPC path to a per-user named pipe.
func PipeName(path string) string {
	if strings.HasPrefix(path, `\\.\pipe\`) {
		return path
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if u, err := user.Current(); err == nil {
		name += "-" + strings.ReplaceAll(filepath.Base(u.Username), " ", "_")
	}
	return `\\.\pipe\tidebot-` + name
}

// Listen opens a named pipe readable only by the current user (owner + SYSTEM).
func Listen(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("ipc path required")
	}
	sddl := "D:P(A;;GA;;;OW)(A;;GA;;;SY)"
	if u, err := user.Current(); err == nil && u.Uid != "" {
		sddl = "D:P(A;;GA;;;" + u.Uid + ")(A;;GA;;;SY)"
	}
	return winio.ListenPipe(PipeName(path), &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: 65536, OutputBufferSize: 65536})
}

// Dial connects to the named pipe.
func Dial(path string) (net.Conn, error) {
	return winio.DialPipe(PipeName(path), nil)
}
