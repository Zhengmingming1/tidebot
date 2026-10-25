package app

import (
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/power"
)

// Which runs wait on battery, per mode and task priority (runs on every OS: no app).
func TestPowerHold(t *testing.T) {
	plugged := power.State{Known: true, HasBattery: true, Percent: 10}
	bat80 := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: 80}
	bat20 := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: 20}
	saver := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: 90, Saver: true}
	noPct := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: -1}
	cases := []struct {
		mode, prio string
		st         power.State
		want       string
	}{
		{PowerModeLow, "normal", bat80, ""},
		{PowerModeLow, "normal", bat20, "low"},
		{PowerModeLow, "normal", saver, "saver"},
		{PowerModeLow, "normal", noPct, ""},
		{PowerModeLow, "background", bat80, "battery"},
		{PowerModeLow, "background", bat20, "low"},
		{PowerModeLow, "urgent", bat20, ""},
		{PowerModeBattery, "normal", bat80, "battery"},
		{PowerModeBattery, "urgent", saver, ""},
		{PowerModeOff, "background", bat20, ""},
		{PowerModeBattery, "background", plugged, ""},
		{PowerModeBattery, "background", power.Unknown, ""},
		{PowerModeLow, "", bat20, "low"}, // unknown priority counts as normal
	}
	for _, c := range cases {
		if got := powerHold(c.mode, c.prio, c.st); got != c.want {
			t.Errorf("%s/%s %+v: got %q want %q", c.mode, c.prio, c.st, got, c.want)
		}
	}
	for m, ok := range map[string]bool{"off": true, "low": true, "battery": true, "": false, "LOW": false, "always": false} {
		if validPowerMode(m) != ok {
			t.Errorf("validPowerMode(%q)", m)
		}
	}
}
