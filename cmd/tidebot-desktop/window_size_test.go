package main

import "testing"

func TestInitialWindowSize(t *testing.T) {
	tests := []struct {
		name                  string
		workWidth, workHeight int
		wantWidth, wantHeight int
	}{
		{"large desktop", 1920, 1040, 1280, 820},
		{"Retina logical work area", 1512, 894, 1280, 820},
		{"small laptop", 1366, 728, 1280, 664},
		{"1280 wide laptop", 1280, 752, 1216, 688},
		{"compact display", 1024, 728, 960, 664},
		{"portrait display", 900, 1400, 836, 820},
		{"short display", 1920, 600, 1280, 536},
		{"unknown display", 0, 0, 1280, 820},
		{"invalid display", -1, -1, 1280, 820},
		{"unknown width", 0, 600, 1280, 536},
		{"unknown height", 1024, 0, 960, 820},
		{"tiny display", 40, 40, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := initialWindowSize(tt.workWidth, tt.workHeight)
			if width != tt.wantWidth || height != tt.wantHeight {
				t.Fatalf("initialWindowSize(%d, %d) = %d×%d, want %d×%d", tt.workWidth, tt.workHeight, width, height, tt.wantWidth, tt.wantHeight)
			}
		})
	}
}

func TestInitialWindowSizeStaysWithinWorkArea(t *testing.T) {
	for workWidth := 1; workWidth <= 3840; workWidth += 31 {
		for workHeight := 1; workHeight <= 2160; workHeight += 29 {
			width, height := initialWindowSize(workWidth, workHeight)
			if width < 1 || width > workWidth || width > defaultWindowWidth || height < 1 || height > workHeight || height > defaultWindowHeight {
				t.Fatalf("initialWindowSize(%d, %d) = %d×%d exceeds positive work area or default", workWidth, workHeight, width, height)
			}
		}
	}
}
