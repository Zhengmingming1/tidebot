package capability

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLiteralLoginPATHNeverEvaluatesShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX profile")
	}
	got := literalProfilePATH("# comment\nexport PATH=\"$HOME/bin:${PATH}:/opt/tools\"\n", "/home/test", "/usr/bin")
	if !reflect.DeepEqual(got, []string{"/home/test/bin", "/usr/bin", "/opt/tools"}) {
		t.Fatal(got)
	}
	for _, profile := range []string{
		"export PATH=\"$(touch /tmp/should-not-exist):$PATH\"", "PATH=`echo /bin`", "PATH=/bin; echo unsafe", "if true; then\nPATH=/bin\nfi", "source ~/.other\nPATH=/bin", "PATH=$HOMEevil/bin", "PATH=./bin", "PATH=$UNKNOWN/bin", "function x {\nPATH=/bin\n}",
	} {
		if got := literalProfilePATH(profile, "/home/test", "/usr/bin"); got != nil {
			t.Fatalf("parsed shell program %q: %v", profile, got)
		}
	}
}
func TestDetectReadsSafeLoginPATHWithoutRunningShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "custom-bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "literal-path-fixture")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho 2.3.4\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export PATH=\"$HOME/custom-bin:$PATH\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// $SHELL is inspected only by basename, never launched.
	shell := filepath.Join(home, "sh")
	marker := filepath.Join(home, "shell-was-run")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", shell)
	got := Detect(context.Background(), []ToolRequirement{{Name: "literal-path-fixture"}}, DetectOptions{PATH: filepath.Join(home, "empty")})["literal-path-fixture"]
	if got.Status != "ready" || got.Path != tool {
		t.Fatal(got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell was started", err)
	}
}
