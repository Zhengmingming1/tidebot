//go:build !windows

package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Short name: macOS caps unix socket paths at 104 bytes and t.TempDir() embeds it.
func TestListenOneCore(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "a.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	// A second core on the same path must not steal the socket.
	if ln2, err := Listen(sock); !errors.Is(err, ErrInUse) {
		if ln2 != nil {
			ln2.Close()
		}
		t.Fatalf("second Listen: %v", err)
	}
	if c, err := net.Dial("unix", sock); err != nil {
		t.Fatalf("first core lost its socket: %v", err)
	} else {
		c.Close()
	}
	// Simulate a crash: the listener is gone but the socket file stays behind.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	ln.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}
	ln3, err := Listen(sock)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	ln3.Close()
}
