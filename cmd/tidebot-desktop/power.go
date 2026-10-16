package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// lowBatteryPercent: below this, while discharging, due runs are deferred (not skipped):
// the task stays due and fires on the first tick after AC power returns.
const lowBatteryPercent = 20

// powerState is the deferral hook's input. Platforms without an implementation report
// OK=false and nothing is ever deferred.
type powerState struct {
	OK        bool
	OnBattery bool
	Percent   int
	SleepSoon bool // reserved: logind PrepareForSleep / WM_POWERBROADCAST / NSWorkspaceWillSleep
}

// readPower is replaced per platform. Linux reads /sys/class/power_supply (no deps).
// Windows (GetSystemPowerStatus) and macOS (IOPSCopyPowerSourcesInfo) are TODO stubs.
var readPower = func() powerState { return powerState{} }

func init() {
	if _, err := os.Stat("/sys/class/power_supply"); err == nil {
		readPower = readLinuxPower
	}
}

func readLinuxPower() powerState {
	root := os.Getenv("TIDEBOT_POWER_SUPPLY_ROOT") // tests
	if root == "" {
		root = "/sys/class/power_supply"
	}
	ents, _ := os.ReadDir(root)
	st := powerState{OK: true, Percent: 100}
	for _, e := range ents {
		dir := filepath.Join(root, e.Name())
		typ := readTrim(filepath.Join(dir, "type"))
		switch typ {
		case "Battery":
			if readTrim(filepath.Join(dir, "status")) == "Discharging" {
				st.OnBattery = true
			}
			if p, err := strconv.Atoi(readTrim(filepath.Join(dir, "capacity"))); err == nil && p < st.Percent {
				st.Percent = p
			}
		}
	}
	return st
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func shouldDefer(st powerState) (bool, string) {
	if !st.OK {
		return false, ""
	}
	if st.SleepSoon {
		return true, "system going to sleep"
	}
	if st.OnBattery && st.Percent < lowBatteryPercent {
		return true, "low battery " + strconv.Itoa(st.Percent) + "%"
	}
	return false, ""
}

// powerGate returns the scheduler Gate. Power state is cached for 30s.
func powerGate(log *slog.Logger) func(db.Task) (bool, string) {
	var mu sync.Mutex
	var last time.Time
	var cached powerState
	return func(db.Task) (bool, string) {
		mu.Lock()
		if time.Since(last) > 30*time.Second {
			cached, last = readPower(), time.Now()
		}
		st := cached
		mu.Unlock()
		return shouldDefer(st)
	}
}
