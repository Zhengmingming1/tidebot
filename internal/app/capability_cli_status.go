package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
)

type capabilityCLIAuthTest struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

// Keep HOME and the OS credential-store context: a previously installed DWS
// login is reused, not copied into tidebot or replaced by an isolated login.
// No arbitrary DWS endpoint/debug/config overrides or host secrets are inherited.
func dwsCLIEnvironment() []string {
	env := []string{"NO_COLOR=1", "TERM=dumb", "DWS_NO_SKILLS=1"}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "TMPDIR", "TEMP", "TMP", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	if v := os.Getenv("DWS_DISABLE_KEYCHAIN"); v == "1" {
		env = append(env, "DWS_DISABLE_KEYCHAIN=1")
	}
	return env
}

func validatedDWSCommand(lock capability.Lock) (capability.Resolved, error) {
	c := lock.Resolved["dws"]
	declared := false
	for _, r := range lock.Requires.Tools {
		if r.Name == "dws" {
			declared = true
		}
	}
	if !curatedDWSLogin(lock) || !declared || c.Status != "ready" || !filepath.IsAbs(c.Path) || (c.Source != "system" && c.Source != "managed") {
		return c, errors.New("DWS must be a declared, verified resolved dependency")
	}
	info, err := os.Stat(c.Path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return c, errors.New("DWS executable unavailable")
	}
	return c, nil
}

// Called only after capability snapshot and managed/system dependencies pass
// integrity checks. These arguments are curated, never read from manifests.
// Verified against official v1.0.66 internal/app/auth_command.go and
// auth_status_readonly.go: ordinary status may refresh/migrate credentials;
// --readonly does neither and never contacts the authentication service.
// https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli/blob/v1.0.66/internal/app/auth_command.go
func (a *App) testDWSCLIAuth(ctx context.Context, lock capability.Lock) capabilityCLIAuthTest {
	result := capabilityCLIAuthTest{Name: "dws", Status: "failed", Note: "DWS login status could not be checked safely. No login was started."}
	c, err := validatedDWSCommand(lock)
	if err != nil {
		return result
	}
	// Older versions may not support readonly: fail closed, never fall back to
	// ordinary status, refresh, login, migration, or filesystem credential reads.
	out, err := managed.ReadStatus(ctx, managed.Command{Path: c.Path, Args: []string{"auth", "status", "--readonly", "--format", "json"}, Env: dwsCLIEnvironment(), Dir: a.Cfg.DataDir})
	if err != nil {
		return result
	}
	var status struct {
		Success       *bool  `json:"success"`
		Authenticated *bool  `json:"authenticated"`
		Reason        string `json:"reason"`
	}
	if json.Unmarshal(out, &status) != nil || status.Success == nil || !*status.Success || status.Authenticated == nil {
		return result
	}
	if status.Reason != "" {
		result.Status = "unknown"
		result.Note = "DWS could not determine local login status. Check the CLI credential store manually; tidebot did not refresh or repair it."
	} else if *status.Authenticated {
		result.Status = "ready"
		result.Note = "DWS reports an existing local login. Credentials remain managed by DWS; server access was not tested."
	} else {
		result.Status = "login_required"
		result.Note = "DWS reports no usable local login. Start login explicitly if needed; no login was started by this check."
	}
	return result
}
