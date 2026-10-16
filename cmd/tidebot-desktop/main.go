// Command tidebot-desktop is the desktop shell (final-plan D5/D8): the Go core runs
// in-process, the embedded WebUI is served to a native webview without opening a TCP
// port, and a tray icon keeps the scheduler running when the window is closed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/updater"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed assets/icon.png
var icon []byte

//go:embed assets/icon-running.png
var iconRunning []byte

//go:embed assets/icon-error.png
var iconError []byte

func main() {
	if guard.IsHookInvocation(os.Args[1:]) {
		guard.HookMain(os.Stdin, os.Stdout)
		return
	}
	configPath := flag.String("config", "", "path to config.yaml/json")
	hidden := flag.Bool("hidden", false, "start minimized to tray (used by autostart)")
	bridge := flag.String("bridge", defaultBridge(), "webview bridge: inproc (no port) | loopback (127.0.0.1 random port + one-time token)")
	autostart := flag.String("autostart", "", "enable|disable launch on login, then exit")
	flag.Parse()

	if *autostart != "" {
		if err := setAutostart(*autostart == "enable", *hidden); err != nil {
			fmt.Fprintln(os.Stderr, "autostart:", err)
			os.Exit(1)
		}
		fmt.Println("autostart", *autostart+"d")
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	// D8: desktop default is no TCP listener; the tray toggle opens LAN access explicitly.
	cfg.WebUIEnabled = false

	// application.New runs the single-instance check (a second launch focuses the first
	// window and exits here), so it must happen before the core binds the IPC socket.
	var handler atomic.Pointer[http.Handler]
	sh := &shell{notifier: notifications.New()}
	sh.app = application.New(application.Options{
		Name:        "Tidebot",
		Description: "Local scheduler for Claude Code tasks",
		Icon:        icon,
		Services:    []application.Service{application.NewService(sh.notifier)},
		Mac:         application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory, ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:       application.LinuxOptions{ProgramName: "tidebot"},
		Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := handler.Load(); h != nil {
				(*h).ServeHTTP(w, r)
				return
			}
			http.Error(w, "starting", http.StatusServiceUnavailable)
		})},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.github.zhengmingming1.tidebot",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if sh.win != nil {
					sh.open()
				}
			},
		},
	})

	// Pending self-update: count this start; after too many failed starts the
	// previous build is restored and we hand over to it.
	if bs := updater.Boot(cfg.HomeDir); bs.RolledBack {
		relaunchSelf()
		return
	}

	core, err := app.Open(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open core:", err)
		os.Exit(1)
	}
	defer core.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sh.core, sh.ctx = core, ctx

	// Setting `language` (JSON string, shared with the WebUI); "auto"/unset → OS locale.
	var lang string
	if raw, _ := core.Auth.GetSetting(ctx, "language"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &lang)
	}
	i18n.SetLang(lang)
	sh.lastLang = lang

	core.Sched.Gate = powerGate(core.Log)
	if v, _ := core.Auth.GetSetting(ctx, "desktop.scheduler_paused"); v == "true" {
		core.Sched.SetPaused(true)
	}
	core.SetUpdateProduct("desktop")
	d, err := core.StartDaemon(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start core (is agentd already running?):", err)
		if updater.RollbackNow(cfg.HomeDir, "start: "+err.Error()) == nil {
			relaunchSelf()
		}
		os.Exit(1)
	}
	restartAfterQuit := false
	core.SetRestartFunc(func() { restartAfterQuit = true; sh.app.Quit() })
	defer d.Stop()

	startURL := "/"
	if *bridge == "loopback" {
		b, err := core.WebUI.StartLoopbackBridge(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bridge:", err)
			os.Exit(1)
		}
		defer b.Close()
		startURL = b.URL
	} else {
		core.WebUI.OSLanguage = i18n.Detect()
		h, err := core.WebUI.DesktopHandler(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bridge:", err)
			os.Exit(1)
		}
		handler.Store(&h)
	}
	sh.win = sh.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Tidebot", Width: 1180, Height: 780, URL: startURL, Hidden: *hidden,
	})
	sh.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel() // close = hide; scheduler keeps running
		sh.win.Hide()
		sh.hintOnce.Do(func() { sh.notify("hint", i18n.T("desktop.n.still_running"), "") })
	})
	sh.daemonErrs = d.Errs
	sh.buildTray()
	go sh.watchEvents(core.Events.Subscribe(32))
	go sh.pollTray(ctx)
	// Health check for a fresh update: core up and the UI loop alive for 20s.
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Second):
			updater.Confirm(cfg.HomeDir)
		}
	}()
	if err := sh.app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if restartAfterQuit {
		d.Stop()
		core.Close()
		relaunchSelf()
	}
}

