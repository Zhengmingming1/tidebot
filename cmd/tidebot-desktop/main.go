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
	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed assets/icon.png
var icon []byte

func main() {
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

	core.Sched.Gate = powerGate(core.Log)
	if v, _ := core.Auth.GetSetting(ctx, "desktop.scheduler_paused"); v == "true" {
		core.Sched.SetPaused(true)
	}
	d, err := core.StartDaemon(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start core (is agentd already running?):", err)
		os.Exit(1)
	}
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
	sh.buildTray()
	go sh.watchEvents(core.Events.Subscribe(32))
	if err := sh.app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
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
}

func (s *shell) open() {
	s.win.Show()
	s.win.Focus()
}

func (s *shell) buildTray() {
	s.tray = s.app.SystemTray.New()
	s.tray.SetIcon(icon)
	s.tray.SetTooltip(i18n.T("desktop.tooltip"))
	s.refreshMenu()
	s.tray.OnClick(s.open)
}

// refreshMenu rebuilds the tray menu (labels follow pause state and preset list).
func (s *shell) refreshMenu() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A fresh Menu each time: the Linux DBus menu caches items by ID, so Clear()+re-add
	// on the same menu leaves stale labels in the tray host.
	m := application.NewMenu()
	s.menu = m
	m.Add(i18n.T("desktop.open")).OnClick(func(*application.Context) { s.open() })
	m.AddSeparator()
	pauseLabel := i18n.T("desktop.pause")
	if s.core.Sched.Paused() {
		pauseLabel = i18n.T("desktop.resume")
	}
	m.Add(pauseLabel).OnClick(func(*application.Context) {
		p := !s.core.Sched.Paused()
		s.core.Sched.SetPaused(p)
		_ = s.core.Auth.SetSetting(s.ctx, "desktop.scheduler_paused", fmt.Sprint(p))
		tip := i18n.T("desktop.tooltip")
		if p {
			tip = i18n.T("desktop.tooltip_paused")
		}
		s.tray.SetTooltip(tip)
		s.refreshMenu()
	})
	// Presets: a real submenu on Windows/macOS. Linux's DBus tray menu (beta.30) shows
	// submenus empty, so there they are listed inline under a disabled header.
	var sub *application.Menu
	if runtime.GOOS == "linux" {
		m.AddSeparator()
		m.Add(i18n.T("desktop.run")).SetEnabled(false)
		sub = m
	} else {
		sub = m.AddSubmenu(i18n.T("desktop.run"))
	}
	btns := s.core.WebUI.Buttons(s.ctx)
	if len(btns) == 0 {
		sub.Add(i18n.T("desktop.no_presets")).SetEnabled(false)
	}
	for _, b := range btns {
		id := b.ID
		label := b.Label
		if runtime.GOOS == "linux" {
			label = "    " + label
		}
		sub.Add(label).OnClick(func(*application.Context) {
			label, err := s.core.WebUI.RunButton(s.ctx, id)
			if err != nil {
				s.notify("btn", i18n.T("desktop.n.failed", label), err.Error())
				return
			}
			s.notify("btn", i18n.T("desktop.n.started", label), "")
		})
	}
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
	m.Add(i18n.T("desktop.quit")).OnClick(func(*application.Context) { s.app.Quit() })
	m.Update()
	if s.tray != nil {
		s.tray.SetMenu(m) // Linux DBus menu only re-exports the layout on SetMenu
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
