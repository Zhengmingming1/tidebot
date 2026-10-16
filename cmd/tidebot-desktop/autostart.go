package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// setAutostart writes the per-user login item. Only the Linux (XDG) path is tested.
func setAutostart(on, hidden bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := ""
	if hidden {
		args = " --hidden"
	}
	switch runtime.GOOS {
	case "linux":
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		p := filepath.Join(dir, "autostart", "tidebot.desktop")
		if !on {
			return ignoreMissing(os.Remove(p))
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		body := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Tidebot\nExec=%q%s\nX-GNOME-Autostart-enabled=true\nTerminal=false\n", exe, args)
		return os.WriteFile(p, []byte(body), 0o644)
	case "darwin": // UNTESTED
		home, _ := os.UserHomeDir()
		p := filepath.Join(home, "Library", "LaunchAgents", "com.github.zhengmingming1.tidebot.plist")
		if !on {
			return ignoreMissing(os.Remove(p))
		}
		progArgs := "<string>" + xmlEsc(exe) + "</string>"
		if hidden {
			progArgs += "<string>--hidden</string>"
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.github.zhengmingming1.tidebot</string>
<key>ProgramArguments</key><array>` + progArgs + `</array>
<key>RunAtLoad</key><true/>
</dict></plist>
`
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		return os.WriteFile(p, []byte(plist), 0o644)
	case "windows": // UNTESTED
		key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
		if !on {
			return exec.Command("reg", "delete", key, "/v", "Tidebot", "/f").Run()
		}
		return exec.Command("reg", "add", key, "/v", "Tidebot", "/t", "REG_SZ", "/d", `"`+exe+`"`+args, "/f").Run()
	}
	return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
}

func ignoreMissing(err error) error {
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