// relaunchSelf starts whatever is now installed at our path (for an AppImage,
// the AppImage file; for macOS, the .app via open) and exits.
func relaunchSelf() {
	exe := updater.Executable()
	in := updater.Detect("desktop", exe)
	var err error
	switch in.Kind {
	case "appimage":
		err = updater.Relaunch(in.Target, os.Args[1:])
	case "app":
		err = updater.Relaunch("/usr/bin/open", []string{"-n", in.Target})
	default:
		err = updater.Relaunch(exe, os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "restart:", err)
	}
	os.Exit(0)
}

func defaultBridge() string {
	// WebView2's custom-scheme handler can't stream bodies, which the run-live SSE view needs.
	if runtime.GOOS == "windows" {
		return "loopback"
	}
	return "inproc"
}

type shell struct {
	core     *app.App
	ctx      context.Context
	app      *application.App
	win      *application.WebviewWindow
	tray     *application.SystemTray
	menu     *application.Menu
	notifier *notifications.NotificationService
	hintOnce sync.Once
	mu       sync.Mutex

	daemonErrs <-chan error
	daemonDead error
	status     app.TrayStatus
	lastSig    string
	lastLang   string
	lastIcon   string
}

func (s *shell) open() {
	s.win.Show()
	s.win.Focus()
}

func (s *shell) buildTray() {
	s.tray = s.app.SystemTray.New()
	s.tray.SetIcon(icon)
	s.lastIcon = app.TrayIdle
	s.tray.SetTooltip(i18n.T("desktop.tooltip"))
	s.status = s.core.TrayStatus(s.ctx, nil)
	s.lastSig = s.status.Signature()
	s.refreshMenu()
	s.tray.OnClick(s.open)
}

// pollTray re-checks health every 2s: the core must answer on its IPC socket within
// 1s, so a dead daemon shows up as a problem in under 5s. The menu is rebuilt only
// when something visible changed, and follows the language setting.
func (s *shell) pollTray(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		select {
		case err := <-s.daemonErrs:
			if err == nil {
				err = fmt.Errorf("ipc server stopped")
			}
			s.daemonDead = err
		default:
		}
		alive := s.daemonDead
		if alive == nil {
			alive = s.core.PingIPC(time.Second)
		}
		lang := s.languageSetting()
		st := s.core.TrayStatus(ctx, alive)
		sig := st.Signature()
		if sig == s.lastSig && lang == s.lastLang {
			continue
		}
		langChanged := lang != s.lastLang
		s.lastSig, s.lastLang, s.status = sig, lang, st
		if langChanged {
			i18n.SetLang(lang)
		}
		application.InvokeSync(func() {
			s.applyIcon(st.Icon)
			s.refreshMenu()
		})
	}
}

func (s *shell) languageSetting() string {
	var lang string
	if raw, _ := s.core.Auth.GetSetting(s.ctx, "language"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &lang)
	}
	return lang
}

func (s *shell) applyIcon(state string) {
	if s.tray == nil || state == s.lastIcon {
		return
	}
	s.lastIcon = state
	switch state {
	case app.TrayRunning:
		s.tray.SetIcon(iconRunning)
	case app.TrayError:
		s.tray.SetIcon(iconError)
	default:
		s.tray.SetIcon(icon)
	}
}

func (s *shell) tooltip() string {
	switch {
	case len(s.status.Problems) > 0:
		return i18n.T("desktop.tooltip_problem")
	case s.core.Sched.Paused():
		return i18n.T("desktop.tooltip_paused")
	case s.status.Running > 0:
		return i18n.T("desktop.n.running", s.status.Running)
	}
	return i18n.T("desktop.tooltip")
}

