package main

const (
	// A comfortable starting point for navigation plus the task workspace. These
	// are logical pixels, so Retina/HiDPI displays must not multiply them by DPI.
	defaultWindowWidth  = 1280
	defaultWindowHeight = 820
	initialWindowMargin = 32
)

// initialWindowSize leaves some desktop visible around the window and keeps its
// controls clear of the taskbar/Dock. Unknown display dimensions keep the default.
// This is only an initial size, not a minimum, maximum, or resize-time constraint.
func initialWindowSize(workWidth, workHeight int) (width, height int) {
	fit := func(preferred, available int) int {
		if available <= 0 {
			return preferred
		}
		return min(preferred, max(1, available-2*initialWindowMargin))
	}
	return fit(defaultWindowWidth, workWidth), fit(defaultWindowHeight, workHeight)
}
