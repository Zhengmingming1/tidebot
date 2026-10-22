// Package osopen shows a folder, or a file selected in its folder, in the OS file
// manager: Finder on macOS, Explorer on Windows, the desktop's file manager on Linux.
// Commands run without a shell and only for paths that exist.
package osopen

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrNotFound is returned for a path that doesn't exist (nothing is started).
var ErrNotFound = errors.New("path does not exist")

// Candidate is one way to open the path; Open tries them in order until one starts.
type Candidate struct {
	Name string
	Args []string
	// Wait: the command's exit status means something (dbus-send). Explorer exits 1
	// even when it worked, and xdg-open/open return at once, so those aren't waited on.
	Wait bool
}

// Commands lists how goos opens path. reveal selects a file in its folder; for a
// folder (or reveal=false) the folder itself is opened.
func Commands(goos, path string, reveal bool) []Candidate {
	switch goos {
	case "darwin":
		if reveal {
			return []Candidate{{Name: "open", Args: []string{"-R", path}}}
		}
		return []Candidate{{Name: "open", Args: []string{path}}}
	case "windows":
		if reveal {
			return []Candidate{{Name: "explorer.exe", Args: []string{"/select," + path}}}
		}
		return []Candidate{{Name: "explorer.exe", Args: []string{path}}}
	default: // linux, BSDs
		dir := path
		var cs []Candidate
		if reveal {
			dir = filepath.Dir(path)
			// The freedesktop FileManager1 interface selects the file (Nautilus, Dolphin,
			// Nemo, Thunar…); without it, open the folder.
			u := (&url.URL{Scheme: "file", Path: path}).String()
			cs = append(cs, Candidate{Name: "dbus-send", Wait: true, Args: []string{"--session", "--print-reply",
				"--dest=org.freedesktop.FileManager1", "--type=method_call", "/org/freedesktop/FileManager1",
				"org.freedesktop.FileManager1.ShowItems", "array:string:" + u, "string:"}})
		}
		return append(cs, Candidate{Name: "xdg-open", Args: []string{dir}})
	}
}

// run is replaced in tests.
var run = func(c Candidate) error {
	cmd := exec.Command(c.Name, c.Args...)
	if c.Wait {
		return cmd.Run()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap; the exit code isn't meaningful
	return nil
}

// Open shows path in the file manager. A file is revealed (selected in its folder)
// when reveal is true; otherwise its folder is opened.
func Open(path string, reveal bool) error {
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil {
		return ErrNotFound
	}
	if st.IsDir() {
		reveal = false
	} else if !reveal {
		path = filepath.Dir(path)
	}
	var last error
	for _, c := range Commands(runtime.GOOS, path, reveal) {
		if last = run(c); last == nil {
			return nil
		}
	}
	return last
}
