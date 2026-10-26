package app

import (
	"testing"
	"time"
)

// Pure parts, run on every OS (the App tests skip Windows).
func TestSchedPauseEnd(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 26, 3, 30, 0, 0, time.UTC) // 11:30 in Shanghai, 23:30 (Oct 25) in New York
	for _, c := range []struct {
		choice string
		loc    *time.Location
		want   time.Time
	}{
		{"1h", sh, now.Add(time.Hour)},
		{"4h", sh, now.Add(4 * time.Hour)},
		{"tomorrow", sh, time.Date(2026, 10, 27, 9, 0, 0, 0, sh)},
		{"tomorrow", ny, time.Date(2026, 10, 26, 9, 0, 0, 0, ny)}, // "tomorrow" in the scheduling timezone
		{"manual", sh, time.Time{}},
	} {
		got, err := schedPauseEnd(c.choice, now, c.loc)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s in %s: %v %v, want %v", c.choice, c.loc, got, err, c.want)
		}
	}
	// Over a DST change "tomorrow 9:00" stays 9:00 on the wall clock.
	d := time.Date(2026, 11, 1, 1, 0, 0, 0, ny)
	if got, _ := schedPauseEnd("tomorrow", d, ny); got.In(ny).Hour() != 9 || got.In(ny).Day() != 2 {
		t.Errorf("dst: %v", got.In(ny))
	}
	for _, bad := range []string{"", "2h", "forever", "1H"} {
		if _, err := schedPauseEnd(bad, now, sh); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if len(SchedPauseChoices) != 4 {
		t.Fatal("menu choices changed: update the WebUI's SPZ_FOR and i18n spz.f_*")
	}

	// Tray tooltip time: clock today, date + clock on another day, nothing by hand.
	if p, u := trayPauseEnd(true, time.Date(2026, 10, 26, 14, 5, 0, 0, sh), sh, now); !p || u != "14:05" {
		t.Errorf("today: %v %q", p, u)
	}
	if _, u := trayPauseEnd(true, time.Date(2026, 10, 27, 9, 0, 0, 0, sh), sh, now); u != "10-27 09:00" {
		t.Errorf("tomorrow: %q", u)
	}
	if p, u := trayPauseEnd(true, time.Time{}, sh, now); !p || u != "" {
		t.Errorf("manual: %v %q", p, u)
	}
	if p, u := trayPauseEnd(false, time.Time{}, sh, now); p || u != "" {
		t.Errorf("off: %v %q", p, u)
	}
}
