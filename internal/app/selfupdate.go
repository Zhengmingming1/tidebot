package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
	"github.com/Zhengmingming1/tidebot/internal/updater"
)

// EventUpdateAvailable fires once per new version found by the daily check.
const (
	EventUpdateAvailable  = "update_available"
	EventUpdateRolledBack = "update_rolled_back"
)

// ExitCodeRestart asks a supervisor (systemd Restart=on-failure) to start the new binary.
const ExitCodeRestart = 75

// UpdateSettings are stored as JSON under setting "update".
type UpdateSettings struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"interval_hours"`
	Channel       string `json:"channel"`
}

func defaultUpdateSettings() UpdateSettings {
	return UpdateSettings{Enabled: true, IntervalHours: 24, Channel: "stable"}
}

// Phases of the background check/apply job shown in Settings → About.
const (
	UpdIdle        = "idle"
	UpdChecking    = "checking"
	UpdDownloading = "downloading"
	UpdInstalling  = "installing"
	UpdRestarting  = "restarting"
)

type selfUpdate struct {
	mu        sync.Mutex
	phase     string // "" = idle
	done      int64  // download progress
	total     int64
	checkedAt time.Time // last finished check, ok or not
	applyErr  string
	applyCode string
	last      *updater.Check
	lastErr   string
	lastCode  string
	attempts  []updater.Attempt
	srcs      []updater.Source // order that worked last, served source first
	notified  string
	restartFn func() // set by the daemon; nil in tests
	client    *http.Client
	pubKey    string
	product   string
}

func (a *App) su() *selfUpdate {
	a.suOnce.Do(func() {
		a.suState = &selfUpdate{client: updater.Client(), pubKey: updater.PublicKey, product: "server"}
		if os.Getenv("TIDEBOT_UPDATE_PUBKEY") != "" && buildinfo.Version == "dev" { // tests / local mirrors only
			a.suState.pubKey = os.Getenv("TIDEBOT_UPDATE_PUBKEY")
		}
	})
	return a.suState
}

// SetUpdateProduct marks this process as the desktop app (different asset).
func (a *App) SetUpdateProduct(p string) { a.su().product = p }

// SetRestartFunc lets the daemon decide how to come back up after an update.
func (a *App) SetRestartFunc(f func()) { a.su().restartFn = f }

// UpdateSettings returns the stored settings with defaults.
func (a *App) UpdateSettings(ctx context.Context) UpdateSettings {
	s := defaultUpdateSettings()
	if raw, _ := a.Auth.GetSetting(ctx, "update"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	if s.IntervalHours < 1 {
		s.IntervalHours = 24
	}
	if s.Channel == "" {
		s.Channel = "stable"
	}
	return s
}

func (a *App) updateInstall() updater.Install {
	exe := updater.Executable()
	return updater.Detect(a.su().product, exe)
}

// CheckTimeout bounds one whole check (every source; each also has updater.AttemptTimeout).
var CheckTimeout = 45 * time.Second

// CheckForUpdate fetches and verifies the signed manifest.
func (a *App) CheckForUpdate(ctx context.Context) (*updater.Check, error) {
	s := a.su()
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	srcs := updater.Sources(a.NetworkSettings(ctx).Mirrors)
	m, used, err := updater.FetchFrom(ctx, s.client, srcs, s.pubKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkedAt = time.Now()
	if err != nil {
		var fe *updater.FetchError
		s.lastErr, s.lastCode, s.attempts = err.Error(), updater.Classify(err), nil
		if errors.As(err, &fe) {
			s.lastCode, s.attempts = fe.Code(), fe.Attempts
		}
		return nil, err
	}
	s.srcs = append([]updater.Source{srcs[used]}, append(append([]updater.Source{}, srcs[:used]...), srcs[used+1:]...)...)
	s.lastCode, s.attempts = "", nil
	c := updater.Evaluate(m, strings.TrimPrefix(buildinfo.Version, "v"), a.updateInstall(), runtime.GOOS, runtime.GOARCH)
	s.last, s.lastErr = c, ""
	return c, nil
}

// UpdateStatus is what Settings → About shows.
func (a *App) UpdateStatus(ctx context.Context) map[string]any {
	s := a.su()
	s.mu.Lock()
	defer s.mu.Unlock()
	var checked string
	if !s.checkedAt.IsZero() {
		checked = s.checkedAt.UTC().Format(time.RFC3339)
	}
	phase := s.phase
	if phase == "" {
		phase = UpdIdle
	}
	return map[string]any{
		"phase":         phase,
		"progress":      map[string]int64{"done": s.done, "total": s.total},
		"checked_at":    checked,
		"apply_error":   s.applyErr,
		"apply_code":    s.applyCode,
		"current":       buildinfo.Version,
		"official":      buildinfo.IsOfficial(),
		"settings":      a.UpdateSettings(ctx),
		"install":       a.updateInstall(),
		"last":          s.last,
		"error":         s.lastErr,
		"error_code":    s.lastCode,
		"attempts":      s.attempts,
		"result":        updater.LastResult(a.Cfg.HomeDir),
		"source":        updater.ManifestBase(),
		"releases_page": updater.ReleasesPage,
		"proxy":         netproxy.Resolve(),
	}
}

// begin claims the job (it starts with a check); false when one is already running.
// An update attempt clears the previous update error; a plain check keeps it.
func (s *selfUpdate) begin(apply bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != "" && s.phase != UpdIdle {
		return false
	}
	s.phase, s.done, s.total = UpdChecking, 0, 0
	if apply {
		s.applyErr, s.applyCode = "", ""
	}
	return true
}

func (s *selfUpdate) setPhase(p string) { s.mu.Lock(); s.phase = p; s.mu.Unlock() }

// StartUpdateCheck runs a check in the background; the UI polls UpdateStatus.
// A check or update already in progress is left alone.
func (a *App) StartUpdateCheck() {
	s := a.su()
	if !s.begin(false) {
		return
	}
	go func() {
		defer s.setPhase(UpdIdle)
		if _, err := a.CheckForUpdate(context.Background()); err != nil {
			a.Log.Info("update check failed", "error", err)
		}
	}()
}

// StartUpdateApply checks, downloads, installs and restarts in the background.
func (a *App) StartUpdateApply() error {
	s := a.su()
	if !s.begin(true) {
		return errors.New("an update check or install is already running")
	}
	go func() { _, _ = a.applyUpdate(context.Background()) }()
	return nil
}

// ApplyUpdateNow downloads, verifies and installs, then restarts the process
// (agentctl update apply: waits for the result).
func (a *App) ApplyUpdateNow(ctx context.Context) (map[string]any, error) {
	if !a.su().begin(true) {
		return nil, errors.New("an update check or install is already running")
	}
	return a.applyUpdate(ctx)
}

// applyUpdate runs with the job claimed and releases it unless restarting.
func (a *App) applyUpdate(ctx context.Context) (out map[string]any, err error) {
	s := a.su()
	restarting := false
	defer func() {
		s.mu.Lock()
		if err != nil {
			s.applyErr, s.applyCode = err.Error(), updater.Classify(err)
			var fe *updater.FetchError
			if errors.As(err, &fe) {
				s.applyCode = fe.Code()
			}
		}
		if !restarting {
			s.phase = UpdIdle
		}
		s.mu.Unlock()
	}()
	c, err := a.CheckForUpdate(ctx)
	if err != nil {
		return nil, err
	}
	if !c.Available {
		return map[string]any{"updated": false, "reason": "up to date"}, nil
	}
	if !c.CanApply {
		return nil, errors.New("this install is updated by " + c.Install.Kind + " (" + c.Install.Reason + "); notify-only")
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	s.mu.Lock()
	srcs := s.srcs
	s.phase = UpdDownloading
	s.mu.Unlock()
	installer, err := updater.ApplyWithProgress(dctx, s.client, c, a.Cfg.HomeDir, srcs, updater.Progress{
		Bytes:   func(done, total int64) { s.mu.Lock(); s.done, s.total = done, total; s.mu.Unlock() },
		Install: func() { s.setPhase(UpdInstalling) },
	})
	if err != nil {
		return nil, err
	}
	if installer != "" { // Windows desktop: silent NSIS reinstall, installer restarts the app
		cmd := exec.Command(installer, "/S")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		restarting = true
		s.setPhase(UpdRestarting)
		a.scheduleRestart(false)
		return map[string]any{"updated": true, "to": c.Latest, "restarting": true, "via": "installer"}, nil
	}
	a.Log.Info("update installed; restarting", "from", c.Current, "to", c.Latest)
	restarting = true
	s.setPhase(UpdRestarting)
	a.scheduleRestart(true)
	return map[string]any{"updated": true, "to": c.Latest, "restarting": true}, nil
}

func (a *App) scheduleRestart(relaunch bool) {
	f := a.su().restartFn
	if f == nil {
		return
	}
	go func() {
		time.Sleep(700 * time.Millisecond) // let the RPC reply go out
		f()
	}()
	_ = relaunch
}

// runUpdateChecker checks on the configured interval and notifies once per version.
func (a *App) runUpdateChecker(ctx context.Context) {
	if r := updater.LastResult(a.Cfg.HomeDir); r != nil && r.Status == "rolled_back" {
		key := "update.rb_notified." + r.To + r.At
		if v, _ := a.Auth.GetSetting(ctx, key); v == "" {
			a.Events.Emit(Event{Kind: EventUpdateRolledBack, Detail: r.To + ": " + r.Detail})
			_ = a.Auth.SetSetting(ctx, key, `"1"`)
		}
	}
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		st := a.UpdateSettings(ctx)
		if st.Enabled {
			if c, err := a.CheckForUpdate(ctx); err != nil {
				a.Log.Info("update check failed", "error", err)
			} else if c.Available {
				key := "update.notified." + c.Latest
				if v, _ := a.Auth.GetSetting(ctx, key); v == "" {
					a.Events.Emit(Event{Kind: EventUpdateAvailable, Detail: c.Latest + "\n" + clipNotes(c.Notes)})
					_ = a.Auth.SetSetting(ctx, key, `"1"`)
				}
			}
		}
		timer.Reset(time.Duration(st.IntervalHours) * time.Hour)
	}
}

func (a *App) rpcUpdateSettings(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		Enabled       *bool `json:"enabled"`
		IntervalHours *int  `json:"interval_hours"`
	}](raw)
	s := a.UpdateSettings(ctx)
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.IntervalHours != nil {
		if *p.IntervalHours < 1 || *p.IntervalHours > 24*30 {
			return nil, errors.New("interval_hours 1.." + strconv.Itoa(24*30))
		}
		s.IntervalHours = *p.IntervalHours
	}
	s.Channel = "stable"
	b, _ := json.Marshal(s)
	if err := a.Auth.SetSetting(ctx, "update", string(b)); err != nil {
		return nil, err
	}
	return s, nil
}

func updateMessage(e Event) (string, string) {
	ver, notes, _ := strings.Cut(e.Detail, "\n")
	if e.Kind == EventUpdateRolledBack {
		return i18n.T("update.n.rolled_back", ver), notes
	}
	return i18n.T("update.n.available", ver), notes
}

func clipNotes(s string) string {
	return textclip.Ellipsis(s, 600)
}
