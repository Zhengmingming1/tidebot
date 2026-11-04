package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type capabilityDeviceLogin struct {
	ID              string    `json:"id"`
	CapabilityID    string    `json:"capability_id"`
	Status          string    `json:"status"`
	VerificationURI string    `json:"verification_uri,omitempty"`
	UserCode        string    `json:"user_code,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	Note            string    `json:"note,omitempty"`
	cancel          context.CancelFunc
	done            chan struct{}
}

func curatedDWSLogin(lock capability.Lock) bool {
	return lock.Source.Kind == "git" && lock.Source.URL == "https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli" && lock.Source.Trust == "official" && lock.Source.Publisher == "DingTalk-Real-AI" && lock.Source.Registry != nil && lock.Source.Registry.Name == "tidebot-curated" && lock.Source.Registry.Entry == "dingtalk-dws"
}
func (a *App) capabilityCLILogin(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	id := r.CapabilityID
	if action == "cli_login_status" || action == "cli_login_cancel" {
		a.capState.mu.Lock()
		defer a.capState.mu.Unlock()
		job := a.capState.deviceLogins[id]
		if job == nil {
			return capabilityDeviceLogin{CapabilityID: id, Status: "idle"}, nil
		}
		if action == "cli_login_cancel" && (job.Status == "starting" || job.Status == "pending") {
			job.cancel()
			job.Status = "cancelled"
			job.UserCode = ""
			job.VerificationURI = ""
		}
		return *job, nil
	}
	if action != "cli_login_start" || !r.Reviewed {
		return nil, capError(400, "login_confirmation_required", "Explicit owner confirmation is required for device login")
	}
	record, err := a.caps().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !curatedDWSLogin(record.Lock) {
		return nil, capError(400, "unsupported_login", "Only the curated official DingTalk DWS device login is supported")
	}
	if err := a.caps().Verify(ctx, id, record.VersionID); err != nil {
		return nil, err
	}
	if err := a.verifyManagedCapabilityDependencies(ctx, record.Lock); err != nil {
		return nil, err
	}
	command, err := validatedDWSCommand(record.Lock)
	if err != nil {
		return nil, err
	}
	// Both flags are registered by official v1.0.66 auth_command.go.
	args := []string{"auth", "login", "--device", "--no-browser"}
	env := dwsCLIEnvironment()
	for _, secret := range record.Lock.Requires.Secrets {
		if secret.Name != "DWS_CLIENT_ID" && secret.Name != "DWS_CLIENT_SECRET" {
			continue
		}
		if a.Secrets != nil {
			if value, e := a.Secrets.Get("cap:" + id + ":" + secret.Name); e == nil && value != "" {
				env = append(env, secret.Name+"="+value)
			}
		}
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	job := &capabilityDeviceLogin{ID: hex.EncodeToString(random), CapabilityID: id, Status: "starting", ExpiresAt: time.Now().UTC().Add(10 * time.Minute), Note: "DWS stores its own credentials. Complete authorization only on the official DingTalk site; organization approval may be required.", cancel: cancel, done: make(chan struct{})}
	a.capState.mu.Lock()
	if a.capState.deviceLogins == nil {
		a.capState.deviceLogins = map[string]*capabilityDeviceLogin{}
	}
	for key, prior := range a.capState.deviceLogins {
		if time.Now().After(prior.ExpiresAt) {
			prior.cancel()
			delete(a.capState.deviceLogins, key)
		}
	}
	if prior := a.capState.deviceLogins[id]; prior != nil && (prior.Status == "starting" || prior.Status == "pending") {
		a.capState.mu.Unlock()
		cancel()
		return nil, capError(409, "login_pending", "Device login already pending")
	}
	if len(a.capState.deviceLogins) >= 32 {
		a.capState.mu.Unlock()
		cancel()
		return nil, errors.New("too many device login sessions")
	}
	a.capState.deviceLogins[id] = job
	initial := *job
	a.capState.mu.Unlock()
	go func() {
		defer close(job.done)
		defer cancel()
		err := managed.RunDeviceLogin(loginCtx, managed.Command{Path: command.Path, Args: args, Env: env, Dir: a.Cfg.DataDir}, func(line string) {
			uri, code := parseDWSDeviceLine(line)
			if uri == "" && code == "" {
				return
			}
			a.capState.mu.Lock()
			defer a.capState.mu.Unlock()
			if job.Status == "cancelled" {
				return
			}
			if uri != "" {
				job.VerificationURI = uri
			}
			if code != "" {
				job.UserCode = code
			}
			job.Status = "pending"
		})
		a.capState.mu.Lock()
		defer a.capState.mu.Unlock()
		if job.Status == "cancelled" {
			return
		}
		job.UserCode = ""
		job.VerificationURI = ""
		if err != nil {
			job.Status = "failed"
			job.Note = "Device login failed, expired, or was cancelled. Check organization authorization and retry."
		} else {
			job.Status = "completed"
			job.Note = "DWS reported successful login; credentials remain managed by DWS."
		}
	}()
	return initial, nil
}

var deviceANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
var deviceCodeLine = regexp.MustCompile(`(?i)^(?:授权码|user code|authorization code)\s*[:：]\s*([A-Z0-9][A-Z0-9-]{3,31})\s*$`)

// DWS v1.0.66 prints a labeled user code and unstyled URL lines on stderr.
// Only those fields are retained. Tokens, identities, debug lines and arbitrary
// URLs are discarded. URL queries are stripped to avoid retaining tracking data.
func parseDWSDeviceLine(line string) (string, string) {
	line = strings.TrimSpace(deviceANSI.ReplaceAllString(line, ""))
	if match := deviceCodeLine.FindStringSubmatch(line); match != nil {
		return "", match[1]
	}
	if len(line) > 2048 || !strings.HasPrefix(line, "https://") {
		return "", ""
	}
	u, err := url.Parse(line)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return "", ""
	}
	if u.Hostname() != "login.dingtalk.com" && u.Hostname() != "login.dingtalk.io" {
		return "", ""
	}
	if !strings.HasPrefix(u.Path, "/oauth2/") {
		return "", ""
	}
	u.RawQuery = ""
	u.ForceQuery = false
	return u.String(), ""
}
func (a *App) cancelCapabilityDeviceLogins() {
	a.capState.mu.Lock()
	jobs := []*capabilityDeviceLogin{}
	for _, job := range a.capState.deviceLogins {
		job.cancel()
		if job.Status == "starting" || job.Status == "pending" {
			job.Status = "cancelled"
		}
		job.UserCode = ""
		job.VerificationURI = ""
		jobs = append(jobs, job)
	}
	a.capState.mu.Unlock()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for _, job := range jobs {
		if job.done != nil {
			select {
			case <-job.done:
			case <-deadline.C:
				return
			}
		}
	}
}