// refreshMenu rebuilds the tray menu. Order: problems (only when there are any) →
// running count → first 5 buttons → pause-all checkbox → LAN → open → quit.
func (s *shell) refreshMenu() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A fresh Menu each time: the Linux DBus menu caches items by ID, so Clear()+re-add
	// on the same menu leaves stale labels in the tray host.
	m := application.NewMenu()
	s.menu = m
	if len(s.status.Problems) > 0 {
		m.Add(i18n.T("desktop.p.header")).SetEnabled(false)
		for _, p := range s.status.Problems {
			label := i18n.T(p.Key)
			if p.N > 0 {
				label = i18n.T(p.Key, p.N)
			}
			m.Add("⚠ " + label).OnClick(func(*application.Context) { s.open() })
		}
		m.AddSeparator()
	}
	if s.status.Running > 0 {
		m.Add(i18n.T("desktop.n.running", s.status.Running)).SetEnabled(false)
	} else {
		m.Add(i18n.T("desktop.idle")).SetEnabled(false)
	}
	m.AddSeparator()
	// Buttons inline (the first 5); Linux's DBus tray menu shows submenus empty anyway.
	m.Add(i18n.T("desktop.run")).SetEnabled(false)
	btns := s.core.WebUI.Buttons(s.ctx)
	if len(btns) == 0 {
		m.Add("    " + i18n.T("desktop.no_presets")).SetEnabled(false)
	}
	if len(btns) > 5 {
		btns = btns[:5]
	}
	for _, b := range btns {
		id := b.ID
		m.Add("    " + b.Label).OnClick(func(*application.Context) {
			label, err := s.core.WebUI.RunButton(s.ctx, id)
			if err != nil {
				s.notify("btn", i18n.T("desktop.n.failed", label), err.Error())
				return
			}
			s.notify("btn", i18n.T("desktop.n.started", label), "")
		})
	}
	m.AddSeparator()
	m.AddCheckbox(i18n.T("desktop.pause_all"), s.core.Sched.Paused()).OnClick(func(*application.Context) {
		p := !s.core.Sched.Paused()
		s.core.Sched.SetPaused(p)
		_ = s.core.Auth.SetSetting(s.ctx, "desktop.scheduler_paused", fmt.Sprint(p))
		s.status.Paused = p
		s.tray.SetTooltip(s.tooltip())
		s.refreshMenu()
	})
	m.AddCheckbox(i18n.T("desktop.lan"), s.core.WebUI.LANEnabled()).OnClick(func(c *application.Context) {
		on := !s.core.WebUI.LANEnabled()
		addr, err := s.core.WebUI.SetLAN(s.ctx, on)
		if err != nil {
			s.notify("lan", i18n.T("desktop.n.lan_fail", err.Error()), "")
		} else if on {
			s.notify("lan", i18n.T("desktop.n.lan_on", addr), "")
		}
		s.refreshMenu()
	})
	m.AddSeparator()
	m.Add(i18n.T("desktop.open")).OnClick(func(*application.Context) { s.open() })
	m.Add(i18n.T("desktop.quit")).OnClick(func(*application.Context) { s.app.Quit() })
	m.Update()
	if s.tray != nil {
		s.tray.SetMenu(m) // Linux DBus menu only re-exports the layout on SetMenu
		s.tray.SetTooltip(s.tooltip())
	}
}

func (s *shell) notify(id, title, body string) {
	if s.notifier == nil {
		return
	}
	err := s.notifier.SendNotification(notifications.NotificationOptions{
		ID: fmt.Sprintf("%s-%d", id, time.Now().UnixNano()), Title: title, Body: body,
	})
	if err != nil {
		s.core.Log.Warn("notification", "error", err)
	}
}

func (s *shell) watchEvents(ch <-chan app.Event) {
	for e := range ch {
		name := e.TaskName
		if name == "" {
			name = i18n.T("desktop.quick_task")
		}
		switch e.Kind {
		case app.EventRunFinished:
			s.notify(e.Kind, i18n.T("desktop.n.finished", name), i18n.T("desktop.n.finished_body", e.CostUSD))
		case app.EventRunFailed:
			s.notify(e.Kind, i18n.T("desktop.n.failed", name), truncate(e.Detail, 160))
		case app.EventApprovalNeeded:
			s.notify(e.Kind, i18n.T("desktop.n.approval", name), i18n.T("desktop.n.approval_body", e.Count))
		case app.EventBudgetBreached:
			detail := strings.ReplaceAll(e.Detail, "task/"+e.TaskID, name)
			s.notify(e.Kind, i18n.T("desktop.n.budget", name), truncate(detail, 160))
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
