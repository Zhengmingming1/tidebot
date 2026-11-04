package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

func dwsStatusFixture(t *testing.T, body string) (*App, capability.Record) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable fixture")
	}
	a := newTestApp(t)
	path := filepath.Join(t.TempDir(), "dws")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'dws 1.0.66'; exit 0; fi\n[ \"$*\" = 'auth status --readonly --format json' ] || exit 91\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	lock := capability.Lock{Format: capability.LockFormat, ID: "dws-auth-test", Title: "DWS", Components: []capability.Component{{Type: "agent", Name: "dws", Path: "SKILL.md"}}, Source: capability.Source{Kind: "git", Commit: strings.Repeat("a", 40), URL: "https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli", Trust: "official", Publisher: "DingTalk-Real-AI", Registry: &capability.Registry{Name: "tidebot-curated", Entry: "dingtalk-dws"}}, Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "dws", Version: ">=1.0.61"}}}, Resolved: map[string]capability.Resolved{"dws": {Source: "system", Status: "ready", Path: path, Version: "1.0.66"}}}
	r, err := a.caps().Install(context.Background(), lock, map[string][]byte{"SKILL.md": []byte("fixture")})
	if err != nil {
		t.Fatal(err)
	}
	return a, r
}
func TestDWSAuthStatusFreeTest(t *testing.T) {
	for _, tt := range []struct{ name, output, want string }{
		{"existing-login", `{"success":true,"authenticated":true,"user_name":"PRIVATE","access_token":"SECRET"}`, "ready"},
		{"logged-out", `{"success":true,"authenticated":false}`, "login_required"},
		{"keychain", `{"success":true,"authenticated":false,"reason":"keychain_unavailable","hint":"SECRET"}`, "unknown"},
		{"future-diagnostic", `{"success":true,"authenticated":false,"reason":"PRIVATE"}`, "unknown"},
		{"malformed", `SECRET`, "failed"},
		{"incomplete", `{"authenticated":true}`, "failed"},
		{"null", `null`, "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, r := dwsStatusFixture(t, "echo '"+tt.output+"'\necho SECRET >&2")
			out, err := a.testInstalledCapability(context.Background(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := out.(capabilityTestResult)
			if len(result.CLIAuth) != 1 || result.CLIAuth[0].Status != tt.want {
				t.Fatalf("%+v", result.CLIAuth)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("raw auth details escaped")
			}
			if len(a.capState.deviceLogins) != 0 {
				t.Fatal("self-test started login")
			}
		})
	}
}
func TestDWSAuthStatusValidationAndIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DWS_CLIENT_SECRET", "PRIVATE")
	t.Setenv("DWS_DEBUG", "1")
	t.Setenv("DWS_DISABLE_KEYCHAIN", "1")
	t.Setenv("DWS_BASE_URL", "https://evil.invalid")
	a, r := dwsStatusFixture(t, `[ "$HOME" = '`+home+`' ] || exit 90
[ -z "$DWS_CLIENT_SECRET$DWS_DEBUG$DWS_BASE_URL" ] || exit 92
[ "$DWS_NO_SKILLS" = 1 ] || exit 93
[ "$DWS_DISABLE_KEYCHAIN" = 1 ] || exit 94
echo '{"success":true,"authenticated":true}'`)
	if result := a.testDWSCLIAuth(context.Background(), r.Lock); result.Status != "ready" {
		t.Fatal(result)
	}
	for _, mutate := range []func(*capability.Lock){
		func(l *capability.Lock) { l.Source.Trust = "community" },
		func(l *capability.Lock) { l.Requires.Tools = nil },
		func(l *capability.Lock) { c := l.Resolved["dws"]; c.Path = "dws"; l.Resolved["dws"] = c },
		func(l *capability.Lock) { c := l.Resolved["dws"]; c.Source = "unknown"; l.Resolved["dws"] = c },
	} {
		lock := r.Lock
		lock.Resolved = map[string]capability.Resolved{"dws": r.Lock.Resolved["dws"]}
		mutate(&lock)
		if _, err := validatedDWSCommand(lock); err == nil {
			t.Fatal("invalid executable scope accepted")
		}
	}
	// The free test verifies the source snapshot before even invoking the CLI.
	path := filepath.Join(a.Cfg.DataDir, "capabilities", r.ID, r.VersionID, "src", "SKILL.md")
	_ = os.Chmod(path, 0600)
	_ = os.WriteFile(path, []byte("tampered"), 0600)
	if _, err := a.testInstalledCapability(context.Background(), r.ID); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}
func TestDWSAuthStatusFailureAndCancellation(t *testing.T) {
	for _, body := range []string{"echo SECRET >&2; exit 1", "while :; do :; done"} {
		a, r := dwsStatusFixture(t, body)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		result := a.testDWSCLIAuth(ctx, r.Lock)
		cancel()
		if result.Status != "failed" || strings.Contains(result.Note, "SECRET") {
			t.Fatal(result)
		}
	}
}
