//go:build !windows

package claudebin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBin(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '2.1.3 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func opts(home string, env map[string]string) Options {
	return Options{Home: home, GOOS: "darwin", Shell: "/nonexistent-shell",
		Env: func(k string) string { return env[k] }}
}

func TestResolveNVMNewestFirst(t *testing.T) {
	home := t.TempDir()
	fakeBin(t, filepath.Join(home, ".nvm/versions/node/v18.20.0/bin/claude"))
	fakeBin(t, filepath.Join(home, ".nvm/versions/node/v22.3.0/bin/claude"))
	fakeBin(t, filepath.Join(home, ".nvm/versions/node/v9.0.0/bin/claude"))
	r := Resolve(context.Background(), opts(home, map[string]string{"PATH": "/usr/bin:/bin"}))
	if !strings.Contains(r.Path, "v22.3.0") || r.Method != "known_dir" || r.Version != "2.1.3" {
		t.Fatalf("%+v", r)
	}
	if !strings.HasPrefix(r.PATH, filepath.Join(home, ".nvm/versions/node/v22.3.0/bin")) {
		t.Fatalf("child PATH must start with claude's dir (node lives there): %s", r.PATH)
	}
}

func TestResolveNativeInstallerBeatsNVM(t *testing.T) {
	home := t.TempDir()
	fakeBin(t, filepath.Join(home, ".claude/local/claude"))
	fakeBin(t, filepath.Join(home, ".nvm/versions/node/v22.3.0/bin/claude"))
	r := Resolve(context.Background(), opts(home, map[string]string{"PATH": "/usr/bin"}))
	if r.Path != filepath.Join(home, ".claude/local/claude") {
		t.Fatalf("%+v", r)
	}
}

func TestResolveConfiguredAndBroken(t *testing.T) {
	home := t.TempDir()
	fakeBin(t, filepath.Join(home, "tools/claude"))
	o := opts(home, map[string]string{"PATH": "/usr/bin"})
	o.Configured = "~/tools/claude"
	if r := Resolve(context.Background(), o); r.Method != "configured" || r.Version != "2.1.3" {
		t.Fatalf("%+v", r)
	}
	// A candidate that exists but doesn't run is skipped.
	bad := filepath.Join(home, ".local/bin/claude")
	os.MkdirAll(filepath.Dir(bad), 0o755)
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	o.Configured = "/nope/claude"
	if r := Resolve(context.Background(), o); r.Path != "" || r.Note == "" {
		t.Fatalf("bad configured path must not fall back: %+v", r)
	}
	o.Configured = ""
	r := Resolve(context.Background(), o)
	if r.Path != "" || r.Method != "not_found" {
		t.Fatalf("broken binary accepted: %+v", r)
	}
	found := false
	for _, s := range r.Searched {
		found = found || s == bad
	}
	if !found || len(r.Searched) < 10 {
		t.Fatalf("searched list should include candidates: %v", r.Searched)
	}
}

func TestResolveLoginShell(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "weird/place/claude")
	fakeBin(t, bin)
	shell := filepath.Join(home, "fakesh")
	// A fake "login shell": prints rc noise, then what our script would print.
	os.WriteFile(shell, []byte("#!/bin/sh\necho 'welcome to zsh'\necho "+bin+"\nprintf '\\n"+marker+"/custom/bin:/usr/bin\\n'\n"), 0o755)
	o := opts(home, map[string]string{"PATH": "/usr/bin"})
	o.Shell = shell
	r := Resolve(context.Background(), o)
	if r.Method != "login_shell" || r.Path != bin || !strings.Contains(r.PATH, "/custom/bin") {
		t.Fatalf("%+v", r)
	}
}

func TestKnownDirsWindows(t *testing.T) {
	d := KnownDirs(`C:\Users\me`, "windows", func(k string) string {
		return map[string]string{"APPDATA": `C:\Users\me\AppData\Roaming`}[k]
	})
	if !strings.Contains(strings.Join(d, "|"), `Roaming`) {
		t.Fatal(d)
	}
}

func TestParseShellOutput(t *testing.T) {
	b, p := parseShellOutput("Last login\n/opt/homebrew/bin/claude\n\n" + marker + "/opt/homebrew/bin:/usr/bin\n")
	if b != "/opt/homebrew/bin/claude" || p != "/opt/homebrew/bin:/usr/bin" {
		t.Fatal(b, p)
	}
}
