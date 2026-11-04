package main

import "github.com/wailsapp/wails/v3/pkg/application"

type initialWindow interface {
	GetScreen() (*application.Screen, error)
	SetSize(int, int) application.Window
	SetScreen(*application.Screen) application.Window
}

// fitInitialWindow runs once, before the first show. It deliberately has no
// resize/display-change listeners, so user resizing and tray reopening keep the
// user's size and position. Use the window's actual display, not physical pixels
// or a hard-coded primary monitor, in multi-monitor setups.
func fitInitialWindow(win initialWindow, screens []*application.Screen) {
	screen, err := win.GetScreen()
	if err != nil {
		screen = nil
	}
	screen = initialWindowScreen(screen, screens)
	if screen == nil {
		return // Let the window manager place the default-sized window.
	}
	width, height := initialWindowSize(screen.WorkArea.Width, screen.WorkArea.Height)
	win.SetSize(width, height)
	win.SetScreen(screen) // Center within this display's work area, including its origin.
}

func initialWindowScreen(current *application.Screen, screens []*application.Screen) *application.Screen {
	valid := func(screen *application.Screen) bool {
		return screen != nil && screen.WorkArea.Width > 0 && screen.WorkArea.Height > 0
	}
	if valid(current) {
		// Wails' GTK3 window query reports full monitor geometry as WorkArea;
		// its screen enumeration has the actual area excluding desktop panels.
		// Match geometry, not ID: the GTK window query uses the window ID.
		for _, screen := range screens {
			if valid(screen) && current.Bounds.Width > 0 && current.Bounds.Height > 0 && screen.Bounds == current.Bounds {
				return screen
			}
		}
		return current
	}
	for _, screen := range screens {
		if valid(screen) && screen.IsPrimary {
			return screen
		}
	}
	for _, screen := range screens {
		if valid(screen) {
			return screen
		}
	}
	return nil
}
