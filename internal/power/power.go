// Package power tells whether the machine runs on battery, how full the battery is and
// whether the OS power saver (macOS Low Power Mode, Windows battery saver, Linux
// "power-saver" profile) is on. tidebot uses it to hold back scheduled runs of
// non-urgent tasks on a laptop that is unplugged (final-plan §12, "power aware").
//
// Like Duplicati's Library/Utility/Power (PowerSupply.GetSource), anything that can't
// be read counts as "unknown", and unknown never holds a run back: a desktop or a
// server without a battery behaves exactly as before.
package power

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// State is one reading. Percent is -1 when unknown.
type State struct {
	Known      bool `json:"known"`       // the platform could tell where power comes from
	HasBattery bool `json:"has_battery"` // a system battery is present (not a mouse)
	OnBattery  bool `json:"on_battery"`  // running from it right now
	Percent    int  `json:"percent"`
	Saver      bool `json:"saver"` // OS power saver / Low Power Mode is on
}

// Unknown is what a platform without a reading returns.
var Unknown = State{Percent: -1}

// Read is the current state (one system call on Windows, a few small file reads on
// Linux, one or two short pmset runs on macOS). Callers cache it.
func Read() State { return read() }

var (
	reBattPct  = regexp.MustCompile(`(\d{1,3})%`)
	reLowPower = regexp.MustCompile(`(?m)^\s*(?:lowpowermode|powermode)\s+1\s*$`)
)

// ParsePmsetBatt reads `pmset -g batt`:
//
//	Now drawing from 'Battery Power'
//	 -InternalBattery-0 (id=4653155)	85%; discharging; 5:12 remaining present: true
//
// A Mac mini prints only the first line ('AC Power'), so no battery.
func ParsePmsetBatt(out string) State {
	st := Unknown
	switch {
	case strings.Contains(out, "'AC Power'"):
		st.Known = true
	case strings.Contains(out, "'Battery Power'"):
		st.Known, st.OnBattery = true, true
	case strings.Contains(out, "'UPS Power'"):
		st.Known, st.OnBattery = true, true
	default:
		return Unknown
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "InternalBattery") {
			continue
		}
		st.HasBattery = true
		if m := reBattPct.FindStringSubmatch(line); m != nil {
			st.Percent = clampPct(m[1])
		}
	}
	return st
}

// ParsePmsetLowPower reads `pmset -g` for Low Power Mode ("lowpowermode 1" up to
// macOS 13, "powermode 1" on later versions, where 2 means High Power).
func ParsePmsetLowPower(out string) bool { return reLowPower.MatchString(out) }

// ReadSysfs reads Linux's /sys/class/power_supply (root) and the ACPI platform
// profile file (profile, "" to skip). Same rule as Duplicati's LinuxPowerSupplyState:
// AC when a "Mains" (or USB-C) supply is online or there is none; battery when there
// are mains supplies and all are offline. Device batteries (mice, headsets: scope
// "Device") are ignored. No mains supply and no system battery: a desktop / server.
func ReadSysfs(root, profile string) State {
	ents, err := os.ReadDir(root)
	if err != nil {
		return Unknown
	}
	st := Unknown
	mains, online := 0, 0
	discharging := false
	for _, e := range ents {
		dir := filepath.Join(root, e.Name())
		typ := readLine(filepath.Join(dir, "type"))
		switch {
		case typ == "Mains" || strings.HasPrefix(typ, "USB"):
			if typ != "Mains" && readLine(filepath.Join(dir, "online")) == "" {
				continue // a USB port without an "online" file says nothing about power
			}
			mains++
			if readLine(filepath.Join(dir, "online")) == "1" {
				online++
			}
		case typ == "Battery":
			if strings.EqualFold(readLine(filepath.Join(dir, "scope")), "Device") {
				continue
			}
			if readLine(filepath.Join(dir, "present")) == "0" {
				continue
			}
			st.HasBattery = true
			if p := readLine(filepath.Join(dir, "capacity")); p != "" && st.Percent < 0 {
				st.Percent = clampPct(p)
			}
			if readLine(filepath.Join(dir, "status")) == "Discharging" {
				discharging = true
			}
		}
	}
	st.Known = true
	switch {
	case mains > 0:
		st.OnBattery = online == 0 && st.HasBattery
	default:
		st.OnBattery = st.HasBattery && discharging
	}
	if profile != "" && readLine(profile) == "low-power" {
		st.Saver = true
	}
	return st
}

// FromWindows turns GetSystemPowerStatus's SYSTEM_POWER_STATUS fields into a State:
// ACLineStatus 0 offline / 1 online / 255 unknown, BatteryFlag 128 = no system
// battery, BatteryLifePercent 255 = unknown, SystemStatusFlag 1 = battery saver on.
func FromWindows(acLine, batteryFlag, percent, statusFlag byte) State {
	st := Unknown
	if acLine == 255 {
		return st
	}
	st.Known = true
	st.HasBattery = batteryFlag != 128 && batteryFlag != 255
	st.OnBattery = acLine == 0 && st.HasBattery
	if st.HasBattery && percent <= 100 {
		st.Percent = int(percent)
	}
	st.Saver = statusFlag == 1
	return st
}

func readLine(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	if len(b) > 256 {
		b = b[:256]
	}
	s := strings.TrimSpace(string(b))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

func clampPct(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return -1
	}
	if n > 100 {
		n = 100
	}
	return n
}
