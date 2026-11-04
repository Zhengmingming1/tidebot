package main

import (
	"errors"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type initialWindowStub struct {
	current             *application.Screen
	err                 error
	width, height, sets int
	centered            *application.Screen
}

func (w *initialWindowStub) GetScreen() (*application.Screen, error) { return w.current, w.err }
func (w *initialWindowStub) SetSize(width, height int) application.Window {
	w.width, w.height = width, height
	w.sets++
	return nil
}
func (w *initialWindowStub) SetScreen(screen *application.Screen) application.Window {
	w.centered = screen
	return nil
}

func TestFitInitialWindow(t *testing.T) {
	primary := &application.Screen{IsPrimary: true, WorkArea: application.Rect{Width: 1920, Height: 1040}}
	secondary := &application.Screen{
		Bounds:   application.Rect{X: -1280, Y: -800, Width: 1280, Height: 800},
		WorkArea: application.Rect{X: -1280, Y: -776, Width: 1280, Height: 728},
	}
	tests := []struct {
		name          string
		current       *application.Screen
		err           error
		screens       []*application.Screen
		want          *application.Screen
		width, height int
	}{
		{"use actual secondary display", secondary, nil, []*application.Screen{primary}, secondary, 1216, 664},
		{"fallback to primary on error", secondary, errors.New("display unavailable"), []*application.Screen{secondary, primary}, primary, 1280, 820},
		{"fallback to primary when missing", nil, nil, []*application.Screen{primary}, primary, 1280, 820},
		{"fallback when native area invalid", &application.Screen{}, nil, []*application.Screen{primary}, primary, 1280, 820},
		{"fallback without primary flag", nil, nil, []*application.Screen{nil, {}, secondary}, secondary, 1216, 664},
		{"no usable screen", nil, nil, []*application.Screen{nil, {}}, nil, 0, 0},
		{"prefer enumerated GTK work area", &application.Screen{Bounds: secondary.Bounds, WorkArea: secondary.Bounds}, nil, []*application.Screen{primary, secondary}, secondary, 1216, 664},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			win := &initialWindowStub{current: tt.current, err: tt.err}
			fitInitialWindow(win, tt.screens)
			if win.width != tt.width || win.height != tt.height || win.centered != tt.want {
				t.Fatalf("fit = %d×%d on %p, want %d×%d on %p", win.width, win.height, win.centered, tt.width, tt.height, tt.want)
			}
			if tt.want == nil && win.sets != 0 {
				t.Fatal("unknown displays must not overwrite the window manager's placement")
			}
		})
	}
}
